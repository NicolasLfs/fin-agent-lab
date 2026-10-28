import os
from uuid import uuid4

import pytest
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from psycopg_pool import AsyncConnectionPool

from fin_agent.graph import build_rag_graph
from fin_agent.llm import RiskAnalysis
from fin_agent.models import RunRequest
from fin_agent.providers import ProviderUnavailableError
from fin_agent.runtime import AgentRuntime


DATABASE_URL = os.environ.get("TEST_DATABASE_URL")


@pytest.mark.anyio
@pytest.mark.skipif(not DATABASE_URL, reason="set TEST_DATABASE_URL to run PostgreSQL checkpoint integration")
async def test_postgres_checkpoint_resumes_after_risk_node_failure():
    calls = {"retrieval": 0, "analysis": 0}

    class Retriever:
        async def retrieve(self, _request):
            calls["retrieval"] += 1
            return []

    class Analyzer:
        async def analyze(self, _request, _news):
            calls["analysis"] += 1
            if calls["analysis"] == 1:
                raise ProviderUnavailableError("simulated temporary model outage")
            return RiskAnalysis(
                rankedTickers=["VALE3"],
                justificationText="No recent news was retrieved.",
                citedNewsIds=[],
            )

    request = RunRequest.model_validate(
        {
            "reportId": str(uuid4()),
            "deterministicContext": {
                "fixedIncomeRates": [],
                "stocksAndFiis": [{"ticker": "VALE3", "type": "STOCK", "referencePrice": "68.50"}],
            },
            "assetTypes": ["STOCK"],
        }
    )

    async with AsyncConnectionPool(
        conninfo=DATABASE_URL,
        min_size=1,
        max_size=3,
        kwargs={"autocommit": True, "prepare_threshold": 0},
    ) as first_pool:
        first_checkpointer = AsyncPostgresSaver(first_pool)
        await first_checkpointer.setup()
        runtime = AgentRuntime(build_rag_graph(Retriever(), Analyzer(), first_checkpointer))
        with pytest.raises(ProviderUnavailableError):
            await runtime.run(request)

    async with AsyncConnectionPool(
        conninfo=DATABASE_URL,
        min_size=1,
        max_size=3,
        kwargs={"autocommit": True, "prepare_threshold": 0},
    ) as restarted_pool:
        restarted_checkpointer = AsyncPostgresSaver(restarted_pool)
        await restarted_checkpointer.setup()
        restarted_runtime = AgentRuntime(build_rag_graph(Retriever(), Analyzer(), restarted_checkpointer))
        completed = await restarted_runtime.run(request)

    assert calls == {"retrieval": 1, "analysis": 2}
    assert completed.status == "COMPLETED"
    assert completed.cited_news_ids == []
