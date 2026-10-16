# Downloader and unifi config volumes: hostPath → Longhorn PVC

Date: 2026-07-24

## Problem

Eight workloads keep their application config on the ZFS node via `hostPath`
mounts:

| App | Namespace | hostPath | Size |
|---|---|---|---|
| headphones | downloader | `/vol_raidz1/docker/configs/downloader/headphones/config` | 41M |
| lidarr | downloader | `/vol_raidz1/container-scheduler/downloader-lidarr` | 165M |
| nzbget | downloader | `/vol_raidz1/docker/configs/downloader/nzbget/config` | 8.5M |
| nzbget (scripts) | downloader | `/vol_raidz1/docker/configs/downloader/nzbget/scripts` | 2.2M |
| nzbhydra2 | downloader | `/vol_raidz1/docker/configs/downloader/nzbhydra2/config` | 168M |
| radarr | downloader | `/vol_raidz1/docker/configs/downloader/radarr/config` | 1.2G |
| radarr (trailer) | downloader | `/vol_raidz1/docker/configs/downloader/Trailer-Downloader-Config` | 7K |
| sonarr | downloader | `/vol_raidz1/docker/configs/downloader/sonarr/config` | 299M |
| trailarr | downloader | `/vol_raidz1/docker/configs/downloader/trailarr/config` | 246M |
| unifi-controller | media | `/vol_raidz1/docker/configs/unify-controller/config` | 372M |

None of these need to be `hostPath`. They should be regular PVCs so the data is
managed by the cluster's storage layer instead of by directory layout on a
single host.

Out of scope: the media trees (`/vol_raidz1/{video,music,photo,downloads}`) stay
`hostPath` — they are bulk data served from ZFS and are not config.

unifi-controller is a late addition to this work. It is the same shape as the
others — one config tree, one node — but it lives in namespace `media` and, unlike
the *arr stack, it has **no backup at all** today. Since the migration turns its
config into a Longhorn PVC, it also gets a restic CronJob modelled on Emby's, and
joins the backup fleet's alerting and restore-test rotation.

## Current state

- All eight Deployments are pinned to node04 via `nodeSelector: node-role/workload: zfs`.
- Longhorn runs on all four nodes, is the default StorageClass (3 replicas,
  `Retain`, volume expansion enabled). node04 has ~1.95 TB free.
- `apps/downloader/nzbget/k8s.backup-cronjob.yaml` mounts six of these config
  directories read-only via `hostPath` and backs them up hourly with restic.
  Its `RESTIC_SOURCE` (in `backup-secrets.enc.yaml`) lists the mount paths
  `/backup/{sonarr,radarr,nzbget,nzbhydra2,trailarr,lidarr}`.
- headphones is intentionally not backed up.
- `apps/emby/k8s.backup-cronjob.yaml` is the reference pattern for backing up a
  Longhorn PVC: mount the PVC `readOnly: true` and co-locate the backup pod with
  the workload pod, because the volume is ReadWriteOnce.

## Design

### 1. PVCs

One `k8s.pvc.yaml` per app, added to that app's `kustomization.yaml` resources.
Naming follows the existing convention (`longhorn-pvc-nextpvr-config`,
`longhorn-pvc-emby`).

| PVC | Namespace | Size |
|---|---|---|
| `longhorn-pvc-headphones-config` | downloader | 2Gi |
| `longhorn-pvc-lidarr-config` | downloader | 2Gi |
| `longhorn-pvc-nzbget-config` | downloader | 1Gi |
| `longhorn-pvc-nzbget-scripts` | downloader | 1Gi |
| `longhorn-pvc-nzbhydra2-config` | downloader | 2Gi |
| `longhorn-pvc-radarr-config` | downloader | 5Gi |
| `longhorn-pvc-radarr-trailer-downloader` | downloader | 1Gi |
| `longhorn-pvc-sonarr-config` | downloader | 2Gi |
| `longhorn-pvc-trailarr-config` | downloader | 2Gi |
| `longhorn-pvc-unifi-controller-config` | media | 5Gi |

All: `storageClassName: longhorn`, `accessModes: [ReadWriteOnce]`,
`volumeMode: Filesystem`. Sizes leave headroom over current usage; Longhorn thin
provisions, so real consumption is ~2.1 GB × 3 replicas.

PVCs carry no explicit sync-wave annotation of their own beyond whatever the
app's `commonAnnotations` already sets. ArgoCD orders PersistentVolumeClaim
before Deployment within a wave, so binding happens first either way.

### 2. Deployment changes

For each app:

- The `config` volume changes from `hostPath` to `persistentVolumeClaim`.
  **Container mount paths do not change.**
- Add `strategy: {type: Recreate}` where it is missing (headphones, nzbget,
  nzbhydra2, radarr, sonarr, unifi-controller). With a ReadWriteOnce volume a rolling update would
  let a second pod attach the same volume and write the same SQLite database.
  lidarr and trailarr already use `Recreate`.
