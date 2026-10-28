import math
from typing import Any

from google import genai
from google.genai import types

from .providers import ConfigurationError, UpstreamUnavailableError


class GeminiEmbedder:
    def __init__(self, client: Any, model: str, dimensions: int):
        if dimensions not in (768, 1536):
            raise ConfigurationError("Gemini embedding dimensions must be 768 or 1536")
        self.client = client
        self.model = model
        self.dimensions = dimensions

    @classmethod
    def from_api_key(cls, api_key: str, model: str, dimensions: int) -> "GeminiEmbedder":
        if not api_key.strip():
            raise ConfigurationError("GEMINI_API_KEY is required")
        return cls(genai.Client(api_key=api_key), model, dimensions)

    def embed_document(self, text: str) -> list[float]:
        return self._embed(text, "RETRIEVAL_DOCUMENT")

    def embed_query(self, text: str) -> list[float]:
        return self._embed(text, "RETRIEVAL_QUERY")

    def close(self) -> None:
        self.client.close()

    def _embed(self, text: str, task_type: str) -> list[float]:
        if not text.strip():
            raise ValueError("embedding text must not be empty")
        try:
            response = self.client.models.embed_content(
                model=self.model,
                contents=text,
                config=types.EmbedContentConfig(
                    task_type=task_type,
                    output_dimensionality=self.dimensions,
                ),
            )
        except genai.errors.APIError as error:
            raise UpstreamUnavailableError("Gemini embedding provider is unavailable") from error
        if not response.embeddings or not response.embeddings[0].values:
            raise UpstreamUnavailableError("Gemini returned an empty embedding")
        vector = list(response.embeddings[0].values)
        if len(vector) != self.dimensions or any(not math.isfinite(value) for value in vector):
            raise UpstreamUnavailableError("Gemini returned an embedding with invalid dimensions or values")
        return vector
