# Distributed Storage

## Longhorn

[Longhorn](https://longhorn.io/){target=_blank} provides the distributed block storage layer for this cluster. The Helm release, default settings, and supporting resources are managed declaratively in `apps/longhorn/kustomization.yaml`, so all configuration changes should flow through GitOps.

### Accessing the Longhorn UI

- Dashboard: `https://storage.internal.neese-web.de` (published through Traefik with Authelia forward-auth).
- In-cluster service: `http://longhorn-frontend.longhorn.svc.cluster.local`.
- Use the UI for day-to-day administration (volume health, replicas, backups, recurring jobs) and to confirm Git-driven changes after syncs.

### Default Storage Classes and PVC Usage

- The `longhorn` `StorageClass` is marked as the default via a sync-time patch, so workloads can omit `storageClassName` unless an alternative is required.
- The reclaim policy is set to `Retain`. PVCs and underlying volumes are kept after a workload is deleted to prevent accidental data loss; clean up volumes explicitly in the UI or via `kubectl delete volume.longhorn.io/<name>`.
- Typical PVC:

  ```yaml
  apiVersion: v1
  kind: PersistentVolumeClaim
  metadata:
    name: example-longhorn-pvc
  spec:
    accessModes:
      - ReadWriteOnce
    resources:
      requests:
        storage: 20Gi
    storageClassName: longhorn
  ```

- Validate provisioning with `kubectl get pvc` and `kubectl get volumes.longhorn.io -n longhorn`.

### Backups, Snapshots, and Automation

- Longhorn's own backup target is disabled (`defaultBackupStore.backupTarget: ""` in `apps/longhorn/kustomization.yaml`). Volume data is backed up by the restic backup fleet instead (see [Backup](backup.md)). The former Wasabi target's credentials were revoked, and with a dead S3 target longhorn-manager re-ran `longhorn backup ls` in a tight loop at 100-200% CPU.
- To re-enable it, set `defaultBackupStore.backupTarget` and `backupTargetCredentialSecret` to a reachable target and a valid secret. Since chart 1.8 these keys live under `defaultBackupStore`; `defaultSettings.backupTarget` is ignored.
- There is no CSI `VolumeSnapshotClass` for Longhorn backups (the former `longhorn-vsc-backup`, `type: bak`, was removed together with the revoked `wasabi-secret`). Re-add one alongside a working backup target if CSI-driven backups are needed again.
- A recurring job named `trim-filesystem` runs daily at 06:00 to perform filesystem trim on all volumes assigned to the `default` group. Adjust or add jobs through Git in `apps/longhorn/longhorn-config.enc.yaml`.

### Operational Guidance

- Follow the controlled upgrade/maintenance steps captured in `docs/operations/longhorn-maintenance.md` when performing Longhorn upgrades or disruptive changes.
- Monitor Longhorn health dashboards exposed through Grafana (PVC `longhorn-pvc-grafana`) to detect replica rebuilds or disk pressure early.

### Troubleshooting

- **Volume still attached to a node:** Longhorn enforces exclusive attachment. If a pod restart reports `Volume is already exclusively attached to one node`, wait for the detachment timeout or force detach the volume (UI → Volume → `Detach`), ensuring the old workload is fully terminated first.
- **Replica rebuild loops:** Check node disk usage and engine replica logs in the UI; if disk capacity is low, cordon the node, migrate workloads, and expand storage.
- **Backup target errors / high longhorn-manager CPU:** Check `kubectl get backuptargets.longhorn.io -n longhorn default -o yaml`. An unreachable or unauthorized target makes longhorn-manager retry `longhorn backup ls` several times per second; fix the credentials or clear the target.
