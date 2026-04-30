# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is a GitOps-managed Kubernetes home cluster running k3s (v1.31.4) with ArgoCD for automated deployments. The cluster is provisioned via Ansible and includes comprehensive monitoring, logging, storage, and backup solutions. All secrets are encrypted using SOPS with age encryption.

## Repository Structure

- `apps/` - Kubernetes applications, each with its own kustomization.yaml
  - `argo-cd/` - ArgoCD installation (Helm chart)
  - `argo-cd-apps/` - ArgoCD Application manifests (numbered by deployment order)
  - Apps are deployed in waves using `argocd.argoproj.io/sync-wave` annotations
- `ansible/` - Cluster provisioning and configuration
  - `inventory/` - Environment-specific inventories (production, staging)
  - `playbooks/` - install.yaml, remove.yaml, upgrade.yaml
  - `group_vars/` - Variable files organized by environment
- `docs/` - MkDocs documentation published to GitHub Pages
- `secrets/` - Age-encrypted secrets
- `shared/` - Node-specific configurations and kubeconfig files
- `scripts/` - Utility scripts for cluster operations

## Deployment Architecture

### ArgoCD Application Ordering

Applications in `apps/argo-cd-apps/` are numbered to control deployment order:
- `00-*` - Core infrastructure (CoreDNS, KubeVIP)
- `01-*` - Certificate management (cert-manager)
- `02-*` - Supporting services (reflector)
- `03-*` - Ingress controllers (Traefik)
- `08-10` - Databases and auth (MariaDB, Redis, Authelia)
- `15-*` - Storage and backups (Longhorn, backup-script)
- `16-18` - Monitoring stack (Prometheus, Loki, Pushgateway)
- `20-*` - Storage drivers (CSI SMB)
- `30-64` - Application workloads
- `70-*` - Optimization tools (Descheduler)
- `90+` - Management tools
- `96-99` - System upgrades and ArgoCD self-management

### Kustomize with Helm

Apps use Kustomize's `helmCharts` feature to deploy Helm charts inline. Each app directory typically contains:
- `kustomization.yaml` - Defines Helm chart and additional resources
- `values.yaml` or inline `valuesInline` - Helm values
- `*.enc.yaml` - SOPS-encrypted secrets
- `kustomize-secret-generator.yaml` - ksops configuration for decrypting secrets
- `k8s.*.yaml` - Additional Kubernetes manifests

## GitOps Workflow — Do Not Bypass ArgoCD

**Never modify ArgoCD-managed objects directly with `kubectl edit`, `kubectl patch`, or `kubectl apply`.** Git is the source of truth; the cluster is a reconciled copy. Manual edits create drift that ArgoCD may silently revert (or worse, fail to detect when its sync timestamp is stale), so the change appears to work, gets reverted minutes later, and is impossible to reproduce.

The only way to change a managed resource is:

1. Edit the YAML in this repo
2. `git commit` and `git push` to the remote tracked by ArgoCD
3. Wait for ArgoCD to auto-sync, or trigger explicitly:
   - `kubectl annotate -n argocd application <app> argocd.argoproj.io/refresh=hard --overwrite`
   - or `argocd app sync <app>` if the CLI is logged in
4. Verify with `kubectl get application -n argocd <app> -o jsonpath='SYNC={.status.sync.status} HEALTH={.status.health.status}{"\n"}'`

Exceptions where direct `kubectl` is acceptable:
- Read-only inspection (`kubectl get`, `kubectl logs`, `kubectl describe`, `kubectl exec` for diagnostics)
- One-off Job triggers from a managed CronJob (`kubectl create job --from=cronjob/...`) — these are not tracked by ArgoCD
- Ephemeral debug pods (`kubectl run --rm`)
- Cluster-state operations not represented in Git (e.g. forcing a pod restart via `kubectl delete pod` to pick up a new ConfigMap mounted via `subPath`)

If a quick verification of a manifest change is needed before committing, do it in a scratch namespace or with a dry-run, not by mutating the live managed resource.

## Secret Management with SOPS

All sensitive data is encrypted using Mozilla SOPS with age encryption.

### SOPS Configuration

Configuration is in `.sops.yaml` with two age public keys. The `encrypted_regex` field defines which YAML keys to encrypt (passwords, URLs, tokens, etc.).

### Working with Encrypted Files

**Decrypt and edit a file:**
```bash
sops --config .sops.yaml -d apps/authelia/values.enc.yaml
```

**Create new encrypted file:**
```bash
sops --config .sops.yaml -e apps/myapp/secrets.enc.yaml
```

**Encrypt after manual editing:**
```bash
sops --config .sops.yaml -e -i apps/myapp/secrets.enc.yaml
```

### Encrypted File Patterns

- `*.enc.yaml` - Encrypted YAML files (secrets, ingress configs with domains, etc.)
- Files use ksops for Kustomize integration via `kustomize-secret-generator.yaml`:
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
    - secrets.enc.yaml
  ```

## Common Commands

### Testing Changes Locally

```bash
# Build and preview kustomization (requires ksops and sops configured)
kustomize build apps/myapp --enable-helm --enable-alpha-plugins --enable-exec

