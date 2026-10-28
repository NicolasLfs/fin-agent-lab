from datetime import datetime, timezone
from uuid import NAMESPACE_URL, uuid5

import pytest
from pydantic import ValidationError
from qdrant_client.http import models as qmodels

from fin_agent.models import RunRequest
from fin_agent.providers import ConfigurationError, ToolValidationError
from fin_agent.retrieval import (
    NewsEvent,
    NewsVectorStore,
    QdrantNewsRetriever,
    build_news_filter,
    content_hash,
)


def test_news_filter_scopes_assets_and_last_seven_days():
    now = datetime(2026, 10, 28, 12, 0, tzinfo=timezone.utc)

    filter_ = build_news_filter(["VALE3", "HGLG11"], now=now)

    assert filter_ == qmodels.Filter(
        must=[
            qmodels.FieldCondition(
                key="asset_ticker",
                match=qmodels.MatchAny(any=["VALE3", "HGLG11"]),
            ),
            qmodels.FieldCondition(
                key="published_at",
                range=qmodels.DatetimeRange(gte=datetime(2026, 10, 21, 12, 0, tzinfo=timezone.utc)),
            ),
        ]
    )


def test_news_filter_rejects_empty_or_invalid_tickers():
    with pytest.raises(ToolValidationError):
        build_news_filter([], now=datetime.now(timezone.utc))
    with pytest.raises(ToolValidationError):
        build_news_filter(["vale3"], now=datetime.now(timezone.utc))


def test_qdrant_collection_uses_embedding_size_and_indexes_structured_metadata():
    class FakeQdrant:
        def __init__(self):
            self.created = None
            self.indexes = []

        def collection_exists(self, name):
            assert name == "news"
            return False

        def create_collection(self, **kwargs):
            self.created = kwargs

        def create_payload_index(self, **kwargs):
            self.indexes.append(kwargs)

    client = FakeQdrant()
    NewsVectorStore(client, "news", 768, lambda _text: [0.0] * 768).ensure_collection()

    assert client.created["vectors_config"].size == 768
    assert client.created["vectors_config"].distance == qmodels.Distance.COSINE
    assert {index["field_name"] for index in client.indexes} == {
        "asset_ticker",
        "published_at",
        "sentiment_score",
    }


def test_qdrant_collection_rejects_embedding_dimension_mismatch():
    class FakeQdrant:
        def collection_exists(self, _name):
            return True

        def get_collection(self, _name):
            return type(
                "Collection",
                (),
                {
                    "config": type(
                        "Config",
                        (),
                        {
                            "params": type(
                                "Params",
                                (),
                                {"vectors": qmodels.VectorParams(size=768, distance=qmodels.Distance.COSINE)},
                            )()
                        },
                    )()
                },
            )()

    with pytest.raises(ConfigurationError, match="vector size"):
        NewsVectorStore(FakeQdrant(), "news", 1536, lambda _text: []).ensure_collection()


@pytest.mark.anyio
async def test_retriever_queries_only_assets_from_deterministic_context():
    searched = []

    class Store:
        def search(self, ticker, _vector, limit):
            searched.append((ticker, limit))
            return [{"id": f"point-{ticker}", "ticker": ticker}]

    request = RunRequest.model_validate(
        {
            "reportId": "0192f298-345e-7e38-af88-e43f851a819d",
            "deterministicContext": {
                "fixedIncomeRates": [],
                "stocksAndFiis": [
                    {"ticker": "VALE3", "type": "STOCK", "referencePrice": "68.50"},
                    {"ticker": "HGLG11", "type": "FII", "referencePrice": "150.00"},
                ],
            },
            "assetTypes": ["STOCK", "FII"],
        }
    )

    retriever = QdrantNewsRetriever(Store(), lambda query: [float(len(query))], limit_per_asset=3)
    results = await retriever.retrieve(request)

    assert searched == [("VALE3", 3), ("HGLG11", 3)]
    assert [item["id"] for item in results] == ["point-VALE3", "point-HGLG11"]


def test_news_event_derives_only_explicit_ticker_mentions_and_simple_sentiment():
    event = NewsEvent.model_validate(
        {
            "eventType": "news.processing",
            "correlationId": "corr-1",
            "news": {
                "title": "VALE3 faces downgrade after losses",
                "source": "wire",
                "url": "https://example.test/news",
                "content": "The company reported losses.",
                "contentHash": "b" * 64,
                "publishedAt": "2026-10-28T10:00:00Z",
            },
        }
    )

    assert event.news.resolved_tickers() == ["VALE3"]
    assert event.news.resolved_sentiment() == -1


