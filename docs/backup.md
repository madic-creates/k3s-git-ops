# Backup

/// note
Backups are still Work in Progress and subjected to change.
I am not satisfied with the current solutions.
///

Velero procides a [cli](https://velero.io/docs/v1.3.0/basic-install/#install-the-cli){target=_blank}. It uses kubeconfig to communicate with the cluster.

## Velero

Sometimes we need to specify the namespace because velero is installed in the longhorn namespace. Velero CLI defaults to the namespace with name velero.

```shell
velero describe schedules
```

Backup history:

```shell
velero backup get
```

```shell
velero describe backup
```

Create on-demand backup with config from already existing schedule:

```shell
velero backup create --from-schedule velero-daily-backup -n longhorn
```
