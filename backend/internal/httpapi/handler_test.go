package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type recommendationServiceFunc func(context.Context, string, GenerateRequest) (GenerateResponse, error)

func (f recommendationServiceFunc) Generate(ctx context.Context, key string, request GenerateRequest) (GenerateResponse, error) {
	return f(ctx, key, request)
}

type healthChecks struct {
	liveErr  error
	readyErr error
}

func (h healthChecks) Live(context.Context) error  { return h.liveErr }
func (h healthChecks) Ready(context.Context) error { return h.readyErr }

func TestGenerateRecommendationsValidatesContract(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		key         string
		body        string
		contentType string
		wantStatus  int
	}{
		{name: "wrong method", method: http.MethodGet, key: "key", body: `{"assetTypes":["STOCK"]}`, wantStatus: http.StatusMethodNotAllowed},
		{name: "missing content type", method: http.MethodPost, key: "key", body: `{"assetTypes":["STOCK"]}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "missing idempotency key", method: http.MethodPost, key: "", body: `{"assetTypes":["STOCK"]}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "malformed JSON", method: http.MethodPost, key: "key", body: `{"assetTypes":`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "empty asset types", method: http.MethodPost, key: "key", body: `{"assetTypes":[]}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unsupported asset type", method: http.MethodPost, key: "key", body: `{"assetTypes":["CRYPTO"]}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceCalled := false
			handler := NewHandler(recommendationServiceFunc(func(context.Context, string, GenerateRequest) (GenerateResponse, error) {
				serviceCalled = true
				return GenerateResponse{}, nil
			}), healthChecks{})
			request := httptest.NewRequest(tt.method, "/recommendations/generate", strings.NewReader(tt.body))
			if tt.key != "" {
				request.Header.Set("Idempotency-Key", tt.key)
			}
			if tt.contentType != "" {
				request.Header.Set("Content-Type", tt.contentType)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body)
			}
			if serviceCalled {
				t.Fatal("recommendation service was called for an invalid request")
			}
		})
	}
}

func TestGenerateRecommendationsReturnsServiceResponse(t *testing.T) {
	createdAt := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	want := GenerateResponse{
		ReportID:  "report-1",
		Status:    "COMPLETED",
		CreatedAt: createdAt,
		Summary: Summary{RankedAssets: []RankedAsset{
			{Rank: 1, Ticker: "VALE3", Type: "STOCK", Score: "0.1000", ScoreMetric: "EARNINGS_YIELD"},
		}},
		Justification: "Contextual explanation.",
		CitedNewsIDs:  []string{"qdrant-id"},
	}
	handler := NewHandler(recommendationServiceFunc(func(_ context.Context, key string, request GenerateRequest) (GenerateResponse, error) {
		if key != "request-1" {
			t.Errorf("idempotency key = %q, want request-1", key)
		}
		if len(request.AssetTypes) != 1 || request.AssetTypes[0] != "STOCK" {
			t.Errorf("request = %#v, want STOCK", request)
		}
		return want, nil
	}), healthChecks{})
	request := httptest.NewRequest(http.MethodPost, "/recommendations/generate", strings.NewReader(`{"assetTypes":["STOCK"]}`))
	request.Header.Set("Idempotency-Key", "request-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body)
	}
	var got GenerateResponse
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.ReportID != want.ReportID || got.Status != want.Status ||
		got.Justification != want.Justification || len(got.CitedNewsIDs) != 1 ||
		got.CitedNewsIDs[0] != "qdrant-id" || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestGenerateRecommendationsMapsRetryableFailureTo503(t *testing.T) {
	handler := NewHandler(recommendationServiceFunc(func(context.Context, string, GenerateRequest) (GenerateResponse, error) {
		return GenerateResponse{}, ErrProcessing
	}), healthChecks{})
	request := httptest.NewRequest(http.MethodPost, "/recommendations/generate", strings.NewReader(`{"assetTypes":["STOCK"]}`))
	request.Header.Set("Idempotency-Key", "request-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"retryable":true`) {
		t.Fatalf("error response must mark retryable: %s", response.Body)
	}
}

func TestGenerateRecommendationsMapsUpstreamFailure(t *testing.T) {
	tests := []struct {
		name       string
		failure    *Failure
		wantStatus int
	}{
		{name: "agent validation", failure: &Failure{Status: http.StatusUnprocessableEntity, Code: "AGENT_VALIDATION", Message: "invalid context"}, wantStatus: http.StatusUnprocessableEntity},
		{name: "provider rate limit", failure: &Failure{Status: http.StatusTooManyRequests, Code: "LLM_PROVIDER_RATE_LIMIT", Message: "quota exhausted", Retryable: true}, wantStatus: http.StatusTooManyRequests},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(recommendationServiceFunc(func(context.Context, string, GenerateRequest) (GenerateResponse, error) {
				return GenerateResponse{}, tt.failure
			}), healthChecks{})
			request := httptest.NewRequest(http.MethodPost, "/recommendations/generate", strings.NewReader(`{"assetTypes":["STOCK"]}`))
			request.Header.Set("Idempotency-Key", "request-1")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body)
			}
			if !strings.Contains(response.Body.String(), `"`+tt.failure.Code+`"`) {
				t.Fatalf("error response does not contain code %q: %s", tt.failure.Code, response.Body)
			}
		})
	}
}

func TestHealthEndpoints(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		checks     healthChecks
		wantStatus int
	}{
		{name: "live", path: "/health/live", wantStatus: http.StatusOK},
		{name: "ready", path: "/health/ready", wantStatus: http.StatusOK},
		{name: "not ready", path: "/health/ready", checks: healthChecks{readyErr: errors.New("postgres unavailable")}, wantStatus: http.StatusServiceUnavailable},
		{name: "live ignores dependency failures", path: "/health/live", checks: healthChecks{readyErr: errors.New("dependency unavailable")}, wantStatus: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(recommendationServiceFunc(func(context.Context, string, GenerateRequest) (GenerateResponse, error) {
				return GenerateResponse{}, nil
			}), tt.checks)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}
