from types import SimpleNamespace

import pytest

from fin_agent.embeddings import GeminiEmbedder
from fin_agent.providers import UpstreamUnavailableError


def test_gemini_embedding_uses_configured_dimension_and_document_task():
    calls = []

    class Models:
        def embed_content(self, **kwargs):
            calls.append(kwargs)
            return SimpleNamespace(embeddings=[SimpleNamespace(values=[0.1] * 768)])

    embedder = GeminiEmbedder(SimpleNamespace(models=Models()), "gemini-embedding-001", 768)
    vector = embedder.embed_document("VALE3 article body")

    assert len(vector) == 768
    assert calls[0]["model"] == "gemini-embedding-001"
    assert calls[0]["config"].output_dimensionality == 768
    assert calls[0]["config"].task_type == "RETRIEVAL_DOCUMENT"


def test_gemini_embedding_rejects_vector_dimension_mismatch():
    class Models:
        def embed_content(self, **_kwargs):
            return SimpleNamespace(embeddings=[SimpleNamespace(values=[0.1])])

    embedder = GeminiEmbedder(SimpleNamespace(models=Models()), "gemini-embedding-001", 768)
    with pytest.raises(UpstreamUnavailableError, match="invalid dimensions"):
        embedder.embed_query("VALE3")
