import os
from datetime import datetime, timezone
from uuid import uuid4

import pytest
from qdrant_client import QdrantClient

from fin_agent.retrieval import NewsEvent, NewsVectorStore, content_hash


QDRANT_URL = os.environ.get("QDRANT_TEST_URL")


@pytest.mark.skipif(not QDRANT_URL, reason="set QDRANT_TEST_URL to run Qdrant integration")
def test_qdrant_persists_metadata_and_applies_asset_and_date_filters():
    client = QdrantClient(url=QDRANT_URL)
    collection_name = f"agent-test-{uuid4().hex}"
    store = NewsVectorStore(client, collection_name, 3, lambda _text: [0.1, 0.2, 0.3])
    content = "VALE3 reported profit growth."
    try:
        store.ensure_collection()
        store.ensure_collection()
        event = NewsEvent.model_validate(
            {
                "eventType": "news.processing",
                "correlationId": "integration-test",
                "news": {
                    "title": "VALE3 reports profit growth",
                    "source": "integration",
                    "url": "https://example.test/news",
                    "content": content,
                    "contentHash": content_hash(content),
                    "publishedAt": datetime.now(timezone.utc).isoformat(),
                    "assetTickers": ["VALE3"],
                    "sentimentScore": "0.5",
                },
            }
        )
        point_ids = store.upsert_news(event)

        results = store.search("VALE3", [0.1, 0.2, 0.3])

        assert point_ids[0] in {item["id"] for item in results}
        assert all(item["ticker"] == "VALE3" for item in results)
    finally:
        client.delete_collection(collection_name)
        client.close()