# Validate a specific app with kubeconform
scripts/kubeconform-validate.sh apps/myapp

# Validate YAML syntax
kubectl apply --dry-run=client -f apps/myapp/manifest.yaml

# Check ArgoCD sync diff
argocd app diff myapp

# Run all pre-commit hooks
pre-commit run --all-files
```

Pre-commit hooks enforce: trailing whitespace, line endings, tab→space (2 spaces), smartquote fixes, unencrypted secret detection, and **kubeconform schema validation** against k8s 1.31.0 for changed apps.

### Cluster Installation

```bash
# Install required Ansible collections
ansible-galaxy collection install -r requirements.yaml

# Deploy cluster (from ansible directory)
cd ansible
ansible-playbook playbooks/install.yaml -i inventory/production/hosts --diff
```

### ArgoCD Bootstrap

```bash
# Deploy ArgoCD
kustomize build apps/argo-cd --enable-helm --enable-alpha-plugins --enable-exec | kubectl apply -f -

# Deploy all applications
kustomize build apps/argo-cd-apps | kubectl apply -f -

# Get ArgoCD admin password
kubectl get secret argocd-initial-admin-secret -n argocd -o jsonpath="{.data.password}" | base64 -d
```

### Vagrant Test Environment

```bash
vagrant up                                          # Full test environment (3 VMs)
vagrant up k3svm1                                   # Server node only
export KUBECONFIG="$PWD/shared/k3svm1/k3s.yaml"    # Use vagrant kubeconfig
vagrant ssh k3svm1                                  # SSH into VM
vagrant destroy -f                                  # Destroy test environment
```

### Cilium / Network Policy Development

```bash
cilium hubble port-forward
hubble observe --namespace media --last 20
```

Use these when creating network policies to observe actual connections.

## Important Conventions

### Sync Wave Annotations

All resources use ArgoCD sync waves via `commonAnnotations` in the app's `kustomization.yaml`:
```yaml
commonAnnotations:
  argocd.argoproj.io/sync-wave: "30"
```

### Namespace Management

Most apps include a `namespace.yaml` or `k8s.namespace.yaml` in their kustomization resources.

### Network Policies

Apps use Cilium network policies with a default-deny pattern:
- `k8s.np.*-default-deny.yaml` - Default deny all ingress/egress
- `k8s.np.<appname>.yaml` - Allow specific traffic for the app

### Kustomize Secret Hashing

Secrets that need hash suffixes use:
```yaml
annotations:
  kustomize.config.k8s.io/needs-hash: "true"
```

### Repository URL Replacement

ArgoCD Application manifests use a placeholder `repoURL: "example.com"` which is replaced via Kustomize replacements reading from the encrypted secret `argocd-repo-url-public` in `apps/argo-cd-apps/kustomization.yaml`.

### LoadBalancer / Ingress Rules

| Host domain | Traefik entrypoints | Certificate |
|---|---|---|
| `internal.neese-web.de` | `web239,websecure239` | `wildcard-cloudflare-production-02` |
| `k8s.neese-web.de` | `web,websecure` | `wildcard-cloudflare-production-01` |

### Dependency Updates

Renovate Bot auto-merges minor/patch updates (with 5-day minimum release age). Major updates and critical apps (mariadb, traefik, kubevip-ha, longhorn) require manual review. Longhorn has a 14-day minimum release age.

## Adding a New Application

1. Create `apps/<appname>/` with a `kustomization.yaml`
2. If using Helm, define `helmCharts` in the kustomization with `valuesInline` or a separate `values.yaml`
3. Add encrypted secrets as `*.enc.yaml` with a `kustomize-secret-generator.yaml` using ksops
4. Add network policies: default-deny + app-specific allow rules
5. Create `apps/argo-cd-apps/<NN>-<appname>.yaml` with the appropriate sync wave number
6. Add the new manifest to `apps/argo-cd-apps/kustomization.yaml` resources list

## Documentation

Full docs: https://madic-creates.github.io/k3s-git-ops/

```bash
pip install -r requirements.txt && mkdocs serve   # Serve docs locally
```

Code comments and documentation must always be in english.

## Ansible Inventory Structure

Inventories are organized by environment in `ansible/inventory/{environment}/hosts`:

Required host groups:
- `k3s_primary_server` - First server node (initializes cluster)
- `k3s_secondary_server` - Additional server nodes for HA
- `k3s_agent` - Worker nodes

Variables are in `ansible/group_vars/{environment}/` to override defaults from `ansible/group_vars/all/main.yaml`.

## Troubleshooting

### Encrypted Files Not Decrypting

Ensure you have:
1. Age private key configured (usually in `~/.config/sops/age/keys.txt`)
2. ksops installed and in PATH
3. SOPS installed

### ArgoCD Application Out of Sync

Check sync wave ordering and dependencies between applications. Lower sync wave numbers deploy first.

### Kustomize Build Fails

Use `--enable-helm` flag when building kustomizations that include helmCharts:
```bash
kustomize apps/myapp --enable-helm --enable-alpha-plugins --enable-exec
```
