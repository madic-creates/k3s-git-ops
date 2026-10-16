# Downloader hostPath → Longhorn PVC Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the config volumes of headphones, lidarr, nzbget, nzbhydra2, radarr, sonarr, trailarr and unifi-controller from node-local `hostPath` directories onto dedicated Longhorn PVCs, without losing data and without breaking the hourly restic backup.

**Architecture:** Ten new ReadWriteOnce Longhorn PVCs, one per config tree. The cutover runs in two ArgoCD-visible commits with a manual copy step in between: first the PVCs are created and all eight Deployments are scaled to zero via Git, then one-off busybox Jobs copy each `hostPath` tree into its PVC with `cp -a`, then the Deployments are switched to the PVCs and scaled back up. The backup CronJob switches from six `hostPath` mounts to the same six PVCs, keeping the `/backup/<app>` mount paths so the encrypted `RESTIC_SOURCE` stays valid.

**Tech Stack:** k3s 1.36, ArgoCD, Kustomize, Longhorn (default StorageClass, 3 replicas, `Retain`), restic, busybox.

**Spec:** `docs/superpowers/specs/2026-07-24-downloader-hostpath-to-pvc-design.md`

---

## Ground rules for this plan

- **Never** run `kubectl edit/patch/apply` against ArgoCD-managed objects. Everything reaches the cluster through Git. The only manual `kubectl apply` in this plan is the one-off migration Jobs, which no ArgoCD Application tracks.
- `git push` and mutating `kubectl` are not available in the agent's shell. Every step that needs them is marked **MANUAL (user)** and the user runs it with the `!` prefix.
- Commits go straight to `main`. No `Co-Authored-By` or "Generated with" trailers.
- ArgoCD Application names for the affected apps: `65-headphones`, `64-downloader-lidarr`, `65-nzbget`, `65-nzbhydra2`, `65-radarr`, `65-sonarr`, `34-trailarr`, `65-unifi-controller`.
- unifi-controller lives in namespace **`media`**, not `downloader`. Its PVC, its migration Job and every `kubectl -n …` command for it use `media`. It has no backup today; Task 6b adds one modelled on Emby's, so it also joins the alerting and restore-test rotation.
- Every Deployment keeps `nodeSelector: node-role/workload: zfs`, including nzbhydra2 and unifi-controller, which would otherwise be free of host storage after the migration. nzbhydra2's PVC is mounted by the shared, node-pinned downloader backup CronJob; unifi-controller is pinned by `hostNetwork: true` and the APs' inform IP. Do not remove either pin as a "cleanup".
- Never paste decrypted secret values into chat or a commit message. Task 6b builds the unifi backup secret by piping SOPS output through `sed`, so no plaintext is ever printed or written into the repo.

## File structure

**Created:**

| File | Responsibility |
|---|---|
| `apps/downloader/headphones/k8s.pvc.yaml` | PVC `longhorn-pvc-headphones-config` |
| `apps/downloader/lidarr/k8s.pvc.yaml` | PVC `longhorn-pvc-lidarr-config` |
| `apps/downloader/nzbget/k8s.pvc.yaml` | PVCs `longhorn-pvc-nzbget-config`, `longhorn-pvc-nzbget-scripts` |
| `apps/downloader/nzbhydra2/k8s.pvc.yaml` | PVC `longhorn-pvc-nzbhydra2-config` |
| `apps/downloader/radarr/k8s.pvc.yaml` | PVCs `longhorn-pvc-radarr-config`, `longhorn-pvc-radarr-trailer-downloader` |
| `apps/downloader/sonarr/k8s.pvc.yaml` | PVC `longhorn-pvc-sonarr-config` |
| `apps/trailarr/k8s.pvc.yaml` | PVC `longhorn-pvc-trailarr-config` |
| `apps/unifi-controller/k8s.pvc.yaml` | PVC `longhorn-pvc-unifi-controller-config` (namespace `media`) |
| `apps/unifi-controller/k8s.backup-cronjob.yaml` | hourly restic CronJob + CiliumNetworkPolicy for unifi |
| `apps/unifi-controller/backup-secrets.enc.yaml` | SOPS-encrypted `backup-env-configuration-unifi` Secret |
| `scripts/migrate-hostpath-to-pvc/*.yaml` | Eight one-off copy Jobs — **never committed**, deleted after the migration |

**Modified:**

| File | Change |
|---|---|
| `apps/downloader/*/kustomization.yaml`, `apps/trailarr/kustomization.yaml`, `apps/unifi-controller/kustomization.yaml` | add `k8s.pvc.yaml` (and for unifi `k8s.backup-cronjob.yaml`) to `resources` |
| `apps/downloader/*/k8s.deployment.yaml`, `apps/trailarr/k8s.deployment.yaml`, `apps/unifi-controller/k8s.deployment.yaml` | `replicas: 0` → copy → `replicas: 1`; config volumes → PVC; `strategy: Recreate` |
| `apps/downloader/nzbget/k8s.backup-cronjob.yaml` | six `hostPath` volumes → six PVCs |
| `apps/unifi-controller/kustomize-secret-generator.yaml` | add `backup-secrets.enc.yaml` |
| `apps/downloader/nzbget/backup-secrets.enc.yaml` | exclude `lost+found` and `.migration-complete` |
| `apps/backup-script/k8s.backup-alerts.yaml` | `BackupNeverRun` learns the `unifi` instance |
| `apps/backup-script/k8s.restore-test.yaml` | `unifi` added to `TAGS` plus a verifier branch |
| `apps/backup-script/k8s.restic-retentionpolicies.yaml` | `unifi` added to the daily forget/prune tag loop |

---

## Task 0: Preflight

**Files:** none

- [ ] **Step 1: Confirm the pending unrelated changes stay out of this work**

Run:
```bash
git status --short
```
Expected: `docs/backup.md` and `apps/minecraft-crossplay/kustomization.yaml` show as modified. These are **unrelated pending changes** and the user has decided they stay untouched.

**`docs/backup.md` is off limits for this plan — no documentation changes go into that file.** Never `git add` either file; every commit below stages explicit paths, never `git add -A` or `git add .`.

- [ ] **Step 2: Record the current data sizes for later comparison**

**MANUAL (user):**
```bash
ssh mne-adm@192.168.1.24 'du -sb \
  /vol_raidz1/docker/configs/downloader/headphones/config \
  /vol_raidz1/container-scheduler/downloader-lidarr \
  /vol_raidz1/docker/configs/downloader/nzbget/config \
  /vol_raidz1/docker/configs/downloader/nzbget/scripts \
  /vol_raidz1/docker/configs/downloader/nzbhydra2/config \
  /vol_raidz1/docker/configs/downloader/radarr/config \
  /vol_raidz1/docker/configs/downloader/Trailer-Downloader-Config \
  /vol_raidz1/docker/configs/downloader/sonarr/config \
  /vol_raidz1/docker/configs/downloader/trailarr/config \
  /vol_raidz1/docker/configs/unify-controller/config'
```
Expected: ten byte counts (note the host directory really is spelled `unify-controller`). Save the output — Task 5 compares against it. `du` will print two `Permission denied` lines for `radarr/config/xdg/.mono/keypairs` and the sonarr equivalent; that is pre-existing and expected, the totals are still comparable because the copy runs as root and the comparison in Task 5 also runs as root.

---

## Task 1: Create the ten PVC manifests

**Files:**
- Create: `apps/downloader/headphones/k8s.pvc.yaml`
- Create: `apps/downloader/lidarr/k8s.pvc.yaml`
- Create: `apps/downloader/nzbget/k8s.pvc.yaml`
- Create: `apps/downloader/nzbhydra2/k8s.pvc.yaml`
- Create: `apps/downloader/radarr/k8s.pvc.yaml`
- Create: `apps/downloader/sonarr/k8s.pvc.yaml`
- Create: `apps/trailarr/k8s.pvc.yaml`
- Create: `apps/unifi-controller/k8s.pvc.yaml`

- [ ] **Step 1: Write `apps/downloader/headphones/k8s.pvc.yaml`**

```yaml
---
# Config volume for headphones. Replaces the hostPath mount on
# /vol_raidz1/docker/configs/downloader/headphones/config.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-headphones-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: headphones
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Gi
  storageClassName: longhorn
  volumeMode: Filesystem
```

- [ ] **Step 2: Write `apps/downloader/lidarr/k8s.pvc.yaml`**

```yaml
---
# Config volume for lidarr. Replaces the hostPath mount on
# /vol_raidz1/container-scheduler/downloader-lidarr.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-lidarr-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: lidarr
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Gi
  storageClassName: longhorn
  volumeMode: Filesystem
```

- [ ] **Step 3: Write `apps/downloader/nzbget/k8s.pvc.yaml`**

```yaml
---
# Config volume for nzbget. Replaces the hostPath mount on
# /vol_raidz1/docker/configs/downloader/nzbget/config.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-nzbget-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: nzbget
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: longhorn
  volumeMode: Filesystem
---
# Post-processing scripts, mounted read-only into the container.
# Replaces the hostPath mount on
# /vol_raidz1/docker/configs/downloader/nzbget/scripts.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-nzbget-scripts
  namespace: downloader
  labels:
    app.kubernetes.io/name: nzbget
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: longhorn
  volumeMode: Filesystem
```

- [ ] **Step 4: Write `apps/downloader/nzbhydra2/k8s.pvc.yaml`**

```yaml
---
# Config volume for nzbhydra2. Replaces the hostPath mount on
# /vol_raidz1/docker/configs/downloader/nzbhydra2/config.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-nzbhydra2-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: nzbhydra2
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Gi
  storageClassName: longhorn
  volumeMode: Filesystem
```

- [ ] **Step 5: Write `apps/downloader/radarr/k8s.pvc.yaml`**

