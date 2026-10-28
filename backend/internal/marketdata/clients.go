package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"fin-agent/internal/domain"
)

const (
	defaultBCBBaseURL   = "https://api.bcb.gov.br"
	defaultBrapiBaseURL = "https://brapi.dev"
)

var decimalValuePattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
var marketTickerPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,31}$`)

type Observation struct {
	SeriesID       int
	ObservedAt     time.Time
	Value          string
	SourceRecordID string
	RawPayload     json.RawMessage
}

type Quote struct {
	Ticker         string
	Price          string
	Currency       string
	QuotedAt       time.Time
	SourceRecordID string
	RawPayload     json.RawMessage
}

type StockIndicators struct {
	PriceToEarnings string
	RawPayload      json.RawMessage
}

type FIIIndicators struct {
	Price            string
	DividendYieldTTM string
	ObservedAt       time.Time
	RawPayload       json.RawMessage
}

type BCBClient struct {
	httpClient *http.Client
	baseURL    string
}

type BrapiClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

type HTTPError struct {
	Provider   string
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s returned status %d", e.Provider, e.StatusCode)
}

func (e *HTTPError) Retryable() bool {
	return e.StatusCode == http.StatusRequestTimeout ||
		e.StatusCode == http.StatusTooManyRequests ||
		e.StatusCode >= http.StatusInternalServerError
}

func NewBCBClient(client *http.Client, baseURL string) *BCBClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBCBBaseURL
	}
	return &BCBClient{httpClient: client, baseURL: strings.TrimRight(baseURL, "/")}
}

func NewBrapiClient(client *http.Client, baseURL, apiKey string) *BrapiClient {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultBrapiBaseURL
	}
	return &BrapiClient{
		httpClient: client,
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     strings.TrimSpace(apiKey),
	}
}

func (c *BCBClient) FetchSGS(ctx context.Context, seriesID int) (Observation, error) {
	if seriesID <= 0 {
		return Observation{}, errors.New("SGS series ID must be positive")
	}
	endpoint, err := url.Parse(c.baseURL + "/dados/serie/bcdata.sgs." + strconv.Itoa(seriesID) + "/dados/ultimos/1")
	if err != nil {
		return Observation{}, fmt.Errorf("build SGS URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("formato", "json")
	endpoint.RawQuery = query.Encode()
	body, err := c.get(ctx, endpoint.String())
	if err != nil {
		return Observation{}, err
	}
	var response []struct {
		Date  string `json:"data"`
		Value string `json:"valor"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Observation{}, fmt.Errorf("decode SGS series %d: %w", seriesID, err)
	}
	if len(response) == 0 {
		return Observation{}, fmt.Errorf("SGS series %d has no observations", seriesID)
	}
	entry := response[len(response)-1]
	date, err := time.Parse("02/01/2006", entry.Date)
	if err != nil {
		return Observation{}, fmt.Errorf("parse SGS series %d date %q: %w", seriesID, entry.Date, err)
	}
	fraction, err := percentageToFraction(entry.Value)
	if err != nil {
		return Observation{}, fmt.Errorf("parse SGS series %d decimal: %w", seriesID, err)
	}
	if _, err := json.Marshal(json.RawMessage(body)); err != nil {
		return Observation{}, fmt.Errorf("validate SGS raw payload: %w", err)
	}
	return Observation{
		SeriesID:       seriesID,
		ObservedAt:     date.UTC(),
		Value:          fraction,
		SourceRecordID: strconv.Itoa(seriesID) + ":" + date.Format("2006-01-02"),
		RawPayload:     append(json.RawMessage(nil), body...),
	}, nil
}

func (c *BCBClient) get(ctx context.Context, target string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("create SGS request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request SGS: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &HTTPError{Provider: "SGS", StatusCode: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read SGS response: %w", err)
	}
	return body, nil
}

