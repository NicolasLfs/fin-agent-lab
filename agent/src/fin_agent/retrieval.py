import asyncio
import hashlib
import math
import re
from contextlib import ExitStack
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from threading import Lock
from typing import Any, Callable, Literal, Protocol
from urllib.parse import urlsplit
from uuid import NAMESPACE_URL, uuid5

import httpx
from pydantic import Field, StrictStr, field_validator, model_validator
from qdrant_client.http import models as qmodels
from qdrant_client.http.exceptions import ApiException, ResponseHandlingException, UnexpectedResponse

from .models import ContractModel, RunRequest, _TICKER
from .providers import ConfigurationError, ToolValidationError, UpstreamUnavailableError
from .text_utils import sanitize_text

_HASH = re.compile(r"^[0-9a-f]{64}$")
_MENTIONED_TICKER = re.compile(r"\b[A-Z]{4}\d{1,2}\b")
_POSITIVE_WORDS = {
    "crescimento", "ganho", "lucro", "melhora", "positivo", "recorde", "upgrade",
    "growth", "gain", "improves", "improvement", "profit", "positive", "upgrade",
}
_NEGATIVE_WORDS = {
    "fraude", "investigação", "investigacao", "queda", "perda", "prejuízo",
    "prejuizo", "rebaixamento", "risco", "negativo", "downgrade", "loss",
    "negative", "recall", "warning",
}
_UPSERT_LOCK_STRIPES = 256


class NewsPayload(ContractModel):
    title: StrictStr = Field(min_length=1, max_length=500)
    source: StrictStr = Field(min_length=1, max_length=200)
    url: StrictStr = Field(min_length=1, max_length=2048)
    content: StrictStr = Field(min_length=1, max_length=100_000)
    content_hash: StrictStr = Field(alias="contentHash")
    published_at: datetime = Field(alias="publishedAt")
    asset_tickers: list[StrictStr] | None = Field(default=None, alias="assetTickers")
    sentiment_score: Decimal | None = Field(default=None, alias="sentimentScore")

    @field_validator("content_hash")
    @classmethod
    def validate_content_hash(cls, value: str) -> str:
        if not _HASH.fullmatch(value):
            raise ValueError("contentHash must be a lowercase SHA-256 digest")
        return value

    @field_validator("published_at")
    @classmethod
    def validate_published_at(cls, value: datetime) -> datetime:
        if value.tzinfo is None or value.utcoffset() is None:
            raise ValueError("publishedAt must include a timezone")
        return value.astimezone(timezone.utc)

    @field_validator("asset_tickers")
    @classmethod
    def validate_asset_tickers(cls, values: list[str] | None) -> list[str] | None:
        if values is None:
            return None
        if not values or len(values) != len(set(values)) or any(not _TICKER.fullmatch(ticker) for ticker in values):
            raise ValueError("assetTickers must contain unique uppercase ticker values")
        return values

    @field_validator("sentiment_score")
    @classmethod
    def validate_sentiment_score(cls, value: Decimal | None) -> Decimal | None:
        if value is not None and (not value.is_finite() or value < -1 or value > 1):
            raise ValueError("sentimentScore must be between -1 and 1")
        return value

    @model_validator(mode="after")
    def validate_url(self) -> "NewsPayload":
        parsed_url = urlsplit(self.url)
        if (
            parsed_url.scheme not in ("http", "https")
            or not parsed_url.hostname
            or parsed_url.username is not None
            or parsed_url.password is not None
        ):
            raise ValueError("url must be an HTTP(S) URL with a host and no embedded credentials")
        return self

    def resolved_tickers(self) -> list[str]:
        tickers = self.asset_tickers or sorted(set(_MENTIONED_TICKER.findall(f"{self.title}\n{self.content}")))
        if not tickers:
            raise ToolValidationError("news item does not identify a supported asset ticker")
        return tickers

    def resolved_sentiment(self) -> Decimal:
        if self.sentiment_score is not None:
            return self.sentiment_score
        words = re.findall(r"[a-zA-ZÀ-ÿ]+", f"{self.title} {self.content}".casefold())
        positive = sum(word in _POSITIVE_WORDS for word in words)
        negative = sum(word in _NEGATIVE_WORDS for word in words)
        known = positive + negative
        if known == 0:
            return Decimal(0)
        return Decimal(positive - negative) / Decimal(known)


