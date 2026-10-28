package pgrepository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"fin-agent/internal/agentclient"
	"fin-agent/internal/domain"
	"fin-agent/internal/httpapi"
	"fin-agent/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const recommendationOperation = "recommendations.generate"

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Execute(
	ctx context.Context,
	key string,
	request httpapi.GenerateRequest,
	invoke func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error),
) (httpapi.GenerateResponse, error) {
	hash, err := requestHash(request)
	if err != nil {
		return httpapi.GenerateResponse{}, err
	}

	record, err := r.prepareExecution(ctx, key, hash)
	if err != nil {
		return httpapi.GenerateResponse{}, err
	}
	if record.Status == store.IdempotencyStatusCOMPLETED {
		return decodeStoredResponse(record.ResponseBody)
	}
	if record.Status == store.IdempotencyStatusFAILED {
		return httpapi.GenerateResponse{}, &httpapi.Failure{
			Status: http.StatusBadGateway, Code: "RECOMMENDATION_FAILED",
			Message: "This idempotent recommendation request previously failed; submit a new key to start another request.",
		}
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return httpapi.GenerateResponse{}, databaseFailure("begin recommendation execution", err)
	}
	defer tx.Rollback(context.Background())
	queries := store.New(tx)
	record, err = queries.LockIdempotencyRecord(ctx, store.LockIdempotencyRecordParams{
		Operation: recommendationOperation, IdempotencyKey: key,
	})
	if err != nil {
		return httpapi.GenerateResponse{}, databaseFailure("lock idempotency record", err)
	}
	if record.RequestHash != hash {
		return httpapi.GenerateResponse{}, httpapi.ErrConflict
	}
	if record.Status == store.IdempotencyStatusCOMPLETED {
		if err := tx.Commit(ctx); err != nil {
			return httpapi.GenerateResponse{}, databaseFailure("commit cached recommendation response", err)
		}
		return decodeStoredResponse(record.ResponseBody)
	}
	if record.Status == store.IdempotencyStatusFAILED {
		if err := tx.Commit(ctx); err != nil {
			return httpapi.GenerateResponse{}, databaseFailure("commit failed recommendation lookup", err)
		}
		return httpapi.GenerateResponse{}, &httpapi.Failure{
			Status: http.StatusBadGateway, Code: "RECOMMENDATION_FAILED",
			Message: "This idempotent recommendation request previously failed; submit a new key to start another request.",
		}
	}
	if !record.ReportID.Valid {
		return httpapi.GenerateResponse{}, errors.New("processing idempotency record has no reportId")
	}

	report, err := queries.GetRecommendationReport(ctx, record.ReportID)
	if err != nil {
		return httpapi.GenerateResponse{}, databaseFailure("load recommendation report", err)
	}
	assetTypes := make([]store.AssetType, 0, len(request.AssetTypes))
	agentAssetTypes := make([]string, 0, len(request.AssetTypes))
	for _, assetType := range request.AssetTypes {
		assetTypes = append(assetTypes, store.AssetType(assetType))
		agentAssetTypes = append(agentAssetTypes, string(assetType))
	}
	marketAssets, err := queries.ListAssetsForRecommendation(ctx, assetTypes)
	if err != nil {
		return httpapi.GenerateResponse{}, databaseFailure("load deterministic market context", err)
	}
	deterministicContext := agentclient.DeterministicContext{
		FixedIncomeRates: make([]agentclient.FixedIncomeRate, 0),
		StocksAndFiis:    make([]agentclient.StockOrFII, 0, len(marketAssets)),
	}
	quoteIDs := make([]pgtype.UUID, 0, len(marketAssets))
	for _, asset := range marketAssets {
		price, err := domain.NewMoney(asset.ReferencePrice, asset.Currency)
		if err != nil {
			slog.Error("stored quote violates the exact-money contract", "reportId", uuidString(record.ReportID), "ticker", asset.Ticker)
			if failErr := failExecution(ctx, queries, record.ID, record.ReportID); failErr != nil {
				return httpapi.GenerateResponse{}, databaseFailure("mark invalid market data as failed", failErr)
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return httpapi.GenerateResponse{}, databaseFailure("commit invalid market data", commitErr)
			}
			return httpapi.GenerateResponse{}, &httpapi.Failure{
				Status: http.StatusBadGateway, Code: "INVALID_MARKET_DATA",
				Message: "Stored quote data violates the exact amount or currency contract.", Retryable: false,
			}
		}
		deterministicContext.StocksAndFiis = append(deterministicContext.StocksAndFiis, agentclient.StockOrFII{
			Ticker: asset.Ticker, Type: string(asset.AssetType), ReferencePrice: price.Amount(),
		})
		quoteIDs = append(quoteIDs, asset.QuoteID)
	}
	if len(deterministicContext.StocksAndFiis) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return httpapi.GenerateResponse{}, databaseFailure("persist pending recommendation", err)
		}
		return httpapi.GenerateResponse{}, httpapi.ErrProcessing
	}

	runResponse, err := invoke(ctx, agentclient.RunRequest{
		ReportID: uuidString(record.ReportID), DeterministicContext: deterministicContext, AssetTypes: agentAssetTypes,
	})
	if err != nil {
		logAgentFailure(record.ReportID, err)
		if shouldKeepProcessing(err) {
			return httpapi.GenerateResponse{}, failureForAgent(err)
		}
		if failErr := failExecution(ctx, queries, record.ID, record.ReportID); failErr != nil {
			return httpapi.GenerateResponse{}, databaseFailure("mark invalid recommendation as failed", failErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return httpapi.GenerateResponse{}, databaseFailure("commit failed recommendation", commitErr)
		}
		return httpapi.GenerateResponse{}, failureForAgent(err)
	}

	rankedAssets := make([]httpapi.RankedAsset, 0, len(runResponse.RankedAssets))
	for _, ranked := range runResponse.RankedAssets {
		rankedAssets = append(rankedAssets, httpapi.RankedAsset{
			Rank: ranked.Rank, Ticker: ranked.Ticker, Type: httpapi.AssetType(ranked.Type),
		})
	}
	response := httpapi.GenerateResponse{
		ReportID:      uuidString(record.ReportID),
		Status:        "COMPLETED",
		CreatedAt:     report.CreatedAt.Time,
		Summary:       httpapi.Summary{RankedAssets: rankedAssets},
		Justification: runResponse.JustificationText,
		CitedNewsIDs:  append([]string{}, runResponse.CitedNewsIDs...),
	}

	newsByPointID := make(map[string]pgtype.UUID, len(runResponse.CitedNewsIDs))
	if len(runResponse.CitedNewsIDs) > 0 {
		news, err := queries.ListMarketNewsByQdrantPointIDs(ctx, runResponse.CitedNewsIDs)
		if err != nil {
			return httpapi.GenerateResponse{}, databaseFailure("resolve cited Qdrant news IDs", err)
		}
		for _, item := range news {
			if item.QdrantPointID.Valid {
				newsByPointID[item.QdrantPointID.String] = item.ID
			}
		}
		if len(newsByPointID) != len(runResponse.CitedNewsIDs) {
			if failErr := failExecution(ctx, queries, record.ID, record.ReportID); failErr != nil {
				return httpapi.GenerateResponse{}, databaseFailure("mark unknown news citation as failed", failErr)
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return httpapi.GenerateResponse{}, databaseFailure("commit invalid news citation", commitErr)
			}
			return httpapi.GenerateResponse{}, &httpapi.Failure{
				Status: http.StatusBadGateway, Code: "UNKNOWN_NEWS_CITATION",
				Message: "The agent cited a news point that is not present in the audit store.",
			}
		}
	}

	reportID := record.ReportID
	for _, quoteID := range quoteIDs {
		if err := queries.LinkRecommendationQuote(ctx, store.LinkRecommendationQuoteParams{ReportID: reportID, QuoteID: quoteID}); err != nil {
			return httpapi.GenerateResponse{}, databaseFailure("link quote to recommendation report", err)
		}
	}
	for _, pointID := range runResponse.CitedNewsIDs {
		if err := queries.LinkRecommendationNews(ctx, store.LinkRecommendationNewsParams{
			ReportID: reportID, NewsID: newsByPointID[pointID], QdrantPointID: pointID,
		}); err != nil {
			return httpapi.GenerateResponse{}, databaseFailure("link cited news to recommendation report", err)
		}
	}
	rankedJSON, err := json.Marshal(response.Summary.RankedAssets)
	if err != nil {
		return httpapi.GenerateResponse{}, fmt.Errorf("encode ranked assets: %w", err)
	}
	rows, err := queries.CompleteRecommendationReport(ctx, store.CompleteRecommendationReportParams{
		ID: reportID, RankedAssets: rankedJSON, JustificationText: pgtype.Text{String: response.Justification, Valid: true},
	})
	if err != nil {
		return httpapi.GenerateResponse{}, databaseFailure("complete recommendation report", err)
	}
	if rows != 1 {
		return httpapi.GenerateResponse{}, errors.New("recommendation report was not in PROCESSING state")
	}
	responseBody, err := json.Marshal(response)
	if err != nil {
		return httpapi.GenerateResponse{}, fmt.Errorf("encode idempotent recommendation response: %w", err)
	}
	rows, err = queries.CompleteIdempotencyRecord(ctx, store.CompleteIdempotencyRecordParams{
		ID: record.ID, ReportID: reportID, ResponseBody: responseBody,
	})
	if err != nil {
		return httpapi.GenerateResponse{}, databaseFailure("complete idempotency record", err)
	}
	if rows != 1 {
		return httpapi.GenerateResponse{}, errors.New("idempotency record was not in PROCESSING state")
	}
	if err := tx.Commit(ctx); err != nil {
		return httpapi.GenerateResponse{}, databaseFailure("commit completed recommendation", err)
	}
	return response, nil
}

