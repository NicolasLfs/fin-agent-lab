ALTER TABLE asset_indicators
    DROP CONSTRAINT asset_indicators_has_metric;

ALTER TABLE asset_indicators
    ADD COLUMN inflation_rate numeric,
    ADD CONSTRAINT asset_indicators_has_metric CHECK (
        price_to_earnings IS NOT NULL
        OR dividend_yield_ttm IS NOT NULL
        OR annual_reference_rate IS NOT NULL
        OR inflation_rate IS NOT NULL
    ),
    ADD CONSTRAINT asset_indicators_nonnegative_inflation CHECK (
        inflation_rate IS NULL OR inflation_rate >= 0
    );

CREATE TABLE market_data_sync_status (
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    data_kind varchar(32) NOT NULL,
    source varchar(128) NOT NULL,
    attempted_at timestamptz NOT NULL,
    error_code text,
    error_message text,
    retryable boolean NOT NULL DEFAULT false,
    PRIMARY KEY (asset_id, data_kind),
    CONSTRAINT market_data_sync_kind_not_empty CHECK (length(data_kind) > 0),
    CONSTRAINT market_data_sync_source_not_empty CHECK (length(source) > 0),
    CONSTRAINT market_data_sync_error_complete CHECK (
        (error_code IS NULL AND error_message IS NULL AND retryable = false)
        OR
        (error_code IS NOT NULL AND length(error_code) > 0
         AND error_message IS NOT NULL AND length(error_message) > 0)
    )
);
CREATE INDEX market_data_sync_attempted_idx
    ON market_data_sync_status (data_kind, attempted_at DESC);
