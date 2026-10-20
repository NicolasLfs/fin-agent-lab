CREATE TYPE asset_type AS ENUM ('FIXED_INCOME', 'STOCK', 'FII');
CREATE TYPE market_news_status AS ENUM ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED');
CREATE TYPE recommendation_status AS ENUM ('PROCESSING', 'COMPLETED', 'FAILED');
CREATE TYPE idempotency_status AS ENUM ('PROCESSING', 'COMPLETED', 'FAILED');

CREATE TABLE assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ticker varchar(32) NOT NULL UNIQUE,
    asset_type asset_type NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT assets_ticker_not_empty CHECK (length(ticker) > 0),
    CONSTRAINT assets_name_not_empty CHECK (length(name) > 0)
);

CREATE TABLE quotes (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE RESTRICT,
    source text NOT NULL,
    source_quote_id text NOT NULL,
    price numeric NOT NULL,
    currency varchar(3) NOT NULL,
    quoted_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT quotes_source_not_empty CHECK (length(source) > 0),
    CONSTRAINT quotes_source_quote_id_not_empty CHECK (length(source_quote_id) > 0),
    CONSTRAINT quotes_price_exact_minor_units CHECK (
        scale(price) <= 2
        AND price > 0
        AND price <= 92233720368547758.07
    ),
    CONSTRAINT quotes_currency_iso4217_format CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT quotes_source_record_unique UNIQUE (source, source_quote_id)
);
CREATE INDEX quotes_asset_quoted_at_idx ON quotes (asset_id, quoted_at DESC);

CREATE TABLE market_news (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title text NOT NULL,
    content_hash varchar(64) NOT NULL UNIQUE,
    source text NOT NULL,
    url text NOT NULL,
    published_at timestamptz NOT NULL,
    processed_status market_news_status NOT NULL DEFAULT 'PENDING',
    qdrant_point_id text UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT market_news_title_not_empty CHECK (length(title) > 0),
    CONSTRAINT market_news_hash_sha256 CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT market_news_source_not_empty CHECK (length(source) > 0),
    CONSTRAINT market_news_url_not_empty CHECK (length(url) > 0),
    CONSTRAINT market_news_qdrant_id_not_empty CHECK (
        qdrant_point_id IS NULL OR length(qdrant_point_id) > 0
    ),
    CONSTRAINT market_news_id_qdrant_point_unique UNIQUE (id, qdrant_point_id)
);
CREATE INDEX market_news_status_published_idx ON market_news (processed_status, published_at);

CREATE TABLE recommendation_reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    status recommendation_status NOT NULL DEFAULT 'PROCESSING',
    ranked_assets jsonb NOT NULL DEFAULT '[]'::jsonb,
    justification_text text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT recommendation_reports_ranked_assets_array CHECK (
        jsonb_typeof(ranked_assets) = 'array'
    ),
    CONSTRAINT recommendation_reports_completed_has_justification CHECK (
        status <> 'COMPLETED' OR justification_text IS NOT NULL
    )
);
CREATE INDEX recommendation_reports_status_created_idx
    ON recommendation_reports (status, created_at DESC);

CREATE TABLE recommendation_report_quotes (
    report_id uuid NOT NULL REFERENCES recommendation_reports(id) ON DELETE CASCADE,
    quote_id uuid NOT NULL REFERENCES quotes(id) ON DELETE RESTRICT,
    PRIMARY KEY (report_id, quote_id)
);

CREATE TABLE recommendation_report_news (
    report_id uuid NOT NULL REFERENCES recommendation_reports(id) ON DELETE CASCADE,
    news_id uuid NOT NULL,
    qdrant_point_id text NOT NULL,
    PRIMARY KEY (report_id, qdrant_point_id),
    CONSTRAINT recommendation_report_news_qdrant_id_not_empty
        CHECK (length(qdrant_point_id) > 0),
    CONSTRAINT recommendation_report_news_source_matches
        FOREIGN KEY (news_id, qdrant_point_id)
        REFERENCES market_news (id, qdrant_point_id) ON DELETE RESTRICT
);
CREATE INDEX recommendation_report_news_news_id_idx ON recommendation_report_news (news_id);

CREATE TABLE idempotency_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    operation varchar(100) NOT NULL,
    idempotency_key varchar(255) NOT NULL,
    request_hash varchar(64) NOT NULL,
    status idempotency_status NOT NULL DEFAULT 'PROCESSING',
    report_id uuid REFERENCES recommendation_reports(id) ON DELETE RESTRICT,
    response_body jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT idempotency_operation_not_empty CHECK (length(operation) > 0),
    CONSTRAINT idempotency_key_not_empty CHECK (length(idempotency_key) > 0),
    CONSTRAINT idempotency_request_hash_sha256 CHECK (request_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT idempotency_completed_has_response CHECK (
        status <> 'COMPLETED' OR response_body IS NOT NULL
    ),
    CONSTRAINT idempotency_operation_key_unique UNIQUE (operation, idempotency_key)
);
