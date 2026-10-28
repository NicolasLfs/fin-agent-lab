package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRunPostsContractAndDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/internal/agent/run" {
			t.Errorf("request = %s %s, want POST /internal/agent/run", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		var payload RunRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload.ReportID != "report-1" || len(payload.DeterministicContext.StocksAndFiis) != 1 ||
			payload.DeterministicContext.StocksAndFiis[0].ReferencePrice != "68.50" {
			t.Errorf("request payload = %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(RunResponse{
			Status:            "COMPLETED",
			RankedAssets:      []RankedAsset{{Rank: 1, Ticker: "VALE3", Type: "STOCK"}},
			JustificationText: "Grounded explanation.",
			CitedNewsIDs:      []string{"point-1"},
		})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := client.Run(context.Background(), RunRequest{
		ReportID: "report-1",
		DeterministicContext: DeterministicContext{
			StocksAndFiis: []StockOrFII{{Ticker: "VALE3", Type: "STOCK", ReferencePrice: "68.50"}},
		},
		AssetTypes: []string{"STOCK"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response.Status != "COMPLETED" || response.JustificationText != "Grounded explanation." ||
		len(response.CitedNewsIDs) != 1 || response.CitedNewsIDs[0] != "point-1" {
		t.Fatalf("Run() response = %#v", response)
	}
}

func TestNewUsesAgreedTimeouts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.http.Timeout != 40*time.Second {
		t.Fatalf("total timeout = %s, want 40s", client.http.Timeout)
	}
	if defaultDialTimeout != 3*time.Second {
		t.Fatalf("dial timeout = %s, want 3s", defaultDialTimeout)
	}
}

func TestRunReturnsStructuredAgentError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"code":"LLM_PROVIDER_RATE_LIMIT","message":"quota exceeded","retryable":true}}`))
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Run(context.Background(), RunRequest{ReportID: "report-1"})
	var agentError *Error
	if !errors.As(err, &agentError) {
		t.Fatalf("Run() error = %v, want *Error", err)
	}
	if agentError.StatusCode != http.StatusTooManyRequests || agentError.Code != "LLM_PROVIDER_RATE_LIMIT" || !agentError.Retryable {
		t.Fatalf("agent error = %#v", agentError)
	}
}

func TestRunRejectsMalformedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"status":"COMPLETED","rankedAssets":`))
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := client.Run(context.Background(), RunRequest{ReportID: "report-1"}); err == nil {
		t.Fatal("Run() error = nil, want malformed response error")
	}
}

func TestRunRejectsNonJSONResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte(`{"status":"COMPLETED","rankedAssets":[],"justificationText":"x","citedNewsIds":[]}`))
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := client.Run(context.Background(), RunRequest{ReportID: "report-1"}); err == nil {
		t.Fatal("Run() error = nil, want content-type validation error")
	}
}

func TestClientTimeoutIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		time.Sleep(100 * time.Millisecond)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"status":"COMPLETED","rankedAssets":[],"justificationText":"","citedNewsIds":[]}`))
	}))
	defer server.Close()
	client, err := NewWithTimeout(server.URL, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("NewWithTimeout() error = %v", err)
	}

	_, err = client.Run(context.Background(), RunRequest{ReportID: "report-1"})
	var transportError *TransportError
	if !errors.As(err, &transportError) {
		t.Fatalf("Run() error = %v, want *TransportError", err)
	}
	if !transportError.Retryable {
		t.Fatal("timeout TransportError must be retryable")
	}
}