class NewsEvent(ContractModel):
    event_type: Literal["news.processing"] = Field(alias="eventType")
    correlation_id: StrictStr = Field(
        alias="correlationId",
        pattern=r"^[A-Za-z0-9._:-]{1,128}$",
    )
    news: NewsPayload


class QueryVectorStore(Protocol):
    def search(self, ticker: str, vector: list[float], limit: int = 5) -> list[dict[str, Any]]: ...


class QdrantNewsRetriever:
    def __init__(
        self,
        store: QueryVectorStore,
        embed_query: Callable[[str], list[float]],
        limit_per_asset: int = 5,
    ):
        if limit_per_asset < 1:
            raise ConfigurationError("Qdrant retrieval limit must be positive")
        self.store = store
        self.embed_query = embed_query
        self.limit_per_asset = limit_per_asset

    async def retrieve(self, request: RunRequest) -> list[dict[str, Any]]:
        tickers = [
            item.ticker for item in request.deterministic_context.fixed_income_rates
        ]
        tickers.extend(item.ticker for item in request.deterministic_context.stocks_and_fiis)
        if not tickers:
            raise ToolValidationError("retrieval requires at least one asset from the deterministic context")
        results: list[dict[str, Any]] = []
        seen_ids: set[str] = set()
        for ticker in tickers:
            query = f"Recent financial news about the Brazilian market asset {ticker}"
            vector = await asyncio.to_thread(self.embed_query, query)
            matches = await asyncio.to_thread(self.store.search, ticker, vector, self.limit_per_asset)
            for item in matches:
                point_id = str(item["id"])
                if point_id not in seen_ids:
                    results.append(item)
                    seen_ids.add(point_id)
        return results


def build_news_filter(
    tickers: list[str],
    *,
    now: datetime | None = None,
) -> qmodels.Filter:
    if not tickers or len(tickers) != len(set(tickers)) or any(not _TICKER.fullmatch(ticker) for ticker in tickers):
        raise ToolValidationError("Qdrant search requires unique, valid asset tickers")
    reference = now or datetime.now(timezone.utc)
    if reference.tzinfo is None or reference.utcoffset() is None:
        raise ToolValidationError("Qdrant search time must include a timezone")
    since = reference.astimezone(timezone.utc) - timedelta(days=7)
    return qmodels.Filter(
        must=[
            qmodels.FieldCondition(
                key="asset_ticker",
                match=qmodels.MatchAny(any=tickers),
            ),
            qmodels.FieldCondition(
                key="published_at",
                range=qmodels.DatetimeRange(gte=since),
            ),
        ]
    )


