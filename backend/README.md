# Backend Go

Backend Go do monorepo, com o domínio em `internal/domain` e acesso tipado ao
PostgreSQL gerado pelo `sqlc` em `internal/store`. O schema de entrada do `sqlc`
fica em `db/schema.sql`; migrations versionadas e reversíveis ficam em
`db/migrations/`.

## API

A aplicação usa Uber Fx para iniciar a API `net/http`. Configure as dependências
por ambiente; não grave URLs com credenciais em arquivos versionados:

```sh
export DATABASE_URL="postgres://fin_agent:<senha>@localhost:5432/fin_agent?sslmode=disable"
export AGENT_URL="http://localhost:8000"
export QDRANT_URL="http://localhost:6333"
export LOCALSTACK_URL="http://localhost:4566"
export HTTP_ADDR=":8080"
go run .
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

O schema atual contém cotações, mas não contém taxas brutas/prazos de renda
fixa nem fundamentos de ações/FIIs. Por isso, o serviço só envia preços de
referência exatos de ações/FIIs com a cotação mais recente; não calcula yield nem
ordena ativos por preço. Ativos sem os dados determinísticos necessários não
são apresentados como oportunidades.

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
`postgres-data`. Defina uma senha local no ambiente (não a salve no repositório)
e Suba PostgreSQL e Qdrant a partir da raiz. O Qdrant fica disponível em
`http://localhost:6333`; ambos os serviços mantêm os dados em volumes nomeados
para que reinícios dos containers não apaguem relatórios, chaves de idempotência
ou embeddings:

```sh
export POSTGRES_PASSWORD='sua-senha-local'
docker compose up -d postgres qdrant
docker compose ps
```

`POSTGRES_PASSWORD` inicializa a senha apenas quando o volume é criado. Para um
volume existente, a variável precisa corresponder à senha já configurada no
PostgreSQL; alterá-la sozinha não troca a senha do usuário.

Aplique a migration inicial uma única vez:

```sh
docker compose exec -T postgres psql -U fin_agent -d fin_agent \
  < backend/db/migrations/000001_persistence_domain.up.sql
```

O arquivo `.down.sql` correspondente reverte esse schema. O Compose não apaga o
volume ao reiniciar os serviços.

## Desenvolvimento e testes

Execute os comandos Go a partir desta pasta:

```sh
go test ./...
sqlc generate -f db/sqlc.yml
```

O teste de integração da migration requer um banco PostgreSQL 18 ou superior.
Com o serviço local ativo, execute:

```sh
TEST_DATABASE_URL="postgres://fin_agent:${POSTGRES_PASSWORD}@localhost:5432/fin_agent?sslmode=disable" \
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