func (r *Repository) prepareExecution(ctx context.Context, key, hash string) (store.IdempotencyRecord, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return store.IdempotencyRecord{}, databaseFailure("begin idempotency transaction", err)
	}
	defer tx.Rollback(context.Background())
	queries := store.New(tx)
	record, err := queries.CreateIdempotencyRecord(ctx, store.CreateIdempotencyRecordParams{
		Operation: recommendationOperation, IdempotencyKey: key, RequestHash: hash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		record, err = queries.LockIdempotencyRecord(ctx, store.LockIdempotencyRecordParams{
			Operation: recommendationOperation, IdempotencyKey: key,
		})
	}
	if err != nil {
		return store.IdempotencyRecord{}, databaseFailure("create or lock idempotency record", err)
	}
	if record.RequestHash != hash {
		return store.IdempotencyRecord{}, httpapi.ErrConflict
	}
	if record.Status == store.IdempotencyStatusPROCESSING && !record.ReportID.Valid {
		report, err := queries.CreateRecommendationReport(ctx, []byte("[]"))
		if err != nil {
			return store.IdempotencyRecord{}, databaseFailure("create recommendation report", err)
		}
		rows, err := queries.SetIdempotencyReportID(ctx, store.SetIdempotencyReportIDParams{
			ID: record.ID, ReportID: report.ID,
		})
		if err != nil {
			return store.IdempotencyRecord{}, databaseFailure("associate report with idempotency record", err)
		}
		if rows != 1 {
			return store.IdempotencyRecord{}, errors.New("idempotency report association was not updated")
		}
		record.ReportID = report.ID
	}
	if err := tx.Commit(ctx); err != nil {
		return store.IdempotencyRecord{}, databaseFailure("commit idempotency transaction", err)
	}
	return record, nil
}

