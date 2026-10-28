package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"fin-agent/internal/agentclient"
	envconfig "fin-agent/internal/config"
	"fin-agent/internal/health"
	"fin-agent/internal/httpapi"
	"fin-agent/internal/pgrepository"
	"fin-agent/internal/recommendation"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

type Config struct {
	DatabaseURL   string
	AgentURL      string
	QdrantURL     string
	LocalStackURL string
	HTTPAddr      string
}

func loadConfig() (Config, error) {
	config := Config{
		DatabaseURL:   strings.TrimSpace(os.Getenv("DATABASE_URL")),
		AgentURL:      strings.TrimSpace(os.Getenv("AGENT_URL")),
		QdrantURL:     strings.TrimSpace(os.Getenv("QDRANT_URL")),
		LocalStackURL: strings.TrimSpace(os.Getenv("LOCALSTACK_URL")),
		HTTPAddr:      strings.TrimSpace(os.Getenv("HTTP_ADDR")),
	}
	if config.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if config.HTTPAddr == "" {
		config.HTTPAddr = ":8080"
	}
	for name, value := range map[string]string{
		"AGENT_URL": config.AgentURL, "QDRANT_URL": config.QdrantURL, "LOCALSTACK_URL": config.LocalStackURL,
	} {
		if err := validateHTTPURL(name, value); err != nil {
			return Config{}, err
		}
	}
	return config, nil
}

func validateHTTPURL(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s must be an HTTP(S) URL with a host and no credentials, query, or fragment", name)
	}
	return nil
}

func newPool(config Config) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(config.DatabaseURL)
	if err != nil {
		return nil, errors.New("DATABASE_URL is invalid")
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	return pool, nil
}

func newAgentClient(config Config) (*agentclient.Client, error) {
	return agentclient.New(config.AgentURL)
}

func newRepository(pool *pgxpool.Pool) *pgrepository.Repository {
	return pgrepository.New(pool)
}

func newRecommendationService(repository *pgrepository.Repository, agent *agentclient.Client) *recommendation.Service {
	return recommendation.New(repository, agent)
}

func newHealthChecks(pool *pgxpool.Pool, config Config) *health.Checks {
	return health.New(pool, config.QdrantURL, config.LocalStackURL)
}

func newHTTPHandler(service *recommendation.Service, checks *health.Checks) http.Handler {
	return httpapi.NewHandler(service, checks)
}

func registerHTTPServer(lifecycle fx.Lifecycle, config Config, handler http.Handler, pool *pgxpool.Pool) {
	server := &http.Server{
		Addr:              config.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			listener, err := net.Listen("tcp", server.Addr)
			if err != nil {
				return fmt.Errorf("listen on %s: %w", server.Addr, err)
			}
			go func() {
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					slog.Error("HTTP server stopped unexpectedly", "error", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			shutdownErr := server.Shutdown(ctx)
			pool.Close()
			if shutdownErr != nil {
				return fmt.Errorf("shut down HTTP server: %w", shutdownErr)
			}
			return nil
		},
	})
}

func applicationOptions(config Config) []fx.Option {
	return []fx.Option{
		fx.Supply(config),
		fx.Provide(
			newPool,
			newAgentClient,
			newRepository,
			newRecommendationService,
			newHealthChecks,
			newHTTPHandler,
		),
		fx.Invoke(registerHTTPServer),
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
		logger.Error("invalid backend configuration", "error", err)
		os.Exit(1)
	}
	fx.New(applicationOptions(config)...).Run()
}
