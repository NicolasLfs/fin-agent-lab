package db_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fin-agent/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const migrationPath = "migrations/000001_persistence_domain.up.sql"

func TestPostgres18PersistenceMigration(t *testing.T) {
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration %s: %v", migrationPath, err)
	}
	if len(strings.TrimSpace(string(migration))) == 0 {
		t.Fatalf("migration %s is empty", migrationPath)
	}
	schema, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("read sqlc schema: %v", err)
	}
	if strings.TrimSpace(string(schema)) != strings.TrimSpace(string(migration)) {
		t.Fatal("sqlc schema and initial migration differ")
	}
	downMigration, err := os.ReadFile(filepath.Join("migrations", "000001_persistence_domain.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if len(strings.TrimSpace(string(downMigration))) == 0 {
		t.Fatal("down migration is empty")
	}

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL 18 integration checks")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	adminConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	adminConn, err := pgx.ConnectConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	defer adminConn.Close(context.Background())

	var serverVersionText string
	if err := adminConn.QueryRow(ctx, "SHOW server_version_num").Scan(&serverVersionText); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	serverVersion, err := strconv.Atoi(serverVersionText)
	if err != nil {
		t.Fatalf("parse PostgreSQL version %q: %v", serverVersionText, err)
	}
	if serverVersion < 180000 {
		t.Fatalf("PostgreSQL server_version_num = %d, want PostgreSQL 18 or newer", serverVersion)
	}

	schemaName := fmt.Sprintf("phase2_test_%d", time.Now().UnixNano())
	if _, err := adminConn.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	defer func() {
		if _, err := adminConn.Exec(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE"); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	}()

	testConfig := adminConfig.Copy()
	testConfig.RuntimeParams["search_path"] = schemaName
	testConn, err := pgx.ConnectConfig(ctx, testConfig)
	if err != nil {
		t.Fatalf("connect to isolated test schema: %v", err)
	}
	defer testConn.Close(context.Background())

	if _, err := testConn.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply persistence migration: %v", err)
	}

	var assetID pgtype.UUID
	if err := testConn.QueryRow(ctx,
		"INSERT INTO assets (ticker, asset_type, name) VALUES ('TEST3', 'STOCK', 'Test asset') RETURNING id",
	).Scan(&assetID); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	assertStatementFails(t, ctx, testConn,
		"INSERT INTO assets (ticker, asset_type, name) VALUES ('TEST3', 'STOCK', 'Duplicate ticker')",
		"duplicate asset ticker")

	assertStatementFails(t, ctx, testConn,
		"INSERT INTO quotes (asset_id, source, source_quote_id, price, currency, quoted_at) VALUES ($1, 'test', 'quote-1', 1.001, 'BRL', now())",
		"quote with more than two decimal places", assetID)
	if _, err := testConn.Exec(ctx,
		"INSERT INTO quotes (asset_id, source, source_quote_id, price, currency, quoted_at) VALUES ($1, 'test', 'quote-1', 1.01, 'BRL', now())",
		assetID,
	); err != nil {
		t.Fatalf("insert valid quote: %v", err)
	}
	assertStatementFails(t, ctx, testConn,
		"INSERT INTO quotes (asset_id, source, source_quote_id, price, currency, quoted_at) VALUES ($1, 'test', 'quote-1', 1.01, 'BRL', now())",
		"duplicate source quote", assetID)

	var newsID pgtype.UUID
	if err := testConn.QueryRow(ctx,
		"INSERT INTO market_news (title, content_hash, source, url, published_at) VALUES ('News', $1, 'test', 'https://example.test/news', now()) RETURNING id",
		strings.Repeat("a", 64),
	).Scan(&newsID); err != nil {
		t.Fatalf("insert news: %v", err)
	}
	assertStatementFails(t, ctx, testConn,
		"INSERT INTO market_news (title, content_hash, source, url, published_at) VALUES ('Duplicate', $1, 'test', 'https://example.test/duplicate', now())",
		"duplicate news content hash", strings.Repeat("a", 64))
	if _, err := testConn.Exec(ctx,
		"INSERT INTO market_news (title, content_hash, source, url, published_at) VALUES ('Second news', $1, 'test', 'https://example.test/second', now())",
		strings.Repeat("b", 64),
	); err != nil {
		t.Fatalf("insert second news item: %v", err)
	}
	if _, err := testConn.Exec(ctx,
		"UPDATE market_news SET qdrant_point_id = 'qdrant-news-1' WHERE id = $1",
		newsID,
	); err != nil {
		t.Fatalf("set Qdrant point ID: %v", err)
	}
	var auditReportID pgtype.UUID
	if err := testConn.QueryRow(ctx,
		"INSERT INTO recommendation_reports DEFAULT VALUES RETURNING id",
	).Scan(&auditReportID); err != nil {
		t.Fatalf("insert audit report: %v", err)
	}
	assertStatementFails(t, ctx, testConn,
		"INSERT INTO recommendation_report_news (report_id, news_id, qdrant_point_id) VALUES ($1, $2, 'wrong-qdrant-id')",
		"news citation with mismatched Qdrant ID", auditReportID, newsID)
	if _, err := testConn.Exec(ctx,
		"INSERT INTO recommendation_report_news (report_id, news_id, qdrant_point_id) VALUES ($1, $2, 'qdrant-news-1')",
		auditReportID, newsID,
	); err != nil {
		t.Fatalf("insert matching Qdrant news citation: %v", err)
	}

	if _, err := testConn.Exec(ctx,
		"INSERT INTO idempotency_records (operation, idempotency_key, request_hash) VALUES ('recommendations.generate', 'request-1', $1)",
		strings.Repeat("c", 64),
	); err != nil {
		t.Fatalf("insert idempotency record: %v", err)
	}
	assertStatementFails(t, ctx, testConn,
		"INSERT INTO idempotency_records (operation, idempotency_key, request_hash) VALUES ('recommendations.generate', 'request-1', $1)",
		"duplicate idempotency key", strings.Repeat("c", 64))

	firstWorkerConn, err := pgx.ConnectConfig(ctx, testConfig)
	if err != nil {
		t.Fatalf("connect first worker: %v", err)
	}
	defer firstWorkerConn.Close(context.Background())
	secondWorkerConn, err := pgx.ConnectConfig(ctx, testConfig)
	if err != nil {
		t.Fatalf("connect second worker: %v", err)
	}
	defer secondWorkerConn.Close(context.Background())

	firstWorkerTx, err := firstWorkerConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin first worker transaction: %v", err)
	}
	defer firstWorkerTx.Rollback(context.Background())
	firstClaim, err := store.New(firstWorkerTx).GetPendingMarketNews(ctx, 1)
	if err != nil {
		t.Fatalf("claim news for first worker: %v", err)
	}
	if len(firstClaim) != 1 {
		t.Fatalf("first worker claimed %d news items, want 1", len(firstClaim))
	}

	secondWorkerTx, err := secondWorkerConn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin second worker transaction: %v", err)
	}
	defer secondWorkerTx.Rollback(context.Background())
	secondClaim, err := store.New(secondWorkerTx).GetPendingMarketNews(ctx, 2)
	if err != nil {
		t.Fatalf("claim news for second worker: %v", err)
	}
	if len(secondClaim) != 1 {
		t.Fatalf("second worker claimed %d news items, want 1 unlocked item", len(secondClaim))
	}
	if secondClaim[0].ID == firstClaim[0].ID {
		t.Fatal("second worker claimed a row locked by the first worker")
	}
	if err := firstWorkerTx.Commit(ctx); err != nil {
		t.Fatalf("commit first worker transaction: %v", err)
	}
	if err := secondWorkerTx.Rollback(ctx); err != nil {
		t.Fatalf("release second worker locks: %v", err)
	}

	if _, err := testConn.Exec(ctx,
		"INSERT INTO recommendation_reports (status) VALUES ('PROCESSING')",
	); err != nil {
		t.Fatalf("insert processing report: %v", err)
	}
	assertStatementFails(t, ctx, testConn,
		"INSERT INTO recommendation_reports (status) VALUES ('UNKNOWN')",
		"unknown report status")

	if _, err := testConn.Exec(ctx, string(downMigration)); err != nil {
		t.Fatalf("apply persistence down migration: %v", err)
	}
	var assetsTableRemoved bool
	if err := testConn.QueryRow(ctx, "SELECT to_regclass('assets') IS NULL").Scan(&assetsTableRemoved); err != nil {
		t.Fatalf("verify down migration: %v", err)
	}
	if !assetsTableRemoved {
		t.Fatal("down migration left the assets table in place")
	}
}

func assertStatementFails(t *testing.T, ctx context.Context, conn *pgx.Conn, statement, name string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(ctx, statement, args...); err == nil {
		t.Errorf("%s unexpectedly succeeded", name)
	}
}