- nzbhydra2's `/downloads` mount becomes an `emptyDir`. The backing directory is
  empty (512 bytes) and is only scratch space.
- radarr's trailer config keeps its current shape: PVC mounted with
  `subPath: settings.ini` at `/opt/Trailer-Downloader/settings.ini`, `readOnly`.
- nzbget's `scripts` volume becomes a PVC, still mounted `readOnly` at
  `/app/nzbget/scripts`.
- `nodeSelector: node-role/workload: zfs` stays on every Deployment. It is what
  keeps the RWO volumes attachable to one node and what co-locates the backup
  pod with the workloads.

Two of the eight would be free of host storage after the migration, and both keep
the pin anyway for reasons that have nothing to do with this change:

- **nzbhydra2** mounts only its config PVC and an `emptyDir`. But its PVC is one
  of six mounted by the shared downloader backup CronJob, which is pinned to
  node04. If the nzbhydra2 pod moved, its ReadWriteOnce volume would attach on
  another node and the backup pod could no longer mount it — the whole downloader
  run would fail. Freeing it would mean splitting it into its own CronJob with
  `podAffinity`, which is out of scope here.
- **unifi-controller** runs with `hostNetwork: true`. UniFi APs inform to a fixed
  host IP on ports the Service does not expose (8080, 3478/UDP, 10001/UDP), so a
  node change would silently orphan the APs. Its node binding comes from the
  network, not from storage, and the PVC does not change that.

### 3. Migration Jobs

One Job manifest per app in `scripts/migrate-hostpath-to-pvc/<app>.yaml`. This
path is outside `apps/`, so no ArgoCD Application ever picks it up; the Jobs are
applied by hand and deleted afterwards. They are never committed — they are
throwaway artifacts that only need to exist in the working tree long enough to
be applied once.

Job shape:

- namespace `downloader`, `nodeSelector: node-role/workload: zfs`,
  `restartPolicy: Never`, `backoffLimit: 2`
- `busybox` container, `runAsUser: 0` (config trees are mixed-ownership: UID 0,
  1000 and 1001)
- mounts the old `hostPath` read-only at `/src` and the new PVC at `/dst`
- nzbget and radarr each copy two volumes in one Job

Each copy step does, in order:

1. Abort if `/dst` contains anything other than `lost+found` — protects against
   a second run overwriting already-migrated data.
2. `cp -a /src/. /dst/` — preserves ownership, modes and timestamps.
3. Copy the source directory's own owner and mode onto the PVC root
   (`stat -c '%u:%g'` / `%a` from `/src`, then `chown`/`chmod` on `/dst`). A
   fresh Longhorn volume's root is `root:root 0755`; trailarr (UID 1000) and
   lidarr (UID 1001) need to be able to create files directly in `/config`.
4. `chmod 0755 /dst/lost+found`. ext4 creates it as `root:root 0700`. The backup
   CronJob runs as UID 1000, and `backup.sh` uses `set -eEu -o pipefail`, so
   restic's exit code 3 on an unreadable directory would fail every backup run
   and fire an ntfy alert.

The `downloader` and `media` namespaces both have a default-deny
CiliumNetworkPolicy. The Jobs need no network, so no policy change is required.
The unifi-controller Job runs in `media`.

### 4. Backup CronJob changes

In `apps/downloader/nzbget/k8s.backup-cronjob.yaml`, the six config `hostPath`
volumes are replaced by the six corresponding PVCs, mounted `readOnly: true`.

- Mount paths `/backup/<app>` stay identical, so the encrypted
  `RESTIC_SOURCE` needs no change.
- `securityContext` stays `runAsUser: 1000 / runAsGroup: 1002 / runAsNonRoot: true`.
  Because `cp -a` preserves ownership and modes, the existing group-bit access
  argument still holds.
- The existing `nodeSelector` already guarantees co-location with the workload
  pods, so no `podAffinity` is needed (Emby needs it only because Emby is not
  pinned to a node). A comment records the RWO dependency.
- The header comment is updated: the volumes are no longer host filesystem
  paths.

### 4b. New backup CronJob for unifi-controller

unifi-controller has never been backed up. Now that its config is a Longhorn PVC,
it gets a restic CronJob built to the same pattern as `apps/emby/k8s.backup-cronjob.yaml`.

New file `apps/unifi-controller/k8s.backup-cronjob.yaml` holding a CronJob and a
CiliumNetworkPolicy:

- schedule `25 * * * *` — the fleet is staggered per ten-minute slot and `:25` is
  free (`:00` paperless, `:10` mariadb, `:15` minecraft, `:20` grafana, `:30` emby,
  `:40` nextpvr, `:50` downloader)
