CREATE TABLE asset_indicators (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    source text NOT NULL,
    source_record_id text NOT NULL,
    observed_at timestamptz NOT NULL,
    price_to_earnings numeric,
    dividend_yield_ttm numeric,
    annual_reference_rate numeric,
    raw_payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT asset_indicators_source_not_empty CHECK (length(source) > 0),
    CONSTRAINT asset_indicators_source_record_not_empty CHECK (length(source_record_id) > 0),
    CONSTRAINT asset_indicators_has_metric CHECK (
        price_to_earnings IS NOT NULL
        OR dividend_yield_ttm IS NOT NULL
        OR annual_reference_rate IS NOT NULL
    ),
    CONSTRAINT asset_indicators_nonnegative_yields CHECK (
        (dividend_yield_ttm IS NULL OR dividend_yield_ttm >= 0)
        AND (annual_reference_rate IS NULL OR annual_reference_rate >= 0)
    ),
    CONSTRAINT asset_indicators_raw_payload_object CHECK (jsonb_typeof(raw_payload) = 'object'),
    CONSTRAINT asset_indicators_source_record_unique UNIQUE (source, source_record_id)
);
CREATE INDEX asset_indicators_asset_observed_idx
    ON asset_indicators (asset_id, observed_at DESC, id DESC);

ALTER TABLE idempotency_records
    ADD COLUMN failure_status integer,
    ADD COLUMN failure_code text,
    ADD COLUMN failure_message text,
    ADD COLUMN failure_retryable boolean;
ALTER TABLE idempotency_records
    ADD CONSTRAINT idempotency_failure_details_complete CHECK (
        num_nonnulls(failure_status, failure_code, failure_message, failure_retryable) IN (0, 4)
    ),
    ADD CONSTRAINT idempotency_failure_status_valid CHECK (
        failure_status IS NULL OR failure_status BETWEEN 400 AND 599
    );
