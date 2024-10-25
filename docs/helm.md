# Helm

Mögliche Einstellungen anzeigen:

```shell
helm show values prometheus-community/kube-prometheus-stack | less
```

Installierte Einstellungen anzeigen:

```shell
helm list --all-namespaces
helm get values kube-prometheus-stack -n k3s-monitoring | less
```

Alle Charts eines Repos anzeigen:

```shell
helm search repo prometheus-community
```

Alle Repos anzeigen:

```shell
helm repo list
```
