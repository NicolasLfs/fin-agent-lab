from collections.abc import Awaitable, Callable
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from pydantic import ValidationError

from .models import RunRequest, RunResponse
from .providers import (
    ConfigurationError,
    ProviderRateLimitError,
    ProviderUnavailableError,
    ServiceUnavailableError,
    ToolValidationError,
    UpstreamUnavailableError,
)

AgentRunner = Callable[[RunRequest], Awaitable[RunResponse]]


def _error(status: int, code: str, message: str, retryable: bool) -> JSONResponse:
    return JSONResponse(
        status_code=status,
        content={"error": {"code": code, "message": message, "retryable": retryable}},
    )


def create_app(
    agent_runner: AgentRunner | None = None,
    readiness_check: Callable[[], Awaitable[bool]] | None = None,
) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI):
        runtime = None
        if agent_runner is None:
            from .runtime import create_runtime

            runtime = await create_runtime()
            app.state.agent_runner = runtime.run
            app.state.readiness_check = runtime.check_ready
        else:
            app.state.agent_runner = agent_runner
            app.state.readiness_check = readiness_check
        app.state.runtime = runtime
        try:
            yield
        finally:
            if runtime is not None:
                await runtime.close()

    app = FastAPI(title="fin-agent", lifespan=lifespan)

    @app.exception_handler(RequestValidationError)
    async def request_validation_error(_request: Request, _exc: RequestValidationError) -> JSONResponse:
        return _error(400, "INVALID_REQUEST", "The request body is malformed or violates the API contract.", False)

    @app.exception_handler(ProviderRateLimitError)
    async def provider_rate_limit_error(_request: Request, _exc: ProviderRateLimitError) -> JSONResponse:
        return _error(
            429,
            "LLM_PROVIDER_RATE_LIMIT",
            "The LLM provider quota is exhausted and no fallback route is available.",
            True,
        )

    @app.exception_handler(ProviderUnavailableError)
    async def provider_unavailable_error(_request: Request, _exc: ProviderUnavailableError) -> JSONResponse:
        return _error(503, "LLM_PROVIDER_UNAVAILABLE", "All configured LLM routes are unavailable.", True)

    @app.exception_handler(UpstreamUnavailableError)
    async def upstream_unavailable_error(_request: Request, _exc: UpstreamUnavailableError) -> JSONResponse:
        return _error(502, "UPSTREAM_UNAVAILABLE", "A required upstream dependency is unavailable.", True)

    @app.exception_handler(ServiceUnavailableError)
    async def service_unavailable_error(_request: Request, _exc: ServiceUnavailableError) -> JSONResponse:
        return _error(503, "AGENT_UNAVAILABLE", "The agent service is temporarily unavailable.", True)

    @app.exception_handler(ConfigurationError)
    async def configuration_error(_request: Request, _exc: ConfigurationError) -> JSONResponse:
        return _error(503, "AGENT_NOT_CONFIGURED", "The agent service is not configured for this operation.", False)

    @app.exception_handler(ToolValidationError)
    async def tool_validation_error(_request: Request, error: ToolValidationError) -> JSONResponse:
        return _error(422, "TOOL_VALIDATION_FAILED", str(error), False)

    @app.get("/health/live")
    async def live() -> dict[str, str]:
        return {"status": "ok"}

    @app.get("/health/ready")
    async def ready(request: Request) -> JSONResponse:
        check = request.app.state.readiness_check
        if check is None or not await check():
            return JSONResponse(status_code=503, content={"status": "not_ready"})
        return JSONResponse(status_code=200, content={"status": "ready"})

    @app.post("/internal/agent/run", response_model=RunResponse, response_model_by_alias=True)
    async def run_agent(payload: RunRequest, request: Request) -> RunResponse:
        try:
            result = await request.app.state.agent_runner(payload)
            return RunResponse.model_validate(result)
        except ValidationError as error:
            raise ToolValidationError("Agent output violates the response contract") from error

    return app


app = create_app()
