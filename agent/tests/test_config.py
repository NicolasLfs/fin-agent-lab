from pathlib import Path

import pytest

from fin_agent.config import Settings
from fin_agent.providers import ConfigurationError


def test_settings_load_root_dotenv_without_overriding_process_environment(tmp_path, monkeypatch):
    root = tmp_path
    nested = root / "agent" / "src"
    nested.mkdir(parents=True)
    (root / ".env").write_text(
        "GEMINI_API_KEY=from-file\nOPENROUTER_TIMEOUT_SECONDS=10\n",
        encoding="utf-8",
    )
    monkeypatch.chdir(nested)
    monkeypatch.delenv("GEMINI_API_KEY", raising=False)
    monkeypatch.setenv("OPENROUTER_TIMEOUT_SECONDS", "15")

    settings = Settings.from_env()

    assert settings.gemini_api_key == "from-file"
    assert settings.openrouter_timeout == 15


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
