package marketworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"fin-agent/internal/httpapi"
	"fin-agent/internal/marketdata"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	selicTicker = "SELIC_SGS_432"
	ipcaTicker  = "IPCA_SGS_433"
)

type Source interface {
	FetchSGS(context.Context, int) (marketdata.Observation, error)
	FetchStock(context.Context, string) (marketdata.Quote, marketdata.StockIndicators, error)
	FetchFII(context.Context, string) (marketdata.Quote, marketdata.FIIIndicators, error)
}

type Repository interface {
	EnsureReferenceAssets(context.Context) error
	ListMarketAssets(context.Context) ([]Asset, error)
	SaveQuote(context.Context, Asset, marketdata.Quote, time.Time) error
	SaveIndicator(context.Context, Asset, Indicator) error
	RecordSyncStatus(context.Context, SyncStatus) error
}

type Asset struct {
	ID     pgtype.UUID
	Ticker string
	Type   httpapi.AssetType
}

type Indicator struct {
	Source              string
	SourceRecordID      string
	CollectedAt         time.Time
	PriceToEarnings     string
	DividendYieldTTM    string
	AnnualReferenceRate string
	InflationRate       string
	RawPayload          json.RawMessage
}

type SyncStatus struct {
	AssetID     AssetID
	DataKind    string
	Source      string
	AttemptedAt time.Time
	Code        string
	Message     string
	Retryable   bool
}

type AssetID = [16]byte

type Worker struct {
	repository Repository
	source     Source
	now        func() time.Time
}

func New(repository Repository, source Source, now func() time.Time) *Worker {
	if now == nil {
		now = time.Now
	}
	return &Worker{repository: repository, source: source, now: now}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	if err := w.repository.EnsureReferenceAssets(ctx); err != nil {
		return fmt.Errorf("ensure official reference assets: %w", err)
	}
	assets, err := w.repository.ListMarketAssets(ctx)
	if err != nil {
		return fmt.Errorf("list assets for market synchronization: %w", err)
	}
	var failures []error
	for _, asset := range assets {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if err := w.syncAsset(ctx, asset); err != nil {
			failures = append(failures, fmt.Errorf("sync %s: %w", asset.Ticker, err))
		}
	}
	return errors.Join(failures...)
}

func (w *Worker) syncAsset(ctx context.Context, asset Asset) error {
	now := w.now().UTC()
	switch asset.Ticker {
	case selicTicker:
		return w.syncSGS(ctx, asset, 432, "REFERENCE_RATE", "BCB_SGS:432", now, false)
	case ipcaTicker:
		return w.syncSGS(ctx, asset, 433, "INFLATION", "BCB_SGS:433", now, true)
	}

	switch asset.Type {
	case httpapi.AssetTypeStock:
		quote, indicators, err := w.source.FetchStock(ctx, asset.Ticker)
		if err != nil {
			return w.recordFailure(ctx, asset, "FUNDAMENTALS", "BRAPI", now, err)
		}
		if err := w.repository.SaveQuote(ctx, asset, quote, now); err != nil {
			return fmt.Errorf("persist quote: %w", err)
		}
		rawPayload, err := collectedPayload(now, quote.RawPayload, indicators.RawPayload)
		if err != nil {
			return fmt.Errorf("encode stock indicator audit payload: %w", err)
		}
		indicator := Indicator{
			Source: "BRAPI", SourceRecordID: contentRecordID(asset.Ticker, "statistics", indicators.RawPayload),
			CollectedAt: now, PriceToEarnings: indicators.PriceToEarnings, RawPayload: rawPayload,
		}
		if err := w.repository.SaveIndicator(ctx, asset, indicator); err != nil {
			return fmt.Errorf("persist stock indicators: %w", err)
		}
		return errors.Join(
			w.recordSuccess(ctx, asset, "QUOTE", "BRAPI", now),
			w.recordSuccess(ctx, asset, "FUNDAMENTALS", "BRAPI", now),
		)
	case httpapi.AssetTypeFII:
		quote, indicators, err := w.source.FetchFII(ctx, asset.Ticker)
		if err != nil {
			return w.recordFailure(ctx, asset, "FUNDAMENTALS", "BRAPI", now, err)
		}
		if err := w.repository.SaveQuote(ctx, asset, quote, now); err != nil {
			return fmt.Errorf("persist quote: %w", err)
		}
		rawPayload, err := collectedPayload(now, quote.RawPayload, indicators.RawPayload)
		if err != nil {
			return fmt.Errorf("encode FII indicator audit payload: %w", err)
		}
		indicator := Indicator{
			Source: "BRAPI", SourceRecordID: contentRecordID(asset.Ticker, "fii-indicators", indicators.RawPayload),
			CollectedAt: now, DividendYieldTTM: indicators.DividendYieldTTM, RawPayload: rawPayload,
		}
		if err := w.repository.SaveIndicator(ctx, asset, indicator); err != nil {
			return fmt.Errorf("persist FII indicators: %w", err)
		}
		return errors.Join(
			w.recordSuccess(ctx, asset, "QUOTE", "BRAPI", now),
			w.recordSuccess(ctx, asset, "FUNDAMENTALS", "BRAPI", now),
		)
	default:
		return w.recordFailure(ctx, asset, "FUNDAMENTALS", "BRAPI", now,
			errors.New("asset type has no configured market-data connector"))
	}
}

