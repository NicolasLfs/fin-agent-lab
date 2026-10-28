package recommendation

import (
	"reflect"
	"testing"

	"fin-agent/internal/httpapi"
)

func TestCalculateNetReferenceRate(t *testing.T) {
	got, err := CalculateNetReferenceRate("0.0650", 1095)
	if err != nil {
		t.Fatalf("CalculateNetReferenceRate() error = %v", err)
	}
	if got != "0.0558" {
		t.Fatalf("CalculateNetReferenceRate() = %q, want %q", got, "0.0558")
	}
}

func TestRankCandidatesUsesAnnualDecimalScoresAndTickerTieBreak(t *testing.T) {
	got, err := RankCandidates([]Candidate{
		{Ticker: "SELIC_SGS_432", Type: httpapi.AssetTypeFixedIncome, GrossAnnualRate: "0.0650"},
		{Ticker: "VALE3", Type: httpapi.AssetTypeStock, PriceToEarnings: "10"},
		{Ticker: "ABEV3", Type: httpapi.AssetTypeStock, PriceToEarnings: "10"},
		{Ticker: "HGLG11", Type: httpapi.AssetTypeFII, DividendYieldTTM: "0.0800"},
	})
	if err != nil {
		t.Fatalf("RankCandidates() error = %v", err)
	}

	want := []httpapi.RankedAsset{
		{Rank: 1, Ticker: "ABEV3", Type: httpapi.AssetTypeStock, Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
		{Rank: 2, Ticker: "VALE3", Type: httpapi.AssetTypeStock, Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
		{Rank: 3, Ticker: "HGLG11", Type: httpapi.AssetTypeFII, Score: "0.0800", ScoreMetric: "DIVIDEND_YIELD_TTM"},
		{Rank: 4, Ticker: "SELIC_SGS_432", Type: httpapi.AssetTypeFixedIncome, Score: "0.0558", ScoreMetric: "REFERENCE_INDEX_NET_RATE"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RankCandidates() = %#v, want %#v", got, want)
	}
}

func TestRankCandidatesExcludesMissingAndNonPositiveFundamentals(t *testing.T) {
	got, err := RankCandidates([]Candidate{
		{Ticker: "NEGATIVE", Type: httpapi.AssetTypeStock, PriceToEarnings: "-5"},
		{Ticker: "ZERO", Type: httpapi.AssetTypeStock, PriceToEarnings: "0"},
		{Ticker: "MISSING", Type: httpapi.AssetTypeFII},
		{Ticker: "VALID", Type: httpapi.AssetTypeStock, PriceToEarnings: "20"},
	})
	if err != nil {
		t.Fatalf("RankCandidates() error = %v", err)
	}
	want := []httpapi.RankedAsset{
		{Rank: 1, Ticker: "VALID", Type: httpapi.AssetTypeStock, Score: "0.0500", ScoreMetric: "EARNINGS_YIELD"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RankCandidates() = %#v, want %#v", got, want)
	}
}

func TestRankCandidatesRejectsInvalidMetricValues(t *testing.T) {
	_, err := RankCandidates([]Candidate{
		{Ticker: "BAD", Type: httpapi.AssetTypeFII, DividendYieldTTM: "1e-2"},
	})
	if err == nil {
		t.Fatal("RankCandidates() error = nil, want invalid decimal error")
	}
}

func TestRankCandidatesComparesExactValuesBeforeFourPlaceSerialization(t *testing.T) {
	got, err := RankCandidates([]Candidate{
		{Ticker: "APE", Type: httpapi.AssetTypeStock, PriceToEarnings: "10.0002"},
		{Ticker: "ZPE", Type: httpapi.AssetTypeStock, PriceToEarnings: "10.0001"},
	})
	if err != nil {
		t.Fatalf("RankCandidates() error = %v", err)
	}
	if got[0].Ticker != "ZPE" || got[1].Ticker != "APE" {
		t.Fatalf("RankCandidates() order = [%s, %s], want [ZPE, APE]", got[0].Ticker, got[1].Ticker)
	}
	if got[0].Score != "0.1000" || got[1].Score != "0.1000" {
		t.Fatalf("serialized scores = [%s, %s], want both 0.1000", got[0].Score, got[1].Score)
	}
}

func TestCalculateNetReferenceRateRejectsUnsupportedHorizonAndFloatSyntax(t *testing.T) {
	for _, test := range []struct {
		name string
		rate string
		days int
	}{
		{name: "unsupported horizon", rate: "0.0650", days: 1094},
		{name: "exponent notation", rate: "6.5e-2", days: 1095},
		{name: "negative reference rate", rate: "-0.01", days: 1095},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CalculateNetReferenceRate(test.rate, test.days); err == nil {
				t.Fatal("CalculateNetReferenceRate() error = nil, want validation error")
			}
		})
	}
}
