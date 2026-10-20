# Terraform

Esta pasta é reservada à configuração Terraform da infraestrutura local. O
plano é provisionar os recursos AWS simulados pelo LocalStack, incluindo as
filas SQS e a dead-letter queue (DLQ).

Ainda não há arquivos `.tf`; portanto, não execute `terraform apply` nesta
pasta até que a configuração e os recursos estejam implementados.

Os recursos provisionados aqui são independentes dos manifests Kubernetes em
[`../k8s/`](../k8s/).
