package main

import (
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
)

func TestLoadConfigRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("BRAPI_API_KEY", "")
	t.Setenv("MARKET_DATA_SYNC_INTERVAL", "")

	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("loadConfig() error = %v, want DATABASE_URL requirement", err)
	}
}

func TestLoadConfigDefaultsSyncIntervalAndLoadsBrapiKeyFromEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/fin_agent")
	t.Setenv("BRAPI_API_KEY", "local-test-key")
	t.Setenv("MARKET_DATA_SYNC_INTERVAL", "")

	config, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.SyncInterval != 15*time.Minute {
		t.Errorf("SyncInterval = %s, want 15m", config.SyncInterval)
	}
	if config.BrapiAPIKey != "local-test-key" {
		t.Errorf("BrapiAPIKey = %q, want environment value", config.BrapiAPIKey)
	}
}

func TestLoadConfigRejectsInvalidSyncInterval(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/fin_agent")
	t.Setenv("BRAPI_API_KEY", "")
	t.Setenv("MARKET_DATA_SYNC_INTERVAL", "not-a-duration")

	if _, err := loadConfig(); err == nil || !strings.Contains(err.Error(), "MARKET_DATA_SYNC_INTERVAL") {
		t.Fatalf("loadConfig() error = %v, want invalid interval error", err)
	}
}

func TestFxApplicationGraphIsValid(t *testing.T) {
	config := Config{
		DatabaseURL:  "postgres://localhost:5432/fin_agent",
		SyncInterval: defaultSyncInterval,
	}
	if err := fx.ValidateApp(applicationOptions(config)...); err != nil {
		t.Fatalf("Fx worker application graph is invalid: %v", err)
	}
}
