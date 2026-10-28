#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

docker compose config --quiet
docker compose up -d --wait postgres qdrant localstack
docker compose run --rm migrate

terraform -chdir=infra/terraform init -input=false -backend=false
terraform -chdir=infra/terraform apply -auto-approve -input=false

docker compose up -d --build --wait --no-deps agent api market-worker
curl --fail --silent --show-error http://localhost:8080/health/ready >/dev/null
printf 'Local Compose stack is ready at http://localhost:8080.\n'