func (c *BrapiClient) FetchStock(ctx context.Context, ticker string) (Quote, StockIndicators, error) {
	ticker, err := validateTicker(ticker)
	if err != nil {
		return Quote{}, StockIndicators{}, err
	}
	quoteBody, err := c.get(ctx, "/api/v2/stocks/quote", url.Values{"symbols": []string{ticker}})
	if err != nil {
		return Quote{}, StockIndicators{}, err
	}
	quote, err := parseBrapiQuote(quoteBody, ticker)
	if err != nil {
		return Quote{}, StockIndicators{}, err
	}
	statisticsBody, err := c.get(ctx, "/api/v2/stocks/statistics", url.Values{
		"symbols": []string{ticker}, "mode": []string{"current"},
	})
	if err != nil {
		return Quote{}, StockIndicators{}, err
	}
	var statistics struct {
		Results []struct {
			RequestedSymbol string          `json:"requestedSymbol"`
			Data            json.RawMessage `json:"data"`
		} `json:"results"`
	}
	if err := json.Unmarshal(statisticsBody, &statistics); err != nil {
		return Quote{}, StockIndicators{}, fmt.Errorf("decode Brapi statistics for %s: %w", ticker, err)
	}
	rawData, err := findResultData(statistics.Results, ticker)
	if err != nil {
		return Quote{}, StockIndicators{}, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &data); err != nil {
		return Quote{}, StockIndicators{}, fmt.Errorf("decode Brapi statistics fields for %s: %w", ticker, err)
	}
	priceToEarnings, err := decimalField(data, "trailingPE")
	if err != nil {
		return Quote{}, StockIndicators{}, fmt.Errorf("Brapi statistics for %s: %w", ticker, err)
	}
	return quote, StockIndicators{
		PriceToEarnings: priceToEarnings,
		RawPayload:      append(json.RawMessage(nil), rawData...),
	}, nil
}

func (c *BrapiClient) FetchFII(ctx context.Context, ticker string) (Quote, FIIIndicators, error) {
	ticker, err := validateTicker(ticker)
	if err != nil {
		return Quote{}, FIIIndicators{}, err
	}
	quoteBody, err := c.get(ctx, "/api/quote/"+ticker, nil)
	if err != nil {
		return Quote{}, FIIIndicators{}, err
	}
	quote, err := parseLegacyBrapiQuote(quoteBody, ticker)
	if err != nil {
		return Quote{}, FIIIndicators{}, err
	}
	indicatorsBody, err := c.get(ctx, "/api/v2/fii/indicators", url.Values{"symbols": []string{ticker}})
	if err != nil {
		return Quote{}, FIIIndicators{}, err
	}
	var response struct {
		FIIs []json.RawMessage `json:"fiis"`
	}
	if err := json.Unmarshal(indicatorsBody, &response); err != nil {
		return Quote{}, FIIIndicators{}, fmt.Errorf("decode Brapi FII indicators for %s: %w", ticker, err)
	}
	var entry struct {
		Symbol           string          `json:"symbol"`
		AsOfDate         string          `json:"asOfDate"`
		Price            json.RawMessage `json:"price"`
		DividendYield12m json.RawMessage `json:"dividendYield12m"`
	}
	var rawEntry json.RawMessage
	for _, raw := range response.FIIs {
		var candidate struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(raw, &candidate); err != nil {
			return Quote{}, FIIIndicators{}, fmt.Errorf("decode Brapi FII indicator symbol: %w", err)
		}
		if candidate.Symbol == ticker {
			rawEntry = raw
			break
		}
	}
	if len(rawEntry) == 0 {
		return Quote{}, FIIIndicators{}, fmt.Errorf("Brapi returned no FII indicators for %s", ticker)
	}
	if err := json.Unmarshal(rawEntry, &entry); err != nil {
		return Quote{}, FIIIndicators{}, fmt.Errorf("decode Brapi FII indicator fields for %s: %w", ticker, err)
	}
	price, err := decimalValue(entry.Price)
	if err != nil {
		return Quote{}, FIIIndicators{}, fmt.Errorf("Brapi FII indicators for %s: invalid price: %w", ticker, err)
	}
	priceMoney, err := domain.NewMoney(price, "BRL")
	if err != nil || strings.HasPrefix(price, "-") || priceMoney.Amount() == "0.00" {
		return Quote{}, FIIIndicators{}, fmt.Errorf("Brapi FII indicators for %s: invalid positive BRL price %q", ticker, price)
	}
	yield, err := decimalValue(entry.DividendYield12m)
	if err != nil || strings.HasPrefix(yield, "-") {
		return Quote{}, FIIIndicators{}, fmt.Errorf("Brapi FII indicators for %s: invalid nonnegative dividendYield12m", ticker)
	}
	observedAt, err := parseBrapiTime(entry.AsOfDate)
	if err != nil {
		return Quote{}, FIIIndicators{}, fmt.Errorf("parse Brapi FII asOfDate for %s: %w", ticker, err)
	}
	return quote, FIIIndicators{
		Price:            priceMoney.Amount(),
		DividendYieldTTM: yield,
		ObservedAt:       observedAt,
		RawPayload:       append(json.RawMessage(nil), rawEntry...),
	}, nil
}

