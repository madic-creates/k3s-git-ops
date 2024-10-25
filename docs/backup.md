# Backup

Velero procides a [cli](https://velero.io/docs/v1.3.0/basic-install/#install-the-cli). It uses kubeconfig to communicate with the cluster.

## Velero

Sometimes we need to specify the namespace because velero is installed in the longhorn namespace. Velero CLI defaults to the namespace with name velero.

``shell
velero describe schedules
```

Backup history:

``shell
velero backup get
```

``shell
velero describe backup
```

Create on-demand backup with config from already existing schedule:

``shell
velero backup create --from-schedule velero-daily-backup -n longhorn
```
