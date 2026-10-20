---
name: kubernetes-local
description: "Use when creating or reviewing native Kubernetes manifests, Deployments, Services, probes, ConfigMaps, or Secrets in infra/k8s for the Docker Desktop local cluster. Apply TDD and verify the target context."
---

# Kubernetes local

Use this skill for native Kubernetes manifests in `infra/k8s/` and their local
deployment behavior.

## Workflow: mandatory TDD for manifest changes

1. Read the Kubernetes requirements in `CHALLENGE.md`, `ARCHITECTURE.md`, and
   `CHECKLIST.md`, plus `infra/k8s/README.md` and the manifests involved.
2. Define the desired resource and runtime behavior. Add/update an automated
   manifest check or focused deployment/smoke test before changing the
   manifest. Validate schema and important behavior such as selectors, ports,
   probes, environment references, and resources.
3. Run the relevant check before implementation and confirm its expected
   failure. Do not treat a missing cluster or wrong context as a test failure.
4. Make the smallest manifest change and rerun the focused validation. Require
   it to pass before proceeding to another behavior.
5. For cluster checks, verify `kubectl config current-context` is
   `docker-desktop` before any request to the cluster. Use a dry-run first;
   apply only when deployment is required and the target is confirmed local.
   After deployment, verify rollout/readiness and the relevant smoke test.

If the active context is not the documented local context, do not switch or
apply against it implicitly; stop and report the mismatch. Do not claim a
cluster-level check passed when only client-side validation ran.

## Project constraints

- Use plain Kubernetes manifests in `infra/k8s/`. Do not introduce Helm or
  Kustomize.
- Keep Deployments separate for the Go API, Go ingestion worker, and Python
  agent where required. Use Services for in-cluster communication and
  ConfigMaps/Secrets for configuration.
- Never commit credentials or other secret values to manifests. Reference
  Kubernetes Secrets and explain local setup without exposing their contents.
- Configure liveness and readiness probes according to process health and
  dependency readiness; do not use liveness probes to restart a service merely
  because an upstream dependency is temporarily unavailable.
- Keep image names, exposed/container ports, service DNS, environment variables,
  and resource dependencies consistent with Docker Compose and application
  configuration.
- Preserve persistent data and avoid destructive namespace/resource cleanup
  during validation.

## Verification

Use available validators and the smallest relevant check. For example:

```sh
kubectl config current-context
kubectl apply --dry-run=client -f ./infra/k8s/
```

When cluster validation is required, verify the Docker Desktop context first,
then use server-side dry-run or a local deployment and check rollout status.
Use schema tools such as `kubeconform` only if already available or approved
for the repository; do not silently install extra tooling.