```yaml
---
# Config volume for radarr. Replaces the hostPath mount on
# /vol_raidz1/docker/configs/downloader/radarr/config.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-radarr-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: radarr
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
  storageClassName: longhorn
  volumeMode: Filesystem
---
# settings.ini for the Trailer-Downloader post-processing script.
# Replaces the hostPath mount on
# /vol_raidz1/docker/configs/downloader/Trailer-Downloader-Config.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-radarr-trailer-downloader
  namespace: downloader
  labels:
    app.kubernetes.io/name: radarr
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: longhorn
  volumeMode: Filesystem
```

- [ ] **Step 6: Write `apps/downloader/sonarr/k8s.pvc.yaml`**

```yaml
---
# Config volume for sonarr. Replaces the hostPath mount on
# /vol_raidz1/docker/configs/downloader/sonarr/config.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-sonarr-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: sonarr
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Gi
  storageClassName: longhorn
  volumeMode: Filesystem
```

- [ ] **Step 7: Write `apps/trailarr/k8s.pvc.yaml`**

```yaml
---
# Config volume for trailarr. Replaces the hostPath mount on
# /vol_raidz1/docker/configs/downloader/trailarr/config.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-trailarr-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: trailarr
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Gi
  storageClassName: longhorn
  volumeMode: Filesystem
```

- [ ] **Step 8: Write `apps/unifi-controller/k8s.pvc.yaml`**

Note the namespace: unifi-controller lives in `media`, and the host directory is
spelled `unify-controller`.

```yaml
---
# Config volume for the UniFi controller (MongoDB plus site config).
# Replaces the hostPath mount on
# /vol_raidz1/docker/configs/unify-controller/config.
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-unifi-controller-config
  namespace: media
  labels:
    app.kubernetes.io/name: unifi-controller
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
  storageClassName: longhorn
  volumeMode: Filesystem
```

- [ ] **Step 9: Wire each PVC file into its kustomization**

Add `  - k8s.pvc.yaml` to the `resources` list of each of these files, as the **first** entry after any namespace/deny resources, so PVCs sort ahead of the Deployment:

`apps/downloader/headphones/kustomization.yaml`:
```yaml
resources:
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.headphones.yaml
```

`apps/downloader/lidarr/kustomization.yaml`:
```yaml
resources:
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.downloader-lidarr.yaml
```

`apps/downloader/nzbget/kustomization.yaml`:
```yaml
resources:
  - k8s.namespace.yaml
  - k8s.np.downloader-default-deny.yaml
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.nzbget.yaml
  - k8s.backup-cronjob.yaml
```

`apps/downloader/nzbhydra2/kustomization.yaml`:
```yaml
resources:
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.nzbhydra2.yaml
```

`apps/downloader/radarr/kustomization.yaml`:
```yaml
resources:
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.radarr.yaml
```

`apps/downloader/sonarr/kustomization.yaml`:
```yaml
resources:
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.sonarr.yaml
```

`apps/trailarr/kustomization.yaml`:
```yaml
resources:
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.trailarr.yaml
```

`apps/unifi-controller/kustomization.yaml`:
```yaml
resources:
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.unifi-controller.yaml
```

- [ ] **Step 10: Verify every kustomization still builds and renders the PVCs**

Run:
```bash
for d in apps/downloader/headphones apps/downloader/lidarr apps/downloader/nzbget \
         apps/downloader/nzbhydra2 apps/downloader/radarr apps/downloader/sonarr \
         apps/trailarr apps/unifi-controller; do
  echo "== $d"
  kustomize build "$d" --enable-helm --enable-alpha-plugins --enable-exec \
    | grep -E '^  name: longhorn-pvc-'
done
```
Expected: ten `name: longhorn-pvc-…` lines in total — one each for headphones, lidarr, nzbhydra2, sonarr, trailarr and unifi-controller, two each for nzbget and radarr. No build errors.

- [ ] **Step 11: Schema-validate the changed apps**

Run:
```bash
for d in apps/downloader/headphones apps/downloader/lidarr apps/downloader/nzbget \
         apps/downloader/nzbhydra2 apps/downloader/radarr apps/downloader/sonarr \
         apps/trailarr apps/unifi-controller; do
  scripts/kubeconform-validate.sh "$d" || echo "FAILED: $d"
done
```
Expected: no `FAILED:` lines.

---

## Task 2: Write the eight migration Jobs

**Files:**
- Create: `scripts/migrate-hostpath-to-pvc/README.md`
- Create: `scripts/migrate-hostpath-to-pvc/headphones.yaml`
- Create: `scripts/migrate-hostpath-to-pvc/lidarr.yaml`
- Create: `scripts/migrate-hostpath-to-pvc/nzbget.yaml`
- Create: `scripts/migrate-hostpath-to-pvc/nzbhydra2.yaml`
- Create: `scripts/migrate-hostpath-to-pvc/radarr.yaml`
- Create: `scripts/migrate-hostpath-to-pvc/sonarr.yaml`
- Create: `scripts/migrate-hostpath-to-pvc/trailarr.yaml`
- Create: `scripts/migrate-hostpath-to-pvc/unifi-controller.yaml`

These files live outside `apps/`, so no ArgoCD Application picks them up. They are applied by hand once and deleted afterwards, and they are **never committed** — they only need to exist in the working tree long enough to run. Do not `git add` this directory in any commit.

The copy logic is identical in every Job — the same `migrate()` shell function, called once for single-volume apps and twice for nzbget and radarr. It is written inline in each file rather than shared, because each Job must be applyable on its own:

1. skip the leg and **return** if `${dst}/.migration-complete` exists, so a retry can still finish the legs that never ran. An `exit` here would kill the whole script: in a two-leg Job, a pod that died between leg 1 and leg 2 could then never be retried, because every attempt would stop at leg 1's guard
2. abort if the target holds anything but `lost+found` and carries no marker — that is a half-finished copy from a dead attempt, and merging into it would produce a silently corrupt config
3. `cp -a "${src}/." "${dst}/"` — preserves ownership, modes, timestamps
4. replicate the source directory's own owner and mode onto the PVC root — a fresh Longhorn volume root is `root:root 0755`, but trailarr runs as UID 1000 and lidarr as UID 1001 and both need to create files directly in `/config`
5. `chmod 0755 "${dst}/lost+found"` — ext4 creates it `root:root 0700`; the backup CronJob runs as UID 1000 and `backup.sh` runs under `set -eEu -o pipefail`, so restic's exit code 3 on an unreadable directory would fail every backup run and fire an ntfy alert
6. `touch "${dst}/.migration-complete"` only after everything above succeeded

The container runs as root because the config trees are mixed-ownership, but it drops all capabilities except the three the copy actually needs: `CHOWN` for `cp -a` and the explicit `chown`, `FOWNER` for `chmod` on files owned by another UID, and `DAC_OVERRIDE` for reading mode-0700 directories such as radarr's and sonarr's `config/xdg/.mono/`. Dropping `ALL` without these — as the read-only backup CronJobs do — would make every Job fail.

- [ ] **Step 1: Write `scripts/migrate-hostpath-to-pvc/README.md`**

````markdown
# One-off hostPath → Longhorn PVC migration

These Jobs copy the downloader/trailarr config trees from the node-local
hostPath directories on node04 into their new Longhorn PVCs. They are **not**
managed by ArgoCD — apply them by hand, exactly once, while the Deployments are
scaled to 0.

```bash
# The apps must be at replicas: 0 first (see the plan, Task 3).
kubectl apply -f scripts/migrate-hostpath-to-pvc/

for ns in downloader media; do
  kubectl -n "$ns" get jobs -l app.kubernetes.io/component=migration
  kubectl -n "$ns" logs -l app.kubernetes.io/component=migration --tail=-1
done

# After the cutover is verified:
kubectl delete -f scripts/migrate-hostpath-to-pvc/
```

A Job whose legs all completed is marked and safe to re-apply — completed legs
are skipped. A Job that died mid-copy leaves unmarked data: re-applying will
abort with "holds unmarked data from a failed attempt". Recovery: delete the
failed Job, then clear the destination volume — the simplest way is to delete
the PVC and let ArgoCD recreate it (its Longhorn StorageClass uses `Retain`,
so the released PV must also be cleaned up in the Longhorn UI) — then
re-apply.

The source hostPath directories are mounted read-only and are never modified,
so nothing is ever lost by a failed attempt.
````

- [ ] **Step 2: Write `scripts/migrate-hostpath-to-pvc/headphones.yaml`**

```yaml
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-headphones-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: migrate-headphones-config
    app.kubernetes.io/component: migration
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate-headphones-config
        app.kubernetes.io/component: migration
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      nodeSelector:
        node-role/workload: zfs
      securityContext:
        runAsUser: 0
        runAsGroup: 0
      containers:
        - name: copy
          image: busybox:1.37
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              migrate() {
                src="$1"
                dst="$2"
                echo "==> ${src} -> ${dst}"
                # A finished leg carries a marker. Skip it and keep going so a
                # retry can still complete the legs that did not run yet.
                if [ -f "${dst}/.migration-complete" ]; then
                  echo "already migrated, skipping"
                  return 0
                fi
                # A fresh ext4 volume holds only lost+found. Anything else with
                # no marker is a half-finished copy from a dead attempt.
                if [ -n "$(ls -A "${dst}" | grep -v '^lost+found$' || true)" ]; then
                  echo "target ${dst} holds unmarked data from a failed attempt"
                  echo "clear the volume or recreate the PVC before retrying"
                  exit 1
                fi
                cp -a "${src}/." "${dst}/"
                # The Longhorn volume root is root:root 0755; take the source
                # directory's own owner and mode so non-root PUIDs can write.
                chown "$(stat -c '%u:%g' "${src}")" "${dst}"
                chmod "$(stat -c '%a' "${src}")" "${dst}"
                # ext4 creates lost+found as root:root 0700, which the restic
                # backup (UID 1000) cannot read.
                if [ -d "${dst}/lost+found" ]; then
                  chmod 0755 "${dst}/lost+found"
                fi
                touch "${dst}/.migration-complete"
                echo "==> done: $(du -sb "${dst}" | cut -f1) bytes"
              }
              migrate /src /dst
          volumeMounts:
            - name: src
              mountPath: /src
              readOnly: true
            - name: dst
              mountPath: /dst
          # Root is needed to chown/chmod to arbitrary UIDs and to read
          # config files with restrictive modes, but nothing beyond the
          # three capabilities that requires.
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
              add:
                - CHOWN
                - DAC_OVERRIDE
                - FOWNER
            privileged: false
      volumes:
        - name: src
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/headphones/config
            type: Directory
        - name: dst
          persistentVolumeClaim:
            claimName: longhorn-pvc-headphones-config
```

