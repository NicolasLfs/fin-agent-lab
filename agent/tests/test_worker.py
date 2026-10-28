import json

from fin_agent.retrieval import content_hash
from fin_agent.worker import SQSNewsWorker


def test_worker_deletes_message_only_after_idempotent_qdrant_upsert():
    calls = []

    class FakeSQS:
        def delete_message(self, **kwargs):
            calls.append(("delete", kwargs["ReceiptHandle"]))

    class FakeStore:
        def upsert_news(self, event):
            calls.append(("upsert", event.news.resolved_tickers()))
            return ["qdrant-point-id"]

    worker = SQSNewsWorker(FakeSQS(), "queue-url", FakeStore())
    message = {
        "MessageId": "sqs-message-id",
        "ReceiptHandle": "receipt-1",
        "Body": json.dumps(
            {
                "eventType": "news.processing",
                "correlationId": "corr-1",
                "news": {
                    "title": "VALE3 reports profit growth",
                    "source": "wire",
                    "url": "https://example.test/news",
                    "content": "VALE3 reported profit growth.",
                    "contentHash": content_hash("VALE3 reported profit growth."),
                    "publishedAt": "2026-10-28T10:00:00Z",
                },
            }
        ),
    }

    worker.handle_message(message)

    assert calls == [("upsert", ["VALE3"]), ("delete", "receipt-1")]


def test_worker_keeps_invalid_message_for_sqs_retry_and_logs_error(caplog):
    class FakeSQS:
        def delete_message(self, **_kwargs):
            raise AssertionError("invalid message must not be deleted")

    class FakeStore:
        def upsert_news(self, _event):
            raise AssertionError("invalid message must not be processed")

    worker = SQSNewsWorker(FakeSQS(), "queue-url", FakeStore())
    worker.handle_message(
        {
            "MessageId": "sqs-message-id",
            "ReceiptHandle": "receipt-1",
            "Body": "{",
        }
    )

    assert "leaving message for retry or dead-letter routing" in caplog.text


def test_worker_rejects_missing_receipt_handle_before_processing(caplog):
    class FakeSQS:
        def delete_message(self, **_kwargs):
            raise AssertionError("message without a receipt handle must not be deleted")

    class FakeStore:
        def upsert_news(self, _event):
            raise AssertionError("invalid envelope must not reach Qdrant")

    worker = SQSNewsWorker(FakeSQS(), "queue-url", FakeStore())
    worker.handle_message(
        {
            "MessageId": "sqs-message-id",
            "Body": json.dumps(
                {
                    "eventType": "news.processing",
                    "correlationId": "corr-1",
                    "news": {
                        "title": "VALE3 reports profit",
                        "source": "wire",
                        "url": "https://example.test/news",
                        "content": "VALE3 profit",
                        "contentHash": content_hash("VALE3 profit"),
                        "publishedAt": "2026-10-28T10:00:00Z",
                    },
                }
            ),
        }
    )

    assert "invalid SQS message envelope" in caplog.text