func (c *BrapiClient) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	endpoint, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, fmt.Errorf("build Brapi URL: %w", err)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Brapi request: %w", err)
	}
	if c.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request Brapi: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, &HTTPError{Provider: "Brapi", StatusCode: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read Brapi response: %w", err)
	}
	return body, nil
}

func parseBrapiQuote(body []byte, ticker string) (Quote, error) {
	var response struct {
		Results []struct {
			RequestedSymbol string          `json:"requestedSymbol"`
			Data            json.RawMessage `json:"data"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Quote{}, fmt.Errorf("decode Brapi quote for %s: %w", ticker, err)
	}
	rawData, err := findResultData(response.Results, ticker)
	if err != nil {
		return Quote{}, err
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &data); err != nil {
		return Quote{}, fmt.Errorf("decode Brapi quote fields for %s: %w", ticker, err)
	}
	price, err := decimalField(data, "regularMarketPrice")
	if err != nil {
		return Quote{}, fmt.Errorf("Brapi quote for %s: %w", ticker, err)
	}
	currency, err := stringField(data, "currency")
	if err != nil {
		return Quote{}, fmt.Errorf("Brapi quote for %s: %w", ticker, err)
	}
	money, err := domain.NewMoney(price, currency)
	if err != nil || strings.HasPrefix(price, "-") || money.Amount() == "0.00" {
		return Quote{}, fmt.Errorf("Brapi quote for %s: invalid positive price %q %s", ticker, price, currency)
	}
	quotedAtText, err := stringField(data, "regularMarketTime")
	if err != nil {
		return Quote{}, fmt.Errorf("Brapi quote for %s: %w", ticker, err)
	}
	quotedAt, err := parseBrapiTime(quotedAtText)
	if err != nil {
		return Quote{}, fmt.Errorf("parse Brapi quote time for %s: %w", ticker, err)
	}
	return Quote{
		Ticker: ticker, Price: money.Amount(), Currency: currency, QuotedAt: quotedAt,
		SourceRecordID: ticker + ":" + quotedAt.UTC().Format(time.RFC3339Nano),
		RawPayload:     append(json.RawMessage(nil), rawData...),
	}, nil
}

func parseLegacyBrapiQuote(body []byte, ticker string) (Quote, error) {
	var response struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return Quote{}, fmt.Errorf("decode Brapi quote for %s: %w", ticker, err)
	}
	var rawData json.RawMessage
	for _, raw := range response.Results {
		var data struct {
			Symbol string `json:"symbol"`
		}
		if err := json.Unmarshal(raw, &data); err != nil {
			return Quote{}, fmt.Errorf("decode Brapi quote symbol: %w", err)
		}
		if data.Symbol == ticker {
			rawData = raw
			break
		}
	}
	if len(rawData) == 0 {
		return Quote{}, fmt.Errorf("Brapi returned no quote for %s", ticker)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rawData, &fields); err != nil {
		return Quote{}, fmt.Errorf("decode Brapi quote fields for %s: %w", ticker, err)
	}
	price, err := decimalField(fields, "regularMarketPrice")
	if err != nil {
		return Quote{}, fmt.Errorf("Brapi quote for %s: %w", ticker, err)
	}
	currency, err := stringField(fields, "currency")
	if err != nil {
		return Quote{}, fmt.Errorf("Brapi quote for %s: %w", ticker, err)
	}
	money, err := domain.NewMoney(price, currency)
	if err != nil || strings.HasPrefix(price, "-") || money.Amount() == "0.00" {
		return Quote{}, fmt.Errorf("Brapi quote for %s: invalid positive price %q %s", ticker, price, currency)
	}
	quotedAtText, err := stringField(fields, "regularMarketTime")
	if err != nil {
		return Quote{}, fmt.Errorf("Brapi quote for %s: %w", ticker, err)
	}
	quotedAt, err := parseBrapiTime(quotedAtText)
	if err != nil {
		return Quote{}, fmt.Errorf("parse Brapi quote time for %s: %w", ticker, err)
	}
	return Quote{
		Ticker: ticker, Price: money.Amount(), Currency: currency, QuotedAt: quotedAt,
		SourceRecordID: ticker + ":" + quotedAt.UTC().Format(time.RFC3339Nano),
		RawPayload:     append(json.RawMessage(nil), rawData...),
	}, nil
}

func findResultData(results []struct {
	RequestedSymbol string          `json:"requestedSymbol"`
	Data            json.RawMessage `json:"data"`
}, ticker string) (json.RawMessage, error) {
	for _, result := range results {
		if result.RequestedSymbol == ticker {
			if len(result.Data) == 0 || string(result.Data) == "null" {
				break
			}
			return result.Data, nil
		}
	}
	return nil, fmt.Errorf("Brapi returned no data for %s", ticker)
}

func decimalField(fields map[string]json.RawMessage, name string) (string, error) {
	value, ok := fields[name]
	if !ok || string(value) == "null" || len(value) == 0 {
		return "", fmt.Errorf("required field %s is absent", name)
	}
	return decimalValue(value)
}

func decimalValue(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		if !decimalValuePattern.MatchString(value) {
			return "", fmt.Errorf("%q is not an exact decimal", value)
		}
		return value, nil
	}
	number := strings.TrimSpace(string(raw))
	if !decimalValuePattern.MatchString(number) {
		return "", fmt.Errorf("%q is not an exact decimal", number)
	}
	return number, nil
}

func stringField(fields map[string]json.RawMessage, name string) (string, error) {
	raw, ok := fields[name]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return "", fmt.Errorf("required field %s is absent", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("required field %s is invalid", name)
	}
	return value, nil
}

func percentageToFraction(value string) (string, error) {
	if !decimalValuePattern.MatchString(value) {
		return "", fmt.Errorf("%q is not a decimal percentage", value)
	}
	percentage, ok := new(big.Rat).SetString(value)
	if !ok || percentage.Sign() < 0 {
		return "", fmt.Errorf("%q is not a nonnegative decimal percentage", value)
	}
	return new(big.Rat).Quo(percentage, big.NewRat(100, 1)).FloatString(decimalScale(value) + 2),
		nil
}

func decimalScale(value string) int {
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		return len(value) - dot - 1
	}
	return 0
}

func parseBrapiTime(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05-07",
		"2006-01-02 15:04:05-07:00",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("timestamp %q is not RFC3339", value)
}

func validateTicker(ticker string) (string, error) {
	ticker = strings.TrimSpace(ticker)
	if !marketTickerPattern.MatchString(ticker) {
		return "", errors.New("ticker is empty or has an invalid format")
	}
	return ticker, nil
}
