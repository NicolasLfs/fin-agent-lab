package ingest

import (
	"encoding/json"
	"testing"
	"time"
)

func TestContentHashNormalizesContent(t *testing.T) {
	content := "  Hello\r\n world\r\n  "
	want := "60b65ab310480818c4289227f2ec68f1714743db8571b4cb190e100c0085be3d"
	if got := ContentHash(content); got != want {
		t.Fatalf("ContentHash() = %q, want %q", got, want)
	}
}

func TestParseRSSFeedParsesItems(t *testing.T) {
	feed := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Mercado</title>
    <item>
      <title>Taxa do Tesouro cai</title>
      <link>https://example.com/news/1</link>
      <description>O Tesouro reduziu a taxa de juros.</description>
      <pubDate>Mon, 02 Jan 2006 15:04:05 -0700</pubDate>
    </item>
  </channel>
</rss>`

	items, err := ParseRSS(feed)
	if err != nil {
		t.Fatalf("ParseRSS() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("ParseRSS() items = %d, want 1", len(items))
	}
	item := items[0]
	if item.Title != "Taxa do Tesouro cai" {
		t.Fatalf("item.Title = %q", item.Title)
	}
	if item.URL != "https://example.com/news/1" {
		t.Fatalf("item.URL = %q", item.URL)
	}
	if item.Source != "Mercado" {
		t.Fatalf("item.Source = %q, want Mercado", item.Source)
	}
	if item.ContentHash == "" {
		t.Fatal("item.ContentHash empty")
	}
	if item.PublishedAt.IsZero() {
		t.Fatal("PublishedAt is zero")
	}
}

func TestBuildNewsProcessingMessageIncludesCorrelationID(t *testing.T) {
	item := NewsItem{
		Title:       "Taxa do Tesouro cai",
		URL:         "https://example.com/news/1",
		Source:      "Mercado",
		Content:     "O Tesouro reduziu a taxa de juros.",
		PublishedAt: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
	}
	item.ContentHash = ContentHash(item.Content)

	body, err := BuildNewsProcessingMessage(item, "corr-123")
	if err != nil {
		t.Fatalf("BuildNewsProcessingMessage() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if payload["eventType"] != "news.processing" {
		t.Fatalf("eventType = %v, want news.processing", payload["eventType"])
	}
	if payload["correlationId"] != "corr-123" {
		t.Fatalf("correlationId = %v, want corr-123", payload["correlationId"])
	}
	if payload["contentHash"] == "" {
		t.Fatal("contentHash missing")
	}
}
