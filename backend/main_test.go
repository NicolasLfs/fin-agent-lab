package main

import (
	"strings"
	"testing"

	"go.uber.org/fx"
)

func TestLoadConfigRequiresConnectionAndDependencyURLs(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("AGENT_URL", "")
	t.Setenv("QDRANT_URL", "")
	t.Setenv("LOCALSTACK_URL", "")

	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("loadConfig() error = %v, want DATABASE_URL requirement", err)
	}
}

func TestLoadConfigDefaultsHTTPAddress(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/fin_agent")
	t.Setenv("AGENT_URL", "http://agent:8000")
	t.Setenv("QDRANT_URL", "http://qdrant:6333")
	t.Setenv("LOCALSTACK_URL", "http://localstack:4566")
	t.Setenv("HTTP_ADDR", "")

	config, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if config.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q, want :8080", config.HTTPAddr)
	}
}

func TestLoadConfigRejectsInvalidDependencyURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/fin_agent")
	t.Setenv("AGENT_URL", "file:///tmp/agent")
	t.Setenv("QDRANT_URL", "http://qdrant:6333")
	t.Setenv("LOCALSTACK_URL", "http://localstack:4566")

	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig() error = nil, want invalid AGENT_URL error")
	}
}

func TestFxApplicationGraphIsValid(t *testing.T) {
	config := Config{
		DatabaseURL: "postgres://localhost:5432/fin_agent",
		AgentURL:    "http://agent:8000", QdrantURL: "http://qdrant:6333",
		LocalStackURL: "http://localstack:4566", HTTPAddr: ":8080",
	}
	if err := fx.ValidateApp(applicationOptions(config)...); err != nil {
		t.Fatalf("Fx application graph is invalid: %v", err)
	}
}
