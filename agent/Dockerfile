FROM ghcr.io/astral-sh/uv:0.13.6 AS uv

FROM python:3.11-slim-bookworm

COPY --from=uv /uv /uvx /bin/

WORKDIR /app
ENV UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    UV_PROJECT_ENVIRONMENT=/opt/venv \
    PATH="/opt/venv/bin:$PATH" \
    PYTHONUNBUFFERED=1

COPY pyproject.toml uv.lock README.md ./
COPY src ./src
RUN uv sync --locked --no-dev --no-editable \
    && useradd --create-home --uid 10001 agent \
    && chown -R agent:agent /app /opt/venv

USER agent
EXPOSE 8080
ENTRYPOINT ["uvicorn", "fin_agent.api:app", "--host", "0.0.0.0", "--port", "8080"]