- [ ] **Step 3: Write `scripts/migrate-hostpath-to-pvc/lidarr.yaml`**

```yaml
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-lidarr-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: migrate-lidarr-config
    app.kubernetes.io/component: migration
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate-lidarr-config
        app.kubernetes.io/component: migration
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      nodeSelector:
        node-role/workload: zfs
      securityContext:
        runAsUser: 0
        runAsGroup: 0
      containers:
        - name: copy
          image: busybox:1.37
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              migrate() {
                src="$1"
                dst="$2"
                echo "==> ${src} -> ${dst}"
                # A finished leg carries a marker. Skip it and keep going so a
                # retry can still complete the legs that did not run yet.
                if [ -f "${dst}/.migration-complete" ]; then
                  echo "already migrated, skipping"
                  return 0
                fi
                # A fresh ext4 volume holds only lost+found. Anything else with
                # no marker is a half-finished copy from a dead attempt.
                if [ -n "$(ls -A "${dst}" | grep -v '^lost+found$' || true)" ]; then
                  echo "target ${dst} holds unmarked data from a failed attempt"
                  echo "clear the volume or recreate the PVC before retrying"
                  exit 1
                fi
                cp -a "${src}/." "${dst}/"
                # The Longhorn volume root is root:root 0755; take the source
                # directory's own owner and mode so non-root PUIDs can write.
                chown "$(stat -c '%u:%g' "${src}")" "${dst}"
                chmod "$(stat -c '%a' "${src}")" "${dst}"
                # ext4 creates lost+found as root:root 0700, which the restic
                # backup (UID 1000) cannot read.
                if [ -d "${dst}/lost+found" ]; then
                  chmod 0755 "${dst}/lost+found"
                fi
                touch "${dst}/.migration-complete"
                echo "==> done: $(du -sb "${dst}" | cut -f1) bytes"
              }
              migrate /src /dst
          volumeMounts:
            - name: src
              mountPath: /src
              readOnly: true
            - name: dst
              mountPath: /dst
          # Root is needed to chown/chmod to arbitrary UIDs and to read
          # config files with restrictive modes, but nothing beyond the
          # three capabilities that requires.
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
              add:
                - CHOWN
                - DAC_OVERRIDE
                - FOWNER
            privileged: false
      volumes:
        - name: src
          hostPath:
            path: /vol_raidz1/container-scheduler/downloader-lidarr
            type: Directory
        - name: dst
          persistentVolumeClaim:
            claimName: longhorn-pvc-lidarr-config
```

- [ ] **Step 4: Write `scripts/migrate-hostpath-to-pvc/nzbget.yaml`**

This Job copies two trees, so the copy logic sits in a shell function called twice.

```yaml
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-nzbget-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: migrate-nzbget-config
    app.kubernetes.io/component: migration
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate-nzbget-config
        app.kubernetes.io/component: migration
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      nodeSelector:
        node-role/workload: zfs
      securityContext:
        runAsUser: 0
        runAsGroup: 0
      containers:
        - name: copy
          image: busybox:1.37
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              migrate() {
                src="$1"
                dst="$2"
                echo "==> ${src} -> ${dst}"
                # A finished leg carries a marker. Skip it and keep going so a
                # retry can still complete the legs that did not run yet.
                if [ -f "${dst}/.migration-complete" ]; then
                  echo "already migrated, skipping"
                  return 0
                fi
                # A fresh ext4 volume holds only lost+found. Anything else with
                # no marker is a half-finished copy from a dead attempt.
                if [ -n "$(ls -A "${dst}" | grep -v '^lost+found$' || true)" ]; then
                  echo "target ${dst} holds unmarked data from a failed attempt"
                  echo "clear the volume or recreate the PVC before retrying"
                  exit 1
                fi
                cp -a "${src}/." "${dst}/"
                # The Longhorn volume root is root:root 0755; take the source
                # directory's own owner and mode so non-root PUIDs can write.
                chown "$(stat -c '%u:%g' "${src}")" "${dst}"
                chmod "$(stat -c '%a' "${src}")" "${dst}"
                # ext4 creates lost+found as root:root 0700, which the restic
                # backup (UID 1000) cannot read.
                if [ -d "${dst}/lost+found" ]; then
                  chmod 0755 "${dst}/lost+found"
                fi
                touch "${dst}/.migration-complete"
                echo "==> done: $(du -sb "${dst}" | cut -f1) bytes"
              }
              migrate /src/config /dst/config
              migrate /src/scripts /dst/scripts
          volumeMounts:
            - name: src-config
              mountPath: /src/config
              readOnly: true
            - name: src-scripts
              mountPath: /src/scripts
              readOnly: true
            - name: dst-config
              mountPath: /dst/config
            - name: dst-scripts
              mountPath: /dst/scripts
          # Root is needed to chown/chmod to arbitrary UIDs and to read
          # config files with restrictive modes, but nothing beyond the
          # three capabilities that requires.
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
              add:
                - CHOWN
                - DAC_OVERRIDE
                - FOWNER
            privileged: false
      volumes:
        - name: src-config
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/nzbget/config
            type: Directory
        - name: src-scripts
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/nzbget/scripts
            type: Directory
        - name: dst-config
          persistentVolumeClaim:
            claimName: longhorn-pvc-nzbget-config
        - name: dst-scripts
          persistentVolumeClaim:
            claimName: longhorn-pvc-nzbget-scripts
```

- [ ] **Step 5: Write `scripts/migrate-hostpath-to-pvc/nzbhydra2.yaml`**

```yaml
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-nzbhydra2-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: migrate-nzbhydra2-config
    app.kubernetes.io/component: migration
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate-nzbhydra2-config
        app.kubernetes.io/component: migration
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      nodeSelector:
        node-role/workload: zfs
      securityContext:
        runAsUser: 0
        runAsGroup: 0
      containers:
        - name: copy
          image: busybox:1.37
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              migrate() {
                src="$1"
                dst="$2"
                echo "==> ${src} -> ${dst}"
                # A finished leg carries a marker. Skip it and keep going so a
                # retry can still complete the legs that did not run yet.
                if [ -f "${dst}/.migration-complete" ]; then
                  echo "already migrated, skipping"
                  return 0
                fi
                # A fresh ext4 volume holds only lost+found. Anything else with
                # no marker is a half-finished copy from a dead attempt.
                if [ -n "$(ls -A "${dst}" | grep -v '^lost+found$' || true)" ]; then
                  echo "target ${dst} holds unmarked data from a failed attempt"
                  echo "clear the volume or recreate the PVC before retrying"
                  exit 1
                fi
                cp -a "${src}/." "${dst}/"
                # The Longhorn volume root is root:root 0755; take the source
                # directory's own owner and mode so non-root PUIDs can write.
                chown "$(stat -c '%u:%g' "${src}")" "${dst}"
                chmod "$(stat -c '%a' "${src}")" "${dst}"
                # ext4 creates lost+found as root:root 0700, which the restic
                # backup (UID 1000) cannot read.
                if [ -d "${dst}/lost+found" ]; then
                  chmod 0755 "${dst}/lost+found"
                fi
                touch "${dst}/.migration-complete"
                echo "==> done: $(du -sb "${dst}" | cut -f1) bytes"
              }
              migrate /src /dst
          volumeMounts:
            - name: src
              mountPath: /src
              readOnly: true
            - name: dst
              mountPath: /dst
          # Root is needed to chown/chmod to arbitrary UIDs and to read
          # config files with restrictive modes, but nothing beyond the
          # three capabilities that requires.
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
              add:
                - CHOWN
                - DAC_OVERRIDE
                - FOWNER
            privileged: false
      volumes:
        - name: src
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/nzbhydra2/config
            type: Directory
        - name: dst
          persistentVolumeClaim:
            claimName: longhorn-pvc-nzbhydra2-config
```

- [ ] **Step 6: Write `scripts/migrate-hostpath-to-pvc/radarr.yaml`**

