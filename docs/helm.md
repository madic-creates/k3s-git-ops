# Helm

Show possible helm chart settings:

```shell
helm show values prometheus-community/kube-prometheus-stack | less
```

Show installed settings:

```shell
helm list --all-namespaces
helm get values kube-prometheus-stack -n k3s-monitoring | less
```

Show all charts of a repository:

```shell
helm search repo prometheus-community
```

Show all repositories:

```shell
helm repo list
```
