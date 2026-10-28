package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type pingerFunc func(context.Context) error

func (f pingerFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReadyChecksPostgresQdrantAndSQS(t *testing.T) {
	checked := map[string]bool{}
	qdrant := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		checked["qdrant"] = request.URL.Path == "/healthz"
		writer.WriteHeader(http.StatusOK)
	}))
	defer qdrant.Close()
	localstack := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		checked["sqs"] = request.URL.Path == "/_localstack/health"
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"services":{"sqs":"available"}}`))
	}))
	defer localstack.Close()
	postgresChecked := false
	checks := New(pingerFunc(func(context.Context) error {
		postgresChecked = true
		return nil
	}), qdrant.URL, localstack.URL)

	if err := checks.Ready(context.Background()); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if !postgresChecked || !checked["qdrant"] || !checked["sqs"] {
		t.Fatalf("checks: postgres=%v qdrant=%v sqs=%v", postgresChecked, checked["qdrant"], checked["sqs"])
	}
}

func TestReadyReportsUnavailableDependency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	checks := New(pingerFunc(func(context.Context) error { return errors.New("db down") }), server.URL, server.URL)

	if err := checks.Ready(context.Background()); err == nil {
		t.Fatal("Ready() error = nil, want dependency failure")
	}
}

func TestReadyChecksAllDependenciesWhenPostgresFails(t *testing.T) {
	checked := map[string]bool{}
	qdrant := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		checked["qdrant"] = true
		writer.WriteHeader(http.StatusOK)
	}))
	defer qdrant.Close()
	localstack := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		checked["sqs"] = true
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"services":{"sqs":"available"}}`))
	}))
	defer localstack.Close()
	checks := New(pingerFunc(func(context.Context) error { return errors.New("db down") }), qdrant.URL, localstack.URL)

	if err := checks.Ready(context.Background()); err == nil {
		t.Fatal("Ready() error = nil, want postgres failure")
	}
	if !checked["qdrant"] || !checked["sqs"] {
		t.Fatalf("readiness skipped dependency checks: %#v", checked)
	}
}

func TestLiveDoesNotProbeDependencies(t *testing.T) {
	called := false
	checks := New(pingerFunc(func(context.Context) error {
		called = true
		return errors.New("must not be called")
	}), "", "")
	if err := checks.Live(context.Background()); err != nil {
		t.Fatalf("Live() error = %v", err)
	}
	if called {
		t.Fatal("Live() probed dependencies")
	}
}
