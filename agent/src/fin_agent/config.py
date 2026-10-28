import os
import math
from dataclasses import dataclass
from typing import Mapping
from urllib.parse import urlparse

from .providers import ConfigurationError


@dataclass(frozen=True)
class Settings:
    database_url: str
    qdrant_url: str
    qdrant_api_key: str | None
    embedding_model: str
    embedding_dimensions: int
    gemini_api_key: str
    openrouter_api_key: str
    openrouter_models: list[str]
    openrouter_timeout: float
    news_queue_url: str
    aws_region: str
    aws_endpoint_url: str | None
    sqs_wait_time_seconds: int

    @classmethod
    def from_env(cls, environ: Mapping[str, str] | None = None) -> "Settings":
        values = os.environ if environ is None else environ
        try:
            dimensions = int(values.get("GEMINI_EMBEDDING_DIMENSIONS", "768"))
        except ValueError as error:
            raise ConfigurationError("GEMINI_EMBEDDING_DIMENSIONS must be a positive integer") from error
        try:
            timeout = float(values.get("OPENROUTER_TIMEOUT_SECONDS", "25"))
            wait_seconds = int(values.get("SQS_WAIT_TIME_SECONDS", "20"))
        except ValueError as error:
            raise ConfigurationError("provider timeout and SQS wait must be numeric") from error
        if dimensions not in (768, 1536):
            raise ConfigurationError("GEMINI_EMBEDDING_DIMENSIONS must be a positive integer of 768 or 1536")
        if not math.isfinite(timeout) or timeout <= 0 or wait_seconds < 0 or wait_seconds > 20:
            raise ConfigurationError("provider timeout must be positive and SQS wait must be between 0 and 20 seconds")
        model_list = [
            model.strip()
            for model in values.get("OPENROUTER_MODELS", "").split(",")
            if model.strip()
        ]
        qdrant_url = values.get("QDRANT_URL", "http://localhost:6333").strip()
        parsed_qdrant_url = urlparse(qdrant_url)
        if (
            parsed_qdrant_url.scheme not in ("http", "https")
            or not parsed_qdrant_url.hostname
            or parsed_qdrant_url.username is not None
            or parsed_qdrant_url.password is not None
        ):
            raise ConfigurationError("QDRANT_URL must be an HTTP(S) URL without embedded credentials")
        return cls(
            database_url=values.get("DATABASE_URL", "").strip(),
            qdrant_url=qdrant_url,
            qdrant_api_key=values.get("QDRANT_API_KEY") or None,
            embedding_model=values.get("GEMINI_EMBEDDING_MODEL", "gemini-embedding-001").strip(),
            embedding_dimensions=dimensions,
            gemini_api_key=values.get("GEMINI_API_KEY", "").strip(),
            openrouter_api_key=values.get("OPENROUTER_API_KEY", "").strip(),
            openrouter_models=model_list,
            openrouter_timeout=timeout,
            news_queue_url=values.get("NEWS_PROCESSING_QUEUE_URL", "").strip(),
            aws_region=values.get("AWS_REGION", "us-east-1").strip(),
            aws_endpoint_url=values.get("AWS_ENDPOINT_URL") or None,
            sqs_wait_time_seconds=wait_seconds,
        )

    def validate_for_runtime(self) -> None:
        missing = [
            name
            for name, value in (
                ("DATABASE_URL", self.database_url),
                ("GEMINI_API_KEY", self.gemini_api_key),
                ("OPENROUTER_API_KEY", self.openrouter_api_key),
                ("NEWS_PROCESSING_QUEUE_URL", self.news_queue_url),
            )
            if not value
        ]
        if missing:
            raise ConfigurationError(f"required runtime configuration is missing: {', '.join(missing)}")
        if len(self.openrouter_models) < 2:
            raise ConfigurationError("OPENROUTER_MODELS must contain a primary model and at least one fallback")
        if len(self.openrouter_models) != len(set(self.openrouter_models)):
            raise ConfigurationError("OPENROUTER_MODELS must not contain duplicate model routes")
        if not self.aws_region:
            raise ConfigurationError("AWS_REGION must not be empty")
        if not self.embedding_model:
            raise ConfigurationError("GEMINI_EMBEDDING_MODEL must not be empty")