def test_news_upsert_uses_stable_point_ids_and_structured_payload():
    class FakeQdrant:
        def __init__(self):
            self.points = {}

        def retrieve(self, collection_name, ids, with_payload=False, with_vectors=False):
            return [self.points[point_id] for point_id in ids if point_id in self.points]

        def upsert(self, collection_name, points, wait=True):
            for point in points:
                self.points[str(point.id)] = point

    embeddings = []

    def embed(text):
        embeddings.append(text)
        return [0.1, 0.2, 0.3]

    store = NewsVectorStore(FakeQdrant(), "news", 3, embed)
    event = NewsEvent.model_validate(
        {
            "eventType": "news.processing",
            "correlationId": "corr-1",
            "news": {
                "title": "Vale outlook improves",
                "source": "wire",
                "url": "https://example.test/news",
                "content": "VALE3 outlook improves.",
                "contentHash": content_hash("VALE3 outlook improves."),
                "publishedAt": "2026-10-28T10:00:00Z",
                "assetTickers": ["VALE3"],
                "sentimentScore": "0.4",
            }
        }
    )

    first_ids = store.upsert_news(event)
    second_ids = store.upsert_news(event)

    expected_id = str(uuid5(NAMESPACE_URL, f"{content_hash('VALE3 outlook improves.')}:VALE3"))
    assert first_ids == [expected_id]
    assert second_ids == first_ids
    assert len(embeddings) == 1
    point = store.client.points[expected_id]
    assert point.payload["asset_ticker"] == "VALE3"
    assert point.payload["published_at"] == "2026-10-28T10:00:00+00:00"
    assert point.payload["sentiment_score"] == 0.4


def test_news_upsert_rejects_content_hash_that_does_not_match_normalized_text():
    store = NewsVectorStore(object(), "news", 3, lambda _text: [0.1, 0.2, 0.3])
    event = NewsEvent.model_validate(
        {
            "eventType": "news.processing",
            "correlationId": "corr-1",
            "news": {
                "title": "VALE3 news",
                "source": "wire",
                "url": "https://example.test/news",
                "content": "content",
                "contentHash": "a" * 64,
                "publishedAt": "2026-10-28T10:00:00Z",
            },
        }
    )

    with pytest.raises(ToolValidationError, match="contentHash"):
        store.upsert_news(event)


def test_news_upsert_sanitizes_control_characters_after_hash_validation():
    class FakeQdrant:
        def __init__(self):
            self.points = {}

        def retrieve(self, collection_name, ids, with_payload=False, with_vectors=False):
            return []

        def upsert(self, collection_name, points, wait=True):
            self.points[str(points[0].id)] = points[0]

    raw_content = "VALE3 reported profit.\u202e"
    client = FakeQdrant()
    store = NewsVectorStore(client, "news", 3, lambda _text: [0.1, 0.2, 0.3])
    event = NewsEvent.model_validate(
        {
            "eventType": "news.processing",
            "correlationId": "corr-1",
            "news": {
                "title": "VALE3\u0000 report",
                "source": "wire",
                "url": "https://example.test/news",
                "content": raw_content,
                "contentHash": content_hash(raw_content),
                "publishedAt": "2026-10-28T10:00:00Z",
            },
        }
    )

    store.upsert_news(event)

    point = next(iter(client.points.values()))
    assert point.payload["title"] == "VALE3 report"
    assert point.payload["content"] == "VALE3 reported profit."


def test_news_event_rejects_missing_or_invalid_metadata():
    with pytest.raises(ValidationError):
        NewsEvent.model_validate({"news": {"title": "missing metadata"}})
    with pytest.raises(ValidationError):
        NewsEvent.model_validate(
            {
                "news": {
                    "title": "news",
                    "source": "wire",
                    "url": "https://example.test/news",
                    "content": "body",
                    "contentHash": "bad",
                    "publishedAt": "2026-10-28T10:00:00Z",
                    "assetTickers": ["vale3"],
                    "sentimentScore": "2",
                }
            }
        )


def test_news_event_rejects_malformed_url():
    with pytest.raises(ValidationError):
        NewsEvent.model_validate(
            {
                "eventType": "news.processing",
                "correlationId": "corr-1",
                "news": {
                    "title": "VALE3 news",
                    "source": "wire",
                    "url": "https://",
                    "content": "VALE3 news body",
                    "contentHash": content_hash("VALE3 news body"),
                    "publishedAt": "2026-10-28T10:00:00Z",
                },
            }
        )
