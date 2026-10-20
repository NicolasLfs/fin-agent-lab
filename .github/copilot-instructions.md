# Instruções do workspace

Estas instruções valem para todo o trabalho neste repositório. Consulte
`CHALLENGE.md`, `ARCHITECTURE.md`, `CHECKLIST.md` e o README do componente antes
de implementar ou alterar comportamento.

## TDD obrigatório

- Trabalhe em ciclos Red-Green-Refactor. Para cada comportamento novo ou bug,
  crie ou ajuste primeiro um teste automatizado que descreva o resultado
  esperado.
- Execute o teste antes da implementação e confirme que ele falha pelo motivo
  esperado. Não interprete erro de ambiente ou teste mal configurado como falha
  válida.
- Implemente apenas o necessário para passar o teste e rode novamente o teste
  relevante. Só prossiga para outro comportamento ou etapa quando ele passar.
- Refatore apenas com os testes verdes e execute-os novamente depois da
  refatoração. Ao concluir uma etapa, rode também a menor suíte relacionada.
- Para mudanças em infraestrutura sem testes existentes, primeiro estabeleça
  uma verificação automatizada adequada à mudança (por exemplo, `terraform test`,
  `terraform validate`, validação de configuração Compose ou validação de
  manifests Kubernetes). Registre o resultado esperado antes de alterar a
  infraestrutura e valide novamente após a alteração. Não substitua um teste
  possível por inspeção visual.
- Se não for possível executar a verificação por falta de dependências,
  serviços ou contexto correto, pare antes de avançar e informe o bloqueio;
  não declare a etapa aprovada.

## Garantias do desafio

- Preserve o escopo de análise e recomendações financeiras; não implemente
  gerenciamento de carteiras ou autenticação de usuários.
- Mantenha cálculos financeiros determinísticos em Go e nunca use `float32` ou
  `float64` para valores monetários, parsing, cálculos, serialização ou
  persistência. O LLM não calcula taxas nem rentabilidade.
- Preserve idempotência após reinícios e concorrência; use constraints e
  transações persistentes onde necessário, não apenas estado em memória.
- Mantenha auditáveis os IDs de cotações e notícias que fundamentam relatórios.
- Não exponha credenciais em código, logs, testes, manifests ou documentação.
  Use configuração por ambiente e nunca envie segredos a serviços externos.
- Siga as decisões arquiteturais registradas: Go `net/http`, Uber Fx e `sqlc`;
  Python 3.11+, `uv`, FastAPI, LangGraph e `qdrant-client`; SQS via LocalStack;
  Terraform em `infra/terraform`; manifests Kubernetes nativos em `infra/k8s`
  sem Helm ou Kustomize.
- Prefira os validadores e padrões já adotados. Não adicione dependências ou
  ferramentas sem necessidade; não esconda erros com fallbacks silenciosos.
- Ao final, informe os testes executados e seus resultados. Nunca descreva uma
  validação não executada como aprovada.
