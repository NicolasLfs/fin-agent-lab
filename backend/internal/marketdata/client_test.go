package marketdata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBCBClientFetchesAndNormalizesSGSPercentages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/dados/serie/bcdata.sgs.432/dados/ultimos/1"; got != want {
			t.Errorf("request path = %q, want %q", got, want)
		}
		if got := r.URL.Query().Get("formato"); got != "json" {
			t.Errorf("formato = %q, want json", got)
		}
		fmt.Fprint(w, `[{"data":"28/10/2026","valor":"13.75"}]`)
	}))
	defer server.Close()

	client := NewBCBClient(server.Client(), server.URL)
	observation, err := client.FetchSGS(context.Background(), 432)
	if err != nil {
		t.Fatalf("FetchSGS() error = %v", err)
	}
	if observation.Value != "0.1375" {
		t.Errorf("normalized annual rate = %q, want 0.1375", observation.Value)
	}
	if observation.SourceRecordID != "432:2026-10-28" {
		t.Errorf("source record ID = %q", observation.SourceRecordID)
	}
	if got, want := observation.ObservedAt, time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("observed at = %s, want %s", got, want)
	}
	if !strings.Contains(string(observation.RawPayload), `"valor":"13.75"`) {
		t.Errorf("raw payload did not preserve source value: %s", observation.RawPayload)
	}
}

func TestBCBClientRejectsMalformedAndUnavailableSeries(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body        string
		series      int
		wantErrPart string
	}{
		{name: "empty series", status: http.StatusOK, body: `[]`, series: 432, wantErrPart: "no observations"},
		{name: "invalid decimal", status: http.StatusOK, body: `[{"data":"28/10/2026","valor":"1e2"}]`, series: 432, wantErrPart: "decimal"},
		{name: "invalid series ID", status: http.StatusOK, body: `[]`, series: 0, wantErrPart: "series"},
		{name: "upstream unavailable", status: http.StatusServiceUnavailable, body: `temporarily unavailable`, series: 432, wantErrPart: "status 503"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()

			_, err := NewBCBClient(server.Client(), server.URL).FetchSGS(context.Background(), test.series)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.wantErrPart) {
				t.Fatalf("FetchSGS() error = %v, want text %q", err, test.wantErrPart)
			}
		})
	}
}

func TestBrapiClientFetchesExactStockAndFIIData(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("Authorization"); got != "Bearer local-test-key" {
			t.Errorf("Authorization header = %q", got)
		}
		if strings.Contains(r.URL.String(), "local-test-key") {
			t.Error("API key was placed in the URL")
		}
		switch {
		case r.URL.Path == "/api/v2/stocks/quote":
			ticker := r.URL.Query().Get("symbols")
			price := "61.03"
			if ticker == "HGLG11" {
				price = "149.72"
			}
			fmt.Fprintf(w, `{"results":[{"requestedSymbol":%q,"symbol":%q,"data":{"currency":"BRL","regularMarketPrice":%s,"regularMarketTime":"2026-10-28T19:51:30Z"}}]}`, ticker, ticker, price)
		case r.URL.Path == "/api/quote/HGLG11":
			fmt.Fprint(w, `{"results":[{"symbol":"HGLG11","currency":"BRL","regularMarketPrice":149.72,"regularMarketTime":"2026-10-28T19:51:30Z"}]}`)
		case r.URL.Path == "/api/v2/stocks/statistics":
			fmt.Fprint(w, `{"results":[{"requestedSymbol":"VALE3","symbol":"VALE3","data":{"trailingPE":10.0002}}]}`)
		case r.URL.Path == "/api/v2/fii/indicators":
			fmt.Fprint(w, `{"fiis":[{"symbol":"HGLG11","asOfDate":"2026-10-28T19:00:00Z","price":149.72,"dividendYield12m":0.08956719}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewBrapiClient(server.Client(), server.URL, "local-test-key")
	quote, stock, err := client.FetchStock(context.Background(), "VALE3")
	if err != nil {
		t.Fatalf("FetchStock() error = %v", err)
	}
	if quote.Price != "61.03" || quote.Currency != "BRL" || stock.PriceToEarnings != "10.0002" {
		t.Errorf("stock quote/indicators = %#v / %#v", quote, stock)
	}
	if !strings.Contains(string(stock.RawPayload), `"trailingPE":10.0002`) {
		t.Errorf("stock raw indicator payload = %s", stock.RawPayload)
	}

	fiiQuote, fii, err := client.FetchFII(context.Background(), "HGLG11")
	if err != nil {
		t.Fatalf("FetchFII() error = %v", err)
	}
	if fiiQuote.Price != "149.72" || fiiQuote.Currency != "BRL" ||
		fii.Price != "149.72" || fii.DividendYieldTTM != "0.08956719" {
		t.Errorf("FII quote/indicators = %#v / %#v", fiiQuote, fii)
	}
	if requests != 4 {
		t.Errorf("HTTP requests = %d, want 4", requests)
	}
}

func TestBrapiClientRejectsMissingFieldsAndNonSuccessResponses(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body        string
		wantErrPart string
	}{
		{name: "missing stock PE", status: http.StatusOK, body: `{"results":[{"requestedSymbol":"VALE3","symbol":"VALE3","data":{"currency":"BRL","regularMarketPrice":61.03,"regularMarketTime":"2026-10-28T19:51:30Z"}}]}`, wantErrPart: "trailingPE"},
		{name: "plan restriction", status: http.StatusForbidden, body: `{"message":"plan required"}`, wantErrPart: "status 403"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer server.Close()

			_, _, err := NewBrapiClient(server.Client(), server.URL, "").FetchStock(context.Background(), "VALE3")
			if err == nil || !strings.Contains(err.Error(), test.wantErrPart) {
				t.Fatalf("FetchStock() error = %v, want text %q", err, test.wantErrPart)
			}
		})
	}
}
