package pgrepository

import (
	"errors"
	"testing"
	"time"

	"fin-agent/internal/httpapi"
	"fin-agent/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestRequestHashIsIndependentOfAssetTypeOrder(t *testing.T) {
	first, err := requestHash(httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock, httpapi.AssetTypeFII}})
	if err != nil {
		t.Fatalf("requestHash() error = %v", err)
	}

	second, err := requestHash(httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeFII, httpapi.AssetTypeStock}})
	if err != nil {
		t.Fatalf("requestHash() error = %v", err)
	}
	if first != second {
		t.Fatalf("request hashes differ for equivalent requests: %q != %q", first, second)
	}
}

func TestMarketDataFreshnessUses24HourLimit(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{name: "exactly 24 hours", at: now.Add(-24 * time.Hour), want: true},
		{name: "older than 24 hours", at: now.Add(-24*time.Hour - time.Nanosecond), want: false},
		{name: "slightly future timestamp", at: now.Add(time.Minute), want: true},
		{name: "implausible future timestamp", at: now.Add(6 * time.Minute), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isFresh(test.at, now); got != test.want {
				t.Fatalf("isFresh() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFailureFromRecordPreservesNonRetryableMarketDataResponse(t *testing.T) {
	failure := failureFromRecord(store.IdempotencyRecord{
		FailureStatus:    pgtype.Int4{Int32: 422, Valid: true},
		FailureCode:      pgtype.Text{String: "MARKET_DATA_INSUFFICIENT", Valid: true},
		FailureMessage:   pgtype.Text{String: "No FII data.", Valid: true},
		FailureRetryable: pgtype.Bool{Bool: false, Valid: true},
	})
	if failure.Status != 422 || failure.Code != "MARKET_DATA_INSUFFICIENT" ||
		failure.Message != "No FII data." || failure.Retryable {
		t.Fatalf("failureFromRecord() = %#v", failure)
	}
}

func TestBuildRecommendationPlanRequiresEveryRequestedAssetType(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	_, err := buildRecommendationPlan(
		[]store.ListRecommendationCandidatesRow{freshStockCandidate(now)},
		[]httpapi.AssetType{httpapi.AssetTypeStock, httpapi.AssetTypeFII},
		now,
	)
	var failure *httpapi.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("buildRecommendationPlan() error = %v, want *httpapi.Failure", err)
	}
	if failure.Status != 422 || failure.Retryable || failure.Code != "MARKET_DATA_INSUFFICIENT" {
		t.Fatalf("market-data failure = %#v, want non-retryable 422", failure)
	}
}

func TestBuildRecommendationPlanReturnsRetryableFailureForStaleData(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	row := freshStockCandidate(now)
	row.QuotedAt.Time = now.Add(-25 * time.Hour)
	row.IndicatorObservedAt.Time = now.Add(-25 * time.Hour)

	_, err := buildRecommendationPlan(
		[]store.ListRecommendationCandidatesRow{row},
		[]httpapi.AssetType{httpapi.AssetTypeStock},
		now,
	)
	var failure *httpapi.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("buildRecommendationPlan() error = %v, want *httpapi.Failure", err)
	}
	if failure.Status != 503 || !failure.Retryable || failure.Code != "MARKET_DATA_STALE" {
		t.Fatalf("market-data failure = %#v, want retryable 503", failure)
	}
}

func TestBuildRecommendationPlanReturnsRetryableFailureForProviderOutage(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	row := store.ListRecommendationCandidatesRow{
		ID:               pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		Ticker:           "VALE3",
		AssetType:        store.AssetTypeSTOCK,
		SyncErrorCode:    "MARKET_DATA_UPSTREAM_UNAVAILABLE",
		SyncRetryable:    true,
		SyncErrorMessage: "Brapi returned status 503",
	}

	_, err := buildRecommendationPlan(
		[]store.ListRecommendationCandidatesRow{row},
		[]httpapi.AssetType{httpapi.AssetTypeStock},
		now,
	)
	var failure *httpapi.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("buildRecommendationPlan() error = %v, want *httpapi.Failure", err)
	}
	if failure.Status != 503 || !failure.Retryable || failure.Code != "MARKET_DATA_UPSTREAM_UNAVAILABLE" {
		t.Fatalf("market-data failure = %#v, want retryable provider outage", failure)
	}
}

