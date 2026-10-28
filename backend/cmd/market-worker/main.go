package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	envconfig "fin-agent/internal/config"
	"fin-agent/internal/marketdata"
	"fin-agent/internal/marketworker"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

const defaultSyncInterval = 15 * time.Minute

type Config struct {
	DatabaseURL  string
	BrapiAPIKey  string
	SyncInterval time.Duration
}

type sourceAdapter struct {
	bcb   *marketdata.BCBClient
	brapi *marketdata.BrapiClient
}

func loadConfig() (Config, error) {
	config := Config{
		DatabaseURL:  strings.TrimSpace(os.Getenv("DATABASE_URL")),
		BrapiAPIKey:  strings.TrimSpace(os.Getenv("BRAPI_API_KEY")),
		SyncInterval: defaultSyncInterval,
	}
	if config.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if interval := strings.TrimSpace(os.Getenv("MARKET_DATA_SYNC_INTERVAL")); interval != "" {
		parsed, err := time.ParseDuration(interval)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("MARKET_DATA_SYNC_INTERVAL must be a positive Go duration")
		}
		config.SyncInterval = parsed
	}
	if _, err := pgxpool.ParseConfig(config.DatabaseURL); err != nil {
		return Config{}, errors.New("DATABASE_URL is invalid")
	}
	return config, nil
}

func newPool(config Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		return nil, errors.New("DATABASE_URL is invalid")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create market worker PostgreSQL pool: %w", err)
	}
	return pool, nil
}

func newSource(config Config) *sourceAdapter {
	return &sourceAdapter{
		bcb:   marketdata.NewBCBClient(nil, ""),
		brapi: marketdata.NewBrapiClient(nil, "", config.BrapiAPIKey),
	}
}

func (s *sourceAdapter) FetchSGS(ctx context.Context, seriesID int) (marketdata.Observation, error) {
	return s.bcb.FetchSGS(ctx, seriesID)
}

func (s *sourceAdapter) FetchStock(ctx context.Context, ticker string) (marketdata.Quote, marketdata.StockIndicators, error) {
	return s.brapi.FetchStock(ctx, ticker)
}

func (s *sourceAdapter) FetchFII(ctx context.Context, ticker string) (marketdata.Quote, marketdata.FIIIndicators, error) {
	return s.brapi.FetchFII(ctx, ticker)
}

func newRepository(pool *pgxpool.Pool) *marketworker.PostgresRepository {
	return marketworker.NewPostgresRepository(pool)
}

func newWorker(repository *marketworker.PostgresRepository, source *sourceAdapter) *marketworker.Worker {
	return marketworker.New(repository, source, time.Now)
}

func registerWorker(lifecycle fx.Lifecycle, config Config, worker *marketworker.Worker, pool *pgxpool.Pool) {
	var cancel context.CancelFunc
	var done chan struct{}
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			done = make(chan struct{})
			go func() {
				defer close(done)
				runWorker(ctx, worker, config.SyncInterval)
			}()
			return nil
		},
		OnStop: func(stopContext context.Context) error {
			if cancel != nil {
				cancel()
			}
			if done != nil {
				select {
				case <-done:
				case <-stopContext.Done():
					pool.Close()
					return fmt.Errorf("stop market worker: %w", stopContext.Err())
				}
			}
			pool.Close()
			return nil
		},
	})
}

func runWorker(ctx context.Context, worker *marketworker.Worker, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := worker.RunOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Error("market-data synchronization failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func applicationOptions(config Config) []fx.Option {
	return []fx.Option{
		fx.Supply(config),
		fx.Provide(newPool, newSource, newRepository, newWorker),
		fx.Invoke(registerWorker),
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if err := envconfig.LoadDotEnv(); err != nil {
		logger.Error("cannot load local environment file", "error", err)
		os.Exit(1)
	}
	config, err := loadConfig()
	if err != nil {
		logger.Error("invalid market worker configuration", "error", err)
		os.Exit(1)
	}
	fx.New(applicationOptions(config)...).Run()
}
