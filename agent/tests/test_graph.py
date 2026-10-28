import pytest
from langgraph.checkpoint.memory import InMemorySaver

from fin_agent.graph import build_rag_graph
from fin_agent.llm import RiskAnalysis
from fin_agent.models import RunRequest
from fin_agent.providers import ToolValidationError


@pytest.mark.anyio
async def test_graph_runs_retrieval_risk_and_formatting_with_report_id_thread():
    request = RunRequest.model_validate(
        {
            "reportId": "0192f298-345e-7e38-af88-e43f851a819d",
            "deterministicContext": {
                "fixedIncomeRates": [],
                "stocksAndFiis": [{"ticker": "VALE3", "type": "STOCK", "referencePrice": "68.50"}],
            },
            "assetTypes": ["STOCK"],
        }
    )
    calls = []

    class Retriever:
        async def retrieve(self, _request):
            calls.append("retrieve")
            return [
                {
                    "id": "qdrant-point-exact",
                    "ticker": "VALE3",
                    "publishedAt": "2026-10-28T10:00:00+00:00",
                    "sentimentScore": -0.5,
                    "title": "Vale faces investigation",
                    "content": "VALE3 disclosed an investigation.",
                }
            ]

    class Analyzer:
        async def analyze(self, _request, news):
            calls.append(("analyze", [item["id"] for item in news]))
            return RiskAnalysis(
                rankedTickers=["VALE3"],
                justificationText="The retrieved report describes an investigation.",
                citedNewsIds=["qdrant-point-exact"],
            )

    graph = build_rag_graph(Retriever(), Analyzer(), InMemorySaver())
    result = await graph.ainvoke(
        {"request": request.model_dump(by_alias=True)},
        config={"configurable": {"thread_id": str(request.report_id)}},
    )

    assert calls == ["retrieve", ("analyze", ["qdrant-point-exact"])]
    assert result["response"] == {
        "status": "COMPLETED",
        "rankedAssets": [{"rank": 1, "ticker": "VALE3", "type": "STOCK"}],
        "justificationText": "The retrieved report describes an investigation.",
        "citedNewsIds": ["qdrant-point-exact"],
    }


@pytest.mark.anyio
async def test_graph_rejects_llm_citation_not_returned_by_qdrant():
    request = RunRequest.model_validate(
        {
            "reportId": "0192f298-345e-7e38-af88-e43f851a819d",
            "deterministicContext": {
                "fixedIncomeRates": [],
                "stocksAndFiis": [{"ticker": "VALE3", "type": "STOCK", "referencePrice": "68.50"}],
            },
            "assetTypes": ["STOCK"],
        }
    )

    class Retriever:
        async def retrieve(self, _request):
            return []

    class Analyzer:
        async def analyze(self, _request, _news):
            return RiskAnalysis(
                rankedTickers=["VALE3"],
                justificationText="Grounded text.",
                citedNewsIds=["fabricated-id"],
            )

    graph = build_rag_graph(Retriever(), Analyzer(), InMemorySaver())
    with pytest.raises(ToolValidationError, match="not retrieved"):
        await graph.ainvoke(
            {"request": request.model_dump(by_alias=True)},
            config={"configurable": {"thread_id": str(request.report_id)}},
        )
