# Desafio Backend — Engine Distribuída de Processamento e RAG Financeiro para a B3 (com Kubernetes, LocalStack & Terraform)

Implemente um serviço híbrido em **Go** e **Python**, executado localmente com **Docker Compose** e **Kubernetes**, usando **Terraform** e **LocalStack** para provisionar e simular os serviços AWS necessários, incluindo SQS. O sistema deve realizar ingestão de dados da B3/Renda Fixa, análise vetorial de notícias (RAG) e ranqueamento de investimentos.

## 1. Objetivo

A aplicação deve oferecer uma API HTTP em **Go** (para consulta de cotações e ingestão determinística) e um **Motor de Agentes em Python** (para orquestração de IA, busca semântica em notícias e geração de rankings justificados).

A solução deve processar volumes contínuos de dados de mercado (cotações, notícias e taxas) de forma resiliável e garantir idempotência, auditabilidade de recomendações, tolerância a falhas na comunicação entre microsserviços e taxa de acerto determinística nos cálculos financeiros.

A avaliação considera precisão monetária, idempotência persistente na ingestão vetorial, concorrência no pipeline de ETL, resiliência do RAG contra alucinações e decisões de arquitetura e infraestrutura Cloud-Native.

## 2. Ambiente de Execução e Falhas

A aplicação opera sobre entidades financeiras e textuais: `Asset` (Ações, FIIs, CDBs, LCIs, Tesouro), `MarketNews` (Notícias/RSS) e `RecommendationReport`.

Assuma entrega **at-least-once** em todas as filas/mensagerias e prepare a solução para:

* Recebimento repetido de notícias com o mesmo hash de conteúdo;

* Falha ou indisponibilidade temporária na API do LLM (OpenRouter / Gemini);

* Ingestão paralela de notícias afetando o mesmo ativo ou setor financeiro;

* Queda da instância Python no meio da geração de um embedding ou relatório RAG;

* Indisponibilidade temporária do PostgreSQL ou do Qdrant.

Nenhuma dessas situações pode gerar cálculo de rentabilidade distorcido, alucinação de dados numéricos pelo LLM ou perda de histórico de recomendações confirmadas no banco.

## 3. Stack Tecnológica

### Tecnologias Obrigatórias

| Responsabilidade | Tecnologia | 
| ----- | ----- | 
| **Backend / Ingestão / ETL** | **Go** em `/backend`; declare a versão utilizada em `go.mod` e no Dockerfile | 
| **Motor de IA / Agentes** | **Python 3.11+**; projeto em `/agent`, configurado e gerido por `pyproject.toml` | 
| **Injeção de Dependência (Go)** | Uber Fx (`go.uber.org/fx`) | 
| **Orquestração de Agentes (Python)** | LangGraph ou CrewAI | 
| **API HTTP (Go)** | Biblioteca padrão `net/http` |
| **Persistência Relacional** | PostgreSQL 18 | 
| **Banco Vetorial** | Qdrant | 
| **Mensageria Assíncrona** | **Amazon SQS**, simulado localmente pelo LocalStack executado via Docker | 
| **Infraestrutura / Cloud (IaC)** | Arquivos de **Terraform** e manifests **Kubernetes** organizados em `/infra`, usando **LocalStack** via Docker para simular os serviços AWS exigidos | 
| **Orquestração de Containers** | **Kubernetes** local, usando manifests Kubernetes nativos em `/infra/k8s`; não usar Helm ou Kustomize | 
| **Evolução do Banco** | Migrations versionadas (`golang-migrate` ou equivalente) | 
| **Testes** | `testing` (Go) e `pytest` (Python), com suporte a `-race` em Go | 

### Acesso ao Banco de Dados e Vetores

* **PostgreSQL (Go):** Uso obrigatório de `sqlc` para geração de código Go tipado a partir de queries SQL. Transações, locks e constraints devem permanecer explícitos e verificáveis.

* **Qdrant (Python):** Uso do SDK oficial `qdrant-client`. Todo ponto armazenado deve ter metadados estruturados (`asset_ticker`, `published_at`, `sentiment_score`).

## 4. Garantias Obrigatórias

1. **Precisão Monetária Rigorosa:** Valores financeiros de cotações e cálculos não podem passar por `float32` ou `float64` durante cálculo, parsing, serialização ou persistência.

2. **Idempotência do Pipeline de Dados:** A ingestão de notícias, cotações e recomendações deve ser idempotente e sobreviver ao reinício de todos os pod/containers.

3. **Isolamento de Cálculos vs. LLM:** O LLM não pode calcular rentabilidades ou taxas líquidas. Todos os números apresentados no relatório devem ser calculados de forma determinística em Go e passados como contexto estrito (*Grounding*) para o Python/LLM.

