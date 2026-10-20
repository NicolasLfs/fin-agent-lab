---
name: terraform-localstack
description: "Use when authoring or reviewing Terraform for the LocalStack-backed infrastructure in infra/terraform, including AWS provider endpoints, SQS queues, DLQs, and Terraform tests. Apply TDD and target LocalStack only."
---

# Terraform with LocalStack

Use this skill for Terraform configuration and validation of the AWS services
simulated by LocalStack in `infra/terraform/`.

## Workflow: mandatory TDD for infrastructure changes

1. Read the infrastructure sections of `CHALLENGE.md`, `ARCHITECTURE.md`, and
   `CHECKLIST.md`, plus `infra/terraform/README.md` and existing `.tf` files.
2. Define the expected resource/configuration behavior as an automated
   Terraform test or focused validation before changing implementation.
   Prefer `terraform test` for behavior and assertions; use formatting,
   initialization, validation, and plan checks as additional gates, not as a
   substitute for a meaningful test when one is feasible.
3. Run the focused test/check first and establish its expected baseline. Do not
   treat a missing provider, invalid credentials, or unreachable LocalStack as
   a meaningful red test.
4. Make the smallest HCL change. Run `terraform fmt -check`,
   `terraform validate`, focused `terraform test`, and a plan against the
   intended local configuration. Do not proceed to another resource behavior
   until the relevant checks pass.
5. After green checks, review the plan for unexpected resources, replacements,
   deletions, or external endpoints. Only apply when the task explicitly
   requires provisioning and the endpoint is verified as LocalStack.

Do not target real AWS, expose credentials, or run an apply against an
unverified endpoint. Never use destructive Terraform operations to make a test
pass.

## Project constraints

- Keep Terraform under `infra/terraform/`. The required AWS services are
  simulated by LocalStack, including `news-processing-queue` and its DLQ.
- Configure provider endpoints and credentials explicitly for LocalStack;
  avoid relying on a developer's ambient AWS profile or silently falling back
  to AWS.
- Make queue redrive policy, visibility timeout, and retry/receive limits
  explicit and consistent with at-least-once processing.
- Keep secrets out of Terraform source, plans committed to Git, outputs, and
  logs. Use local-safe placeholders only when required by a provider.
- Keep changes declarative, minimal, and testable; document any decision that
  resolves a still-open choice in `ARCHITECTURE.md`.

## Verification

Use the repository's Terraform version/provider setup. Typical checks from the
repository root are:

```sh
terraform -chdir=infra/terraform fmt -check -recursive
terraform -chdir=infra/terraform init -backend=false
terraform -chdir=infra/terraform validate
terraform -chdir=infra/terraform test
terraform -chdir=infra/terraform plan
```

Run the applicable focused Terraform test during each TDD cycle. Initialization
or plan may require LocalStack; report unavailable dependencies rather than
claiming validation succeeded.
