from typing import Any, Protocol, TypedDict

from langgraph.graph import END, START, StateGraph

from .llm import RiskAnalysis, validate_risk_analysis
from .models import RunRequest, RunResponse


class Retriever(Protocol):
    async def retrieve(self, request: RunRequest) -> list[dict[str, Any]]: ...


class Analyzer(Protocol):
    async def analyze(self, request: RunRequest, news: list[dict[str, Any]]) -> RiskAnalysis: ...


class AgentState(TypedDict, total=False):
    request: dict[str, Any]
    news: list[dict[str, Any]]
    risk: dict[str, Any]
    response: dict[str, Any]


def build_rag_graph(retriever: Retriever, analyzer: Analyzer, checkpointer):
    async def retrieve(state: AgentState) -> dict[str, Any]:
        request = RunRequest.model_validate(state["request"])
        return {"news": await retriever.retrieve(request)}

    async def analyze(state: AgentState) -> dict[str, Any]:
        request = RunRequest.model_validate(state["request"])
        news = state["news"]
        result = await analyzer.analyze(request, news)
        validated = validate_risk_analysis(result, request, news)
        return {"risk": validated.model_dump(by_alias=True)}

    def format_response(state: AgentState) -> dict[str, Any]:
        request = RunRequest.model_validate(state["request"])
        risk = RiskAnalysis.model_validate(state["risk"])
        types_by_ticker = {
            item.ticker: "FIXED_INCOME"
            for item in request.deterministic_context.fixed_income_rates
        }
        types_by_ticker.update(
            {item.ticker: item.type for item in request.deterministic_context.stocks_and_fiis}
        )
        response = RunResponse(
            status="COMPLETED",
            rankedAssets=[
                {"rank": rank, "ticker": ticker, "type": types_by_ticker[ticker]}
                for rank, ticker in enumerate(risk.ranked_tickers, start=1)
            ],
            justificationText=risk.justification_text,
            citedNewsIds=risk.cited_news_ids,
        )
        return {"response": response.model_dump(by_alias=True)}

    workflow = StateGraph(AgentState)
    workflow.add_node("retrieval", retrieve)
    workflow.add_node("risk_analysis", analyze)
    workflow.add_node("formatting", format_response)
    workflow.add_edge(START, "retrieval")
    workflow.add_edge("retrieval", "risk_analysis")
    workflow.add_edge("risk_analysis", "formatting")
    workflow.add_edge("formatting", END)
    return workflow.compile(checkpointer=checkpointer)
