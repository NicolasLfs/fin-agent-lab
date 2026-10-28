-- name: CreateAsset :one
INSERT INTO assets (ticker, asset_type, name)
VALUES ($1, $2, $3)
RETURNING id, ticker, asset_type, name, created_at;

-- name: GetAssetByTicker :one
SELECT id, ticker, asset_type, name, created_at
FROM assets
WHERE ticker = $1;

-- name: ListAssets :many
SELECT id, ticker, asset_type, name, created_at
FROM assets
ORDER BY ticker;

-- name: UpsertMarketAsset :one
INSERT INTO assets (ticker, asset_type, name)
VALUES ($1, $2, $3)
ON CONFLICT (ticker) DO UPDATE SET ticker = EXCLUDED.ticker
RETURNING id, ticker, asset_type, name, created_at;

-- name: ListMarketAssets :many
SELECT id, ticker, asset_type
FROM assets
WHERE asset_type IN ('STOCK', 'FII')
   OR ticker IN ('SELIC_SGS_432', 'IPCA_SGS_433')
ORDER BY ticker;

-- name: ListRecommendationCandidates :many
SELECT a.id, a.ticker, a.asset_type,
       q.id AS quote_id,
       q.price::text AS reference_price,
       q.currency,
       q.quoted_at,
       COALESCE(i.price_to_earnings::text, ''::text)::varchar AS price_to_earnings,
       COALESCE(i.dividend_yield_ttm::text, ''::text)::varchar AS dividend_yield_ttm,
       COALESCE(i.annual_reference_rate::text, ''::text)::varchar AS annual_reference_rate,
       COALESCE(i.source, ''::text)::varchar AS indicator_source,
       i.observed_at AS indicator_observed_at,
       sync.attempted_at AS sync_attempted_at,
       COALESCE(sync.error_code, ''::text)::varchar AS sync_error_code,
       COALESCE(sync.error_message, ''::text)::varchar AS sync_error_message,
       COALESCE(sync.retryable, false) AS sync_retryable
FROM assets a
LEFT JOIN LATERAL (
    SELECT id, price, currency, quoted_at
    FROM quotes
    WHERE asset_id = a.id
    ORDER BY quoted_at DESC, id
    LIMIT 1
) q ON true
LEFT JOIN LATERAL (
    SELECT price_to_earnings, dividend_yield_ttm, annual_reference_rate,
           source, observed_at
    FROM asset_indicators
    WHERE asset_id = a.id
    ORDER BY observed_at DESC, id DESC
    LIMIT 1
) i ON true
LEFT JOIN LATERAL (
    SELECT attempted_at, error_code, error_message, retryable
    FROM market_data_sync_status
    WHERE asset_id = a.id
    ORDER BY attempted_at DESC, data_kind
    LIMIT 1
) sync ON true
WHERE a.asset_type::text = ANY($1::text[])
ORDER BY a.ticker;

