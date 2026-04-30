# Backup migration: Wasabi-only → local-first with Wasabi mirror

This runbook covers the one-time migration from the Wasabi-only backup
architecture to the local-first architecture introduced by the
`backup-rest-server` app. Run it from a workstation with `kubectl`
access and an `~/.config/sops/age/keys.txt` containing the cluster
age key.

The cluster-side YAML changes already exist in git. Do **not** push
them to ArgoCD until the migration is complete — the new backup
secrets point to a rest-server that has no repository yet, and any
backup CronJob that fires before the initial copy will fail.

## 0. Prerequisites

- [ ] `restic` ≥ 0.16 on the workstation
- [ ] `kubectl` configured for the production cluster
- [ ] `sops` and the cluster age key (`~/.config/sops/age/keys.txt`)
- [ ] Workstation can resolve and reach Wasabi
- [ ] Optional: a coffee — the initial copy is the slow step

## 1. Provision the ZFS dataset on node04

ssh into node04 and create a dedicated dataset for the local repo:

```bash
sudo zfs create vol_raidz1/restic-local
sudo zfs set compression=zstd vol_raidz1/restic-local
sudo zfs set atime=off vol_raidz1/restic-local
sudo zfs set quota=200G vol_raidz1/restic-local      # adjust to taste
sudo chown 65534:65534 /vol_raidz1/restic-local
sudo chmod 700 /vol_raidz1/restic-local
```

The dataset must be owned by UID 65534 — the same user the rest-server
pod and all backup-related CronJobs run as. The `chmod 700` is
defensive; the contents are restic-encrypted anyway.

## 2. Deploy rest-server only (without flipping the workloads)

The `apps/backup-rest-server/` ArgoCD Application should sync first.
The other YAML changes (backup secrets, NetworkPolicies) are already
committed but ArgoCD has not seen them yet.

If ArgoCD auto-sync is enabled and the changes are already pushed,
**pause it for the affected applications** before pushing:

```bash
kubectl annotate application -n argocd 15-backup-rest-server \
  argocd.argoproj.io/sync-policy=Manual --overwrite

for app in 08-mariadb 16-monitoring 30-emby 31-nextpvr \
           40-paperless-ngx 65-nzbget 15-backup-script; do
  kubectl annotate application -n argocd "$app" \
    argocd.argoproj.io/sync-policy=Manual --overwrite
done
```

Push the branch / let CI propagate it. Then sync **only**
`15-backup-rest-server`:

```bash
argocd app sync 15-backup-rest-server
kubectl wait --for=condition=Available -n backup deploy/rest-server --timeout=5m
kubectl logs -n backup -l app.kubernetes.io/name=rest-server --tail=10
```

Immediately after the sync, suspend the Wasabi mirror CronJob until the
migration is complete. Both targets share the Wasabi bucket during the
migration window — workload backups still push to Wasabi (until step 6),
the workstation `restic copy` reads from Wasabi, and the mirror would
write local → Wasabi. Most restic files are content-addressed and
cannot collide, but lock files and the `config` blob are not, and
running the mirror against a bucket that retention is also pruning
risks resurrecting deleted index files. Cleaner to keep the mirror off:

```bash
kubectl patch cronjob -n backup backup-mirror-wasabi \
  -p '{"spec":{"suspend":true}}'
```

Confirm rest-server is healthy. Read the rest-server password from
the encrypted Secret on disk and pipe it through curl:

```bash
REST_PW=$(sops --config .sops.yaml -d \
  apps/backup-rest-server/secrets.enc.yaml \
  | yq -r '.stringData.htpasswd' | awk -F: '{print $2}')
# (just for the manual smoke test below — the real password lives in
# every backup-secrets.enc.yaml under RESTIC_REPOSITORY)

kubectl run -n backup --rm -it --restart=Never \
  --image=curlimages/curl curltest -- \
  sh -c 'echo "Use a per-target backup-secrets.enc.yaml or ksops-decoded value"'
# Expected: empty 200 response (or "Restic REST Server" body) when
# you supply -u backup:<password>
```

