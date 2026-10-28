ALTER TABLE idempotency_records
    DROP CONSTRAINT idempotency_failure_status_valid,
    DROP CONSTRAINT idempotency_failure_details_complete,
    DROP COLUMN failure_retryable,
    DROP COLUMN failure_message,
    DROP COLUMN failure_code,
    DROP COLUMN failure_status;
DROP TABLE asset_indicators;
