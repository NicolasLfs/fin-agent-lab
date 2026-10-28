# Kubernetes local

Os manifests Kubernetes nativos são destinados ao cluster integrado ao Docker
Desktop. Não usamos Helm ou Kustomize. O ambiente cria PostgreSQL 18, Qdrant,
API Go, worker de mercado, agente Python e um Job de migrations. PostgreSQL e
Qdrant usam PVCs locais.

## Pré-requisitos e implantação

1. Habilite Kubernetes nas configurações do Docker Desktop e aguarde o cluster.
2. Crie o `.env` na raiz com as credenciais necessárias; o arquivo não deve ser
   versionado.
3. Execute `kubectl config current-context` e confirme `docker-desktop`.
4. A partir da raiz, execute:

   ```sh
   bash scripts/k8s-up.sh
   ```

O script valida o contexto antes de qualquer operação no cluster, constrói as
imagens locais e cria o Secret `fin-agent-secrets` diretamente do `.env` sem
salvar seus valores em manifests ou logs. Também gera um ConfigMap temporário
com as migrations e o script de execução, espera PostgreSQL/Qdrant, aplica as
migrations e valida o rollout dos serviços.

O LocalStack é executado pelo Compose e compartilhado com os pods através do
Service `ExternalName` `host.docker.internal`, compatível com Docker Desktop.
Terraform provisiona as filas nesse mesmo LocalStack. Inicie o stack ou rode o
script Kubernetes, que inicia LocalStack e aplica Terraform antes do cluster.

## Acesso e validação

Não há Ingress. Acesse a API por port-forward:

```sh
kubectl port-forward -n fin-agent svc/api 8080:8080
curl http://localhost:8080/health/ready
```

Uma validação cliente sem aplicar recursos é:

```sh
kubectl config current-context
kubectl apply --dry-run=client -f ./infra/k8s/
```

Para implantação, sempre confirme que o contexto é `docker-desktop` antes de
executar `bash scripts/k8s-up.sh`. O `.env` contém credenciais locais; o Secret
Kubernetes também deve ser tratado como dado sensível e não exportado para
arquivos versionados.