func requestHash(request httpapi.GenerateRequest) (string, error) {
	assetTypes := append([]httpapi.AssetType(nil), request.AssetTypes...)
	sort.Slice(assetTypes, func(i, j int) bool { return assetTypes[i] < assetTypes[j] })
	body, err := json.Marshal(struct {
		AssetTypes []httpapi.AssetType `json:"assetTypes"`
	}{AssetTypes: assetTypes})
	if err != nil {
		return "", fmt.Errorf("encode recommendation request for idempotency: %w", err)
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}

func decodeStoredResponse(body []byte) (httpapi.GenerateResponse, error) {
	var response httpapi.GenerateResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return httpapi.GenerateResponse{}, fmt.Errorf("decode stored recommendation response: %w", err)
	}
	if response.ReportID == "" || response.Status != "COMPLETED" {
		return httpapi.GenerateResponse{}, errors.New("stored recommendation response is incomplete")
	}
	return response, nil
}

func failExecution(ctx context.Context, queries *store.Queries, id, reportID pgtype.UUID) error {
	reportRows, err := queries.FailRecommendationReport(ctx, reportID)
	if err != nil {
		return err
	}
	if reportRows != 1 {
		return errors.New("recommendation report was not in PROCESSING state")
	}
	idempotencyRows, err := queries.FailIdempotencyRecord(ctx, id)
	if err != nil {
		return err
	}
	if idempotencyRows != 1 {
		return errors.New("idempotency record was not in PROCESSING state")
	}
	return nil
}

