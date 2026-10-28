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
├── terraform/  Provisionamento local com Terraform (em estruturação)
└── k8s/        Manifests Kubernetes nativos (em estruturação)
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

O backend Go está em implementação. Consulte
[backend/README.md](./backend/README.md) para os comandos e detalhes atuais.

## Configuração

Use um único `.env` local na raiz do monorepo para configurar Compose, API Go,
worker de mercado e agente Python:

```sh
cp .env.example .env
# Edite .env e informe suas chaves e modelos do OpenRouter.
```

O `.env` é ignorado pelo Git e não deve ser compartilhado. O Compose lê esse
arquivo automaticamente; os processos Go e Python também o carregam ao iniciar.
Variáveis já definidas no ambiente do processo têm precedência. Os valores de
credenciais no `.env.example` são apenas exemplos locais; substitua-os antes de
usar provedores externos.

Com as dependências locais disponíveis, os processos podem ser iniciados em
terminais separados a partir da raiz:

```sh
docker compose up -d postgres qdrant
go -C backend run .
go -C backend run ./cmd/market-worker
uv --directory agent run --locked uvicorn fin_agent.api:app --host 0.0.0.0 --port 8000
```
