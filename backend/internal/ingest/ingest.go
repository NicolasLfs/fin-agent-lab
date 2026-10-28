package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// NewsItem is a normalized market-news item ready to be persisted and queued.
type NewsItem struct {
	Title       string    `json:"title"`
	Source      string    `json:"source"`
	URL         string    `json:"url"`
	Content     string    `json:"content"`
	PublishedAt time.Time `json:"publishedAt"`
	ContentHash string    `json:"contentHash"`
}

// NormalizeContent canonicalizes raw content before hashing or publishing.
func NormalizeContent(raw string) string {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.TrimSpace(normalized)
}

// ContentHash returns the SHA-256 of the normalized content in lowercase hex.
func ContentHash(raw string) string {
	hash := sha256.Sum256([]byte(NormalizeContent(raw)))
	return hex.EncodeToString(hash[:])
}

type rssFeed struct {
	Title   string      `xml:"title"`
	Channel rssChannel  `xml:"channel"`
	Entries []atomEntry `xml:"entry"`
}

type rssChannel struct {
	Title string    `xml:"title"`
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

type atomEntry struct {
	Title     string   `xml:"title"`
	Link      atomLink `xml:"link"`
	Published string   `xml:"published"`
	Updated   string   `xml:"updated"`
	Summary   string   `xml:"summary"`
	Content   string   `xml:"content"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
}

// ParseRSS parses a simple RSS or Atom feed and returns a normalized slice of NewsItem records.
func ParseRSS(feedXML string) ([]NewsItem, error) {
	trimmed := strings.TrimSpace(feedXML)
	if trimmed == "" {
		return nil, fmt.Errorf("feed is empty")
	}

	var feed rssFeed
	if err := xml.Unmarshal([]byte(trimmed), &feed); err != nil {
		return nil, fmt.Errorf("decode RSS feed: %w", err)
	}

	feedTitle := strings.TrimSpace(feed.Title)
	if feedTitle == "" {
		feedTitle = strings.TrimSpace(feed.Channel.Title)
	}
	if feedTitle == "" {
		feedTitle = "feed"
	}

	items := make([]NewsItem, 0, len(feed.Channel.Items)+len(feed.Entries))
	for _, item := range feed.Channel.Items {
		art := NewsItem{
			Title:       strings.TrimSpace(item.Title),
			Source:      feedTitle,
			URL:         strings.TrimSpace(item.Link),
			Content:     strings.TrimSpace(item.Description),
			PublishedAt: parseRSSDate(item.PubDate),
		}
		if art.URL == "" {
			art.URL = strings.TrimSpace(item.GUID)
		}
		if art.Content == "" {
			art.Content = art.Title
		}
		if art.ContentHash = ContentHash(art.Content); art.ContentHash == "" {
			return nil, fmt.Errorf("hash missing for news item %q", art.Title)
		}
		items = append(items, art)
	}
	for _, entry := range feed.Entries {
		art := NewsItem{
			Title:       strings.TrimSpace(entry.Title),
			Source:      feedTitle,
			URL:         strings.TrimSpace(entry.Link.Href),
			Content:     strings.TrimSpace(entry.Summary),
			PublishedAt: parseRSSDate(entry.Published),
		}
		if art.Content == "" {
			art.Content = strings.TrimSpace(entry.Content)
		}
		if art.PublishedAt.IsZero() {
			art.PublishedAt = parseRSSDate(entry.Updated)
		}
		if art.Content == "" {
			art.Content = art.Title
		}
		art.ContentHash = ContentHash(art.Content)
		items = append(items, art)
	}
	for i := range items {
		if items[i].Title == "" {
			items[i].Title = items[i].URL
		}
		if items[i].Source == "" {
			items[i].Source = "feed"
		}
		if items[i].PublishedAt.IsZero() {
			items[i].PublishedAt = time.Now().UTC()
		}
		if items[i].ContentHash == "" {
			items[i].ContentHash = ContentHash(items[i].Content)
		}
	}
	return items, nil
}

func parseRSSDate(value string) time.Time {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339,
		time.RFC1123,
		time.RFC1123Z,
		time.RFC822,
		time.RFC822Z,
		"2006-01-02T15:04:05Z07:00",
		"2006-01-02 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 MST",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, trimmed); err == nil {
			return ts.UTC()
		}
	}
	return time.Time{}
}

// BuildNewsProcessingMessage creates the JSON payload published to the news-processing SQS queue.
func BuildNewsProcessingMessage(item NewsItem, correlationID string) ([]byte, error) {
	if item.ContentHash == "" {
		item.ContentHash = ContentHash(item.Content)
	}
	if item.PublishedAt.IsZero() {
		item.PublishedAt = time.Now().UTC()
	}

	payload := map[string]any{
		"eventType":     "news.processing",
		"correlationId": correlationID,
		"news": map[string]any{
			"title":       item.Title,
			"source":      item.Source,
			"url":         item.URL,
			"content":     item.Content,
			"contentHash": item.ContentHash,
			"publishedAt": item.PublishedAt.Format(time.RFC3339),
		},
	}
	return json.Marshal(payload)
}

// SQSClient publishes news-processing messages to an AWS SQS endpoint, such as LocalStack.
type SQSClient struct {
	client *sqs.Client
}

func NewSQSClientFromEnv(ctx context.Context) (*SQSClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	return &SQSClient{client: sqs.NewFromConfig(cfg)}, nil
}

func NewSQSClientFromConfig(cfg aws.Config) *SQSClient {
	return &SQSClient{client: sqs.NewFromConfig(cfg)}
}

func (c *SQSClient) PublishNews(ctx context.Context, queueURL string, item NewsItem, correlationID string) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("sqs client is not configured")
	}
	if strings.TrimSpace(queueURL) == "" {
		return fmt.Errorf("queue URL is required")
	}
	body, err := BuildNewsProcessingMessage(item, correlationID)
	if err != nil {
		return fmt.Errorf("build message: %w", err)
	}
	input := &sqs.SendMessageInput{
		QueueUrl:    aws.String(queueURL),
		MessageBody: aws.String(string(body)),
	}
	if strings.HasSuffix(queueURL, ".fifo") {
		input.MessageGroupId = aws.String("news-processing")
		input.MessageDeduplicationId = aws.String(item.ContentHash)
	}
	_, err = c.client.SendMessage(ctx, input)
	if err != nil {
		return fmt.Errorf("send SQS message: %w", err)
	}
	return nil
}

func queueURLFromEnv() string {
	if env := strings.TrimSpace(os.Getenv("NEWS_PROCESSING_QUEUE_URL")); env != "" {
		return env
	}
	return "http://localhost:4566/000000000000/news-processing-queue"
}
