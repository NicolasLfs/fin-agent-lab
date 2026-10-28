package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

var (
	ErrProcessing = errors.New("recommendation is still processing")
	ErrConflict   = errors.New("idempotency key was already used with a different request")
)

type Failure struct {
	Status    int
	Code      string
	Message   string
	Retryable bool
}

func (e *Failure) Error() string {
	return e.Code + ": " + e.Message
}

type AssetType string

const (
	AssetTypeFixedIncome AssetType = "FIXED_INCOME"
	AssetTypeStock       AssetType = "STOCK"
	AssetTypeFII         AssetType = "FII"
)

type GenerateRequest struct {
	AssetTypes []AssetType `json:"assetTypes"`
}

type RankedAsset struct {
	Rank        int       `json:"rank"`
	Ticker      string    `json:"ticker"`
	Type        AssetType `json:"type"`
	Score       string    `json:"score"`
	ScoreMetric string    `json:"scoreMetric"`
}

type Summary struct {
	RankedAssets []RankedAsset `json:"rankedAssets"`
}

type GenerateResponse struct {
	ReportID      string    `json:"reportId"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	Summary       Summary   `json:"summary"`
	Justification string    `json:"justification"`
	CitedNewsIDs  []string  `json:"citedNewsIds"`
}

type RecommendationService interface {
	Generate(context.Context, string, GenerateRequest) (GenerateResponse, error)
}

type HealthChecks interface {
	Live(context.Context) error
	Ready(context.Context) error
}

type Handler struct {
	service RecommendationService
	health  HealthChecks
}

func NewHandler(service RecommendationService, health HealthChecks) http.Handler {
	return &Handler{service: service, health: health}
}

func (h *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/recommendations/generate":
		h.generate(writer, request)
	case "/health/live":
		h.live(writer, request)
	case "/health/ready":
		h.ready(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (h *Handler) generate(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writer.Header().Set("Allow", http.MethodPost)
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST for this endpoint.", false)
		return
	}
	mediaType, _, contentTypeErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if contentTypeErr != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json.", false)
		return
	}

	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 255 {
		writeError(writer, http.StatusBadRequest, "INVALID_IDEMPOTENCY_KEY", "A non-empty Idempotency-Key of at most 255 characters is required.", false)
		return
	}

	var input GenerateRequest
	if err := decodeRequest(writer, request, &input); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), false)
		return
	}
	if err := validateRequest(input); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), false)
		return
	}

	response, err := h.service.Generate(request.Context(), key, input)
	if err != nil {
		switch {
		case errors.Is(err, ErrProcessing):
			writeError(writer, http.StatusServiceUnavailable, "REPORT_PROCESSING", "The report is still processing; retry with the same Idempotency-Key.", true)
		case errors.Is(err, ErrConflict):
			writeError(writer, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", err.Error(), false)
		default:
			var failure *Failure
			if errors.As(err, &failure) {
				writeError(writer, failure.Status, failure.Code, failure.Message, failure.Retryable)
				return
			}
			writeError(writer, http.StatusBadGateway, "RECOMMENDATION_FAILED", "The recommendation service could not complete the request.", true)
		}
		return
	}

	writeJSON(writer, http.StatusOK, response)
}

func (h *Handler) live(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET for this endpoint.", false)
		return
	}
	if err := h.health.Live(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "NOT_LIVE", "The service is not live.", true)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) ready(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET for this endpoint.", false)
		return
	}
	if err := h.health.Ready(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "NOT_READY", "One or more required dependencies are unavailable.", true)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func decodeRequest(writer http.ResponseWriter, request *http.Request, destination any) error {
	const maxRequestSize = 1 << 20
	reader := http.MaxBytesReader(writer, request.Body, maxRequestSize)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain exactly one JSON value")
		}
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	return nil
}

func validateRequest(input GenerateRequest) error {
	if len(input.AssetTypes) == 0 {
		return errors.New("assetTypes must contain at least one supported type")
	}
	seen := make(map[AssetType]struct{}, len(input.AssetTypes))
	for _, assetType := range input.AssetTypes {
		switch assetType {
		case AssetTypeFixedIncome, AssetTypeStock, AssetTypeFII:
		default:
			return fmt.Errorf("unsupported asset type %q", assetType)
		}
		if _, ok := seen[assetType]; ok {
			return fmt.Errorf("assetTypes contains duplicate value %q", assetType)
		}
		seen[assetType] = struct{}{}
	}
	return nil
}

type apiError struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func writeError(writer http.ResponseWriter, status int, code, message string, retryable bool) {
	writeJSON(writer, status, apiError{Error: errorDetail{Code: code, Message: message, Retryable: retryable}})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		http.Error(writer, "failed to encode response", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(body.Bytes())
}