func (w *Worker) syncSGS(
	ctx context.Context,
	asset Asset,
	seriesID int,
	dataKind string,
	source string,
	now time.Time,
	inflation bool,
) error {
	observation, err := w.source.FetchSGS(ctx, seriesID)
	if err != nil {
		return w.recordFailure(ctx, asset, dataKind, source, now, err)
	}
	rawPayload, err := collectedPayload(now, observation.RawPayload)
	if err != nil {
		return fmt.Errorf("encode SGS %d audit payload: %w", seriesID, err)
	}
	indicator := Indicator{
		Source: source, SourceRecordID: observation.SourceRecordID,
		CollectedAt: now, RawPayload: rawPayload,
	}
	if inflation {
		indicator.InflationRate = observation.Value
	} else {
		indicator.AnnualReferenceRate = observation.Value
	}
	if err := w.repository.SaveIndicator(ctx, asset, indicator); err != nil {
		return fmt.Errorf("persist SGS %d indicator: %w", seriesID, err)
	}
	return w.recordSuccess(ctx, asset, dataKind, source, now)
}

func (w *Worker) recordSuccess(ctx context.Context, asset Asset, dataKind, source string, now time.Time) error {
	if err := w.repository.RecordSyncStatus(ctx, SyncStatus{
		AssetID: asset.ID.Bytes, DataKind: dataKind, Source: source, AttemptedAt: now,
	}); err != nil {
		return fmt.Errorf("persist %s success status: %w", dataKind, err)
	}
	return nil
}

func (w *Worker) recordFailure(ctx context.Context, asset Asset, dataKind, source string, now time.Time, cause error) error {
	retryable := isRetryable(cause)
	code := "MARKET_DATA_UNAVAILABLE"
	if retryable {
		code = "MARKET_DATA_UPSTREAM_UNAVAILABLE"
	}
	if err := w.repository.RecordSyncStatus(ctx, SyncStatus{
		AssetID: asset.ID.Bytes, DataKind: dataKind, Source: source, AttemptedAt: now,
		Code: code, Message: safeFailureMessage(cause), Retryable: retryable,
	}); err != nil {
		return errors.Join(cause, fmt.Errorf("persist %s failure status: %w", dataKind, err))
	}
	return cause
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var statusError *marketdata.HTTPError
	if errors.As(err, &statusError) {
		return statusError.Retryable()
	}
	var networkError net.Error
	if errors.As(err, &networkError) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return false
}

func safeFailureMessage(err error) string {
	var statusError *marketdata.HTTPError
	if errors.As(err, &statusError) {
		return statusError.Error()
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func contentRecordID(ticker, kind string, payload []byte) string {
	sum := sha256.Sum256(payload)
	return ticker + ":" + kind + ":" + hex.EncodeToString(sum[:])
}

func collectedPayload(collectedAt time.Time, payloads ...json.RawMessage) (json.RawMessage, error) {
	encoded, err := json.Marshal(struct {
		CollectedAt time.Time         `json:"collectedAt"`
		SourceData  []json.RawMessage `json:"sourceData"`
	}{CollectedAt: collectedAt.UTC(), SourceData: payloads})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}
