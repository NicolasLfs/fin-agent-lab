# Checklist de Implementação

Checklist de etapas para implementar o [desafio](./CHALLENGE.md). Marque cada item conforme for concluído.

## 1. Planejamento e estrutura

- [x] Definir as decisões de arquitetura e registrar os fluxos entre API Go, worker Go, agente Python e dependências.
- [x] Definir comunicação síncrona entre Go e Python via HTTP/REST com payloads JSON; Go usa `net/http` e Python usa FastAPI.
- [x] Definir o contrato síncrono interno `POST /internal/agent/run`, com request/response JSON e erros padronizados.
- [x] Definir os códigos HTTP para payload inválido, validação interna, limite do provedor de IA e indisponibilidade temporária.
- [x] Definir timeouts de comunicação Go–Python: dial de 3 segundos e timeout total de 30 a 45 segundos.
- [x] Definir a estrutura base do monorepo: `/backend` para Go, `/agent` para o motor Python e `/infra` para Terraform e Kubernetes.
- [x] Organizar o código Go dentro de `/backend` e manter o `go.mod` e o Dockerfile do backend nessa área.
- [x] Configurar o projeto Python dentro de `/agent`, usando `pyproject.toml`, `uv` e `setuptools`.
- [x] Organizar a configuração Terraform em `/infra/terraform` e os manifests Kubernetes em `/infra/k8s`.
- [x] Definir Amazon SQS como mensageria, simulado localmente pelo LocalStack executado via Docker; não usar RabbitMQ.
- [x] Definir PostgreSQL 18 como banco relacional.
- [x] Definir Qdrant como banco vetorial.
- [x] Definir `net/http` da biblioteca padrão como implementação da API HTTP Go.
- [x] Definir Kubernetes local com manifests nativos; não usar Helm nem Kustomize, pois o ambiente é de aprendizado e não terá implantação em produção ou múltiplos ambientes.
- [x] Escolher Docker Desktop para executar o cluster Kubernetes local e documentar como iniciá-lo.
- [x] Definir LangGraph para orquestração do fluxo de recomendação.
- [x] Definir `uv` para gerenciamento e instalação das dependências Python, com `uv.lock` versionado.
- [x] Definir `setuptools` como backend de build do pacote Python.

## 2. Persistência e domínio

- [ ] Implementar o value object `Money` em Go com precisão decimal exata.
- [ ] Validar o contrato JSON de `Money` e rejeitar `NaN`, notação científica e valores com mais de duas casas decimais.
- [ ] Garantir que valores monetários não usem `float32` ou `float64` em cálculos, parsing, serialização ou persistência.
- [ ] Definir o esquema PostgreSQL para ativos, cotações, notícias, idempotência e relatórios de recomendação.
- [ ] Usar PostgreSQL 18 no ambiente local e nos testes de integração.
- [ ] Criar migrations versionadas para o esquema e suas constraints e índices.
- [ ] Configurar `sqlc` como único meio de gerar o código tipado de acesso ao PostgreSQL.
- [ ] Escrever as queries SQL e gerar os pacotes Go com `sqlc`.
- [ ] Manter explícitos e testáveis os limites transacionais e os locks necessários.
- [ ] Definir os estados dos relatórios: `PROCESSING`, `COMPLETED` e `FAILED`.

## 3. API e serviço Go

- [ ] Inicializar os serviços Go com Uber Fx.
- [ ] Implementar a API HTTP e validação dos contratos de entrada e saída.
- [ ] Implementar a API usando `net/http` da biblioteca padrão.
- [ ] Implementar `POST /recommendations/generate` e exigir `Idempotency-Key`.
- [ ] Implementar `GET /health/live` e `GET /health/ready`.
- [ ] Fazer a readiness verificar as conexões com PostgreSQL, Qdrant e Amazon SQS no LocalStack.
- [ ] Implementar o cálculo determinístico das taxas líquidas e dos indicadores usados no ranking.
- [ ] Implementar ordenação determinística dos ativos ranqueados.
- [ ] Passar ao serviço Python os dados calculados em Go como contexto estrito; não delegar cálculos financeiros ao LLM.
- [ ] Chamar o motor Python via HTTP/REST usando `net/http` e JSON, com timeout e tratamento explícito de respostas e erros.
- [ ] Configurar o cliente Go com dial timeout de 3 segundos e timeout total dentro da faixa de 30 a 45 segundos.
- [ ] Tratar timeout do cliente HTTP sem bloquear goroutines nem iniciar novamente uma execução que já possa estar em andamento; atualizar o estado do relatório para reprocessamento ou falha conforme a política definida.
- [ ] Persistir o relatório final e as referências às cotações e notícias usadas.

## 4. Ingestão e mensageria

- [ ] Implementar a coleta configurável de taxas, cotações e notícias nas fontes definidas.
- [ ] Implementar parsing de RSS e cálculo SHA-256 do conteúdo das notícias.
- [ ] Persistir notícias e cotações de forma idempotente, inclusive após reinício dos serviços.
- [ ] Publicar notícias novas na fila SQS `news-processing-queue`.
- [ ] Configurar visibility timeout, retries e dead-letter queue (DLQ) para mensagens com falha, considerando entrega at-least-once.
- [ ] Garantir que mensagens repetidas não causem registros ou embeddings duplicados.
- [ ] Implementar logs estruturados com `correlationId` e `reportId`.

## 5. Agente Python e RAG