4. **Auditabilidade de Recomendações:** Todo relatório de investimento gerado pela IA deve armazenar os IDs exatos das notícias (Qdrant) e das cotações (Postgres) utilizadas na tomada de decisão.

5. **IaC e Provisionamento Reprodutível:** Todo o ambiente Kubernetes e os recursos AWS (simulados via LocalStack) devem ser provisionados declarativamente via scripts Terraform.

## 5. Modelo de Domínio e Entidades

### 5.1. Money (Value Object)

* Imutável, contendo valor decimal de precisão exata e código de moeda ISO 4217 (ex: `BRL`).

* Contrato JSON de entrada/saída: `{"amount": "10000.50", "currency": "BRL"}`.

* Proibido o uso de ponto flutuante (`float`). Utilize inteiros em unidades mínimas (centavos) ou bibliotecas de precisão arbitrária (`shopspring/decimal` em Go).

* Rejeite valores inválidos (`NaN`, notação científica, escala superior a 2 casas decimais).

### 5.2. MarketNews & EmbeddingPayload

* Representa a notícia ou fato relevante ingerido pelo worker em Go.

* Campos: `id`, `title`, `content_hash` (SHA-256 do conteúdo), `source`, `url`, `publishedAt`, `processedStatus`.

* O payload enviado ao Qdrant deve incluir o vetor (embedding de 1536 ou 768 dimensões dependendo do modelo) e o payload JSON estruturado com ticker citado, data e grau de sentimento.

### 5.3. RecommendationReport

Entidade terminal do fluxo de IA:

* Campos: `id`, `createdAt`, `rankedAssets` (JSON com os ativos ranqueados), `justificationText` (Markdown gerado pela IA), `sourceNewsIDs` (array de IDs do Qdrant).

* Estados: `PROCESSING`, `COMPLETED`, `FAILED`.

## 6. Operações e Pipelines

### Pipeline A: Ingestão Determinística (Go Worker)

1. Varre fontes de dados (APIs públicas do Banco Central, Tesouro, Cotações Yahoo Finance) a cada intervalo configurável.

2. Inspeciona feeds RSS/Notícias, calcula o `SHA-256` do corpo e verifica duplicidade antes de salvar no PostgreSQL.

3. Notícias novas são publicadas na fila SQS `news-processing-queue` para o serviço Python gerar embeddings e persistir no Qdrant.
4. Configurar tentativas de reprocessamento e uma dead-letter queue (DLQ) para mensagens que excederem o limite de falhas.

### Pipeline B: Agente de Recomendação (Python RAG)

1. Recebe a solicitação de geração de um ranking de oportunidades de investimento.

2. **Passo 1 (Determinístico):** O serviço Go consulta o banco, calcula as taxas reais líquidas da renda fixa e busca múltiplos de ações/FIIs, entregando a lista ordenada matematicamente para o serviço Python.

3. **Passo 2 (Semântico/RAG):** O Python faz uma busca vetorial no Qdrant filtrando notícias dos últimos 7 dias para os ativos ranqueados no Passo 1.

4. **Passo 3 (Síntese do Agente):** O agente orquestrado (LangGraph) analisa se existem "bandeiras vermelhas" (notícias negativas graves, revisões de rating) para os ativos selecionados e compõe um ranking justificado.

5. **Passo 4 (Auditabilidade):** O relatório é gravado no PostgreSQL e o estado passa para `COMPLETED`.

## 7. Contratos HTTP (Serviço Go)

### Solicitar Ranking de Oportunidades de Investimento

```
POST /recommendations/generate
Content-Type: application/json
Idempotency-Key: req-rec-2026-10-19

```

```
{
  "assetTypes": ["FIXED_INCOME", "STOCK", "FII"]
}

```

Exemplo de resposta de conclusão:

```
{
  "reportId": "0192f298-345e-7e38-af88-e43f851a819d",
  "status": "COMPLETED",
  "createdAt": "2026-10-19T15:00:00Z",
  "summary": {
    "rankedAssets": [
      { "rank": 1, "ticker": "TESOURO_IPCA_2029", "type": "FIXED_INCOME" },
      { "rank": 2, "ticker": "VALE3", "type": "STOCK" },
      { "rank": 3, "ticker": "HGLG11", "type": "FII" }
    ]
  },
  "justification": "O ranking priorizou Renda Fixa atrelada à inflação devido às projeções recentes do relatório Focus...",
  "citedNewsIds": ["qdrant-uuid-1", "qdrant-uuid-2"]
}

```

### Health Checks Públicos

```
GET /health/live
GET /health/ready

```

