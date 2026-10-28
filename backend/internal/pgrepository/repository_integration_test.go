package pgrepository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fin-agent/internal/agentclient"
	"fin-agent/internal/httpapi"
	"fin-agent/internal/store"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExecuteIsIdempotentAcrossConcurrentRequestsAndRetries(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL 18 idempotency integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	schemaName := fmt.Sprintf("pgrepository_test_%d", time.Now().UnixNano())
	if _, err := adminConn.Exec(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatalf("create isolated test schema: %v", err)
	}
	defer func() {
		if _, err := adminConn.Exec(context.Background(), "DROP SCHEMA "+schemaName+" CASCADE"); err != nil {
			t.Errorf("drop isolated test schema: %v", err)
		}
	}()

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse PostgreSQL pool config: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schemaName
	poolConfig.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	defer pool.Close()

	migration, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "000001_persistence_domain.up.sql"))
	if err != nil {
		t.Fatalf("read persistence migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply persistence migration: %v", err)
	}
	var assetID string
	if err := pool.QueryRow(ctx,
		"INSERT INTO assets (ticker, asset_type, name) VALUES ('TEST3', 'STOCK', 'Test asset') RETURNING id",
	).Scan(&assetID); err != nil {
		t.Fatalf("insert test asset: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO quotes (asset_id, source, source_quote_id, price, currency, quoted_at) VALUES ($1, 'test', 'quote-1', 68.50, 'BRL', now())",
		assetID,
	); err != nil {
		t.Fatalf("insert test quote: %v", err)
	}

	const concurrentNewsIngestions = 50
	newsStart := make(chan struct{})
	newsIDs := make(chan pgtype.UUID, concurrentNewsIngestions)
	newsErrors := make(chan error, concurrentNewsIngestions)
	newsParams := store.UpsertMarketNewsParams{
		Title:       "Concurrent duplicate",
		ContentHash: strings.Repeat("a", 64),
		Source:      "test",
		Url:         "https://example.test/news",
		PublishedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}
	var newsWorkers sync.WaitGroup
	for range concurrentNewsIngestions {
		newsWorkers.Add(1)
		go func() {
			defer newsWorkers.Done()
			<-newsStart
			item, err := store.New(pool).UpsertMarketNews(ctx, newsParams)
			if err != nil {
				newsErrors <- err
				return
			}
			newsIDs <- item.ID
		}()
	}
	close(newsStart)
	newsWorkers.Wait()
	close(newsIDs)
	close(newsErrors)
	for err := range newsErrors {
		t.Errorf("concurrent news upsert error = %v", err)
	}
	var persistedNewsID pgtype.UUID
	for id := range newsIDs {
		if !persistedNewsID.Valid {
			persistedNewsID = id
		}
		if id != persistedNewsID {
			t.Errorf("concurrent news IDs differ: %v and %v", id, persistedNewsID)
		}
	}
	if !persistedNewsID.Valid {
		t.Fatal("no concurrent news upsert returned a persistent ID")
	}
	var newsCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM market_news WHERE content_hash = $1", newsParams.ContentHash).Scan(&newsCount); err != nil {
		t.Fatalf("count concurrent news records: %v", err)
	}
	if newsCount != 1 {
		t.Fatalf("concurrent news record count = %d, want 1", newsCount)
	}

	repository := New(pool)
	request := httpapi.GenerateRequest{AssetTypes: []httpapi.AssetType{httpapi.AssetTypeStock}}
	const concurrentRequests = 10
	start := make(chan struct{})
	responses := make(chan httpapi.GenerateResponse, concurrentRequests)
	errorsFound := make(chan error, concurrentRequests)
	var agentCalls atomic.Int32
	var workers sync.WaitGroup

	invoke := func(context.Context, agentclient.RunRequest) (agentclient.RunResponse, error) {
		agentCalls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return agentclient.RunResponse{
			Status:            "COMPLETED",
			RankedAssets:      []agentclient.RankedAsset{{Rank: 1, Ticker: "TEST3", Type: "STOCK"}},
			JustificationText: "Grounded test response.",
			CitedNewsIDs:      []string{},
		}, nil
	}
	for range concurrentRequests {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			response, err := repository.Execute(ctx, "same-key", request, invoke)
			if err != nil {
				errorsFound <- err
				return
			}
			responses <- response
		}()
	}
	close(start)
	workers.Wait()
	close(responses)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent Execute() error = %v", err)
	}

	var reportID string
	for response := range responses {
		if reportID == "" {
			reportID = response.ReportID
		}
		if response.ReportID != reportID || response.Status != "COMPLETED" {
			t.Errorf("concurrent response = %#v, want completed report %q", response, reportID)
		}
	}
	if reportID == "" {
		t.Fatal("no concurrent request returned a completed response")
	}
	if got := agentCalls.Load(); got != 1 {
		t.Fatalf("agent invocations = %d, want 1", got)
	}

	var reportCount, keyCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recommendation_reports").Scan(&reportCount); err != nil {
		t.Fatalf("count recommendation reports: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM idempotency_records").Scan(&keyCount); err != nil {
		t.Fatalf("count idempotency records: %v", err)
	}
	if reportCount != 1 || keyCount != 1 {
		t.Fatalf("persisted reports/keys = %d/%d, want 1/1", reportCount, keyCount)
	}

	var originalReportID string
	_, err = repository.Execute(ctx, "retry-key", request, func(_ context.Context, payload agentclient.RunRequest) (agentclient.RunResponse, error) {
		originalReportID = payload.ReportID
		return agentclient.RunResponse{}, &agentclient.TransportError{
			Err: errors.New("simulated temporary agent outage"), Retryable: true,
		}
	})
	var failure *httpapi.Failure
	if !errors.As(err, &failure) || !failure.Retryable {
		t.Fatalf("first retryable Execute() error = %v, want retryable API failure", err)
	}

	retried, err := repository.Execute(ctx, "retry-key", request, func(_ context.Context, payload agentclient.RunRequest) (agentclient.RunResponse, error) {
		if payload.ReportID != originalReportID {
			t.Errorf("retry reportId = %q, want %q", payload.ReportID, originalReportID)
		}
		return invoke(context.Background(), payload)
	})
	if err != nil {
		t.Fatalf("retry Execute() error = %v", err)
	}
	if retried.ReportID != originalReportID {
		t.Fatalf("retried reportId = %q, want %q", retried.ReportID, originalReportID)
	}

	if err := pool.QueryRow(ctx, "SELECT count(*) FROM recommendation_reports").Scan(&reportCount); err != nil {
		t.Fatalf("count recommendation reports after retry: %v", err)
	}
	if reportCount != 2 {
		t.Fatalf("recommendation report count after retry = %d, want 2", reportCount)
	}
}