- [ ] Configurar o motor de agentes em `/agent`, com Python 3.11+, `pyproject.toml`, backend de build `setuptools` e `uv.lock`.
- [ ] Instalar e sincronizar dependências com `uv` nos ambientes locais e Docker, respeitando o lockfile.
- [x] Configurar `setuptools` como build backend no `pyproject.toml` e validar a construção do pacote Python.
- [ ] Expor o serviço do motor de agentes com FastAPI e implementar os endpoints internos HTTP/REST consumidos pelo backend Go.
- [ ] Validar requests e responses JSON da API FastAPI, incluindo erros e campos obrigatórios.
- [ ] Implementar `POST /internal/agent/run` com os campos de request e response definidos em [ARCHITECTURE.md](./ARCHITECTURE.md).
- [ ] Retornar erros no envelope padronizado `{ "error": { "code", "message", "retryable" } }` e aplicar o mapeamento de status HTTP acordado.
- [ ] Configurar handlers FastAPI para retornar `400` para JSON malformado/campos obrigatórios ausentes e reservar `422` para falhas de validação interna das tools.
- [ ] Manter tentativas de fallback entre modelos OpenRouter dentro do motor Python; retornar `429` ao Go quando a cota estiver esgotada e não houver rota de contingência disponível.
- [ ] Implementar o worker de embeddings usando o SDK oficial `qdrant-client`.
- [ ] Integrar a geração de embeddings com o provedor Gemini e configurar o modelo de embedding escolhido.
- [ ] Persistir no Qdrant o vetor e os metadados `asset_ticker`, `published_at` e `sentiment_score`.
- [ ] Implementar busca vetorial filtrada para notícias dos últimos sete dias e ativos considerados no ranking.
- [ ] Implementar o grafo linear LangGraph: busca no Qdrant, análise de risco/bandeiras vermelhas via LLM e formatação do JSON/Markdown de saída.
- [ ] Integrar o LLM via OpenRouter, priorizando o tier gratuito.
- [ ] Configurar rotas de contingência no OpenRouter e definir quando alternar para elas diante de falhas, indisponibilidade ou limites do provedor/modelo primário.
- [ ] Definir o estado do grafo para transportar os dados financeiros calculados pelo Go, notícias recuperadas, `sourceNewsIDs`, análise de risco e resultado final.
- [ ] Configurar um checkpointer LangGraph conectado ao PostgreSQL para persistir e recuperar o estado das execuções após falhas do motor Python.
- [ ] Validar e sanitizar entradas e saídas das ferramentas do agente.
- [ ] Restringir justificativas aos dados fornecidos e às fontes recuperadas para reduzir alucinações.
- [ ] Tratar indisponibilidade e timeout do LLM sem perder o estado ou o histórico do relatório.
- [ ] Testar fallback entre a rota gratuita principal e as rotas de contingência do OpenRouter, incluindo erros e limites de uso.
- [ ] Incluir no relatório os IDs das notícias citadas e das cotações usadas.

## 6. Idempotência, concorrência e resiliência

- [ ] Implementar idempotência persistente para solicitações de recomendação com `Idempotency-Key`.
- [ ] Garantir que solicitações concorrentes com a mesma chave gerem apenas um relatório.
- [ ] Garantir que 50 ingestões concorrentes da mesma notícia gerem um único registro e um único embedding.
- [ ] Tratar indisponibilidade temporária do PostgreSQL, Qdrant, SQS e API do LLM.
- [ ] Testar retomada de uma execução LangGraph a partir do estado persistido no PostgreSQL após reinício ou falha do processo Python.
- [ ] Garantir que reinícios dos containers não apaguem o estado necessário para idempotência e auditoria.
- [ ] Verificar que falhas e retries não gerem relatórios duplicados nem percam relatórios concluídos.

## 7. Testes

- [ ] Testar invariantes de `Money` e rejeição de entradas inválidas.
- [ ] Testar cálculos financeiros e ordenação determinística sem uso de ponto flutuante.
- [ ] Testar parsing de RSS e deduplicação por SHA-256.
- [ ] Testar esquemas de entrada, sanitização de texto e filtros de busca do Qdrant.
- [ ] Testar serialização e validação dos contratos JSON entre Go e FastAPI, o envelope de erro e o mapeamento de status HTTP.
- [ ] Testar dial timeout de 3 segundos, timeout total configurado entre 30 e 45 segundos e atualização de estado em caso de timeout.
- [ ] Testar idempotência e concorrência da ingestão e da API de recomendações.
- [ ] Executar testes de integração com PostgreSQL 18, Qdrant e SQS no LocalStack.
- [ ] Executar `go test -race ./...`.
- [ ] Executar `pytest`.

## 8. Execução local, infraestrutura e entrega

- [ ] Criar Dockerfiles para os serviços Go e Python.
- [ ] Criar Docker Compose para serviços da aplicação e dependências locais, incluindo o LocalStack com SQS.
- [ ] Configurar Terraform em `/infra/terraform` para LocalStack e provisionar os recursos necessários.
- [ ] Provisionar filas SQS e DLQ pelo Terraform no LocalStack.
- [ ] Criar manifests Kubernetes nativos em `/infra/k8s` para o ambiente local de aprendizado, sem Helm ou Kustomize.
- [ ] Configurar Deployments, Services, ConfigMaps, Secrets e probes; adicionar Ingress apenas se necessário para acesso local.
- [ ] Criar automação para iniciar dependências com Docker, subir LocalStack, executar Terraform e aplicar os manifests no cluster Kubernetes local.
- [ ] Validar `terraform init && terraform apply` a partir de `/infra/terraform`.
- [ ] Validar `kubectl apply -f ./infra/k8s/`.
- [ ] Documentar arquitetura, decisões de design, configuração e comandos de execução no `README.md` e [ARCHITECTURE.md](./ARCHITECTURE.md).
- [ ] Confirmar que a solução não inclui gerenciamento de carteiras nem requisitos de autenticação.
