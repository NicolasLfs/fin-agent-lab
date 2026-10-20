# Backend Go

Backend Go do monorepo, com o domínio em `internal/domain` e acesso tipado ao
PostgreSQL gerado pelo `sqlc` em `internal/store`. O schema de entrada do `sqlc`
fica em `db/schema.sql`; migrations versionadas e reversíveis ficam em
`db/migrations/`.

## PostgreSQL local

O Compose da raiz executa PostgreSQL 18 e mantém os dados no volume
`postgres-data`. Defina uma senha local no ambiente (não a salve no repositório)
e suba o serviço a partir da raiz:

```sh
export POSTGRES_PASSWORD='sua-senha-local'
docker compose up -d postgres
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
