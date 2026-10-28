import asyncio
import json
import logging
from typing import Any

from botocore.exceptions import BotoCoreError, ClientError
from pydantic import ValidationError

from .providers import ToolValidationError, UpstreamUnavailableError
from .retrieval import NewsEvent, NewsVectorStore

logger = logging.getLogger(__name__)


class SQSNewsWorker:
    def __init__(
        self,
        sqs_client: Any,
        queue_url: str,
        store: NewsVectorStore,
        wait_time_seconds: int = 20,
    ):
        if not queue_url.strip():
            raise ValueError("SQS queue URL is required")
        if not 0 <= wait_time_seconds <= 20:
            raise ValueError("SQS long-poll wait time must be between 0 and 20 seconds")
        self.sqs_client = sqs_client
        self.queue_url = queue_url
        self.store = store
        self.wait_time_seconds = wait_time_seconds

    def handle_message(self, message: dict[str, Any]) -> None:
        message_id = str(message.get("MessageId", "unknown"))
        body = message.get("Body")
        receipt_handle = message.get("ReceiptHandle")
        if not isinstance(body, str) or not body or not isinstance(receipt_handle, str) or not receipt_handle:
            logger.error(
                "invalid SQS message envelope; leaving message for retry or dead-letter routing",
                extra={
                    "messageId": message_id,
                    "correlationId": self._correlation_id(message),
                    "errorType": "InvalidEnvelope",
                },
            )
            return
        try:
            event = NewsEvent.model_validate(json.loads(body))
            self.store.upsert_news(event)
        except (json.JSONDecodeError, ValidationError, ToolValidationError, UpstreamUnavailableError) as error:
            logger.error(
                "news message processing failed; leaving message for retry or dead-letter routing",
                extra={
                    "messageId": message_id,
                    "correlationId": self._correlation_id(message),
                    "errorType": type(error).__name__,
                },
            )
            return
        try:
            self.sqs_client.delete_message(
                QueueUrl=self.queue_url,
                ReceiptHandle=receipt_handle,
            )
        except (BotoCoreError, ClientError) as error:
            logger.error(
                "processed news message could not be acknowledged",
                extra={
                    "messageId": message_id,
                    "correlationId": event.correlation_id,
                    "errorType": type(error).__name__,
                },
            )

    async def run(self) -> None:
        while True:
            try:
                response = await asyncio.to_thread(
                    self.sqs_client.receive_message,
                    QueueUrl=self.queue_url,
                    MaxNumberOfMessages=10,
                    WaitTimeSeconds=self.wait_time_seconds,
                    VisibilityTimeout=60,
                )
            except (BotoCoreError, ClientError) as error:
                logger.error("SQS news queue polling failed", extra={"errorType": type(error).__name__})
                await asyncio.sleep(2)
                continue
            for message in response.get("Messages", []):
                await asyncio.to_thread(self.handle_message, message)

    @staticmethod
    def _correlation_id(message: dict[str, Any]) -> str:
        try:
            payload = json.loads(message.get("Body", "{}"))
        except json.JSONDecodeError:
            return "unknown"
        correlation_id = payload.get("correlationId", "unknown")
        return correlation_id if isinstance(correlation_id, str) else "unknown"