func TestBuildRecommendationPlanReturnsRetryableFailureBeforeFirstSync(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	row := store.ListRecommendationCandidatesRow{
		ID:        pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		Ticker:    "VALE3",
		AssetType: store.AssetTypeSTOCK,
	}

	_, err := buildRecommendationPlan(
		[]store.ListRecommendationCandidatesRow{row},
		[]httpapi.AssetType{httpapi.AssetTypeStock},
		now,
	)
	var failure *httpapi.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("buildRecommendationPlan() error = %v, want *httpapi.Failure", err)
	}
	if failure.Status != 503 || !failure.Retryable || failure.Code != "MARKET_DATA_SYNC_PENDING" {
		t.Fatalf("market-data failure = %#v, want retryable pending synchronization", failure)
	}
}

func TestBuildRecommendationPlanKeepsPermanentProviderFailuresNonRetryable(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	row := store.ListRecommendationCandidatesRow{
		ID:                pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		Ticker:            "HGLG11",
		AssetType:         store.AssetTypeFII,
		SyncAttemptedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		SyncErrorCode:     "MARKET_DATA_UNAVAILABLE",
		SyncErrorMessage:  "Brapi plan does not include this indicator",
		SyncRetryable:     false,
	}

	_, err := buildRecommendationPlan(
		[]store.ListRecommendationCandidatesRow{row},
		[]httpapi.AssetType{httpapi.AssetTypeFII},
		now,
	)
	var failure *httpapi.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("buildRecommendationPlan() error = %v, want *httpapi.Failure", err)
	}
	if failure.Status != 422 || failure.Retryable {
		t.Fatalf("market-data failure = %#v, want non-retryable 422", failure)
	}
}

func TestBuildRecommendationPlanBuildsGoOwnedScoreAndContext(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	plan, err := buildRecommendationPlan(
		[]store.ListRecommendationCandidatesRow{freshStockCandidate(now)},
		[]httpapi.AssetType{httpapi.AssetTypeStock},
		now,
	)
	if err != nil {
		t.Fatalf("buildRecommendationPlan() error = %v", err)
	}
	if len(plan.RankedAssets) != 1 || plan.RankedAssets[0].Score != "0.1000" ||
		plan.RankedAssets[0].ScoreMetric != "EARNINGS_YIELD" {
		t.Fatalf("Go ranking = %#v", plan.RankedAssets)
	}
	if len(plan.Context.StocksAndFiis) != 1 || plan.Context.StocksAndFiis[0].ReferencePrice != "68.50" {
		t.Fatalf("deterministic context = %#v", plan.Context)
	}
	if len(plan.QuoteIDs) != 1 || !plan.QuoteIDs[0].Valid {
		t.Fatalf("audited quote IDs = %#v", plan.QuoteIDs)
	}
}

func freshStockCandidate(now time.Time) store.ListRecommendationCandidatesRow {
	return store.ListRecommendationCandidatesRow{
		ID:                  pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
		Ticker:              "VALE3",
		AssetType:           store.AssetTypeSTOCK,
		QuoteID:             pgtype.UUID{Bytes: [16]byte{2}, Valid: true},
		ReferencePrice:      "68.50",
		Currency:            "BRL",
		QuotedAt:            pgtype.Timestamptz{Time: now, Valid: true},
		PriceToEarnings:     "10",
		IndicatorSource:     "Brapi",
		IndicatorObservedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}
}

func TestRequestHashChangesWhenSelectedTypesChange(t *testing.T) {
	first, err := requestHash(httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock}})
	if err != nil {
		t.Fatalf("requestHash() error = %v", err)
	}
	second, err := requestHash(httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeFII}})
	if err != nil {
		t.Fatalf("requestHash() error = %v", err)
	}
	if first == second {
		t.Fatal("requestHash() returned equal values for different requests")
	}
}
