# Backend Go

Este diretório contém o backend Go do monorepo. O módulo e a versão da linguagem
estão definidos em `go.mod`; o `Dockerfile` do serviço também fica nesta pasta.

## Estrutura

```text
backend/
├── db/
│   ├── query.sql   # queries PostgreSQL para o sqlc
│   ├── schema.sql  # schema de entrada do sqlc
│   └── sqlc.yml    # configuração de geração Go
├── Dockerfile
├── go.mod
└── main.go
```

O código da aplicação e suas funções serão organizados em `internal/`. O
`main.go` na raiz do módulo será o ponto de entrada e chamará essas funções.
O acesso a PostgreSQL é definido em `db/` e o código tipado será gerado pelo
`sqlc`, conforme a configuração do projeto.

## Comandos

Execute os comandos a partir desta pasta:

```sh
go test ./...
sqlc generate -f db/sqlc.yml
```

O esqueleto atual ainda não implementa o servidor nem as queries do banco.
Assim, a geração do `sqlc` só terá código para produzir depois que forem
definidos o schema e as queries.

## Docker

O Dockerfile usa esta pasta como contexto de build. Quando o serviço estiver
implementado, construa a imagem a partir da raiz do monorepo com:

```sh
docker build -f backend/Dockerfile backend
```
