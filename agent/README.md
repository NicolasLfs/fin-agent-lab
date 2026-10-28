# Motor de agentes Python

Serviço FastAPI do fin-agent-lab. Requer Python 3.11+ e usa `uv` com o
`uv.lock` versionado para instalar as dependências.

## Preparação e testes

```sh
uv sync --locked --extra dev
uv run --locked pytest
uv build
```

Para executar a API, crie o `.env` na raiz do repositório a partir de
`.env.example`, preencha as credenciais e inicie a partir da raiz. O agente
carrega esse arquivo sem sobrescrever variáveis já existentes no ambiente:

```sh
uv --directory agent run --locked uvicorn fin_agent.api:app --host 0.0.0.0 --port 8000
```

O serviço também pode ser empacotado em container; o Dockerfile instala apenas
as dependências travadas no lockfile:

```sh
docker build -t fin-agent:local agent
docker run --rm --env-file .env -p 8000:8080 fin-agent:local
```

O arquivo `.env` é local, ignorado pelo Git e não deve ser versionado. Nunca
coloque chaves reais em código, logs, testes, fixtures ou documentação.

## Configuração

| Variável | Uso |
| --- | --- |
| `DATABASE_URL` | PostgreSQL para o checkpointer LangGraph, por exemplo o endpoint acessível ao container. |
| `QDRANT_URL` | URL HTTP(S) do Qdrant; padrão local `http://localhost:6333`. |
| `QDRANT_API_KEY` | Chave opcional do Qdrant. |
| `GEMINI_API_KEY` | Credencial do serviço de embeddings Gemini. |
| `GEMINI_EMBEDDING_MODEL` | Modelo de embedding; padrão `gemini-embedding-001`. |
| `GEMINI_EMBEDDING_DIMENSIONS` | Dimensão vetorial `768` (padrão) ou `1536`; deve corresponder à coleção. |
| `OPENROUTER_API_KEY` | Credencial do OpenRouter. |
| `OPENROUTER_MODELS` | Lista ordenada e separada por vírgula: modelo primário primeiro, seguido por pelo menos uma contingência. Configure primeiro uma rota do tier gratuito, quando disponível. |
| `OPENROUTER_TIMEOUT_SECONDS` | Timeout por tentativa do LLM; padrão `25`. |
| `NEWS_PROCESSING_QUEUE_URL` | URL da fila SQS `news-processing-queue`. |
| `AWS_REGION` | Região AWS; padrão `us-east-1`. |
| `AWS_ENDPOINT_URL` | Endpoint opcional do LocalStack, acessível a partir do processo/container. |
| `SQS_WAIT_TIME_SECONDS` | Long polling SQS entre `0` e `20` segundos; padrão `20`. |

O serviço exige PostgreSQL, Qdrant, SQS, uma chave Gemini, uma chave OpenRouter
e pelo menos duas rotas OpenRouter para iniciar. As credenciais AWS usam a
cadeia padrão do SDK boto3. A coleção Qdrant `market-news` e os índices de
`asset_ticker`, `published_at` e `sentiment_score` são criados no startup;
uma coleção existente com dimensão vetorial diferente causa erro explícito.

## API e fluxo RAG

- `POST /internal/agent/run` recebe o contrato `reportId`,
  `deterministicContext`, `rankedAssets` calculado pelo Go e `assetTypes` definido em
  [ARCHITECTURE.md](../ARCHITECTURE.md).
- `GET /health/live` verifica o processo; `GET /health/ready` verifica
  PostgreSQL, Qdrant e SQS.
- Erros usam `{ "error": { "code", "message", "retryable" } }`. JSON inválido
  ou request incompleto retorna `400`; validação interna, `422`; limite sem
  contingência, `429`; falhas upstream, `502`; indisponibilidade temporária,
  `503`.

O fluxo LangGraph é linear: busca no Qdrant por ticker e janela dos últimos
sete dias, análise de risco pelo OpenRouter e formatação da resposta. O
`reportId` é o `thread_id` persistido pelo checkpointer PostgreSQL, permitindo
retomar a mesma execução após falhas. Rotas OpenRouter são tentadas na ordem
configurada; limite, timeout, indisponibilidade e resposta inválida acionam a
próxima rota. Se todas as rotas responderem com limite, a API retorna `429`.

O LLM recebe os valores financeiros e a ordenação calculados em Go como
contexto imutável: não calcula nem altera taxas, rentabilidade, scores ou
posições. Ele retorna tickers com sinalização de risco e uma justificativa, e o
LangGraph repassa `rankedAssets` sem alterações. A resposta valida que cada
notícia citada corresponde a um ID efetivamente retornado pelo Qdrant. Os IDs
citados permanecem exatos na resposta para auditoria.

## Ingestão de notícias

O worker consome mensagens `news.processing` da fila SQS, valida o conteúdo,
gera embeddings Gemini e persiste pontos via SDK oficial `qdrant-client`.
Pontos usam UUID determinístico a partir do SHA-256 do conteúdo e ticker,
evitando duplicação de pontos em reentregas. Ingestões concorrentes do mesmo
ponto são serializadas no processo e consultam novamente o Qdrant antes de
gerar o embedding. A mensagem só é removida da fila
depois da persistência; falhas são registradas com `messageId` e
`correlationId` e ficam sujeitas a retry/DLQ.

O produtor atual envia título, conteúdo, hash, fonte, URL e data. Quando a
mensagem não inclui `assetTickers`, o agente identifica códigos B3 explícitos
no texto (`ABCD3`, `HGLG11`); mensagens sem ticker identificável não são
indexadas e seguem a política de retry/DLQ. Se `sentimentScore` não vier
informado, o worker calcula uma pontuação heurística determinística a partir
de um léxico local de termos positivos/negativos (ou `0` quando não houver
termos reconhecidos). Não é uma recomendação nem cálculo financeiro.

Metadados persistidos:

- `asset_ticker`: código B3 validado;
- `published_at`: data/hora UTC;
- `sentiment_score`: valor no intervalo `[-1, 1]`.

Testes de integração opcionais com serviços reais:

```sh
set -a
. ../.env
set +a
uv run --locked pytest tests/integration/test_postgres_checkpoint.py
uv run --locked pytest tests/integration/test_qdrant.py
```
