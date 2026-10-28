package marketworker

import (
	"context"
	"testing"
	"time"

	"fin-agent/internal/httpapi"
	"fin-agent/internal/marketdata"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestRunOncePersistsQuotesIndicatorsAndMacroReferences(t *testing.T) {
	now := time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC)
	repository := &fakeRepository{
		assets: []Asset{
			{ID: testUUID(1), Ticker: "SELIC_SGS_432", Type: httpapi.AssetTypeFixedIncome},
			{ID: testUUID(2), Ticker: "IPCA_SGS_433", Type: httpapi.AssetTypeFixedIncome},
			{ID: testUUID(3), Ticker: "VALE3", Type: httpapi.AssetTypeStock},
			{ID: testUUID(4), Ticker: "HGLG11", Type: httpapi.AssetTypeFII},
		},
	}
	source := &fakeSource{
		observations: map[int]marketdata.Observation{
			432: {SeriesID: 432, ObservedAt: now.Add(-time.Hour), Value: "0.1375", SourceRecordID: "432:2026-10-28", RawPayload: []byte(`[{"data":"28/10/2026","valor":"13.75"}]`)},
			433: {SeriesID: 433, ObservedAt: now.Add(-24 * time.Hour), Value: "0.0082", SourceRecordID: "433:2026-09-28", RawPayload: []byte(`[{"data":"28/09/2026","valor":"0.82"}]`)},
		},
		stockQuote: marketdata.Quote{
			Ticker: "VALE3", Price: "61.03", Currency: "BRL", QuotedAt: now.Add(-time.Minute),
			SourceRecordID: "VALE3:2026-10-28T19:59:00Z", RawPayload: []byte(`{"regularMarketPrice":61.03}`),
		},
		stockIndicators: marketdata.StockIndicators{PriceToEarnings: "10.0002", RawPayload: []byte(`{"trailingPE":10.0002}`)},
		fiiQuote: marketdata.Quote{
			Ticker: "HGLG11", Price: "149.72", Currency: "BRL", QuotedAt: now.Add(-time.Minute),
			SourceRecordID: "HGLG11:2026-10-28T19:59:00Z", RawPayload: []byte(`{"regularMarketPrice":149.72}`),
		},
		fiiIndicators: marketdata.FIIIndicators{
			Price: "149.72", DividendYieldTTM: "0.08956719", ObservedAt: now.Add(-time.Hour),
			RawPayload: []byte(`{"dividendYield12m":0.08956719}`),
		},
	}

	err := New(repository, source, func() time.Time { return now }).RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if repository.ensureCalls != 1 {
		t.Fatalf("reference-asset ensure calls = %d, want 1", repository.ensureCalls)
	}
	if len(repository.quotes) != 2 {
		t.Fatalf("persisted quotes = %d, want 2", len(repository.quotes))
	}
	if len(repository.indicators) != 4 {
		t.Fatalf("persisted indicators = %d, want 4", len(repository.indicators))
	}
	if repository.indicators[0].AnnualReferenceRate != "0.1375" ||
		repository.indicators[1].InflationRate != "0.0082" ||
		repository.indicators[2].PriceToEarnings != "10.0002" ||
		repository.indicators[3].DividendYieldTTM != "0.08956719" {
		t.Fatalf("persisted indicator values = %#v", repository.indicators)
	}
	for _, status := range repository.syncStatuses {
		if status.Code != "" || status.Retryable || status.AttemptedAt != now {
			t.Errorf("sync status = %#v, want successful collection at %s", status, now)
		}
	}
}

func TestRunOncePersistsRetryableProviderFailuresForAPI(t *testing.T) {
	now := time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC)
	repository := &fakeRepository{assets: []Asset{
		{ID: testUUID(1), Ticker: "VALE3", Type: httpapi.AssetTypeStock},
	}}
	source := &fakeSource{stockErr: &marketdata.HTTPError{Provider: "Brapi", StatusCode: 503}}

	err := New(repository, source, func() time.Time { return now }).RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce() error = nil, want provider failure")
	}
	if len(repository.syncStatuses) != 1 {
		t.Fatalf("sync status count = %d, want 1", len(repository.syncStatuses))
	}
	status := repository.syncStatuses[0]
	if status.Retryable != true || status.Code != "MARKET_DATA_UPSTREAM_UNAVAILABLE" {
		t.Fatalf("persisted failure = %#v, want retryable upstream status", status)
	}
	if len(repository.quotes) != 0 || len(repository.indicators) != 0 {
		t.Fatalf("partial data persisted: quotes=%d indicators=%d", len(repository.quotes), len(repository.indicators))
	}
}

func TestRunOncePersistsNonRetryableProviderRestrictions(t *testing.T) {
	now := time.Date(2026, 10, 28, 20, 0, 0, 0, time.UTC)
	repository := &fakeRepository{assets: []Asset{
		{ID: testUUID(1), Ticker: "VALE3", Type: httpapi.AssetTypeStock},
	}}
	source := &fakeSource{stockErr: &marketdata.HTTPError{Provider: "Brapi", StatusCode: 403}}

	if err := New(repository, source, func() time.Time { return now }).RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce() error = nil, want provider-plan failure")
	}
	if len(repository.syncStatuses) != 1 {
		t.Fatalf("sync status count = %d, want 1", len(repository.syncStatuses))
	}
	status := repository.syncStatuses[0]
	if status.Retryable || status.Code != "MARKET_DATA_UNAVAILABLE" {
		t.Fatalf("persisted failure = %#v, want non-retryable structural/provider-plan failure", status)
	}
}

type fakeRepository struct {
	assets       []Asset
	ensureCalls  int
	quotes       []marketdata.Quote
	indicators   []Indicator
	syncStatuses []SyncStatus
}

func (r *fakeRepository) EnsureReferenceAssets(context.Context) error {
	r.ensureCalls++
	return nil
}

func (r *fakeRepository) ListMarketAssets(context.Context) ([]Asset, error) {
	return r.assets, nil
}

func (r *fakeRepository) SaveQuote(_ context.Context, _ Asset, quote marketdata.Quote, _ time.Time) error {
	r.quotes = append(r.quotes, quote)
	return nil
}

func (r *fakeRepository) SaveIndicator(_ context.Context, _ Asset, indicator Indicator) error {
	r.indicators = append(r.indicators, indicator)
	return nil
}

func (r *fakeRepository) RecordSyncStatus(_ context.Context, status SyncStatus) error {
	r.syncStatuses = append(r.syncStatuses, status)
	return nil
}

type fakeSource struct {
	observations    map[int]marketdata.Observation
	stockQuote      marketdata.Quote
	stockIndicators marketdata.StockIndicators
	stockErr        error
	fiiQuote        marketdata.Quote
	fiiIndicators   marketdata.FIIIndicators
}

func (s *fakeSource) FetchSGS(_ context.Context, seriesID int) (marketdata.Observation, error) {
	return s.observations[seriesID], nil
}

func (s *fakeSource) FetchStock(context.Context, string) (marketdata.Quote, marketdata.StockIndicators, error) {
	return s.stockQuote, s.stockIndicators, s.stockErr
}

func (s *fakeSource) FetchFII(context.Context, string) (marketdata.Quote, marketdata.FIIIndicators, error) {
	return s.fiiQuote, s.fiiIndicators, nil
}

func testUUID(last byte) pgtype.UUID {
	var bytes [16]byte
	bytes[15] = last
	return pgtype.UUID{Bytes: bytes, Valid: true}
}
