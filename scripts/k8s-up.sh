#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$ROOT"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl is required to deploy the Docker Desktop Kubernetes manifests." >&2
  exit 1
fi
if [ ! -f .env ]; then
  echo "Create the root .env file before deploying." >&2
  exit 1
fi

context=$(kubectl config current-context)
if [ "$context" != "docker-desktop" ]; then
  echo "Refusing to deploy: kubectl context is not docker-desktop." >&2
  exit 1
fi

docker compose config --quiet
docker compose up -d --wait localstack
curl --fail --silent --show-error http://localhost:4566/_localstack/health |
  python3 -c 'import json,sys; status=json.load(sys.stdin)["services"].get("sqs"); sys.exit(0 if status in {"available", "running"} else 1)'

terraform -chdir=infra/terraform init -input=false -backend=false
terraform -chdir=infra/terraform apply -auto-approve -input=false
docker compose build api agent

kubectl apply -f infra/k8s/00-namespace.yaml
kubectl create secret generic fin-agent-secrets \
  --namespace fin-agent \
  --from-env-file=.env \
  --dry-run=client \
  -o yaml |
  kubectl apply -f -
kubectl create configmap fin-agent-migrations \
  --namespace fin-agent \
  --from-file=backend/db/migrations \
  --from-file=apply-migrations.sh=scripts/apply-migrations.sh \
  --dry-run=client \
  -o yaml |
  kubectl apply -f -

kubectl apply -f infra/k8s/10-config.yaml -f infra/k8s/20-dependencies.yaml
kubectl rollout status statefulset/postgres -n fin-agent --timeout=5m
kubectl rollout status deployment/qdrant -n fin-agent --timeout=5m

kubectl delete job fin-agent-migrations -n fin-agent --ignore-not-found --wait=true
kubectl apply -f infra/k8s/30-migration-job.yaml
kubectl wait --for=condition=complete job/fin-agent-migrations -n fin-agent --timeout=5m

kubectl apply -f infra/k8s/40-applications.yaml
kubectl rollout status deployment/agent -n fin-agent --timeout=5m
kubectl rollout status deployment/api -n fin-agent --timeout=5m
kubectl rollout status deployment/market-worker -n fin-agent --timeout=5m

printf 'Kubernetes stack is ready. Use kubectl port-forward -n fin-agent svc/api 8080:8080 to reach the API.\n'