-- name: UpsertAssetIndicator :one
INSERT INTO asset_indicators (
    asset_id, source, source_record_id, observed_at, price_to_earnings,
    dividend_yield_ttm, annual_reference_rate, raw_payload, inflation_rate
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (source, source_record_id) DO UPDATE
SET observed_at = EXCLUDED.observed_at,
    price_to_earnings = EXCLUDED.price_to_earnings,
    dividend_yield_ttm = EXCLUDED.dividend_yield_ttm,
    annual_reference_rate = EXCLUDED.annual_reference_rate,
    inflation_rate = EXCLUDED.inflation_rate,
    raw_payload = EXCLUDED.raw_payload
RETURNING id, asset_id, source, source_record_id, observed_at, price_to_earnings,
          dividend_yield_ttm, annual_reference_rate, raw_payload, created_at, inflation_rate;

-- name: RecordMarketDataSyncStatus :exec
INSERT INTO market_data_sync_status (
    asset_id, data_kind, source, attempted_at, error_code, error_message, retryable
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (asset_id, data_kind) DO UPDATE
SET source = EXCLUDED.source,
    attempted_at = EXCLUDED.attempted_at,
    error_code = EXCLUDED.error_code,
    error_message = EXCLUDED.error_message,
    retryable = EXCLUDED.retryable;

-- name: ListMarketNewsByQdrantPointIDs :many
SELECT id, qdrant_point_id
FROM market_news
WHERE qdrant_point_id = ANY($1::text[])
ORDER BY qdrant_point_id;

-- name: UpsertQuote :one
INSERT INTO quotes (asset_id, source, source_quote_id, price, currency, quoted_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source, source_quote_id) DO UPDATE
SET price = EXCLUDED.price,
    currency = EXCLUDED.currency,
    quoted_at = EXCLUDED.quoted_at
RETURNING id, asset_id, source, source_quote_id, price, currency, quoted_at, created_at;

-- name: ListQuotesForAsset :many
SELECT id, asset_id, source, source_quote_id, price, currency, quoted_at, created_at
FROM quotes
WHERE asset_id = $1
ORDER BY quoted_at DESC, id;

-- name: UpsertMarketNews :one
INSERT INTO market_news (title, content_hash, source, url, published_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (content_hash) DO UPDATE
SET content_hash = EXCLUDED.content_hash
RETURNING id, title, content_hash, source, url, published_at, processed_status,
          qdrant_point_id, created_at, updated_at;

-- name: GetPendingMarketNews :many
SELECT id, title, content_hash, source, url, published_at, processed_status,
       qdrant_point_id, created_at, updated_at
FROM market_news
WHERE processed_status = 'PENDING'
ORDER BY published_at, id
FOR UPDATE SKIP LOCKED
LIMIT $1;

-- name: SetMarketNewsStatus :execrows
UPDATE market_news
SET processed_status = $2, updated_at = now()
WHERE id = $1;

-- name: CreateIdempotencyRecord :one
INSERT INTO idempotency_records (operation, idempotency_key, request_hash)
VALUES ($1, $2, $3)
ON CONFLICT (operation, idempotency_key) DO NOTHING
RETURNING id, operation, idempotency_key, request_hash, status, report_id,
          response_body, created_at, updated_at;

-- name: LockIdempotencyRecord :one
SELECT id, operation, idempotency_key, request_hash, status, report_id,
       response_body, failure_status, failure_code, failure_message,
       failure_retryable, created_at, updated_at
FROM idempotency_records
WHERE operation = $1 AND idempotency_key = $2
FOR UPDATE;

-- name: CompleteIdempotencyRecord :execrows
UPDATE idempotency_records
SET status = 'COMPLETED', report_id = $2, response_body = $3, updated_at = now()
WHERE id = $1 AND status = 'PROCESSING';

-- name: SetIdempotencyReportID :execrows
UPDATE idempotency_records
SET report_id = $2, updated_at = now()
WHERE id = $1 AND status = 'PROCESSING' AND report_id IS NULL;

-- name: FailIdempotencyRecord :execrows
UPDATE idempotency_records
SET status = 'FAILED', updated_at = now()
WHERE id = $1 AND status = 'PROCESSING';

-- name: FailIdempotencyRecordWithError :execrows
UPDATE idempotency_records
SET status = 'FAILED', failure_status = $2, failure_code = $3,
    failure_message = $4, failure_retryable = $5, updated_at = now()
WHERE id = $1 AND status = 'PROCESSING';

-- name: CreateRecommendationReport :one
INSERT INTO recommendation_reports (ranked_assets)
VALUES ($1)
RETURNING id, status, ranked_assets, justification_text, created_at, updated_at;

-- name: GetRecommendationReport :one
SELECT id, status, ranked_assets, justification_text, created_at, updated_at
FROM recommendation_reports
WHERE id = $1;

-- name: CompleteRecommendationReport :execrows
UPDATE recommendation_reports
SET status = 'COMPLETED', ranked_assets = $2, justification_text = $3, updated_at = now()
WHERE id = $1 AND status = 'PROCESSING';

-- name: FailRecommendationReport :execrows
UPDATE recommendation_reports
SET status = 'FAILED', updated_at = now()
WHERE id = $1 AND status = 'PROCESSING';

-- name: LinkRecommendationQuote :exec
INSERT INTO recommendation_report_quotes (report_id, quote_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: LinkRecommendationNews :exec
INSERT INTO recommendation_report_news (report_id, news_id, qdrant_point_id)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: ListRecommendationReportQuotes :many
SELECT q.id, q.asset_id, q.source, q.source_quote_id, q.price, q.currency, q.quoted_at
FROM recommendation_report_quotes rq
JOIN quotes q ON q.id = rq.quote_id
WHERE rq.report_id = $1
ORDER BY q.quoted_at, q.id;

-- name: ListRecommendationReportNews :many
SELECT rn.news_id, rn.qdrant_point_id
FROM recommendation_report_news rn
WHERE rn.report_id = $1
ORDER BY rn.qdrant_point_id;