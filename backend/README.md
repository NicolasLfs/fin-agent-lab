# Backend Go

Backend Go do monorepo, com o domínio em `internal/domain` e acesso tipado ao
PostgreSQL gerado pelo `sqlc` em `internal/store`. O schema de entrada do `sqlc`
fica em `db/schema.sql`; migrations versionadas e reversíveis ficam em
`db/migrations/`.

## API

A aplicação usa Uber Fx para iniciar a API `net/http`. Configure as dependências
no `.env` da raiz do monorepo (criado a partir de `.env.example`) ou no ambiente
do processo. O executável carrega o `.env` mais próximo na pasta atual ou em
uma pasta ancestral; variáveis já exportadas têm precedência:

```sh
go -C backend run .
```

`HTTP_ADDR` é opcional e assume `:8080`. `DATABASE_URL`, `AGENT_URL`,
`QDRANT_URL` e `LOCALSTACK_URL` são obrigatórios. A readiness consulta
PostgreSQL, `GET <QDRANT_URL>/healthz` e
`GET <LOCALSTACK_URL>/_localstack/health`, exigindo que o serviço SQS apareça
como `available` ou `running`.

`POST /recommendations/generate` exige `Idempotency-Key` e JSON no formato
`{"assetTypes":["STOCK","FII"]}`. Repetir a chave e o mesmo pedido reproduz a
resposta concluída; uma chave com outro pedido retorna `409`. Se o agente ou
uma dependência estiver indisponível depois da criação do relatório, a API
retorna `503` retryable e mantém o mesmo `reportId` para a próxima tentativa
com a mesma chave. Também estão
disponíveis `GET /health/live` e `GET /health/ready`.

As migrations `000002` e `000003` adicionam snapshots numéricos em
`asset_indicators`, o IPCA mensal e o status de sincronização por ativo. O
serviço lê indicadores recentes e cotações: calcula E/P de ações, DY TTM de
FIIs e uma taxa líquida de referência do SGS/BCB para renda fixa. Os dados são
normalizados como frações decimais, sem ponto flutuante, antes de persistir.

## Worker de dados de mercado

Execute o sincronizador de mercado em um processo separado da API:

```sh
go -C backend run ./cmd/market-worker
```

O worker executa uma coleta imediatamente ao iniciar e repete no intervalo
configurado. Ele cria idempotentemente os ativos de referência `SELIC_SGS_432`
e `IPCA_SGS_433`; ações e FIIs precisam estar previamente cadastrados em
`assets`. `BRAPI_API_KEY` é opcional para os tickers de demonstração liberados
pela Brapi e deve ser configurada no `.env` local ou no ambiente, nunca em URL, log ou código.
O plano gratuito limita tickers e não inclui todos os indicadores de FIIs; um
HTTP 401/403 do provedor é persistido como falha não retryable e a API retorna
`422` se não houver métrica rankeável. Falhas de rede, HTTP 429 e HTTP 5xx são
persistidas como retryable e resultam em `503` quando a API não dispõe de
snapshots válidos.

O worker consulta o SGS/BCB (`432` para Selic anual e `433` para variação mensal
do IPCA), normaliza percentuais textuais para frações decimais exatas e
preserva o conteúdo original e a data oficial no `raw_payload`. A variação
mensal do IPCA é armazenada em `inflation_rate` e não participa do ranking.
Para ações, a Brapi fornece cotação e `trailingPE`; para FIIs, fornece cotação e
`dividendYield12m`. `observed_at` dos indicadores registra a coleta bem-sucedida;
datas de referência fornecidas pelo provedor permanecem no payload auditável.
Quotes preservam o horário de mercado informado pela Brapi. O worker mantém o
status da última sincronização por ativo/tipo de dado para a API distinguir
indisponibilidade temporária (`503`) de ausência estrutural (`422`).

O score combinado é um proxy determinístico: maior taxa líquida de referência,
E/P positivo ou DY TTM fica primeiro; empates exatos usam ticker alfabético.
O índice SGS é rotulado como `REFERENCE_INDEX`, não como rentabilidade de um
título do Tesouro. Tesouro IPCA+ está fora do escopo até haver uma fonte por
título confirmada. O agente recebe `rankedAssets` calculado pelo Go e não pode
alterar sua ordem ou scores.

Dados estruturais ausentes para uma classe solicitada retornam `422` não
retryable; dados com mais de 24 horas retornam `503` retryable. Não há ranking
parcial entre classes solicitadas. Ativos sem indicador válido são excluídos;
se uma classe inteira não tiver ativos rankeáveis, a solicitação falha.

## Ingestão de notícias e fila SQS

A biblioteca `internal/ingest` fornece o fluxo mínimo de ingestão de notícias:

- parsing de feeds RSS/Atom em `ParseRSS`
- cálculo de hash SHA-256 do conteúdo em `ContentHash`
- payload JSON enviado à fila SQS em `BuildNewsProcessingMessage`
- publicador do LocalStack em `SQSClient.PublishNews`

Use o código para persistir o item no PostgreSQL com idempotência pelo `contentHash`
antes de publicar a mensagem na fila `news-processing-queue`. Para deploy local,
configure `NEWS_PROCESSING_QUEUE_URL` ou use o valor padrão do LocalStack em
`http://localhost:4566/000000000000/news-processing-queue`.

## PostgreSQL local

O Compose da raiz executa PostgreSQL 18 e mantém os dados no volume
`postgres-data`. Ele lê `POSTGRES_PASSWORD` e `POSTGRES_PORT` do `.env` da raiz.
Suba PostgreSQL e Qdrant a partir da raiz. O Qdrant fica disponível em
`http://localhost:6333`; ambos os serviços mantêm os dados em volumes nomeados
para que reinícios dos containers não apaguem relatórios, chaves de idempotência
ou embeddings:

```sh
docker compose up -d postgres qdrant
docker compose ps
```

`POSTGRES_PASSWORD` inicializa a senha apenas quando o volume é criado. Para um
volume existente, a variável precisa corresponder à senha já configurada no
PostgreSQL; alterá-la sozinha não troca a senha do usuário.

Aplique as migrations na ordem numérica, uma única vez:

```sh
docker compose exec -T postgres psql -U fin_agent -d fin_agent \
  < backend/db/migrations/000001_persistence_domain.up.sql
docker compose exec -T postgres psql -U fin_agent -d fin_agent \
  < backend/db/migrations/000002_deterministic_market_indicators.up.sql
docker compose exec -T postgres psql -U fin_agent -d fin_agent \
  < backend/db/migrations/000003_market_data_sync_status.up.sql
```

Para reverter, aplique os arquivos `.down.sql` em ordem inversa. O Compose não
apaga o volume ao reiniciar os serviços.

## Desenvolvimento e testes

Execute os comandos Go a partir desta pasta:

```sh
go test ./...
sqlc generate -f db/sqlc.yml
```

O teste de integração da migration requer um banco PostgreSQL 18 ou superior.
Com o serviço local ativo, carregue o `.env` da raiz para disponibilizar a
variável de teste ao processo `go test` (o carregamento automático é feito pelos
executáveis da aplicação, não pelo runner de testes):

```sh
set -a
. ../.env
set +a
go test ./db -run TestPostgres18PersistenceMigration
```

O teste cria e remove um schema isolado; não apaga nem altera as tabelas do
schema padrão do banco configurado.

## Docker

O Dockerfile usa esta pasta como contexto de build. Construa a imagem a partir
da raiz do monorepo com:

```sh
docker build -f backend/Dockerfile backend
```
