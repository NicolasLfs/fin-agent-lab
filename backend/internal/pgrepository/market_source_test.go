package pgrepository

import (
	"context"
	"time"

	"fin-agent/internal/marketdata"
)

type integrationMarketDataSource struct {
	collectedAt time.Time
}

func (s integrationMarketDataSource) FetchSGS(_ context.Context, seriesID int) (marketdata.Observation, error) {
	value := "0.1375"
	record := "432:2026-10-28"
	raw := `{"data":"28/10/2026","valor":"13.75"}`
	if seriesID == 433 {
		value = "0.0082"
		record = "433:2026-09-28"
		raw = `{"data":"28/09/2026","valor":"0.82"}`
	}
	return marketdata.Observation{
		SeriesID: seriesID, ObservedAt: s.collectedAt, Value: value,
		SourceRecordID: record, RawPayload: []byte(raw),
	}, nil
}

func (s integrationMarketDataSource) FetchStock(
	_ context.Context,
	ticker string,
) (marketdata.Quote, marketdata.StockIndicators, error) {
	return marketdata.Quote{
			Ticker: ticker, Price: "68.50", Currency: "BRL", QuotedAt: s.collectedAt,
			SourceRecordID: ticker + ":quote-integration", RawPayload: []byte(`{"regularMarketPrice":68.50}`),
		}, marketdata.StockIndicators{
			PriceToEarnings: "10", RawPayload: []byte(`{"trailingPE":10}`),
		}, nil
}

func (s integrationMarketDataSource) FetchFII(
	_ context.Context,
	ticker string,
) (marketdata.Quote, marketdata.FIIIndicators, error) {
	return marketdata.Quote{
			Ticker: ticker, Price: "100.00", Currency: "BRL", QuotedAt: s.collectedAt,
			SourceRecordID: ticker + ":quote-integration", RawPayload: []byte(`{"regularMarketPrice":100}`),
		}, marketdata.FIIIndicators{
			Price: "100.00", DividendYieldTTM: "0.05", ObservedAt: s.collectedAt,
			RawPayload: []byte(`{"dividendYield12m":0.05}`),
		}, nil
}