```yaml
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-radarr-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: migrate-radarr-config
    app.kubernetes.io/component: migration
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate-radarr-config
        app.kubernetes.io/component: migration
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      nodeSelector:
        node-role/workload: zfs
      securityContext:
        runAsUser: 0
        runAsGroup: 0
      containers:
        - name: copy
          image: busybox:1.37
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              migrate() {
                src="$1"
                dst="$2"
                echo "==> ${src} -> ${dst}"
                # A finished leg carries a marker. Skip it and keep going so a
                # retry can still complete the legs that did not run yet.
                if [ -f "${dst}/.migration-complete" ]; then
                  echo "already migrated, skipping"
                  return 0
                fi
                # A fresh ext4 volume holds only lost+found. Anything else with
                # no marker is a half-finished copy from a dead attempt.
                if [ -n "$(ls -A "${dst}" | grep -v '^lost+found$' || true)" ]; then
                  echo "target ${dst} holds unmarked data from a failed attempt"
                  echo "clear the volume or recreate the PVC before retrying"
                  exit 1
                fi
                cp -a "${src}/." "${dst}/"
                # The Longhorn volume root is root:root 0755; take the source
                # directory's own owner and mode so non-root PUIDs can write.
                chown "$(stat -c '%u:%g' "${src}")" "${dst}"
                chmod "$(stat -c '%a' "${src}")" "${dst}"
                # ext4 creates lost+found as root:root 0700, which the restic
                # backup (UID 1000) cannot read.
                if [ -d "${dst}/lost+found" ]; then
                  chmod 0755 "${dst}/lost+found"
                fi
                touch "${dst}/.migration-complete"
                echo "==> done: $(du -sb "${dst}" | cut -f1) bytes"
              }
              migrate /src/config /dst/config
              migrate /src/trailer /dst/trailer
          volumeMounts:
            - name: src-config
              mountPath: /src/config
              readOnly: true
            - name: src-trailer
              mountPath: /src/trailer
              readOnly: true
            - name: dst-config
              mountPath: /dst/config
            - name: dst-trailer
              mountPath: /dst/trailer
          # Root is needed to chown/chmod to arbitrary UIDs and to read
          # config files with restrictive modes, but nothing beyond the
          # three capabilities that requires.
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
              add:
                - CHOWN
                - DAC_OVERRIDE
                - FOWNER
            privileged: false
      volumes:
        - name: src-config
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/radarr/config
            type: Directory
        - name: src-trailer
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/Trailer-Downloader-Config
            type: Directory
        - name: dst-config
          persistentVolumeClaim:
            claimName: longhorn-pvc-radarr-config
        - name: dst-trailer
          persistentVolumeClaim:
            claimName: longhorn-pvc-radarr-trailer-downloader
```

- [ ] **Step 7: Write `scripts/migrate-hostpath-to-pvc/sonarr.yaml`**

```yaml
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-sonarr-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: migrate-sonarr-config
    app.kubernetes.io/component: migration
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate-sonarr-config
        app.kubernetes.io/component: migration
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      nodeSelector:
        node-role/workload: zfs
      securityContext:
        runAsUser: 0
        runAsGroup: 0
      containers:
        - name: copy
          image: busybox:1.37
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              migrate() {
                src="$1"
                dst="$2"
                echo "==> ${src} -> ${dst}"
                # A finished leg carries a marker. Skip it and keep going so a
                # retry can still complete the legs that did not run yet.
                if [ -f "${dst}/.migration-complete" ]; then
                  echo "already migrated, skipping"
                  return 0
                fi
                # A fresh ext4 volume holds only lost+found. Anything else with
                # no marker is a half-finished copy from a dead attempt.
                if [ -n "$(ls -A "${dst}" | grep -v '^lost+found$' || true)" ]; then
                  echo "target ${dst} holds unmarked data from a failed attempt"
                  echo "clear the volume or recreate the PVC before retrying"
                  exit 1
                fi
                cp -a "${src}/." "${dst}/"
                # The Longhorn volume root is root:root 0755; take the source
                # directory's own owner and mode so non-root PUIDs can write.
                chown "$(stat -c '%u:%g' "${src}")" "${dst}"
                chmod "$(stat -c '%a' "${src}")" "${dst}"
                # ext4 creates lost+found as root:root 0700, which the restic
                # backup (UID 1000) cannot read.
                if [ -d "${dst}/lost+found" ]; then
                  chmod 0755 "${dst}/lost+found"
                fi
                touch "${dst}/.migration-complete"
                echo "==> done: $(du -sb "${dst}" | cut -f1) bytes"
              }
              migrate /src /dst
          volumeMounts:
            - name: src
              mountPath: /src
              readOnly: true
            - name: dst
              mountPath: /dst
          # Root is needed to chown/chmod to arbitrary UIDs and to read
          # config files with restrictive modes, but nothing beyond the
          # three capabilities that requires.
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
              add:
                - CHOWN
                - DAC_OVERRIDE
                - FOWNER
            privileged: false
      volumes:
        - name: src
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/sonarr/config
            type: Directory
        - name: dst
          persistentVolumeClaim:
            claimName: longhorn-pvc-sonarr-config
```

- [ ] **Step 8: Write `scripts/migrate-hostpath-to-pvc/trailarr.yaml`**

```yaml
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-trailarr-config
  namespace: downloader
  labels:
    app.kubernetes.io/name: migrate-trailarr-config
    app.kubernetes.io/component: migration
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate-trailarr-config
        app.kubernetes.io/component: migration
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      nodeSelector:
        node-role/workload: zfs
      securityContext:
        runAsUser: 0
        runAsGroup: 0
      containers:
        - name: copy
          image: busybox:1.37
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              migrate() {
                src="$1"
                dst="$2"
                echo "==> ${src} -> ${dst}"
                # A finished leg carries a marker. Skip it and keep going so a
                # retry can still complete the legs that did not run yet.
                if [ -f "${dst}/.migration-complete" ]; then
                  echo "already migrated, skipping"
                  return 0
                fi
                # A fresh ext4 volume holds only lost+found. Anything else with
                # no marker is a half-finished copy from a dead attempt.
                if [ -n "$(ls -A "${dst}" | grep -v '^lost+found$' || true)" ]; then
                  echo "target ${dst} holds unmarked data from a failed attempt"
                  echo "clear the volume or recreate the PVC before retrying"
                  exit 1
                fi
                cp -a "${src}/." "${dst}/"
                # The Longhorn volume root is root:root 0755; take the source
                # directory's own owner and mode so non-root PUIDs can write.
                chown "$(stat -c '%u:%g' "${src}")" "${dst}"
                chmod "$(stat -c '%a' "${src}")" "${dst}"
                # ext4 creates lost+found as root:root 0700, which the restic
                # backup (UID 1000) cannot read.
                if [ -d "${dst}/lost+found" ]; then
                  chmod 0755 "${dst}/lost+found"
                fi
                touch "${dst}/.migration-complete"
                echo "==> done: $(du -sb "${dst}" | cut -f1) bytes"
              }
              migrate /src /dst
          volumeMounts:
            - name: src
              mountPath: /src
              readOnly: true
            - name: dst
              mountPath: /dst
          # Root is needed to chown/chmod to arbitrary UIDs and to read
          # config files with restrictive modes, but nothing beyond the
          # three capabilities that requires.
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
              add:
                - CHOWN
                - DAC_OVERRIDE
                - FOWNER
            privileged: false
      volumes:
        - name: src
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/trailarr/config
            type: Directory
        - name: dst
          persistentVolumeClaim:
            claimName: longhorn-pvc-trailarr-config
```

- [ ] **Step 9: Write `scripts/migrate-hostpath-to-pvc/unifi-controller.yaml`**

Same copy logic as the others, but in namespace `media` — and note the host
directory is spelled `unify-controller` while the app is `unifi-controller`.

```yaml
---
apiVersion: batch/v1
kind: Job
metadata:
  name: migrate-unifi-controller-config
  namespace: media
  labels:
    app.kubernetes.io/name: migrate-unifi-controller-config
    app.kubernetes.io/component: migration
spec:
  backoffLimit: 2
  template:
    metadata:
      labels:
        app.kubernetes.io/name: migrate-unifi-controller-config
        app.kubernetes.io/component: migration
    spec:
      restartPolicy: Never
      automountServiceAccountToken: false
      nodeSelector:
        node-role/workload: zfs
      securityContext:
        runAsUser: 0
        runAsGroup: 0
      containers:
        - name: copy
          image: busybox:1.37
          command:
            - /bin/sh
            - -c
            - |
              set -eu
              migrate() {
                src="$1"
                dst="$2"
                echo "==> ${src} -> ${dst}"
                # A finished leg carries a marker. Skip it and keep going so a
                # retry can still complete the legs that did not run yet.
                if [ -f "${dst}/.migration-complete" ]; then
                  echo "already migrated, skipping"
                  return 0
                fi
                # A fresh ext4 volume holds only lost+found. Anything else with
                # no marker is a half-finished copy from a dead attempt.
                if [ -n "$(ls -A "${dst}" | grep -v '^lost+found$' || true)" ]; then
                  echo "target ${dst} holds unmarked data from a failed attempt"
                  echo "clear the volume or recreate the PVC before retrying"
                  exit 1
                fi
                cp -a "${src}/." "${dst}/"
                # The Longhorn volume root is root:root 0755; take the source
                # directory's own owner and mode so non-root PUIDs can write.
                chown "$(stat -c '%u:%g' "${src}")" "${dst}"
                chmod "$(stat -c '%a' "${src}")" "${dst}"
                # ext4 creates lost+found as root:root 0700, which the restic
                # backup (UID 1000) cannot read.
                if [ -d "${dst}/lost+found" ]; then
                  chmod 0755 "${dst}/lost+found"
                fi
                touch "${dst}/.migration-complete"
                echo "==> done: $(du -sb "${dst}" | cut -f1) bytes"
              }
              migrate /src /dst
          volumeMounts:
            - name: src
              mountPath: /src
              readOnly: true
            - name: dst
              mountPath: /dst
          # Root is needed to chown/chmod to arbitrary UIDs and to read
          # config files with restrictive modes, but nothing beyond the
          # three capabilities that requires.
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
              add:
                - CHOWN
                - DAC_OVERRIDE
                - FOWNER
            privileged: false
      volumes:
        - name: src
          hostPath:
            path: /vol_raidz1/docker/configs/unify-controller/config
            type: Directory
        - name: dst
          persistentVolumeClaim:
            claimName: longhorn-pvc-unifi-controller-config
```

- [ ] **Step 10: Validate the Job manifests**

Run:
```bash
kubectl apply --dry-run=client -f scripts/migrate-hostpath-to-pvc/
```
Expected: eight `job.batch/migrate-… created (dry run)` lines, no errors.