## 3. Initialize the empty local repo

From the workstation, port-forward the rest-server and `restic init`.
Pull the credentials from the existing encrypted Secrets — never
hard-code them in this document:

```bash
kubectl port-forward -n backup svc/rest-server 8000:8000 &
PF_PID=$!

# Decrypt one of the backup target secrets and reuse its values.
# Any backup-secrets.enc.yaml works — they all share the same
# repository password and rest-server URL.
eval "$(sops --config .sops.yaml -d apps/monitoring/backup-grafana-secrets.enc.yaml \
  | yq -r '.stringData | to_entries[] | "export \(.key)='\(.value)'"' \
  | grep -E '^export RESTIC_(REPOSITORY|PASSWORD)=')"

# Override the in-cluster service URL with the local port-forward.
export RESTIC_REPOSITORY="${RESTIC_REPOSITORY/rest-server.backup.svc.cluster.local/127.0.0.1}"

restic init
restic snapshots          # should print "no snapshots found"

kill $PF_PID
```

## 4. Copy snapshots from Wasabi

Still from the workstation, set up the source (Wasabi) and target
(local rest-server) and run `restic copy`. This is the slow part —
expect ~2–8 hours for a ~180 GB Wasabi repo, depending on workstation
download speed.

```bash
kubectl port-forward -n backup svc/rest-server 8000:8000 &
PF_PID=$!

# Pull both Wasabi and rest-server credentials from encrypted secrets.
eval "$(sops --config .sops.yaml -d apps/backup-rest-server/mirror-secrets.enc.yaml \
  | yq -r '.stringData | to_entries[] | "export \(.key)='\(.value)'"' \
  | grep -E '^export AWS_(ACCESS_KEY_ID|SECRET_ACCESS_KEY)=')"

# Source: Wasabi
export RESTIC_FROM_REPOSITORY='s3:s3.eu-central-2.wasabisys.com/k3s-at-home-01/restic-backup'
# Both repos share the same encryption password — already exported via
# RESTIC_PASSWORD in step 3. RESTIC_FROM_PASSWORD defaults to it.

# Target: local rest-server (already in $RESTIC_REPOSITORY from step 3)

restic copy --from-repo "$RESTIC_FROM_REPOSITORY"
# Run with --verbose for progress; restic groups output by snapshot.

kill $PF_PID
```

For long initial copies, prefer the in-cluster Job approach (no
port-forward fragility): a one-shot `Job` in the `backup` namespace
that injects the credentials from existing Secrets. The Job pattern
is documented separately and reuses the
`backup-mirror-wasabi-creds` Secret for Wasabi and any of the
per-target backup secrets for rest-server.

Both repos use the same encryption password, so `restic copy` is a
straight blob-copy without re-encryption.

If the copy is interrupted, just restart it — `restic copy` is
idempotent and skips snapshots that already exist on the destination.

## 5. Verify the local repo

```bash
kubectl port-forward -n backup svc/rest-server 8000:8000 &
PF_PID=$!

restic snapshots                      # should mirror Wasabi
restic stats --mode raw-data          # repo size on disk
restic check --read-data-subset 5%    # spot-check pack files

kill $PF_PID
```

The `snapshots` count should match what's in Wasabi (modulo a
handful that may have been pushed during the copy).

## 6. Flip the workloads

Re-enable auto-sync on the Applications you paused:

```bash
for app in 15-backup-rest-server 08-mariadb 16-monitoring 30-emby \
           31-nextpvr 40-paperless-ngx 65-nzbget 15-backup-script; do
  kubectl annotate application -n argocd "$app" \
    argocd.argoproj.io/sync-policy- --overwrite
done

# Or sync them all at once:
for app in 08-mariadb 16-monitoring 30-emby 31-nextpvr \
           40-paperless-ngx 65-nzbget 15-backup-script; do
  argocd app sync "$app"
done
```

