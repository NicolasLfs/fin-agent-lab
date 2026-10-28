import asyncio
import logging
from typing import Any

import boto3
import httpx
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from psycopg import Error as PostgresError
from psycopg_pool import AsyncConnectionPool
from qdrant_client import QdrantClient
from botocore.exceptions import BotoCoreError, ClientError
from pydantic import ValidationError
from qdrant_client.http.exceptions import ApiException, ResponseHandlingException, UnexpectedResponse

from .config import Settings
from .embeddings import GeminiEmbedder
from .graph import build_rag_graph
from .llm import OpenRouterAnalyzer
from .models import RunRequest, RunResponse
from .providers import ConfigurationError, ServiceUnavailableError, ToolValidationError
from .retrieval import NewsVectorStore, QdrantNewsRetriever
from .worker import SQSNewsWorker

logger = logging.getLogger(__name__)


class AgentRuntime:
    def __init__(
        self,
        graph,
        *,
        qdrant: Any = None,
        collection_name: str = "",
        postgres_pool: Any = None,
        sqs_client: Any = None,
        queue_url: str = "",
        worker: SQSNewsWorker | None = None,
        embedder: GeminiEmbedder | None = None,
        analyzer: OpenRouterAnalyzer | None = None,
    ):
        self.graph = graph
        self.qdrant = qdrant
        self.collection_name = collection_name
        self.postgres_pool = postgres_pool
        self.sqs_client = sqs_client
        self.queue_url = queue_url
        self.worker = worker
        self.embedder = embedder
        self.analyzer = analyzer
        self.worker_task: asyncio.Task | None = None

    async def run(self, request: RunRequest) -> RunResponse:
        report_id = str(request.report_id)
        logger.info("agent graph execution started", extra={"reportId": report_id})
        config = {"configurable": {"thread_id": report_id}}
        try:
            snapshot = await self.graph.aget_state(config)
            if snapshot.values and not snapshot.next:
                return self._response(snapshot.values, report_id)
            if snapshot.next:
                result = await self.graph.ainvoke(None, config=config)
            else:
                result = await self.graph.ainvoke(
                    {"request": request.model_dump(mode="json", by_alias=True)},
                    config=config,
                )
        except PostgresError as error:
            logger.error("LangGraph checkpoint store is unavailable", extra={"reportId": report_id})
            raise ServiceUnavailableError("PostgreSQL checkpoint store is unavailable") from error
        except (ToolValidationError, ConfigurationError):
            raise
        response = self._response(result, report_id)
        logger.info("agent graph execution completed", extra={"reportId": report_id})
        return response

    @staticmethod
    def _response(state: dict[str, Any], report_id: str) -> RunResponse:
        if "response" not in state:
            raise ToolValidationError("LangGraph completed without a formatted response")
        try:
            return RunResponse.model_validate(state["response"])
        except ValidationError as error:
            logger.error("LangGraph checkpoint contains an invalid response", extra={"reportId": report_id})
            raise ToolValidationError("LangGraph response violates the API contract") from error

    async def check_ready(self) -> bool:
        if (
            self.qdrant is None
            or self.postgres_pool is None
            or self.sqs_client is None
            or not self.queue_url
        ):
            return False
        try:
            await self.postgres_pool.check()
            await asyncio.to_thread(self.qdrant.get_collection, self.collection_name)
            await asyncio.to_thread(
                self.sqs_client.get_queue_attributes,
                QueueUrl=self.queue_url,
                AttributeNames=["QueueArn"],
            )
        except (
            PostgresError,
            ApiException,
            ResponseHandlingException,
            UnexpectedResponse,
            httpx.RequestError,
            BotoCoreError,
            ClientError,
        ):
            return False
        return True

    def start_worker(self) -> None:
        if self.worker is None:
            raise ConfigurationError("SQS news worker is not configured")
        if self.worker_task is not None:
            raise RuntimeError("SQS news worker has already been started")
        self.worker_task = asyncio.create_task(self.worker.run(), name="sqs-news-worker")

    async def close(self) -> None:
        if self.worker_task is not None:
            self.worker_task.cancel()
            try:
                await self.worker_task
            except asyncio.CancelledError:
                pass
        if self.sqs_client is not None:
            self.sqs_client.close()
        if self.qdrant is not None:
            self.qdrant.close()
        if self.postgres_pool is not None:
            await self.postgres_pool.close()
        if self.embedder is not None:
            self.embedder.close()
        if self.analyzer is not None:
            await self.analyzer.close()


async def create_runtime(settings: Settings | None = None) -> AgentRuntime:
    settings = settings or Settings.from_env()
    settings.validate_for_runtime()

    qdrant = QdrantClient(
        url=settings.qdrant_url,
        api_key=settings.qdrant_api_key,
        timeout=5,
    )
    embedder = None
    pool = None
    analyzer = None
    sqs_client = None
    try:
        embedder = GeminiEmbedder.from_api_key(
            settings.gemini_api_key,
            settings.embedding_model,
            settings.embedding_dimensions,
        )
        store = NewsVectorStore(qdrant, "market-news", settings.embedding_dimensions, embedder.embed_document)
        await asyncio.to_thread(store.ensure_collection)

        pool = AsyncConnectionPool(
            conninfo=settings.database_url,
            min_size=1,
            max_size=5,
            kwargs={"autocommit": True, "prepare_threshold": 0},
            open=False,
        )
        await pool.open(wait=True)
        checkpointer = AsyncPostgresSaver(pool)
        await checkpointer.setup()

        analyzer = OpenRouterAnalyzer.from_env(
            settings.openrouter_api_key,
            settings.openrouter_models,
            settings.openrouter_timeout,
        )
        retriever = QdrantNewsRetriever(store, embedder.embed_query)
        graph = build_rag_graph(retriever, analyzer, checkpointer)

        sqs_client = boto3.client(
            "sqs",
            region_name=settings.aws_region,
            endpoint_url=settings.aws_endpoint_url,
        )
        worker = SQSNewsWorker(sqs_client, settings.news_queue_url, store, settings.sqs_wait_time_seconds)
        runtime = AgentRuntime(
            graph,
            qdrant=qdrant,
            collection_name=store.collection_name,
            postgres_pool=pool,
            sqs_client=sqs_client,
            queue_url=settings.news_queue_url,
            worker=worker,
            embedder=embedder,
            analyzer=analyzer,
        )
        runtime.start_worker()
        return runtime
    except BaseException:
        if sqs_client is not None:
            sqs_client.close()
        if pool is not None:
            await pool.close()
        qdrant.close()
        if embedder is not None:
            embedder.close()
        if analyzer is not None:
            await analyzer.close()
        raise
