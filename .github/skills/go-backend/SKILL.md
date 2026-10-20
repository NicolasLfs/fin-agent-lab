---
name: go-backend
description: "Use when implementing or reviewing Go APIs, workers, domain persistence, SQL, concurrency, or tests in backend/. Follow the project's mandatory TDD gate and financial precision requirements."
---

# Go backend

Use this skill for Go API, ingestion worker, persistence, domain, concurrency,
and test work in `backend/`.

## Workflow: TDD is a hard gate

1. Read the relevant challenge and architecture requirements, the backend
   README, and adjacent code. Identify the observable behavior and its boundary.
2. Add or update the smallest focused Go test first. Cover normal behavior,
   invalid input, and relevant boundary or failure cases.
3. Run the focused test from `backend/`, for example
   `go test ./internal/domain -run TestRelevantBehavior`. Confirm it fails
   because the behavior is not implemented, not because the test or environment
   is broken.
4. Implement the smallest change that makes that test pass. Run the focused
   test again; do not start another behavior until it passes.
5. Refactor only while tests stay green. Then run `go test ./...` and, for
   concurrency-sensitive changes, `go test -race ./...`.

Never skip the red phase or claim a test passed without running it. If the
required Go toolchain or dependencies are unavailable, stop and report the
blocker rather than proceeding without the gate.

## Project constraints

- Keep HTTP handlers on the standard `net/http` library and wire application
  dependencies with Uber Fx, following existing project boundaries.
- Keep business rules in domain/application code rather than handlers or SQL
  generated code. Use explicit errors and context-aware I/O.
- Use `sqlc` for typed PostgreSQL access. Edit SQL sources and migrations, then
  regenerate through the configured `backend/db/sqlc.yml`; do not hand-edit
  generated code. Keep transaction boundaries, locking, constraints, and
  migration changes explicit and testable.
- Model money with exact decimal or integer-minor-unit representations. Never
  use `float32`/`float64` for monetary parsing, computation, serialization, or
  persistence. Enforce the JSON amount-string contract, currency, scale, and
  invalid-number rules in tests.
- Make ingestion and recommendation idempotency durable across restarts and
  safe under concurrent requests. Prefer database uniqueness and transactional
  guarantees over in-memory checks.
- Go computes all rates and numeric ranking inputs deterministically. Pass
  decimal values as strings to Python; the LLM must not calculate or modify
  financial values.
- Preserve the agreed HTTP contracts, status/error envelope, timeouts, and
  report/audit identifiers in `ARCHITECTURE.md`. Log errors with useful
  correlation context without logging secrets.

## Verification

Run package-focused tests during the red/green cycle. Before completing a
related change, run `go test ./...`; run `go test -race ./...` for changes
affecting shared state, workers, or concurrency. For SQL changes, validate the
configured `sqlc generate` path and the relevant database tests. Report every
verification command and its actual result.
