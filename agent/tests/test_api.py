from uuid import uuid4

from fastapi.testclient import TestClient

from fin_agent.api import create_app
from fin_agent.models import RunResponse
from fin_agent.providers import ProviderRateLimitError, ServiceUnavailableError, UpstreamUnavailableError


def valid_request() -> dict:
    return {
        "reportId": str(uuid4()),
        "deterministicContext": {
            "fixedIncomeRates": [{"ticker": "TESOURO_IPCA_2029", "netRate": "0.0650"}],
            "stocksAndFiis": [{"ticker": "VALE3", "type": "STOCK", "referencePrice": "68.50"}],
        },
        "assetTypes": ["FIXED_INCOME", "STOCK"],
    }


def test_run_returns_contract_response():
    expected = RunResponse(
        status="COMPLETED",
        rankedAssets=[
            {"rank": 1, "ticker": "TESOURO_IPCA_2029", "type": "FIXED_INCOME"},
            {"rank": 2, "ticker": "VALE3", "type": "STOCK"},
        ],
        justificationText="As notícias recuperadas não indicam bandeiras vermelhas.",
        citedNewsIds=["news-point-1"],
    )
    received = []

    async def runner(request):
        received.append(request)
        return expected

    request = valid_request()
    with TestClient(create_app(agent_runner=runner)) as client:
        response = client.post("/internal/agent/run", json=request)

    assert response.status_code == 200
    assert response.json() == expected.model_dump(by_alias=True)
    assert str(received[0].report_id) == request["reportId"]


def test_malformed_json_maps_to_bad_request():
    with TestClient(create_app(agent_runner=_unexpected_runner)) as client:
        response = client.post(
            "/internal/agent/run",
            content='{"reportId":',
            headers={"content-type": "application/json"},
        )

    assert response.status_code == 400
    assert response.json()["error"]["code"] == "INVALID_REQUEST"
    assert response.json()["error"]["retryable"] is False


def test_missing_required_fields_maps_to_bad_request():
    with TestClient(create_app(agent_runner=_unexpected_runner)) as client:
        response = client.post("/internal/agent/run", json={"reportId": str(uuid4())})

    assert response.status_code == 400
    assert response.json()["error"]["code"] == "INVALID_REQUEST"


def test_provider_rate_limit_uses_retryable_429_envelope():
    async def runner(_request):
        raise ProviderRateLimitError("all configured model routes are exhausted")

    with TestClient(create_app(agent_runner=runner)) as client:
        response = client.post("/internal/agent/run", json=valid_request())

    assert response.status_code == 429
    assert response.json() == {
        "error": {
            "code": "LLM_PROVIDER_RATE_LIMIT",
            "message": "The LLM provider quota is exhausted and no fallback route is available.",
            "retryable": True,
        }
    }


def test_dependency_failures_map_to_gateway_and_service_unavailable():
    async def unavailable_qdrant(_request):
        raise UpstreamUnavailableError("qdrant unavailable")

    async def unavailable_checkpoint(_request):
        raise ServiceUnavailableError("postgres unavailable")

    with TestClient(create_app(agent_runner=unavailable_qdrant)) as client:
        gateway = client.post("/internal/agent/run", json=valid_request())
    with TestClient(create_app(agent_runner=unavailable_checkpoint)) as client:
        service = client.post("/internal/agent/run", json=valid_request())

    assert gateway.status_code == 502
    assert gateway.json()["error"]["retryable"] is True
    assert service.status_code == 503
    assert service.json()["error"]["retryable"] is True


def test_invalid_agent_output_uses_tool_validation_envelope():
    async def runner(_request):
        return {"justificationText": "invalid output"}

    with TestClient(create_app(agent_runner=runner)) as client:
        response = client.post("/internal/agent/run", json=valid_request())

    assert response.status_code == 422
    assert response.json()["error"]["code"] == "TOOL_VALIDATION_FAILED"


def test_health_endpoints_distinguish_liveness_and_dependency_readiness():
    async def not_ready():
        return False

    with TestClient(create_app(agent_runner=_unexpected_runner, readiness_check=not_ready)) as client:
        live = client.get("/health/live")
        ready = client.get("/health/ready")

    assert live.status_code == 200
    assert ready.status_code == 503


def _unexpected_runner(_request):
    raise AssertionError("runner must not be called for invalid requests")
