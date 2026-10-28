import pytest

from fin_agent.models import RunRequest, RunResponse
from fin_agent.runtime import AgentRuntime


@pytest.mark.anyio
async def test_runtime_correlates_langgraph_checkpoint_with_stable_report_id():
    request = RunRequest.model_validate(
        {
            "reportId": "0192f298-345e-7e38-af88-e43f851a819d",
            "deterministicContext": {
                "fixedIncomeRates": [],
                "stocksAndFiis": [{"ticker": "VALE3", "type": "STOCK", "referencePrice": "68.50"}],
            },
            "rankedAssets": [
                {
                    "rank": 1,
                    "ticker": "VALE3",
                    "type": "STOCK",
                    "score": "0.1000",
                    "scoreMetric": "EARNINGS_YIELD",
                }
            ],
            "assetTypes": ["STOCK"],
        }
    )
    recorded = {}

    class Graph:
        async def aget_state(self, _config):
            return type("Snapshot", (), {"values": {}, "next": ()})()

        async def ainvoke(self, state, config):
            recorded["state"] = state
            recorded["config"] = config
            return {
                "response": {
                    "status": "COMPLETED",
                    "rankedAssets": [
                        {
                            "rank": 1,
                            "ticker": "VALE3",
                            "type": "STOCK",
                            "score": "0.1000",
                            "scoreMetric": "EARNINGS_YIELD",
                        }
                    ],
                    "justificationText": "Grounded analysis.",
                    "citedNewsIds": [],
                }
            }

    runtime = AgentRuntime(Graph())

    response = await runtime.run(request)

    assert response == RunResponse.model_validate(
        {
            "status": "COMPLETED",
            "rankedAssets": [
                {
                    "rank": 1,
                    "ticker": "VALE3",
                    "type": "STOCK",
                    "score": "0.1000",
                    "scoreMetric": "EARNINGS_YIELD",
                }
            ],
            "justificationText": "Grounded analysis.",
            "citedNewsIds": [],
        }
    )
    assert recorded["config"]["configurable"]["thread_id"] == str(request.report_id)
    assert recorded["state"]["request"]["reportId"] == str(request.report_id)


@pytest.mark.anyio
async def test_runtime_returns_completed_checkpoint_without_replaying_graph():
    request = RunRequest.model_validate(
        {
            "reportId": "0192f298-345e-7e38-af88-e43f851a819d",
            "deterministicContext": {
                "fixedIncomeRates": [],
                "stocksAndFiis": [{"ticker": "VALE3", "type": "STOCK", "referencePrice": "68.50"}],
            },
            "rankedAssets": [
                {
                    "rank": 1,
                    "ticker": "VALE3",
                    "type": "STOCK",
                    "score": "0.1000",
                    "scoreMetric": "EARNINGS_YIELD",
                }
            ],
            "assetTypes": ["STOCK"],
        }
    )

    class Graph:
        async def aget_state(self, config):
            assert config["configurable"]["thread_id"] == str(request.report_id)
            return type(
                "Snapshot",
                (),
                {
                    "values": {
                        "response": {
                            "status": "COMPLETED",
                            "rankedAssets": [
                                {
                                    "rank": 1,
                                    "ticker": "VALE3",
                                    "type": "STOCK",
                                    "score": "0.1000",
                                    "scoreMetric": "EARNINGS_YIELD",
                                }
                            ],
                            "justificationText": "Previously completed.",
                            "citedNewsIds": ["qdrant-id"],
                        }
                    },
                    "next": (),
                },
            )()

        async def ainvoke(self, *_args, **_kwargs):
            raise AssertionError("completed checkpoint must not be replayed")

    response = await AgentRuntime(Graph()).run(request)

    assert response.justification_text == "Previously completed."
    assert response.cited_news_ids == ["qdrant-id"]
