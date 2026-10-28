package marketworker

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"fin-agent/internal/domain"
	"fin-agent/internal/httpapi"
	"fin-agent/internal/marketdata"
	"fin-agent/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var postgresDecimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) EnsureReferenceAssets(ctx context.Context) error {
	queries := store.New(r.pool)
	for _, reference := range []struct {
		ticker string
		name   string
	}{
		{ticker: selicTicker, name: "Taxa Selic diária de referência (SGS 432)"},
		{ticker: ipcaTicker, name: "IPCA mensal de referência (SGS 433)"},
	} {
		asset, err := queries.UpsertMarketAsset(ctx, store.UpsertMarketAssetParams{
			Ticker: reference.ticker, AssetType: store.AssetTypeFIXEDINCOME, Name: reference.name,
		})
		if err != nil {
			return fmt.Errorf("upsert reference asset %s: %w", reference.ticker, err)
		}
		if asset.AssetType != store.AssetTypeFIXEDINCOME {
			return fmt.Errorf("reference ticker %s is already registered as %s", reference.ticker, asset.AssetType)
		}
	}
	return nil
}

func (r *PostgresRepository) ListMarketAssets(ctx context.Context) ([]Asset, error) {
	rows, err := store.New(r.pool).ListMarketAssets(ctx)
	if err != nil {
		return nil, fmt.Errorf("query market assets: %w", err)
	}
	assets := make([]Asset, 0, len(rows))
	for _, row := range rows {
		assets = append(assets, Asset{
			ID: row.ID, Ticker: row.Ticker, Type: httpapi.AssetType(row.AssetType),
		})
	}
	return assets, nil
}

