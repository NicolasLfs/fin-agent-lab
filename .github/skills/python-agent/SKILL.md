---
name: python-agent
description: "Use when implementing or reviewing the Python agent, FastAPI contracts, LangGraph workflows, Qdrant search, embeddings, validation, or pytest tests in agent/. Follow mandatory TDD and keep financial calculations in Go."
---

# Python agent

Use this skill for the Python agent service, its API, RAG workflow, Qdrant
integration, and tests in `agent/`.

## Workflow: TDD is a hard gate

1. Read the relevant challenge and architecture sections, `agent/README.md`,
   `pyproject.toml`, and nearby implementation/tests.
2. Add or update a focused `pytest` test before implementation. Cover expected
   behavior, invalid input, and external-service failures where applicable.
3. From `agent/`, run a focused test such as
   `uv run pytest tests/test_example.py -k relevant_behavior`. Confirm the test
   fails for the expected reason.
4. Implement the smallest change, rerun the focused test, and do not proceed to
   another behavior until it passes.
5. Refactor with tests green, then run the relevant test suite and `uv run
   pytest` before declaring the work complete.

Do not bypass the red phase, use a mocked success as proof of an integration
contract, or report a test as passing if it was not executed. If dependencies
or a required service are unavailable, stop and report the blocker.

## Project constraints

- Support Python 3.11+ and manage dependencies with `uv` using the committed
  `uv.lock`. Do not introduce `requirements.txt` or bypass the lockfile.
- Use FastAPI for the service API, Pydantic or existing validation patterns for
  contracts, and explicit error mapping matching `ARCHITECTURE.md`. Preserve
  the agreed JSON shapes and HTTP status semantics.
- Use LangGraph for the explicit retrieval, risk-analysis, and formatting
  workflow. Keep state and retries correlated by the stable `reportId`, and
  preserve checkpoint-based resume behavior.
- Use the official `qdrant-client`. Store and filter the agreed structured
  metadata (`asset_ticker`, `published_at`, `sentiment_score`), ensure vector
  dimensions match the configured embedding model, and make message processing
  idempotent under at-least-once delivery.
- Keep deterministic finance calculations in Go. Treat numeric context from Go
  as authoritative input; the LLM may explain grounded results but must not
  calculate, infer, or alter financial values.
- Validate and sanitize agent/tool inputs and outputs. Ground explanations in
  retrieved news and supplied context, preserve exact cited news IDs, and
  handle provider limits, timeouts, and unavailable dependencies explicitly.
- Keep credentials in environment configuration; never place secrets in
  source, tests, logs, or committed fixtures.

## Verification

Use focused `uv run pytest` selectors during each TDD cycle and run
`uv run pytest` before completion. Test API serialization/status mappings,
validation, Qdrant filters, duplicate message handling, and retry/resume
behavior at the appropriate unit or integration boundary. Use real local
services for integration claims; clearly identify tests that require them.