---

## Task 3: Commit A — scale to zero and create the PVCs

**Files:**
- Modify: `apps/downloader/headphones/k8s.deployment.yaml:10`
- Modify: `apps/downloader/lidarr/k8s.deployment.yaml:10`
- Modify: `apps/downloader/nzbget/k8s.deployment.yaml:10`
- Modify: `apps/downloader/nzbhydra2/k8s.deployment.yaml:10`
- Modify: `apps/downloader/radarr/k8s.deployment.yaml:10`
- Modify: `apps/downloader/sonarr/k8s.deployment.yaml:10`
- Modify: `apps/trailarr/k8s.deployment.yaml:10`
- Modify: `apps/unifi-controller/k8s.deployment.yaml:11`

- [ ] **Step 1: Set `replicas: 0` in all eight Deployments**

In each file, change the single line

```yaml
  replicas: 1
```

to

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
```

- [ ] **Step 2: Verify the rendered replica counts**

Run:
```bash
for d in apps/downloader/headphones apps/downloader/lidarr apps/downloader/nzbget \
         apps/downloader/nzbhydra2 apps/downloader/radarr apps/downloader/sonarr \
         apps/trailarr apps/unifi-controller; do
  printf '%s: ' "$d"
  kustomize build "$d" --enable-helm --enable-alpha-plugins --enable-exec \
    | grep -E '^  replicas:' | tr -d '\n'
  echo
done
```
Expected: `replicas: 0` for all eight.

- [ ] **Step 3: Run the pre-commit hooks**

Run:
```bash
pre-commit run --all-files
```
Expected: all hooks pass. If `kubeconform` reports failures for apps this plan does not touch, note them and continue; failures in the eight touched apps must be fixed.

- [ ] **Step 4: Commit**

```bash
git add apps/downloader/headphones/k8s.pvc.yaml \
        apps/downloader/lidarr/k8s.pvc.yaml \
        apps/downloader/nzbget/k8s.pvc.yaml \
        apps/downloader/nzbhydra2/k8s.pvc.yaml \
        apps/downloader/radarr/k8s.pvc.yaml \
        apps/downloader/sonarr/k8s.pvc.yaml \
        apps/trailarr/k8s.pvc.yaml \
        apps/unifi-controller/k8s.pvc.yaml \
        apps/downloader/*/kustomization.yaml \
        apps/trailarr/kustomization.yaml \
        apps/unifi-controller/kustomization.yaml \
        apps/downloader/*/k8s.deployment.yaml \
        apps/trailarr/k8s.deployment.yaml \
        apps/unifi-controller/k8s.deployment.yaml
git commit -m "feat(downloader,unifi): add Longhorn config PVCs and scale apps to zero for migration"
```

- [ ] **Step 5: MANUAL (user) — push and let ArgoCD sync**

```bash
git push
```

Then wait for the sync (or force it):
```bash
for a in 65-headphones 64-downloader-lidarr 65-nzbget 65-nzbhydra2 65-radarr 65-sonarr \
         34-trailarr 65-unifi-controller; do
  kubectl annotate -n argocd application "$a" argocd.argoproj.io/refresh=hard --overwrite
done
```

- [ ] **Step 6: MANUAL (user) — verify the cluster reached the expected state**

```bash
kubectl -n downloader get pvc | grep longhorn-pvc-
kubectl -n media get pvc | grep longhorn-pvc-unifi
kubectl -n downloader get deploy
kubectl -n media get deploy unifi-controller
kubectl -n downloader get pods
kubectl -n media get pods -l app.kubernetes.io/name=unifi-controller
```
Expected: nine PVCs `Bound` in `downloader` and one in `media`; all eight Deployments show `0/0`; no headphones/lidarr/nzbget/nzbhydra2/radarr/sonarr/trailarr/unifi-controller pods left.

**Do not continue until every app pod is gone** — copying a live SQLite database is exactly the failure this ordering avoids.

---

## Task 4: Run the migration Jobs

**Files:** none (cluster operation)

- [ ] **Step 1: MANUAL (user) — apply the Jobs**

```bash
kubectl apply -f scripts/migrate-hostpath-to-pvc/
```
Expected: eight `job.batch/migrate-… created`.

- [ ] **Step 2: MANUAL (user) — wait for completion**

```bash
for ns in downloader media; do
  kubectl -n "$ns" wait --for=condition=complete --timeout=900s \
    job -l app.kubernetes.io/component=migration
done
```
Expected: eight `condition met` lines. radarr (1.2G) is the slowest.

- [ ] **Step 3: MANUAL (user) — read the logs**

```bash
for ns in downloader media; do
  kubectl -n "$ns" logs -l app.kubernetes.io/component=migration --tail=-1
done
```
Expected: one `==> done: <bytes> bytes` line per copied tree (ten in total). No `target … is not empty` lines.

- [ ] **Step 4: MANUAL (user) — compare the byte counts against Task 0**

The `done:` byte counts must match the `du -sb` values recorded in Task 0 Step 2, within the size of `lost+found` (16 KiB per volume). A materially smaller number means the copy was incomplete — stop and investigate before continuing.

If a Job failed, delete the failed Job, fix the cause, wipe the target by deleting and letting ArgoCD recreate the PVC, and re-apply. The hostPath sources are untouched, so nothing is lost.

---

## Task 5: Commit B — switch the Deployments to the PVCs

**Files:**
- Modify: `apps/downloader/headphones/k8s.deployment.yaml`
- Modify: `apps/downloader/lidarr/k8s.deployment.yaml`
- Modify: `apps/downloader/nzbget/k8s.deployment.yaml`
- Modify: `apps/downloader/nzbhydra2/k8s.deployment.yaml`
- Modify: `apps/downloader/radarr/k8s.deployment.yaml`
- Modify: `apps/downloader/sonarr/k8s.deployment.yaml`
- Modify: `apps/trailarr/k8s.deployment.yaml`
- Modify: `apps/unifi-controller/k8s.deployment.yaml`

- [ ] **Step 1: headphones — restore replicas, add `Recreate`, switch the config volume**

In `apps/downloader/headphones/k8s.deployment.yaml`, replace

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
  revisionHistoryLimit: 3
```

with

```yaml
  replicas: 1
  revisionHistoryLimit: 3
  # The config volume is ReadWriteOnce; a rolling update would let a second
  # pod attach it and write the same database.
  strategy:
    type: Recreate
```

and replace the `config` volume

```yaml
        - name: config
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/headphones/config
            type: Directory
```

with

```yaml
        - name: config
          persistentVolumeClaim:
            claimName: longhorn-pvc-headphones-config
```

Leave the `video`, `music`, `photo` and `downloads` hostPath volumes untouched.

- [ ] **Step 2: lidarr — restore replicas and switch the config volume**

In `apps/downloader/lidarr/k8s.deployment.yaml`, replace

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
```

with

```yaml
  replicas: 1
```

(lidarr already has `strategy: {type: Recreate}` — leave it as is) and replace

```yaml
        - name: config-vol
          hostPath:
            path: /vol_raidz1/container-scheduler/downloader-lidarr
            type: DirectoryOrCreate
```

with

```yaml
        - name: config-vol
          persistentVolumeClaim:
            claimName: longhorn-pvc-lidarr-config
```

Leave `music-vol` untouched.

- [ ] **Step 3: nzbget — restore replicas, add `Recreate`, switch config and scripts**

In `apps/downloader/nzbget/k8s.deployment.yaml`, replace

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
  revisionHistoryLimit: 3
```

with

```yaml
  replicas: 1
  revisionHistoryLimit: 3
  # The config volume is ReadWriteOnce; a rolling update would let a second
  # pod attach it and write the same database.
  strategy:
    type: Recreate
```

and replace

```yaml
        - name: config
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/nzbget/config
            type: Directory
        - name: scripts
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/nzbget/scripts
            type: Directory
```

with

```yaml
        - name: config
          persistentVolumeClaim:
            claimName: longhorn-pvc-nzbget-config
        - name: scripts
          persistentVolumeClaim:
            claimName: longhorn-pvc-nzbget-scripts
```

The `scripts` volumeMount keeps `readOnly: true`. Leave the media hostPaths untouched.

- [ ] **Step 4: nzbhydra2 — restore replicas, add `Recreate`, switch config, downloads to emptyDir**

In `apps/downloader/nzbhydra2/k8s.deployment.yaml`, replace

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
  revisionHistoryLimit: 3
```

with

```yaml
  replicas: 1
  revisionHistoryLimit: 3
  # The config volume is ReadWriteOnce; a rolling update would let a second
  # pod attach it and write the same database.
  strategy:
    type: Recreate
```

and replace the whole `volumes` block

```yaml
      volumes:
        - name: config
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/nzbhydra2/config
            type: Directory
        - name: downloads
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/nzbhydra2/downloads
            type: Directory
```

with

```yaml
      volumes:
        - name: config
          persistentVolumeClaim:
            claimName: longhorn-pvc-nzbhydra2-config
        # nzbhydra2 never stores anything durable here; the old hostPath
        # directory was empty.
        - name: downloads
          emptyDir: {}
```

- [ ] **Step 5: radarr — restore replicas, add `Recreate`, switch config and trailer config**

In `apps/downloader/radarr/k8s.deployment.yaml`, replace

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
  revisionHistoryLimit: 3
```

with

```yaml
  replicas: 1
  revisionHistoryLimit: 3
  # The config volume is ReadWriteOnce; a rolling update would let a second
  # pod attach it and write the same database.
  strategy:
    type: Recreate
```

and replace

```yaml
        - name: config
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/radarr/config
            type: Directory
```

with

```yaml
        - name: config
          persistentVolumeClaim:
            claimName: longhorn-pvc-radarr-config
```