func (r *PostgresRepository) SaveQuote(ctx context.Context, asset Asset, quote marketdata.Quote, now time.Time) error {
	if quote.Ticker != asset.Ticker {
		return fmt.Errorf("quote ticker %q does not match asset %q", quote.Ticker, asset.Ticker)
	}
	money, err := domain.NewMoney(quote.Price, quote.Currency)
	if err != nil || money.Amount() == "0.00" || strings.HasPrefix(money.Amount(), "-") {
		return fmt.Errorf("quote for %s violates the positive exact-money contract", asset.Ticker)
	}
	price, err := numericFromDecimal(money.Amount())
	if err != nil {
		return fmt.Errorf("convert quote for %s to PostgreSQL numeric: %w", asset.Ticker, err)
	}
	if strings.TrimSpace(quote.SourceRecordID) == "" || quote.QuotedAt.IsZero() {
		return fmt.Errorf("quote for %s has no source record ID or market timestamp", asset.Ticker)
	}
	_, err = store.New(r.pool).UpsertQuote(ctx, store.UpsertQuoteParams{
		AssetID: asset.ID, Source: "BRAPI", SourceQuoteID: quote.SourceRecordID,
		Price: price, Currency: money.Currency(),
		QuotedAt: pgtype.Timestamptz{Time: quote.QuotedAt.UTC(), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("upsert quote for %s at %s: %w", asset.Ticker, now.UTC().Format(time.RFC3339Nano), err)
	}
	return nil
}

func (r *PostgresRepository) SaveIndicator(ctx context.Context, asset Asset, indicator Indicator) error {
	if strings.TrimSpace(indicator.Source) == "" || strings.TrimSpace(indicator.SourceRecordID) == "" {
		return fmt.Errorf("indicator for %s requires source and source record ID", asset.Ticker)
	}
	if indicator.CollectedAt.IsZero() || len(indicator.RawPayload) == 0 || !jsonObject(indicator.RawPayload) {
		return fmt.Errorf("indicator for %s requires collection timestamp and raw JSON object", asset.Ticker)
	}
	priceToEarnings, err := numericFromDecimal(indicator.PriceToEarnings)
	if err != nil {
		return fmt.Errorf("convert P/E for %s: %w", asset.Ticker, err)
	}
	dividendYield, err := numericFromDecimal(indicator.DividendYieldTTM)
	if err != nil {
		return fmt.Errorf("convert TTM dividend yield for %s: %w", asset.Ticker, err)
	}
	annualRate, err := numericFromDecimal(indicator.AnnualReferenceRate)
	if err != nil {
		return fmt.Errorf("convert annual reference rate for %s: %w", asset.Ticker, err)
	}
	inflationRate, err := numericFromDecimal(indicator.InflationRate)
	if err != nil {
		return fmt.Errorf("convert monthly inflation rate for %s: %w", asset.Ticker, err)
	}
	if !priceToEarnings.Valid && !dividendYield.Valid && !annualRate.Valid && !inflationRate.Valid {
		return fmt.Errorf("indicator for %s has no numeric metric", asset.Ticker)
	}
	_, err = store.New(r.pool).UpsertAssetIndicator(ctx, store.UpsertAssetIndicatorParams{
		AssetID: asset.ID, Source: indicator.Source, SourceRecordID: indicator.SourceRecordID,
		ObservedAt:      pgtype.Timestamptz{Time: indicator.CollectedAt.UTC(), Valid: true},
		PriceToEarnings: priceToEarnings, DividendYieldTtm: dividendYield,
		AnnualReferenceRate: annualRate, InflationRate: inflationRate,
		RawPayload: indicator.RawPayload,
	})
	if err != nil {
		return fmt.Errorf("upsert indicator for %s: %w", asset.Ticker, err)
	}
	return nil
}

func (r *PostgresRepository) RecordSyncStatus(ctx context.Context, status SyncStatus) error {
	if strings.TrimSpace(status.DataKind) == "" || strings.TrimSpace(status.Source) == "" || status.AttemptedAt.IsZero() {
		return errors.New("market-data sync status requires kind, source, and attempted timestamp")
	}
	errorCode := pgtype.Text{}
	errorMessage := pgtype.Text{}
	if status.Code != "" || status.Message != "" {
		if status.Code == "" || status.Message == "" {
			return errors.New("market-data sync error code and message must be set together")
		}
		errorCode = pgtype.Text{String: status.Code, Valid: true}
		errorMessage = pgtype.Text{String: status.Message, Valid: true}
	}
	if status.Retryable && !errorCode.Valid {
		return errors.New("successful market-data sync status cannot be retryable")
	}
	if err := store.New(r.pool).RecordMarketDataSyncStatus(ctx, store.RecordMarketDataSyncStatusParams{
		AssetID:  pgtype.UUID{Bytes: status.AssetID, Valid: true},
		DataKind: status.DataKind, Source: status.Source,
		AttemptedAt: pgtype.Timestamptz{Time: status.AttemptedAt.UTC(), Valid: true},
		ErrorCode:   errorCode, ErrorMessage: errorMessage,
		Retryable: status.Retryable,
	}); err != nil {
		return fmt.Errorf("upsert market-data sync status for asset %x (%s): %w", status.AssetID, status.DataKind, err)
	}
	return nil
}

func numericFromDecimal(value string) (pgtype.Numeric, error) {
	if value == "" {
		return pgtype.Numeric{}, nil
	}
	if !postgresDecimalPattern.MatchString(value) {
		return pgtype.Numeric{}, fmt.Errorf("%q is not a decimal string without exponent notation", value)
	}
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(value, "-")
	parts := strings.SplitN(unsigned, ".", 2)
	scale := 0
	digits := parts[0]
	if len(parts) == 2 {
		scale = len(parts[1])
		digits += parts[1]
	}
	integer, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return pgtype.Numeric{}, fmt.Errorf("%q is not a valid decimal number", value)
	}
	if negative {
		integer.Neg(integer)
	}
	return pgtype.Numeric{Int: integer, Exp: -int32(scale), Valid: true}, nil
}

func jsonObject(value []byte) bool {
	trimmed := strings.TrimSpace(string(value))
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}
