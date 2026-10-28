package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type pinger interface {
	Ping(context.Context) error
}

type Checks struct {
	postgres   pinger
	qdrant     string
	localstack string
	client     *http.Client
}

func New(postgres pinger, qdrantURL, localstackURL string) *Checks {
	return &Checks{
		postgres:   postgres,
		qdrant:     strings.TrimRight(qdrantURL, "/") + "/healthz",
		localstack: strings.TrimRight(localstackURL, "/") + "/_localstack/health",
		client:     &http.Client{Timeout: 2 * time.Second},
	}
}

func (c *Checks) Live(context.Context) error {
	return nil
}

func (c *Checks) Ready(ctx context.Context) error {
	var failures []error
	if c.postgres == nil {
		failures = append(failures, errors.New("postgres readiness check is not configured"))
	} else if err := c.postgres.Ping(ctx); err != nil {
		failures = append(failures, fmt.Errorf("postgres is not ready: %w", err))
	}
	if err := c.checkHTTP(ctx, "Qdrant", c.qdrant); err != nil {
		failures = append(failures, err)
	}
	if err := c.checkSQS(ctx); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (c *Checks) checkHTTP(ctx context.Context, dependency, endpoint string) error {
	if endpoint == "/healthz" || endpoint == "/_localstack/health" {
		return fmt.Errorf("%s readiness endpoint is not configured", dependency)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create %s health request: %w", dependency, err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("%s is not ready: %w", dependency, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return fmt.Errorf("read %s health response: %w", dependency, err)
	}
	if len(body) > 4096 {
		return fmt.Errorf("%s health response exceeds 4 KiB", dependency)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s health endpoint returned HTTP %d", dependency, response.StatusCode)
	}
	return nil
}

func (c *Checks) checkSQS(ctx context.Context) error {
	if c.localstack == "/_localstack/health" {
		return errors.New("LocalStack readiness endpoint is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.localstack, nil)
	if err != nil {
		return fmt.Errorf("create LocalStack health request: %w", err)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("LocalStack SQS is not ready: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("LocalStack health endpoint returned HTTP %d", response.StatusCode)
	}
	var status struct {
		Services map[string]string `json:"services"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil {
		return fmt.Errorf("read LocalStack health response: %w", err)
	}
	if len(body) > 16<<10 {
		return errors.New("LocalStack health response exceeds 16 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&status); err != nil {
		return fmt.Errorf("decode LocalStack health response: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("LocalStack health response contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing LocalStack health response: %w", err)
	}
	sqsStatus, ok := status.Services["sqs"]
	if !ok || (sqsStatus != "available" && sqsStatus != "running") {
		return fmt.Errorf("LocalStack SQS is not ready (status %q)", sqsStatus)
	}
	return nil
}