and replace

```yaml
        - name: trailer-config
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/Trailer-Downloader-Config
            type: Directory
```

with

```yaml
        - name: trailer-config
          persistentVolumeClaim:
            claimName: longhorn-pvc-radarr-trailer-downloader
```

The `trailer-config` volumeMount keeps `subPath: settings.ini` and `readOnly: true`. Leave the media hostPaths untouched.

- [ ] **Step 6: sonarr — restore replicas, add `Recreate`, switch the config volume**

In `apps/downloader/sonarr/k8s.deployment.yaml`, replace

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
  revisionHistoryLimit: 3
```

with

```yaml
  replicas: 1
  revisionHistoryLimit: 3
  # The config volume is ReadWriteOnce; a rolling update would let a second
  # pod attach it and write the same database.
  strategy:
    type: Recreate
```

and replace

```yaml
        - name: config
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/sonarr/config
            type: Directory
```

with

```yaml
        - name: config
          persistentVolumeClaim:
            claimName: longhorn-pvc-sonarr-config
```

Leave the media hostPaths untouched.

- [ ] **Step 7: trailarr — restore replicas and switch the config volume**

In `apps/trailarr/k8s.deployment.yaml`, replace

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
```

with

```yaml
  replicas: 1
```

(trailarr already has `strategy: {type: Recreate}` — leave it as is) and replace

```yaml
        - name: config-vol
          hostPath:
            path: /vol_raidz1/docker/configs/downloader/trailarr/config
            type: DirectoryOrCreate
```

with

```yaml
        - name: config-vol
          persistentVolumeClaim:
            claimName: longhorn-pvc-trailarr-config
```

Leave `video-vol` untouched.

- [ ] **Step 8: unifi-controller — restore replicas, add `Recreate`, switch the config volume**

In `apps/unifi-controller/k8s.deployment.yaml`, replace

```yaml
  # Temporarily 0 for the hostPath -> PVC config migration.
  replicas: 0
  revisionHistoryLimit: 3
```

with

```yaml
  replicas: 1
  revisionHistoryLimit: 3
  # The config volume is ReadWriteOnce; a rolling update would let a second
  # pod attach it and write the same MongoDB data directory.
  strategy:
    type: Recreate
```

and replace the whole `volumes` block

```yaml
      volumes:
        - name: config
          hostPath:
            path: /vol_raidz1/docker/configs/unify-controller/config
            type: Directory
```

with

```yaml
      volumes:
        - name: config
          persistentVolumeClaim:
            claimName: longhorn-pvc-unifi-controller-config
```

`hostNetwork: true` and the `nodeSelector` stay as they are.

- [ ] **Step 9: Verify no config hostPath remains**

Run:
```bash
grep -rn "docker/configs/downloader\|container-scheduler/downloader\|configs/unify-controller" \
  apps/downloader/*/k8s.deployment.yaml apps/trailarr/k8s.deployment.yaml \
  apps/unifi-controller/k8s.deployment.yaml
```
Expected: **no output**.

- [ ] **Step 10: Verify the rendered volumes**

Run:
```bash
for d in apps/downloader/headphones apps/downloader/lidarr apps/downloader/nzbget \
         apps/downloader/nzbhydra2 apps/downloader/radarr apps/downloader/sonarr \
         apps/trailarr apps/unifi-controller; do
  echo "== $d"
  kustomize build "$d" --enable-helm --enable-alpha-plugins --enable-exec \
    | grep -E 'claimName:|replicas:|type: Recreate'
done
```
Expected: `replicas: 1` and `type: Recreate` for every app, and ten `claimName: longhorn-pvc-…` lines in total.

---

## Task 6: Switch the backup CronJob to the PVCs

**Files:**
- Modify: `apps/downloader/nzbget/k8s.backup-cronjob.yaml`

- [ ] **Step 1: Update the file header comment**

Replace

```yaml
# Hourly backup of *arr-stack and downloader configs via restic.
# All paths live on the ZFS host's local filesystem and are mounted
# read-only via hostPath. Pinned to the ZFS node via nodeSelector.
# Headphones and Epic Games are intentionally excluded — they are
# not mounted, so restic never sees them.
```

with

```yaml
# Hourly backup of *arr-stack and downloader configs via restic.
# Every config tree is a ReadWriteOnce Longhorn PVC, mounted read-only.
# The nodeSelector below pins this Job to the same node as the workloads,
# which is what allows it to attach volumes those pods already hold.
# Headphones and Epic Games are intentionally excluded — they are
# not mounted, so restic never sees them.
```

- [ ] **Step 2: Replace the six config volumes**

Replace

```yaml
            - name: sonarr-config
              hostPath:
                path: /vol_raidz1/docker/configs/downloader/sonarr/config
                type: Directory
            - name: radarr-config
              hostPath:
                path: /vol_raidz1/docker/configs/downloader/radarr/config
                type: Directory
            - name: nzbget-config
              hostPath:
                path: /vol_raidz1/docker/configs/downloader/nzbget/config
                type: Directory
            - name: nzbhydra2-config
              hostPath:
                path: /vol_raidz1/docker/configs/downloader/nzbhydra2/config
                type: Directory
            - name: trailarr-config
              hostPath:
                path: /vol_raidz1/docker/configs/downloader/trailarr/config
                type: Directory
            - name: lidarr-config
              hostPath:
                path: /vol_raidz1/container-scheduler/downloader-lidarr
                type: Directory
```

with

```yaml
            - name: sonarr-config
              persistentVolumeClaim:
                claimName: longhorn-pvc-sonarr-config
            - name: radarr-config
              persistentVolumeClaim:
                claimName: longhorn-pvc-radarr-config
            - name: nzbget-config
              persistentVolumeClaim:
                claimName: longhorn-pvc-nzbget-config
            - name: nzbhydra2-config
              persistentVolumeClaim:
                claimName: longhorn-pvc-nzbhydra2-config
            - name: trailarr-config
              persistentVolumeClaim:
                claimName: longhorn-pvc-trailarr-config
            - name: lidarr-config
              persistentVolumeClaim:
                claimName: longhorn-pvc-lidarr-config
```

The `volumeMounts` block is **not** changed: the mount paths stay `/backup/sonarr`, `/backup/radarr`, `/backup/nzbget`, `/backup/nzbhydra2`, `/backup/trailarr`, `/backup/lidarr` with `readOnly: true`, which is exactly what the encrypted `RESTIC_SOURCE` lists. The `securityContext` (`runAsUser: 1000`, `runAsGroup: 1002`, `runAsNonRoot: true`) also stays — `cp -a` preserved ownership and modes, so the group-bit access argument in the existing comment still holds.

- [ ] **Step 2b: Add `lost+found` and the migration marker to the exclude list**

The downloader secret excludes `asp`, `.mono`, `.bash_history` and `.__smb*` but
not `lost+found`. On a hostPath there was none; on an ext4-backed Longhorn volume
there always is, and ext4 creates it `root:root 0700`. The migration Jobs chmod
it to 0755, but that fix lives outside Git — if any of the six PVCs is ever
recreated (restore, re-provision), the unreadable `lost+found` comes back and
restic exits 3, failing the run and firing an ntfy alert. Excluding it removes
the dependency entirely. The `.migration-complete` markers are excluded in the
same pass so they never enter a snapshot.

Use Workflow B from `CLAUDE.md` — the `EDITOR=sed` form does not match this file:

```bash
T=/tmp/dl-backup.enc.yaml
sops --config .sops.yaml -d apps/downloader/nzbget/backup-secrets.enc.yaml > "$T"
sed -i 's|\(RESTIC_ADDITIONAL_BACKUP_PARAMETERS: .*\)$|\1 --exclude lost+found --exclude .migration-complete|' "$T"
sops --config .sops.yaml -e "$T" > apps/downloader/nzbget/backup-secrets.enc.yaml
: > "$T"
```

Verify without printing values — expect `1`, `1`, `12`:

```bash
sops --config .sops.yaml -d apps/downloader/nzbget/backup-secrets.enc.yaml \
  | grep -c -- "--exclude lost+found --exclude .migration-complete"
sops --config .sops.yaml -d apps/downloader/nzbget/backup-secrets.enc.yaml \
  | grep -c "^    RESTIC_SOURCE: /backup/sonarr /backup/radarr /backup/nzbget /backup/nzbhydra2 /backup/trailarr /backup/lidarr$"
sops --config .sops.yaml -d apps/downloader/nzbget/backup-secrets.enc.yaml \
  | grep -cE "^    (RESTIC|NTFY|PUSHGATEWAY)_"
```

Apply the same `--exclude .migration-complete` to
`apps/unifi-controller/backup-secrets.enc.yaml` (it already excludes
`lost+found`), so both secrets end up consistent.

Note: each `sops -e` produces a fresh MAC/IV, so the ciphertext changes even
where the plaintext did not. That is expected.

- [ ] **Step 3: Verify the CronJob renders correctly**

Run:
```bash
kustomize build apps/downloader/nzbget --enable-helm --enable-alpha-plugins --enable-exec \
  | grep -E 'claimName: longhorn-pvc-|mountPath: /backup/'
```
Expected: six `mountPath: /backup/…` lines and, together with the Deployment's two, eight `claimName:` lines for the nzbget app.

- [ ] **Step 4: Confirm no downloader hostPath config mount is left anywhere**

Run:
```bash
grep -rn "docker/configs/downloader\|container-scheduler/downloader" apps/ \
  | grep -v "^apps/[^:]*k8s\.pvc\.yaml:"
```
Expected: **no output**. The `k8s.pvc.yaml` files are filtered out on purpose — each carries a comment naming the hostPath it replaced. Those are documentation, not mounts. Any hit outside them is a real leftover.

---

## Task 6b: Add a restic backup for unifi-controller

