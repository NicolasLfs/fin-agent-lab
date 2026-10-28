# Terraform local

Esta configuração provisiona somente os recursos SQS usados pelo projeto no
LocalStack:

- `news-processing-queue`, com visibility timeout de 60 segundos, long polling
  de 20 segundos e redrive após cinco recebimentos;
- `news-processing-dlq`, com retenção de 14 dias.

O provider AWS está fixado no lockfile e configurado com credenciais locais de
teste. A variável `localstack_endpoint` só aceita
`http://localhost:4566`; este diretório não pode apontar para AWS real nem para
outro endpoint.

Com o LocalStack iniciado pela raiz, valide e provisione:

```sh
terraform -chdir=infra/terraform fmt -check -recursive
terraform -chdir=infra/terraform init -input=false -backend=false
terraform -chdir=infra/terraform validate
terraform -chdir=infra/terraform test
terraform -chdir=infra/terraform plan
terraform -chdir=infra/terraform apply
```

O teste cria filas temporárias com sufixo `-test`, valida o redrive e as remove
sem tocar nas filas persistentes. Para uso diário, `bash scripts/local-up.sh`
inicia o LocalStack e executa `terraform apply` automaticamente.

O estado Terraform é local. Mantenha `terraform.tfstate` e `.terraform/`
fora do Git; o `.gitignore` já exclui esses arquivos. Não execute
`terraform destroy` para validar a configuração, pois isso remove as filas
persistentes do ambiente local.
