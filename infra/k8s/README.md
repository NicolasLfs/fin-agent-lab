# Kubernetes local

Os manifests nativos Kubernetes da aplicação serão mantidos nesta pasta. O
cluster local escolhido é o Kubernetes integrado ao Docker Desktop. Não usamos
Helm ou Kustomize.

## Iniciar o cluster

1. Abra as configurações do Docker Desktop e habilite **Kubernetes**.
2. Aguarde o estado do cluster indicar que está em execução.
3. Confirme que o contexto `docker-desktop` está selecionado:

   ```sh
   kubectl config current-context
   kubectl get nodes
   ```

Se outro contexto estiver ativo, selecione o contexto local explicitamente:

```sh
kubectl config use-context docker-desktop
```

Os manifests da aplicação ainda serão adicionados. Quando estiverem disponíveis,
serão aplicados a partir da raiz do monorepo com:

```sh
kubectl apply -f ./infra/k8s/
```
