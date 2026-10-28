DROP TABLE market_data_sync_status;
DELETE FROM asset_indicators
WHERE price_to_earnings IS NULL
  AND dividend_yield_ttm IS NULL
  AND annual_reference_rate IS NULL
  AND inflation_rate IS NOT NULL;
ALTER TABLE asset_indicators
    DROP CONSTRAINT asset_indicators_has_metric,
    ADD CONSTRAINT asset_indicators_has_metric CHECK (
        price_to_earnings IS NOT NULL
        OR dividend_yield_ttm IS NOT NULL
        OR annual_reference_rate IS NOT NULL
    );
ALTER TABLE asset_indicators
    DROP CONSTRAINT asset_indicators_nonnegative_inflation,
    DROP COLUMN inflation_rate;