unifi-controller has never been backed up. Now that its config is a Longhorn PVC
it can join the fleet using the same pattern as `apps/emby/k8s.backup-cronjob.yaml`.

**Files:**
- Create: `apps/unifi-controller/k8s.backup-cronjob.yaml`
- Create: `apps/unifi-controller/backup-secrets.enc.yaml`
- Modify: `apps/unifi-controller/kustomization.yaml`
- Modify: `apps/unifi-controller/kustomize-secret-generator.yaml`
- Modify: `apps/backup-script/k8s.backup-alerts.yaml`
- Modify: `apps/backup-script/k8s.restore-test.yaml`

- [ ] **Step 1: Write `apps/unifi-controller/k8s.backup-cronjob.yaml`**

```yaml
---
# Hourly UniFi controller config backup via restic.
# Modelled on apps/emby/k8s.backup-cronjob.yaml.
# Co-located with the unifi-controller pod via podAffinity because the
# Longhorn PVC is ReadWriteOnce.
apiVersion: batch/v1
kind: CronJob
metadata:
  name: backup-unifi-controller
  namespace: media
spec:
  # The fleet is staggered per ten-minute slot; :25 is the free one
  # between grafana (:20) and emby (:30).
  schedule: "25 * * * *"
  concurrencyPolicy: Forbid
  failedJobsHistoryLimit: 1
  successfulJobsHistoryLimit: 1
  startingDeadlineSeconds: 3600
  jobTemplate:
    spec:
      backoffLimit: 2
      # 50 minutes
      activeDeadlineSeconds: 3000
      template:
        metadata:
          labels:
            app.kubernetes.io/name: backup-unifi-controller
            app.kubernetes.io/component: backup
        spec:
          automountServiceAccountToken: false
          restartPolicy: Never
          # unifi-controller runs with PUID/PGID 1000, so every file under
          # /config is owned by 1000:1000 — no need to read as root.
          securityContext:
            runAsUser: 1000
            runAsGroup: 1000
            runAsNonRoot: true
          affinity:
            podAffinity:
              requiredDuringSchedulingIgnoredDuringExecution:
                - labelSelector:
                    matchLabels:
                      app.kubernetes.io/name: unifi-controller
                  namespaces:
                    - media
                  topologyKey: kubernetes.io/hostname
          containers:
            - name: restic
              image: ghcr.io/madic-creates/container-restic:latest@sha256:5882ed8d36b4381ee2e6a7dad1a0c99f0341653a262209d655c69cee89770e62
              imagePullPolicy: IfNotPresent
              command:
                - /bin/sh
                - /usr/local/bin/backup.sh
              env:
                - name: HOME
                  value: /home/restic
              envFrom:
                - secretRef:
                    name: backup-env-configuration-unifi
              volumeMounts:
                - name: backup-script
                  mountPath: /usr/local/bin/backup.sh
                  subPath: backup.sh
                - name: unifi-config
                  mountPath: /backup/unifi-controller
                  readOnly: true
                - name: home
                  mountPath: /home/restic
              securityContext:
                allowPrivilegeEscalation: false
                capabilities:
                  drop:
                    - ALL
                privileged: false
          volumes:
            - name: unifi-config
              persistentVolumeClaim:
                claimName: longhorn-pvc-unifi-controller-config
            - name: backup-script
              configMap:
                name: crontab-backup-script
            - name: home
              # Node-disk cache, not RAM — same reasoning as the emby job:
              # restic's parent-snapshot working set outgrows a small memory
              # tmpfs and hits ENOSPC. Cache blobs are encrypted repo packs.
              emptyDir:
                sizeLimit: 5Gi
---
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: backup-unifi-controller
  namespace: media
spec:
  endpointSelector:
    matchLabels:
      app.kubernetes.io/name: backup-unifi-controller
  egress:
    - toEndpoints:
        - matchLabels:
            k8s:io.cilium.k8s.namespace.labels.kubernetes.io/metadata.name: kube-system
            k8s:io.kubernetes.pod.namespace: kube-system
            k8s:k8s-app: kube-dns
      toPorts:
        - ports:
            - port: "53"
              protocol: UDP
            - port: "53"
              protocol: TCP
    - toEndpoints:
        - matchLabels:
            k8s:io.cilium.k8s.namespace.labels.kubernetes.io/metadata.name: monitoring
            k8s:io.kubernetes.pod.namespace: monitoring
            k8s:app.kubernetes.io/name: prometheus-pushgateway
      toPorts:
        - ports:
            - port: "9091"
              protocol: TCP
    # Local restic REST-server (primary backup target)
    - toEndpoints:
        - matchLabels:
            k8s:io.cilium.k8s.namespace.labels.kubernetes.io/metadata.name: backup
            k8s:io.kubernetes.pod.namespace: backup
            k8s:app.kubernetes.io/name: rest-server
      toPorts:
        - ports:
            - port: "8000"
              protocol: TCP
    # ntfy notifications on failure
    - toEntities:
        - world
      toPorts:
        - ports:
            - port: "443"
              protocol: TCP
```

- [ ] **Step 2: Create the encrypted backup secret**

The fleet shares one restic repository, password, ntfy and Pushgateway config, so
the unifi secret is Emby's with three values changed. Build it by piping SOPS
output through `sed` — no plaintext is ever printed to the terminal or written
into the repo. Emby is also in namespace `media`, so the namespace needs no edit.

Run:
```bash
sops --config .sops.yaml -d apps/emby/backup-secrets.enc.yaml \
  | sed -e 's|^    name: .*|    name: backup-env-configuration-unifi|' \
        -e 's|^    RESTIC_SOURCE: .*|    RESTIC_SOURCE: /backup/unifi-controller|' \
        -e 's|^    RESTIC_HOSTNAME: .*|    RESTIC_HOSTNAME: unifi|' \
        -e 's|^    RESTIC_ADDITIONAL_BACKUP_PARAMETERS: .*|    RESTIC_ADDITIONAL_BACKUP_PARAMETERS: --tag unifi --exclude lost+found|' \
  > /tmp/unifi-backup.enc.yaml

sops --config .sops.yaml -e /tmp/unifi-backup.enc.yaml \
  > apps/unifi-controller/backup-secrets.enc.yaml

# rm is not allowlisted; truncate the plaintext temp file instead
: > /tmp/unifi-backup.enc.yaml
```

- [ ] **Step 3: Verify the secret without printing its values**

Run:
```bash
sops --config .sops.yaml -d apps/unifi-controller/backup-secrets.enc.yaml \
  | grep -cE '^    (RESTIC|NTFY|PUSHGATEWAY)_'
sops --config .sops.yaml -d apps/unifi-controller/backup-secrets.enc.yaml \
  | grep -q 'name: backup-env-configuration-unifi' && echo "NAME OK"
sops --config .sops.yaml -d apps/unifi-controller/backup-secrets.enc.yaml \
  | grep -q 'RESTIC_SOURCE: /backup/unifi-controller' && echo "SOURCE OK"
sops --config .sops.yaml -d apps/unifi-controller/backup-secrets.enc.yaml \
  | grep -q 'RESTIC_HOSTNAME: unifi' && echo "HOSTNAME OK"
```
Expected: `12`, then `NAME OK`, `SOURCE OK`, `HOSTNAME OK`.

- [ ] **Step 4: Wire the secret and the CronJob into the app**

`apps/unifi-controller/kustomize-secret-generator.yaml` — add the new file:
```yaml
apiVersion: viaduct.ai/v1
kind: ksops
metadata:
  name: secret-generator
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: ksops
files:
  - ingress.enc.yaml
  - backup-secrets.enc.yaml
```

`apps/unifi-controller/kustomization.yaml` — the full file after Task 1 Step 9 and this step:
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: media

resources:
  - k8s.pvc.yaml
  - k8s.deployment.yaml
  - k8s.service.yaml
  - k8s.np.unifi-controller.yaml
  - k8s.backup-cronjob.yaml

generators:
  - kustomize-secret-generator.yaml
```

- [ ] **Step 5: Teach the `BackupNeverRun` alert about unifi**

In `apps/backup-script/k8s.backup-alerts.yaml`, replace

```yaml
            or absent(backup_start_timestamp{instance="minecraft"})
            or absent(backup_start_timestamp{instance="restic-retentionpolicies"})
```

with

```yaml
            or absent(backup_start_timestamp{instance="minecraft"})
            or absent(backup_start_timestamp{instance="unifi"})
            or absent(backup_start_timestamp{instance="restic-retentionpolicies"})
```

and in the same rule's description, replace

```yaml
            description: "One or more of the expected backup instances has not pushed any metrics. Check CronJob status for: mariadb, emby, nextpvr, downloader, paperless, grafana, minecraft, restic-retentionpolicies."
```

with

```yaml
            description: "One or more of the expected backup instances has not pushed any metrics. Check CronJob status for: mariadb, emby, nextpvr, downloader, paperless, grafana, minecraft, unifi, restic-retentionpolicies."
```

- [ ] **Step 6: Add unifi to the weekly restore test**

In `apps/backup-script/k8s.restore-test.yaml`, replace

```yaml
                  TAGS="mariadb emby nextpvr downloader paperless grafana minecraft"
```

with

```yaml
                  TAGS="mariadb emby nextpvr downloader paperless grafana minecraft unifi"
```

and add a verifier branch immediately after the `minecraft)` branch, before the
`*)` fallback:

```yaml
                        unifi)
                          # system.properties is written by the controller for
                          # every site and lives directly under data/.
                          if [ ! -s "$TARGET/backup/unifi-controller/data/system.properties" ]; then
                            STATUS=1
                            FAIL_REASON="$TARGET/backup/unifi-controller/data/system.properties missing or empty"
                          fi ;;