class NewsVectorStore:
    def __init__(
        self,
        client: Any,
        collection_name: str,
        vector_size: int,
        embed: Callable[[str], list[float]],
    ):
        if vector_size <= 0:
            raise ConfigurationError("embedding dimensions must be positive")
        self.client = client
        self.collection_name = collection_name
        self.vector_size = vector_size
        self.embed = embed
        self._upsert_locks = tuple(Lock() for _ in range(_UPSERT_LOCK_STRIPES))

    def ensure_collection(self) -> None:
        try:
            if not self.client.collection_exists(self.collection_name):
                self.client.create_collection(
                    collection_name=self.collection_name,
                    vectors_config=qmodels.VectorParams(size=self.vector_size, distance=qmodels.Distance.COSINE),
                )
            else:
                collection = self.client.get_collection(self.collection_name)
                vectors = collection.config.params.vectors
                if not isinstance(vectors, qmodels.VectorParams) or vectors.size != self.vector_size:
                    raise ConfigurationError("Qdrant collection vector size does not match the embedding configuration")
            for field_name, schema in (
                ("asset_ticker", qmodels.PayloadSchemaType.KEYWORD),
                ("published_at", qmodels.PayloadSchemaType.DATETIME),
                ("sentiment_score", qmodels.PayloadSchemaType.FLOAT),
            ):
                self.client.create_payload_index(
                    collection_name=self.collection_name,
                    field_name=field_name,
                    field_schema=schema,
                )
        except (ApiException, ResponseHandlingException, UnexpectedResponse, httpx.RequestError) as error:
            raise UpstreamUnavailableError("Qdrant collection is unavailable") from error

    def upsert_news(self, event: NewsEvent) -> list[str]:
        normalized_content = event.news.content.replace("\r\n", "\n").replace("\r", "\n").strip()
        if content_hash(normalized_content) != event.news.content_hash:
            raise ToolValidationError("news contentHash does not match the normalized article content")
        tickers = event.news.resolved_tickers()
        point_ids = [str(uuid5(NAMESPACE_URL, f"{event.news.content_hash}:{ticker}")) for ticker in tickers]
        lock_indexes = sorted(
            {
                int(point_id.replace("-", "")[:8], 16) % len(self._upsert_locks)
                for point_id in point_ids
            }
        )
        with ExitStack() as locks:
            for lock_index in lock_indexes:
                locks.enter_context(self._upsert_locks[lock_index])
            return self._upsert_news(event, normalized_content, tickers, point_ids)

    def _upsert_news(
        self,
        event: NewsEvent,
        normalized_content: str,
        tickers: list[str],
        point_ids: list[str],
    ) -> list[str]:
        try:
            existing = self.client.retrieve(
                collection_name=self.collection_name,
                ids=point_ids,
                with_payload=False,
                with_vectors=False,
            )
        except (ApiException, ResponseHandlingException, UnexpectedResponse, httpx.RequestError) as error:
            raise UpstreamUnavailableError("Qdrant could not check existing news points") from error
        existing_ids = {str(point.id) for point in existing}
        missing = [(ticker, point_id) for ticker, point_id in zip(tickers, point_ids) if point_id not in existing_ids]
        if not missing:
            return point_ids
        title = sanitize_text(event.news.title)
        article = sanitize_text(event.news.content)
        if not title or not article:
            raise ToolValidationError("news title and content must remain non-empty after sanitization")
        content = f"{title}\n{article}"
        try:
            vector = self.embed(content)
        except UpstreamUnavailableError:
            raise
        except ValueError as error:
            raise ToolValidationError("Gemini rejected the news content for embedding") from error
        except OSError as error:
            raise UpstreamUnavailableError("Gemini embedding request failed") from error
        if len(vector) != self.vector_size or any(
            not isinstance(value, (int, float)) or not math.isfinite(value) for value in vector
        ):
            raise ToolValidationError("embedding provider returned a vector with an invalid size or values")
        published_at = event.news.published_at.astimezone(timezone.utc).isoformat()
        sentiment = float(event.news.resolved_sentiment())
        points = [
            qmodels.PointStruct(
                id=point_id,
                vector=vector,
                payload={
                    "asset_ticker": ticker,
                    "published_at": published_at,
                    "sentiment_score": sentiment,
                    "title": title,
                    "content": article,
                    "source": event.news.source,
                    "url": event.news.url,
                    "content_hash": event.news.content_hash,
                },
            )
            for ticker, point_id in missing
        ]
        try:
            self.client.upsert(collection_name=self.collection_name, points=points, wait=True)
        except (ApiException, ResponseHandlingException, UnexpectedResponse, httpx.RequestError) as error:
            raise UpstreamUnavailableError("Qdrant could not persist news embeddings") from error
        return point_ids

    def search(self, ticker: str, vector: list[float], limit: int = 5) -> list[dict]:
        if not _TICKER.fullmatch(ticker) or limit < 1:
            raise ToolValidationError("Qdrant search parameters are invalid")
        try:
            response = self.client.query_points(
                collection_name=self.collection_name,
                query=vector,
                query_filter=build_news_filter([ticker]),
                limit=limit,
                with_payload=True,
            )
        except (ApiException, ResponseHandlingException, UnexpectedResponse, httpx.RequestError) as error:
            raise UpstreamUnavailableError("Qdrant news search failed") from error
        return [
            {
                "id": str(point.id),
                "ticker": point.payload["asset_ticker"],
                "publishedAt": point.payload["published_at"],
                "sentimentScore": point.payload["sentiment_score"],
                "title": point.payload["title"],
                "content": point.payload["content"],
            }
            for point in response.points
            if isinstance(point.payload, dict)
            and all(key in point.payload for key in ("asset_ticker", "published_at", "sentiment_score", "title", "content"))
        ]


def content_hash(content: str) -> str:
    return hashlib.sha256(content.encode("utf-8")).hexdigest()