ArgoCD updates the backup secrets (now pointing at rest-server) and
the NetworkPolicies (egress to rest-server, ntfy only on the public
internet). The next backup hour each CronJob fires writes its
snapshot into the local rest-server.

## 7. Smoke-test each backup target

Trigger one manual run per target and confirm it lands in the local
repo:

```bash
for ns_cron in databases/backup-mariadb monitoring/backup-grafana \
               media/backup-emby media/backup-nextpvr \
               downloader/backup-downloader paper/backup-paperless; do
  ns="${ns_cron%/*}"; cron="${ns_cron#*/}"
  kubectl create job -n "$ns" --from="cronjob/$cron" "$cron-smoke"
done

# Watch the logs as they start to land:
kubectl logs -n monitoring -l job-name=backup-grafana-smoke -f
```

Verify that snapshots accumulate locally:

```bash
kubectl port-forward -n backup svc/rest-server 8000:8000 &
restic snapshots --tag mariadb --last 1
restic snapshots --tag grafana --last 1
restic snapshots --tag emby --last 1
restic snapshots --tag nextpvr --last 1
restic snapshots --tag downloader --last 1
restic snapshots --tag paperless --last 1
```

## 8. Re-enable and smoke-test the Wasabi mirror

Now that the workloads write to local and no other process is touching
the Wasabi bucket, it's safe to flip the mirror back on:

```bash
kubectl patch cronjob -n backup backup-mirror-wasabi \
  -p '{"spec":{"suspend":false}}'
```

Trigger the daily mirror once to confirm the pipeline works
end-to-end:

```bash
kubectl create job -n backup \
  --from=cronjob/backup-mirror-wasabi backup-mirror-smoke
kubectl logs -n backup -l job-name=backup-mirror-smoke -f
```

The first run should be a near-no-op — Wasabi already has every blob
the local repo just received via step 4. From then on the daily 05:00
slot keeps Wasabi in sync.

## 9. Clean up the legacy Wasabi-direct artifacts

After the first successful daily mirror (and optionally a second day
of normal hourly backups going through), the system is steady-state.

Optional cleanup commits (kept out of the migration commit so they
land separately):

- Remove `apps/backup-script/rclone-config.enc.yaml` and its entry
  in `apps/backup-script/kustomize-secret-generator.yaml` — the
  retention CronJob no longer uses rclone.
- Tighten any per-app NetworkPolicies that still allow `world:443/80`
  but only need `:443` for ntfy (already done in this migration —
  double-check via `kubectl describe ciliumnetworkpolicy`).

## Rollback

If something goes wrong before step 6 (the workload flip), nothing
else is affected — Wasabi is still authoritative. Simply scale the
rest-server Deployment to 0 and revert the un-pushed git changes.

If something goes wrong after step 6:

1. Revert the per-target backup secrets back to the Wasabi
   `RESTIC_REPOSITORY` URL and re-add `AWS_*` (the values are still
   in this runbook above).
2. Re-apply or `argocd app sync` each affected Application.
3. Backups resume going to Wasabi directly.

The local rest-server stays in place — it just receives no new
writes. You can leave it running, or scale to 0 and remove the app.

## Reference

| Component | URL / location |
|---|---|
| rest-server Service | `rest-server.backup.svc.cluster.local:8000` |
| Local repo on disk | `/vol_raidz1/restic-local` (node04, owner `65534:65534`) |
| Wasabi repo | `s3:s3.eu-central-2.wasabisys.com/k3s-at-home-01/restic-backup` |
| Mirror schedule | Daily 05:00 (`apps/backup-rest-server/k8s.backup-mirror-wasabi.yaml`) |
| Backup user (rest-server) | `backup` |
| Repository password | shared, same as before — encrypted in every `backup-secrets.enc.yaml` |
