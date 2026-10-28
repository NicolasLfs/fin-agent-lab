import pytest

from fin_agent.config import Settings
from fin_agent.providers import ConfigurationError


def test_settings_choose_768_dimensional_gemini_embeddings_and_order_routes():
    settings = Settings.from_env(
        {
            "GEMINI_EMBEDDING_MODEL": "gemini-embedding-001",
            "GEMINI_EMBEDDING_DIMENSIONS": "768",
            "OPENROUTER_MODELS": "primary/model,fallback/model",
        }
    )

    assert settings.embedding_model == "gemini-embedding-001"
    assert settings.embedding_dimensions == 768
    assert settings.openrouter_models == ["primary/model", "fallback/model"]


def test_runtime_configuration_fails_explicitly_when_required_services_are_missing():
    with pytest.raises(ConfigurationError, match="DATABASE_URL"):
        Settings.from_env({}).validate_for_runtime()


def test_settings_reject_invalid_embedding_dimensions():
    with pytest.raises(ConfigurationError, match="positive integer"):
        Settings.from_env({"GEMINI_EMBEDDING_DIMENSIONS": "not-an-int"})
