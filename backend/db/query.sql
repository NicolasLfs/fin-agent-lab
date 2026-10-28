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

-- name: ListAssetsForRecommendation :many
SELECT a.id, a.ticker, a.asset_type,
       q.id AS quote_id,
       q.price::text AS reference_price,
       q.currency
FROM assets a
JOIN LATERAL (
    SELECT id, price, currency
    FROM quotes
    WHERE asset_id = a.id
    ORDER BY quoted_at DESC, id
    LIMIT 1
) q ON true
WHERE a.asset_type = ANY($1::asset_type[])
  AND a.asset_type <> 'FIXED_INCOME'
ORDER BY a.ticker;

-- name: ListMarketNewsByQdrantPointIDs :many
SELECT id, qdrant_point_id
FROM market_news
WHERE qdrant_point_id = ANY($1::text[])
ORDER BY qdrant_point_id;

-- name: UpsertQuote :one
INSERT INTO quotes (asset_id, source, source_quote_id, price, currency, quoted_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (source, source_quote_id) DO UPDATE
SET source_quote_id = EXCLUDED.source_quote_id
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
       response_body, created_at, updated_at
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