```

- [ ] **Step 6b: Add unifi to the daily retention job**

Without this, `unifi`-tagged snapshots are created hourly and never pruned — the
per-app secrets set `RESTIC_RETENTION_POLICIES_ENABLED: "false"` and defer all
`forget`/`prune` work to this one central CronJob.

In `apps/backup-script/k8s.restic-retentionpolicies.yaml`, replace

```yaml
                  for TAG in mariadb emby nextpvr downloader paperless grafana minecraft; do
```

with

```yaml
                  for TAG in mariadb emby nextpvr downloader paperless grafana minecraft unifi; do
```

and add the `:25` slot to the schedule comment at the top of the file:

```yaml
  # 03:05 daily. The other backup CronJobs are staggered around the hour
  # (mariadb :10, grafana :20, unifi :25, emby :30, nextpvr :40,
  # downloader :50, paperless :00); 03:05 lands in the gap before
  # mariadb's 03:10 slot.
```

- [ ] **Step 7: Verify the app builds with the CronJob and Secret**

Run:
```bash
kustomize build apps/unifi-controller --enable-helm --enable-alpha-plugins --enable-exec \
  | grep -E '^kind:|claimName:|name: backup-env-configuration-unifi'
```
Expected: a `kind: PersistentVolumeClaim`, `kind: Deployment`, `kind: Service`,
`kind: CiliumNetworkPolicy` (twice — the app policy and the backup policy),
`kind: CronJob`, `kind: Secret`, two `claimName: longhorn-pvc-unifi-controller-config`
lines (Deployment and CronJob) and the generated secret name with its hash suffix.

- [ ] **Step 8: Schema-validate the touched apps**

Run:
```bash
scripts/kubeconform-validate.sh apps/unifi-controller
scripts/kubeconform-validate.sh apps/backup-script
```
Expected: both pass.

---

## Task 8: Commit B and cut over

**Files:** none beyond those already edited

- [ ] **Step 1: Run the pre-commit hooks**

Run:
```bash
pre-commit run --all-files
```
Expected: all hooks pass for the touched apps.

- [ ] **Step 2: Commit**

```bash
git add apps/downloader/*/k8s.deployment.yaml \
        apps/trailarr/k8s.deployment.yaml \
        apps/unifi-controller/k8s.deployment.yaml \
        apps/unifi-controller/k8s.backup-cronjob.yaml \
        apps/unifi-controller/backup-secrets.enc.yaml \
        apps/unifi-controller/kustomization.yaml \
        apps/unifi-controller/kustomize-secret-generator.yaml \
        apps/downloader/nzbget/k8s.backup-cronjob.yaml \
        apps/downloader/nzbget/backup-secrets.enc.yaml \
        apps/backup-script/k8s.backup-alerts.yaml \
        apps/backup-script/k8s.restore-test.yaml \
        apps/backup-script/k8s.restic-retentionpolicies.yaml
git commit -m "feat(downloader,unifi): move config volumes to Longhorn PVCs and back up unifi"
```

- [ ] **Step 3: MANUAL (user) — push and sync**

```bash
git push
for a in 65-headphones 64-downloader-lidarr 65-nzbget 65-nzbhydra2 65-radarr 65-sonarr \
         34-trailarr 65-unifi-controller; do
  kubectl annotate -n argocd application "$a" argocd.argoproj.io/refresh=hard --overwrite
done
```

- [ ] **Step 4: MANUAL (user) — verify sync and health**

```bash
for a in 65-headphones 64-downloader-lidarr 65-nzbget 65-nzbhydra2 65-radarr 65-sonarr \
         34-trailarr 65-unifi-controller; do
  printf '%s ' "$a"
  kubectl get application -n argocd "$a" \
    -o jsonpath='SYNC={.status.sync.status} HEALTH={.status.health.status}{"\n"}'
done
kubectl -n downloader get pods -o wide
kubectl -n media get pods -o wide -l app.kubernetes.io/name=unifi-controller
```
Expected: every Application `Synced` / `Healthy`; all eight pods `Running` and `1/1` on node04.

- [ ] **Step 5: MANUAL (user) — verify each app sees its data**

Open each web UI and confirm the library, indexers and history are intact:
sonarr, radarr, lidarr, nzbget, nzbhydra2, trailarr, headphones.

For a quick check without the UI:
```bash
kubectl -n downloader logs deploy/sonarr --tail=30
kubectl -n downloader logs deploy/radarr --tail=30
kubectl -n downloader logs deploy/trailarr --tail=30
kubectl -n media logs deploy/unifi-controller --tail=30
```
Expected: no `database is locked`, no `permission denied`, no fresh-install/first-run messages.

Give NZBGet's extension-script list one glance in its web UI (Settings →
Extension Scripts). Unlike the other config volumes, that directory is actively
scanned by the application, and the migration left a `.migration-complete`
marker in it. The marker has no execute bit and no script header so NZBGet
should ignore it — confirm rather than assume. For unifi, confirm the UI at `https://192.168.1.45:8443` still shows the existing site and that the APs report as connected — a fresh setup wizard means the config did not come across.

- [ ] **Step 6: MANUAL (user) — verify the next backup run**

The CronJob fires at `:50`. After it runs:
```bash
kubectl -n downloader get jobs -l app.kubernetes.io/name=backup-downloader
kubectl -n downloader logs -l app.kubernetes.io/name=backup-downloader --tail=50
```
Expected: `Backup process completed`, exit code 0, no `permission denied` on `lost+found`. If the run failed on `lost+found`, the migration Jobs' `chmod` did not apply to that volume — fix it with a one-off debug pod that mounts the PVC and runs `chmod 0755 /mnt/lost+found`.

To trigger it immediately instead of waiting:
```bash
kubectl -n downloader create job --from=cronjob/backup-downloader backup-downloader-manual
```

- [ ] **Step 7: MANUAL (user) — verify the new unifi backup**

The new CronJob fires at `:25`. Trigger it immediately instead:
```bash
kubectl -n media create job --from=cronjob/backup-unifi-controller backup-unifi-manual
kubectl -n media logs -l app.kubernetes.io/name=backup-unifi-controller --tail=50
```
Expected: `Backup process completed` and exit code 0. Then confirm the snapshot
and its tag exist — run this from any pod that already has the restic env, e.g.
the retention CronJob's image, or check the Grafana backup dashboard for an
`instance="unifi"` series appearing after the run.

A first run has no parent snapshot, so it uploads all ~372 MB and takes longer
than later incremental runs.

---

## Task 9: Clean up

**Files:**
- Delete: `scripts/migrate-hostpath-to-pvc/` (after the migration is verified)

- [ ] **Step 1: MANUAL (user) — delete the migration Jobs from the cluster**

```bash
kubectl delete -f scripts/migrate-hostpath-to-pvc/
```

- [ ] **Step 2: MANUAL (user) — delete the migration manifests from the working tree**

They were never committed, so this is a plain filesystem delete with nothing to push:

```bash
rm -r scripts/migrate-hostpath-to-pvc/
git status --short   # the directory should simply disappear from the untracked list
```

- [ ] **Step 3: MANUAL (user, optional) — remove the migration markers**

Each migrated volume keeps a zero-byte `.migration-complete` file in its root.
It is harmless, but removing it is tidy and makes a stray re-apply of the Jobs
abort loudly instead of silently skipping:

```bash
kubectl -n downloader exec deploy/sonarr     -- rm -f /config/.migration-complete
kubectl -n downloader exec deploy/radarr     -- rm -f /config/.migration-complete
kubectl -n downloader exec deploy/lidarr     -- rm -f /config/.migration-complete
kubectl -n downloader exec deploy/nzbhydra2  -- rm -f /config/.migration-complete
kubectl -n downloader exec deploy/headphones -- rm -f /config/.migration-complete
kubectl -n downloader exec deploy/trailarr   -- rm -f /config/.migration-complete
kubectl -n downloader exec deploy/nzbget     -- rm -f /config/.migration-complete /app/nzbget/scripts/.migration-complete
kubectl -n media      exec deploy/unifi-controller -- rm -f /config/.migration-complete
```

nzbget's `scripts` volume is mounted read-only in the container, so its marker
cannot be removed this way — leave it, or clear it with a short-lived debug pod
that mounts the PVC read-write. radarr's trailer-downloader marker is invisible
to the container (the volume is mounted with `subPath: settings.ini`) and needs
the same debug-pod treatment if you care.

Do this only after the migration is confirmed good — while the markers are in
place, re-applying a Job is a safe no-op.

- [ ] **Step 4: Leave the old hostPath data in place**

The source directories on node04 are the fallback. Delete them only after several days of confirmed-good operation and at least one verified restic restore test:

```bash
ssh mne-adm@192.168.1.24 'sudo rm -rf \
  /vol_raidz1/docker/configs/downloader/{headphones,nzbget,nzbhydra2,radarr,sonarr,trailarr} \
  /vol_raidz1/docker/configs/downloader/Trailer-Downloader-Config \
  /vol_raidz1/container-scheduler/downloader-lidarr \
  /vol_raidz1/docker/configs/unify-controller'
```

Do **not** run this as part of the migration. `/vol_raidz1/docker/configs/downloader/lidarr/config` (899M, stale — the running lidarr used `container-scheduler/downloader-lidarr`) and the `tdarr`/`tdarr2` directories are out of scope and must not be deleted here.

---

## Rollback

**Before Task 5 is pushed:** nothing is lost. `git revert` the Commit A hash, push, and the Deployments come back on their hostPath volumes with the original data.

**After Task 8 is pushed:** `git revert` the Commit B hash and push. The Deployments return to the hostPath volumes; that data is stale by however long the apps ran on the PVCs. The PVCs are untouched by the revert, and the `longhorn` StorageClass uses `Retain`, so the migrated data survives even a PVC delete and can be re-attached via a `longhorn-static` PV.
