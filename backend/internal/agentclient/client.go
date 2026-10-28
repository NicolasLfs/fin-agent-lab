package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultDialTimeout  = 3 * time.Second
	defaultTotalTimeout = 40 * time.Second
	maxResponseBytes    = 1 << 20
)

type StockOrFII struct {
	Ticker         string `json:"ticker"`
	Type           string `json:"type"`
	ReferencePrice string `json:"referencePrice"`
}

type FixedIncomeRate struct {
	Ticker    string `json:"ticker"`
	Basis     string `json:"basis"`
	Source    string `json:"source"`
	GrossRate string `json:"grossRate"`
	NetRate   string `json:"netRate"`
}

type DeterministicContext struct {
	FixedIncomeRates []FixedIncomeRate `json:"fixedIncomeRates"`
	StocksAndFiis    []StockOrFII      `json:"stocksAndFiis"`
}

type RunRequest struct {
	ReportID             string               `json:"reportId"`
	DeterministicContext DeterministicContext `json:"deterministicContext"`
	RankedAssets         []RankedAsset        `json:"rankedAssets"`
	AssetTypes           []string             `json:"assetTypes"`
}

type RankedAsset struct {
	Rank        int    `json:"rank"`
	Ticker      string `json:"ticker"`
	Type        string `json:"type"`
	Score       string `json:"score"`
	ScoreMetric string `json:"scoreMetric"`
}

type RunResponse struct {
	Status            string        `json:"status"`
	RankedAssets      []RankedAsset `json:"rankedAssets"`
	JustificationText string        `json:"justificationText"`
	CitedNewsIDs      []string      `json:"citedNewsIds"`
}

type errorEnvelope struct {
	Error *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

type Error struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

type TransportError struct {
	Err       error
	Retryable bool
}

func (e *TransportError) Error() string {
	return "agent transport failure: " + e.Err.Error()
}

func (e *TransportError) Unwrap() error {
	return e.Err
}

func (e *Error) Error() string {
	return fmt.Sprintf("agent returned HTTP %d (%s): %s", e.StatusCode, e.Code, e.Message)
}

type Client struct {
	endpoint string
	http     *http.Client
}

func New(baseURL string) (*Client, error) {
	return NewWithTimeout(baseURL, defaultTotalTimeout)
}

func NewWithTimeout(baseURL string, totalTimeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse agent URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("agent URL must be an HTTP(S) URL with a host and no credentials, query, or fragment")
	}
	if totalTimeout <= 0 {
		return nil, errors.New("agent client timeout must be positive")
	}

	transport := &http.Transport{
		Proxy:             http.ProxyFromEnvironment,
		DialContext:       (&net.Dialer{Timeout: defaultDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true,
	}
	return &Client{
		endpoint: parsed.String() + "/internal/agent/run",
		http: &http.Client{
			Transport: transport,
			Timeout:   totalTimeout,
		},
	}, nil
}

func (c *Client) Run(ctx context.Context, payload RunRequest) (RunResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return RunResponse{}, fmt.Errorf("encode agent request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return RunResponse{}, fmt.Errorf("create agent request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return RunResponse{}, &TransportError{Err: err, Retryable: true}
	}
	defer response.Body.Close()
	mediaType, _, contentTypeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if contentTypeErr != nil || mediaType != "application/json" {
		return RunResponse{}, fmt.Errorf("agent response Content-Type must be application/json")
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return RunResponse{}, &TransportError{Err: fmt.Errorf("read agent response: %w", err), Retryable: true}
	}
	if len(responseBody) > maxResponseBytes {
		return RunResponse{}, errors.New("agent response exceeds 1 MiB")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return RunResponse{}, decodeAgentError(response.StatusCode, responseBody)
	}
	if response.StatusCode != http.StatusOK {
		return RunResponse{}, fmt.Errorf("agent returned unexpected success status %d", response.StatusCode)
	}

	var result RunResponse
	if err := decodeStrict(responseBody, &result); err != nil {
		return RunResponse{}, fmt.Errorf("decode agent response: %w", err)
	}
	if result.Status != "COMPLETED" || result.JustificationText == "" {
		return RunResponse{}, errors.New("agent response must be COMPLETED and include justificationText")
	}
	return result, nil
}

func decodeAgentError(status int, body []byte) error {
	var envelope errorEnvelope
	if err := decodeStrict(body, &envelope); err != nil {
		return fmt.Errorf("agent returned HTTP %d with invalid error envelope: %w", status, err)
	}
	if envelope.Error == nil || envelope.Error.Code == "" || envelope.Error.Message == "" {
		return fmt.Errorf("agent returned HTTP %d with an incomplete error envelope", status)
	}
	return &Error{
		StatusCode: status,
		Code:       envelope.Error.Code,
		Message:    envelope.Error.Message,
		Retryable:  envelope.Error.Retryable,
	}
}

func decodeStrict(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