Liveness do processo e readiness verificando conexões ativas com PostgreSQL, Qdrant e Amazon SQS no LocalStack.

## 8. Infraestrutura DevOps (Terraform + Kubernetes + LocalStack)

A fase final do projeto consiste no empacotamento para ambiente Kubernetes distribuído e provisionamento via Infraestrutura como Código (IaC).

### 8.1. LocalStack & Terraform (`/infra/terraform`)

* Scripts Terraform configurados com o provider `aws` apontado para a URL local do **LocalStack** (`http://localhost:4566`).
* Provisionamento dos recursos AWS simulados necessários à aplicação, incluindo filas SQS e DLQ.
* Configuração de *Secrets Manager* simulado, caso seja utilizado para armazenar credenciais de API (Gemini/OpenRouter) e banco de dados.

### 8.2. Manifests Kubernetes (`/infra/k8s`)

* **Deployments & Pods:** Deployments isolados para a API em Go, Worker de Ingestão Go e Motor Python, com dependências PostgreSQL, Qdrant e Amazon SQS simulado pelo LocalStack.
* **Services & Ingress:** Exposição interna entre pods via `ClusterIP`; configurar Ingress apenas se necessário para acesso local.
* **ConfigMaps & Secrets:** Injeção de variáveis de ambiente sem expor dados sensíveis no código.
* **Probes:** Adição de `livenessProbe` e `readinessProbe` nos pods consumindo os endpoints `/health/live` e `/health/ready`.
* Use manifests Kubernetes nativos para aprender os recursos básicos de orquestração. O ambiente é local, de aprendizado e não será promovido para produção nem implantado em múltiplos ambientes; não usar Helm Charts ou Kustomize.

## 9. Testes e Validação Obrigatórios

### Testes Unitários e de Integração

* **Go:** Invariantes do `Money`, validação de limites numéricos sem uso de `float`, ordenação determinística de Renda Fixa e parsers de RSS.
* **Python:** Validação de esquemas de entrada das *tools* do agente, sanitização de texto e filtros de busca do Qdrant.
* **Containers Reais:** Execução com instâncias de PostgreSQL, Qdrant e LocalStack com SQS.

### Testes de Concorrência e Deduplicação

1. **Ingestão Paralela:** Envie a mesma notícia por 50 requisições simultâneas e comprove que apenas um registro é gravado no Postgres e um embedding gerado no Qdrant.
2. **Idempotência de Recomendações:** Envie 10 solicitações paralelas de recomendação com o mesmo header `Idempotency-Key` e comprove que apenas um relatório é gravado.
3. Execução obrigatória de `go test -race ./...`.

## 10. Critérios de Avaliação

| Critério | Pontos | Evidência Esperada | 
| ----- | ----- | ----- | 
| **Integridade e Precisão Monetária** | 15 | Ausência total de `float` e cálculos exatos de taxas no Go | 
| **Arquitetura e Separação de Conceitos** | 15 | Ingestão determinística em Go isolada do raciocínio/RAG em Python | 
| **Resiliência do Agente / RAG** | 15 | Proteção contra alucinações, tratamento de timeouts de LLM e auditabilidade das notícias citadas | 
| **Idempotência e Concorrência** | 15 | Deduplicação de notícias no Qdrant/Postgres e idempotência na API HTTP | 
| **DevOps, K8s & Terraform (LocalStack)** | 15 | Provisionamento IaC via Terraform no LocalStack e manifests K8s funcionais | 
| **Testes e Qualidade** | 10 | Testes de integração com containers reais e execução limpa com `-race` | 
| **Observabilidade e Logs** | 5 | Logs estruturados em JSON com rastreamento (`correlationId`, `reportId`) | 
| **Documentação e Execução** | 10 | Scripts de provisionamento claros e arquivo `ARCHITECTURE.md` explicativo | 
| **Total** | **100** |  | 

## 11. Instruções de Entrega

1. **Código-Fonte, Kubernetes e Terraform:** Organizados no monorepo com `/backend` para Go, `/agent` para Python e `/infra` para Terraform e Kubernetes.
2. **Ambiente local:** Automação via script shell ou Makefile para iniciar as dependências com Docker, subir o LocalStack, aplicar o Terraform e implantar os manifests Kubernetes no cluster local.
3. **Documentação (`README.md` e `ARCHITECTURE.md`):** Explicação detalhada dos padrões de software, diagramas da arquitetura K8s/LocalStack e decisões de design.

Comandos obrigatórios que devem funcionar sem erros:

```
terraform -chdir=./infra/terraform init && terraform -chdir=./infra/terraform apply
kubectl apply -f ./infra/k8s/
go test -race ./...
pytest

```