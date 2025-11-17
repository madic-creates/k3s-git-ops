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

- Longhorn stores backups in Wasabi (`s3://k3s-at-home-01@eu-central-2/`); credentials live in the `longhorn/wasabi-secret` and are managed with `sops`.
- CSI snapshots use the `VolumeSnapshotClass` named `longhorn-vsc-backup`. Create `VolumeSnapshot` objects against that class to trigger Longhorn backups that Velero can consume.
- A recurring job named `trim-filesystem` runs daily at 06:00 to perform filesystem trim on all volumes assigned to the `default` group. Adjust or add jobs through Git in `apps/longhorn/longhorn-config.enc.yaml`.

### Operational Guidance

- Follow the controlled upgrade/maintenance steps captured in `docs/operations/longhorn-maintenance.md` when performing Longhorn upgrades or disruptive changes.
- Monitor Longhorn health dashboards exposed through Grafana (PVC `longhorn-pvc-grafana`) to detect replica rebuilds or disk pressure early.

### Troubleshooting

- **Volume still attached to a node:** Longhorn enforces exclusive attachment. If a pod restart reports `Volume is already exclusively attached to one node`, wait for the detachment timeout or force detach the volume (UI → Volume → `Detach`), ensuring the old workload is fully terminated first.
- **Replica rebuild loops:** Check node disk usage and engine replica logs in the UI; if disk capacity is low, cordon the node, migrate workloads, and expand storage.
- **Backup failures:** Verify Wasabi connectivity (`Settings → General → Test backup target`) and confirm the `wasabi-secret` matches the remote credentials.
