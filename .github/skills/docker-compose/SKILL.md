---
name: docker-compose
description: "Use when changing Dockerfiles, container images, Docker Compose services, networks, volumes, health checks, or local container workflows for this project. Apply test-first validation and protect secrets."
---

# Docker and Docker Compose

Use this skill for Dockerfiles, Compose configuration, local service
dependencies, and container build/run workflows.

## Workflow: mandatory TDD for container changes

1. Read the relevant challenge/architecture requirements, root README,
   `docker-compose.yml`, and the affected service README and Dockerfile.
2. Specify the observable change first and add/update an automated validation
   before editing the Docker configuration. Use existing service tests and
   Compose health/readiness behavior where appropriate; add a focused smoke
   check if the behavior is runtime-specific.
3. Run the check before the implementation and confirm the expected failure.
   A broken Docker daemon, missing image, or unavailable dependency is an
   environment failure, not a valid red result.
4. Make the smallest Dockerfile/Compose change. Rerun the focused check and
   require a pass before moving to another behavior.
5. Before completion, validate Compose rendering and build or smoke-test the
   affected image when the local environment supports it.

If Docker is unavailable, stop before claiming the container change is
validated and report which checks could not run.

## Project constraints

- Keep service build contexts and Dockerfiles aligned with the documented
  repository layout (`backend/` and `agent/`). Pin or intentionally select
  base image versions; use reproducible dependency installation and the
  project's existing lockfiles.
- Prefer multi-stage builds, minimal runtime images, non-root execution where
  compatible, explicit ports, and health checks that reflect actual service
  readiness. Do not add build tools to the runtime image without need.
- Keep local dependencies (including PostgreSQL, Qdrant, and LocalStack/SQS)
  explicit in Compose and consistent with application configuration.
- Do not bake credentials or `.env` contents into image layers, build args,
  Compose files, logs, or committed fixtures. Keep secrets in local environment
  configuration.
- Avoid `latest` for service dependencies where a stable version is known.
  Keep persistent state in declared volumes when restart survival is required.
- Do not run destructive cleanup commands or remove user data/volumes as part
  of validation.

## Verification

For Compose changes, validate configuration before starting containers:

```sh
docker compose config --quiet
```

For Dockerfile changes, build the affected image and run its focused smoke
check, in addition to the application's own test suite. Run only the relevant
services when possible and report any service-dependent test skipped because
the environment is unavailable.
