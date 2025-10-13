# Longhorn Maintenance Window

This guide describes a repeatable procedure to stop workloads in a controlled way for Longhorn upgrades and then restore normal operations.

## Prerequisites

- `kubectl` with access to the cluster
- `yq` to read the maintenance plan
- Up-to-date checkout of this repository (`git pull`) so that `scripts/scale-maintenance-plan.yaml` and `scripts/scale-maintenance.sh` are available

> Note: The affected Argo CD applications ignore replica drift thanks to the `spec.ignoreDifferences` entries. During the maintenance window they will report as "Degraded" but will not attempt to roll replicas back automatically.

## Procedure

1. **Check status**
   ```bash
   scripts/scale-maintenance.sh status
   ```
   Shows the currently configured replicas and any stored original value for each workload.

2. **Stop workloads**
   ```bash
   scripts/scale-maintenance.sh down
   ```
   - Stores the current replica count as the annotation `maintenance.k3s-git-ops/original-replicas`.
   - Scales the Deployments, StatefulSets, and operator-managed CRs (Prometheus / Alertmanager) listed in `scripts/scale-maintenance-plan.yaml` down to `0`.
   - The command is idempotent and can be dry-run with `--dry-run`.

3. **Perform the Longhorn upgrade**
   - Upgrade Longhorn (UI or Argo CD).
   - Afterwards verify that the storage cluster is healthy.

4. **Restore workloads**
   ```bash
   scripts/scale-maintenance.sh up
   ```
   - Reads the stored values and restores the replicas.
   - Removes the maintenance annotation afterwards.

5. **Post checks**
   ```bash
   scripts/scale-maintenance.sh status
   ```
   - Verifies that no maintenance annotations remain (empty `STORED` column).
   - Trigger a "Refresh" in Argo CD for affected apps to update their health status.

## Adjustments

- Add additional workloads to `scripts/scale-maintenance-plan.yaml` (`id`, `namespace`, `kind`, `name`).
- Alternate plans (for other clusters, for example) can be provided via `PLAN_FILE=/path/to/plan.yaml` or the `--plan-file` flag.
- Use `--dry-run` for a rehearsal without actually touching replicas; the commands will be logged only.

## Troubleshooting

- **Annotation missing on restore:** Confirm that the scale-down completed successfully; otherwise set the desired replica count manually (for Prometheus / Alertmanager CRs: `kubectl patch ... --type merge -p '{"spec":{"replicas":<n>}}'`) and re-run the script.
- **Resource not found:** Check namespace and name in the plan. Helm releases with prefixes must match exactly.
- **Argo CD immediately restores replicas:** Confirm that the application includes the `ignoreDifferences` entry and that it has been refreshed after the maintenance window.