- `podAffinity` on `app.kubernetes.io/name: unifi-controller` with
  `topologyKey: kubernetes.io/hostname`, exactly as Emby does, because the PVC is
  ReadWriteOnce
- the PVC mounted read-only at `/backup/unifi-controller`
- `securityContext: runAsUser: 1000 / runAsGroup: 1000 / runAsNonRoot: true` —
  unifi-controller runs with `PUID/PGID 1000` and every file under `/config` is
  owned by `1000:1000`, so it does not need Emby's root treatment
- `home` as a node-disk `emptyDir` with `sizeLimit: 5Gi` for restic's cache, with
  Emby's comment about why this is not a memory tmpfs
- the CiliumNetworkPolicy mirrors Emby's: egress to kube-dns, the Pushgateway,
  the local rest-server, and `world:443` for ntfy

New file `apps/unifi-controller/backup-secrets.enc.yaml` holding the
`backup-env-configuration-unifi` Secret, SOPS-encrypted, with the same keys Emby's
secret uses. Only three values differ from Emby's: `RESTIC_SOURCE`
(`/backup/unifi-controller`), `RESTIC_HOSTNAME` (`unifi`) and the `--tag unifi` in
`RESTIC_ADDITIONAL_BACKUP_PARAMETERS`. Repository, password, ntfy and Pushgateway
settings are copied verbatim — the fleet shares one repository. The file is added
to `apps/unifi-controller/kustomize-secret-generator.yaml`.

Joining the fleet also means three edits outside the app:

- `apps/backup-script/k8s.backup-alerts.yaml` — add
  `or absent(backup_start_timestamp{instance="unifi"})` to `BackupNeverRun` and
  name unifi in its description
- `apps/backup-script/k8s.restic-retentionpolicies.yaml` — add `unifi` to the
  daily forget/prune tag loop. Every per-app secret sets
  `RESTIC_RETENTION_POLICIES_ENABLED: "false"` and defers pruning to this one
  job, so a missing tag means snapshots accumulate forever
- `apps/backup-script/k8s.restore-test.yaml` — add `unifi` to `TAGS` and add a
  verifier branch checking
  `$TARGET/backup/unifi-controller/data/system.properties`, the file the
  controller writes for every site

### 5. Cutover

Two commits, with a manual step in between. All eight apps are down for the
duration of the copy (~2.5 GB, a few minutes).

**Commit A** — prepare:
- add the ten PVC manifests and wire them into the kustomizations
- set `replicas: 0` on all eight Deployments

ArgoCD syncs: pods terminate, PVCs are created and bind.

**Manual step** — run by the user (mutating kubectl is not available to the
agent):
```bash
kubectl apply -f scripts/migrate-hostpath-to-pvc/
kubectl -n downloader get jobs
kubectl -n media get jobs
kubectl -n <namespace> logs job/<name>
```
All eight Jobs must report success before continuing.

**Commit B** — cut over:
- `replicas: 1` on all eight Deployments
- config volumes switched to the PVCs, `strategy: Recreate` added where missing
- nzbhydra2 `/downloads` → `emptyDir`
- downloader backup CronJob switched to PVCs
- new unifi backup CronJob, its encrypted secret, the alert-rule and
  restore-test additions

**After**:
- confirm each app starts and its UI shows the expected library/history
- watch the next downloader backup run (`:50`) and the first unifi run (`:25`)
  and confirm both exit 0
- delete the migration Jobs
- the old hostPath directories are left in place as a fallback and deleted
  manually after a few days

`/vol_raidz1/docker/configs/downloader/lidarr/config` (899M) is a stale leftover
— the running lidarr uses `/vol_raidz1/container-scheduler/downloader-lidarr`.
It is not touched by this work.

## Verification

- `kustomize build apps/downloader/<app> --enable-helm --enable-alpha-plugins --enable-exec`
  succeeds for each changed app, and for `apps/trailarr` and `apps/unifi-controller`
- `sops --config .sops.yaml -d apps/unifi-controller/backup-secrets.enc.yaml`
  decrypts and the rendered Secret is named `backup-env-configuration-unifi`
- `scripts/kubeconform-validate.sh` passes for each changed app
- `pre-commit run --all-files` passes
- after Commit A: all ten PVCs `Bound`, all eight Deployments at 0 pods
- after the Jobs: `du -s` on source and target agree per volume
- after Commit B: all eight pods `Running` and `Ready`, apps reachable through
  their ingress, the downloader backup run exits 0, and the first unifi run
  produces a `unifi` snapshot (`restic snapshots --tag unifi`)

## Rollback

Before Commit B nothing is lost — the hostPath data is untouched and reverting
Commit A restores the previous state.

After Commit B, reverting the commit puts the Deployments back on the hostPath
volumes; the hostPath data is still present but stale by however long the apps
ran on the PVCs. Longhorn's `Retain` policy means the PVs survive a PVC delete,
so the migrated data is recoverable either way.
