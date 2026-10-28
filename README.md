# fin-agent-lab

Monorepo de uma plataforma de ingestão de dados financeiros e notícias, com
processamento RAG e geração de rankings de ativos. O escopo e os requisitos
estão em [CHALLENGE.md](./CHALLENGE.md), as decisões técnicas em
[ARCHITECTURE.md](./ARCHITECTURE.md) e o progresso de implementação em
[CHECKLIST.md](./CHECKLIST.md).

## Estrutura

```text
backend/  API e workers em Go; PostgreSQL/sqlc em backend/db
agent/    Motor de agentes Python (FastAPI, LangGraph, Gemini e Qdrant)
infra/
├── terraform/  Provisionamento do SQS local com Terraform e LocalStack
└── k8s/        Manifests Kubernetes nativos para Docker Desktop
scripts/        Inicialização local e validação de infraestrutura
```

Cada serviço tem um README com instruções específicas:

- [Backend Go](./backend/README.md)
- [Agente Python](./agent/README.md)

## Skills de desenvolvimento

As skills do workspace ficam em `.github/skills/` e orientam o trabalho neste
desafio:

- [Go backend](./.github/skills/go-backend/SKILL.md)
- [Agente Python](./.github/skills/python-agent/SKILL.md)
- [Terraform com LocalStack](./.github/skills/terraform-localstack/SKILL.md)
- [Docker e Docker Compose](./.github/skills/docker-compose/SKILL.md)
- [Kubernetes local](./.github/skills/kubernetes-local/SKILL.md)

O TDD é obrigatório em todas as mudanças: escrever ou ajustar o teste antes da
implementação, observar a falha esperada, implementar a menor mudança e só
avançar quando os testes relevantes passarem. A instrução permanente está em
`.github/copilot-instructions.md`.

## Requisitos de desenvolvimento

- Go 1.26.1, conforme `backend/go.mod`
- Python 3.11 ou superior e [uv](https://docs.astral.sh/uv/), para o agente
- Docker Desktop, com Kubernetes habilitado para executar os manifests locais
- `kubectl` e Terraform, para as etapas de infraestrutura correspondentes

Para habilitar o cluster, abra as configurações do Docker Desktop, ative
**Kubernetes** e aguarde o cluster ficar pronto. Confira o contexto ativo com:

```sh
kubectl config current-context
kubectl get nodes
```

O contexto esperado é `docker-desktop`. As instruções detalhadas estão em
[infra/k8s/README.md](./infra/k8s/README.md).

## Preparação dos projetos

Sincronize o ambiente Python e valide o pacote:

```sh
cd agent
uv sync --locked
uv build
```

Consulte [backend/README.md](./backend/README.md) para os comandos e detalhes do
backend Go.

## Execução local com Docker Compose

Use um único `.env` local na raiz para configurar PostgreSQL, a API Go, o worker
de mercado e o agente Python:

```sh
cp .env.example .env
# Preencha as credenciais e rotas de modelos necessárias.
```

O `.env` é ignorado pelo Git e não deve ser compartilhado. O Compose lê esse
arquivo para interpolação; as imagens não contêm credenciais. O agente precisa
de `GEMINI_API_KEY`, `OPENROUTER_API_KEY` e `OPENROUTER_MODELS` com uma rota
primária e pelo menos uma contingência. `POSTGRES_PASSWORD` deve corresponder
à senha inicializada no volume PostgreSQL existente; mudar apenas o `.env` não
altera a senha gravada no banco.

Suba o ambiente integrado:

```sh
bash scripts/local-up.sh
```

O script inicia PostgreSQL, Qdrant e LocalStack, executa migrations
idempotentes, aplica as filas SQS via Terraform, constrói as imagens e aguarda
a readiness da API. A API fica em `http://localhost:8080`; Qdrant, em
`http://localhost:6333`; LocalStack, em `http://localhost:4566`. Os volumes
nomeados preservam PostgreSQL, Qdrant e estado local do LocalStack ao reiniciar.
Não use `docker compose down -v` se quiser conservar esses dados.

## Kubernetes local

Habilite Kubernetes no Docker Desktop, confirme o contexto `docker-desktop`,
preencha o `.env` e execute:

```sh
bash scripts/k8s-up.sh
```

O script recusa qualquer outro contexto, cria o Secret Kubernetes a partir do
`.env` sem gravar valores no repositório, provisiona SQS no LocalStack do
Compose, aplica as migrations e aguarda os Deployments. O cluster reutiliza
esse LocalStack através de `host.docker.internal`; PostgreSQL e Qdrant do
Kubernetes usam PVCs próprios. Não há Ingress: para acessar a API:

```sh
kubectl port-forward -n fin-agent svc/api 8080:8080
```

Para validar os contratos locais sem aplicar recursos Kubernetes:

```sh
uv --directory agent run --locked python ../scripts/test_local_infra.py
docker compose config --quiet
terraform -chdir=infra/terraform fmt -check -recursive
terraform -chdir=infra/terraform validate
terraform -chdir=infra/terraform test
```

O teste Terraform cria filas com sufixo isolado e as remove ao concluir; a
execução normal provisiona apenas `news-processing-queue` e
`news-processing-dlq`.