func shouldKeepProcessing(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var transportError *agentclient.TransportError
	if errors.As(err, &transportError) {
		return transportError.Retryable
	}
	var agentError *agentclient.Error
	return errors.As(err, &agentError) && agentError.Retryable
}

func failureForAgent(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return httpapi.ErrProcessing
	}
	var transportError *agentclient.TransportError
	if errors.As(err, &transportError) {
		return &httpapi.Failure{
			Status: http.StatusServiceUnavailable, Code: "AGENT_UNAVAILABLE",
			Message: "The agent service could not be reached; retry with the same Idempotency-Key.", Retryable: true,
		}
	}
	var agentError *agentclient.Error
	if errors.As(err, &agentError) {
		status := agentError.StatusCode
		if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity &&
			status != http.StatusTooManyRequests && status != http.StatusBadGateway &&
			status != http.StatusServiceUnavailable {
			status = http.StatusBadGateway
		}
		return &httpapi.Failure{
			Status: status, Code: agentError.Code, Message: agentError.Message, Retryable: agentError.Retryable,
		}
	}
	return &httpapi.Failure{
		Status: http.StatusBadGateway, Code: "INVALID_AGENT_RESPONSE",
		Message: "The agent returned a response that violated the recommendation contract.", Retryable: false,
	}
}

func databaseFailure(operation string, err error) error {
	slog.Error("recommendation database operation failed", "operation", operation, "error", err)
	failure := &httpapi.Failure{
		Status: http.StatusServiceUnavailable, Code: "DATABASE_UNAVAILABLE",
		Message: "Unable to " + operation + "; retry with the same Idempotency-Key.", Retryable: true,
	}
	return fmt.Errorf("%w: %v", failure, err)
}

func logAgentFailure(reportID pgtype.UUID, err error) {
	var agentError *agentclient.Error
	if errors.As(err, &agentError) {
		slog.Warn("agent recommendation request failed", "reportId", uuidString(reportID),
			"status", agentError.StatusCode, "code", agentError.Code, "retryable", agentError.Retryable)
		return
	}
	var transportError *agentclient.TransportError
	if errors.As(err, &transportError) {
		slog.Warn("agent transport failed", "reportId", uuidString(reportID), "retryable", transportError.Retryable)
		return
	}
	slog.Error("agent returned an invalid recommendation", "reportId", uuidString(reportID))
}

func uuidString(id pgtype.UUID) string {
	raw := id.Bytes
	encoded := hex.EncodeToString(raw[:])
	return strings.Join([]string{encoded[:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:]}, "-")
}
