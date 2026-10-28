package pgrepository

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fin-agent/internal/agentclient"
	"fin-agent/internal/httpapi"
	"fin-agent/internal/marketworker"
	"fin-agent/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExecuteIsIdempotentAcrossConcurrentRequestsAndRetries(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL 18 idempotency integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	adminConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	adminConn, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	defer adminConn.Close(context.Background())

	schemaName := fmt.Sprintf("pgrepository_test_%d", time.Now().UnixNano())
	if _, err := adminConn.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	defer func() {
		if _, err := adminConn.Exec(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE"); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	}()

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL pool config: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaName
	poolConfig.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	defer pool.Close()

	for _, migrationName := range []string{
		"000001_persistence_domain.up.sql",
		"000002_deterministic_market_indicators.up.sql",
		"000003_market_data_sync_status.up.sql",
	} {
		migration, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", migrationName))
		if err != nil {
			t.Fatalf("read migration %s: %v", migrationName, err)
		}
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply migration %s: %v", migrationName, err)
		}
	}
	var assetID string
	if err := pool.QueryRow(ctx,
		"INSERT INTO assets (ticker, asset_type, name) VALUES ('TEST3', 'STOCK', 'Test asset') RETURNING id",
	).Scan(&assetID); err != nil {
		t.Fatalf("insert test asset: %v", err)
	}
	if err := marketworker.New(
		marketworker.NewPostgresRepository(pool),
		integrationMarketDataSource{collectedAt: time.Now().UTC()},
		time.Now,
	).RunOnce(ctx); err != nil {
		t.Fatalf("run market ingestion worker: %v", err)
	}

	const concurrentNewsIngestions = 50
	newsStart := make(chan struct{})
	newsIDs := make(chan pgtype.UUID, concurrentNewsIngestions)
	newsErrors := make(chan error, concurrentNewsIngestions)
	newsParams := store.UpsertMarketNewsParams{
		Title:       "Concurrent duplicate",
		ContentHash: strings.Repeat("a", 64),
		Source:      "test",
		Url:         "https://example.test/news",
		PublishedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}

	var newsWorkers sync.WaitGroup
	for range concurrentNewsIngestions {
		newsWorkers.Add(1)
		go func() {
			defer newsWorkers.Done()
			<-newsStart
			item, err := store.New(pool).UpsertMarketNews(ctx, newsParams)
			if err != nil {
				newsErrors <- err
				return
			}
			newsIDs <- item.ID
		}()
	}
	close(newsStart)
	newsWorkers.Wait()
	close(newsIDs)
	close(newsErrors)
	for err := range newsErrors {
		t.Errorf("concurrent news upsert error = %v", err)
	}
	var persistedNewsID pgtype.UUID
	for id := range newsIDs {
		if !persistedNewsID.Valid {
			persistedNewsID = id
		}
		if id != persistedNewsID {
			t.Errorf("concurrent news IDs differ: %v and %v", id, persistedNewsID)
		}
	}
	if !persistedNewsID.Valid {
		t.Fatal("no concurrent news upsert returned a persistent ID")
	}
	var newsCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM market_news WHERE content_hash = $1", newsParams.ContentHash).Scan(&newsCount); err != nil {
		t.Fatalf("count concurrent news records: %v", err)
	}
	if newsCount != 1 {
		t.Fatalf("concurrent news record count = %d, want 1", newsCount)
	}

	repository := New(pool)
	request := httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock}}
	const concurrentRequests = 10
	start := make(chan struct{})
	responses := make(chan httpapi.GenerateResponse, concurrentRequests)
	errorsFound := make(chan error, concurrentRequests)
	var agentCalls atomic.Int32
	var workers sync.WaitGroup

	invoke := func(_ context.Context, payload agentclient.RunRequest) (agentclient.RunResponse, error) {
		if len(payload.RankedAssets) != 1 || payload.RankedAssets[0].Ticker != "TEST3" ||
			payload.RankedAssets[0].Score != "0.1000" ||
			payload.DeterministicContext.StocksAndFiis[0].ReferencePrice != "68.50" {
			return agentclient.RunResponse{}, fmt.Errorf("unexpected Go-calculated agent request: %#v", payload)
		}
		agentCalls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return agentclient.RunResponse{
			Status: "COMPLETED",
			RankedAssets: []agentclient.RankedAsset{
				{Rank: 1, Ticker: "TEST3", Type: "STOCK", Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
			},
			JustificationText: "Grounded test response.",
			CitedNewsIDs:      []string{},
		}, nil
	}
	for range concurrentRequests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			response, err := repository.Execute(ctx, "same-key", request, invoke)
			if err != nil {
				errorsFound <- err
				return
			}
			responses <- response
		}()
	}
	close(start)
	workers.Wait()
	close(responses)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent Execute() error = %v", err)
	}

	var reportID string
	for response := range responses {
		if reportID == "" {
			reportID = response.ReportID
		}
		if response.ReportID != reportID || response.Status != "COMPLETED" {
			t.Errorf("concurrent response = %#v, want completed report %q", response, reportID)
		}
	}
	if reportID == "" {
		t.Fatal("no concurrent request returned a completed response")
	}
	if got := agentCalls.Load(); got != 1 {
		t.Fatalf("agent invocations = %d, want 1", got)
	}

	var reportCount, keyCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recommendation_reports").Scan(&reportCount); err != nil {
		t.Fatalf("count recommendation reports: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM idempotency_records").Scan(&keyCount); err != nil {
		t.Fatalf("count idempotency records: %v", err)
	}
	if reportCount != 1 || keyCount != 1 {
		t.Fatalf("persisted reports/keys = %d/%d, want 1/1", reportCount, keyCount)
	}

	var originalReportID string
	_, err = repository.Execute(ctx, "retry-key", request, func(_ context.Context, payload agentclient.RunRequest) (agentclient.RunResponse, error) {
		originalReportID = payload.ReportID
		return agentclient.RunResponse{}, &agentclient.TransportError{
			Err: context.DeadlineExceeded, Retryable: true,
		}
	})
	if !errors.Is(err, httpapi.ErrProcessing) {
		t.Fatalf("timeout Execute() error = %v, want pending processing response", err)
	}
	var idempotencyStatus, reportStatus string
	if err := pool.QueryRow(ctx,
		`SELECT idempotency.status::text, reports.status::text
		 FROM idempotency_records AS idempotency
		 JOIN recommendation_reports AS reports ON reports.id = idempotency.report_id
		 WHERE idempotency.operation = 'recommendations.generate'
		   AND idempotency.idempotency_key = 'retry-key'`,
	).Scan(&idempotencyStatus, &reportStatus); err != nil {
		t.Fatalf("load timeout execution state: %v", err)
	}
	if idempotencyStatus != "PROCESSING" || reportStatus != "PROCESSING" {
		t.Fatalf("timeout state = idempotency %q/report %q, want both PROCESSING", idempotencyStatus, reportStatus)
	}

	retried, err := repository.Execute(ctx, "retry-key", request, func(_ context.Context, payload agentclient.RunRequest) (agentclient.RunResponse, error) {
		if payload.ReportID != originalReportID {
			t.Errorf("retry reportId = %q, want %q", payload.ReportID, originalReportID)
		}
		return invoke(context.Background(), payload)
	})
	if err != nil {
		t.Fatalf("retry Execute() error = %v", err)
	}
	if retried.ReportID != originalReportID {
		t.Fatalf("retried reportId = %q, want %q", retried.ReportID, originalReportID)
	}

	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recommendation_reports").Scan(&reportCount); err != nil {
		t.Fatalf("count recommendation reports after retry: %v", err)
	}
	if reportCount != 2 {
		t.Fatalf("recommendation report count after retry = %d, want 2", reportCount)
	}

	invokedWithoutFIIData := false
	_, missingErr := repository.Execute(ctx, "missing-fii", httpapi.GenerateRequest{
		AssetTypes: []httpapi.AssetType{httpapi.AssetTypeFII},
	}, func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error) {
		invokedWithoutFIIData = true
		return agentclient.RunResponse{}, nil
	})
	var missingFailure *httpapi.Failure
	if !errors.As(missingErr, &missingFailure) || missingFailure.Status != http.StatusUnprocessableEntity ||
		missingFailure.Retryable || invokedWithoutFIIData {
		t.Fatalf("missing-data Execute() = error %v, agent invoked %v; want non-retryable 422", missingErr, invokedWithoutFIIData)
	}
	_, repeatedMissingErr := repository.Execute(ctx, "missing-fii", httpapi.GenerateRequest{
		AssetTypes: []httpapi.AssetType{httpapi.AssetTypeFII},
	}, func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error) {
		t.Fatal("agent must not run when FII data is absent")
		return agentclient.RunResponse{}, nil
	})
	var repeatedFailure *httpapi.Failure
	if !errors.As(repeatedMissingErr, &repeatedFailure) ||
		repeatedFailure.Status != missingFailure.Status || repeatedFailure.Code != missingFailure.Code {
		t.Fatalf("repeated missing-data failure = %v, want the original 422 failure", repeatedMissingErr)
	}

	var staleFIIID string
	if err := pool.QueryRow(ctx,
		"INSERT INTO assets (ticker, asset_type, name) VALUES ('STALE11', 'FII', 'Stale FII') RETURNING id",
	).Scan(&staleFIIID); err != nil {
		t.Fatalf("insert stale FII: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO quotes (asset_id, source, source_quote_id, price, currency, quoted_at) VALUES ($1, 'test', 'stale-quote', 10.00, 'BRL', now() - interval '25 hours')",
		staleFIIID,
	); err != nil {
		t.Fatalf("insert stale FII quote: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO asset_indicators (
			asset_id, source, source_record_id, observed_at, dividend_yield_ttm, raw_payload
		) VALUES ($1, 'test-provider', 'stale-dy', now() - interval '25 hours', 0.05, '{"dy":0.05}')`,
		staleFIIID,
	); err != nil {
		t.Fatalf("insert stale FII indicator: %v", err)
	}
	_, staleErr := repository.Execute(ctx, "stale-fii", httpapi.GenerateRequest{
		AssetTypes: []httpapi.AssetType{httpapi.AssetTypeFII},
	}, func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error) {
		t.Fatal("agent must not run with stale FII data")
		return agentclient.RunResponse{}, nil
	})
	var staleFailure *httpapi.Failure
	if !errors.As(staleErr, &staleFailure) || staleFailure.Status != http.StatusServiceUnavailable ||
		!staleFailure.Retryable {
		t.Fatalf("stale-data Execute() error = %v, want retryable 503", staleErr)
	}
}
