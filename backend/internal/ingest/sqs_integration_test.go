package ingest

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

func TestSQSClientPublishesNewsToLocalStack(t *testing.T) {
	endpoint := os.Getenv("LOCALSTACK_TEST_URL")
	if endpoint == "" {
		t.Skip("set LOCALSTACK_TEST_URL to run LocalStack SQS integration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := sqs.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(options *sqs.Options) {
		options.BaseEndpoint = aws.String(endpoint)
	})

	queueName := "news-processing-test-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	queue, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String(queueName)})
	if err != nil {
		t.Fatalf("create LocalStack queue: %v", err)
	}
	if queue.QueueUrl == nil {
		t.Fatal("LocalStack CreateQueue returned no queue URL")
	}
	defer func() {
		if _, err := client.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: queue.QueueUrl}); err != nil {
			t.Errorf("delete LocalStack queue: %v", err)
		}
	}()

	content := "VALE3 reports profit growth."
	item := NewsItem{
		Title:       "VALE3 reports profit growth",
		Source:      "integration-test",
		URL:         "https://example.test/news",
		Content:     content,
		PublishedAt: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
		ContentHash: ContentHash(content),
	}
	publisher := &SQSClient{client: client}
	if err := publisher.PublishNews(ctx, *queue.QueueUrl, item, "integration-correlation"); err != nil {
		t.Fatalf("publish news to LocalStack: %v", err)
	}

	messages, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            queue.QueueUrl,
		MaxNumberOfMessages: 1,
		WaitTimeSeconds:     5,
	})
	if err != nil {
		t.Fatalf("receive LocalStack message: %v", err)
	}
	if len(messages.Messages) != 1 || messages.Messages[0].Body == nil {
		t.Fatalf("received %d LocalStack messages, want one with a body", len(messages.Messages))
	}

	var payload struct {
		EventType     string `json:"eventType"`
		CorrelationID string `json:"correlationId"`
		News          struct {
			ContentHash string `json:"contentHash"`
			Content     string `json:"content"`
		} `json:"news"`
	}
	if err := json.Unmarshal([]byte(*messages.Messages[0].Body), &payload); err != nil {
		t.Fatalf("decode LocalStack message: %v", err)
	}
	if payload.EventType != "news.processing" ||
		payload.CorrelationID != "integration-correlation" ||
		payload.News.ContentHash != item.ContentHash ||
		payload.News.Content != content {
		t.Fatalf("LocalStack message = %#v, want published news contract", payload)
	}
}
