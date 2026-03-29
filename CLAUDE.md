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

The Kubeconfig for the production cluster is below shared/node01.neese-web.de/k3s.yaml and can be used for read-only kubectl commands to get more information from the cluster.

### Cluster Installation

```bash
# Install required Ansible collections
ansible-galaxy collection install -r requirements.yaml

# Deploy cluster (from ansible directory)
cd ansible
ansible-playbook playbooks/install.yaml -i inventory/production/hosts --diff

# Kubeconfig is saved to: shared/${HOSTNAME}/k3s.yaml
```

### ArgoCD Bootstrap

```bash
# Deploy ArgoCD
kustomize apps/argo-cd --enable-helm --enable-alpha-plugins --enable-exec | kubectl apply -f -

# Deploy all applications
kustomize apps/argo-cd-apps | kubectl apply -f -

# Get ArgoCD admin password
kubectl get secret argocd-initial-admin-secret -n argocd -o jsonpath="{.data.password}" | base64 -d
```

### Testing Changes Locally

```bash
# Build and preview kustomization (requires ksops and sops configured)
kustomize apps/myapp --enable-helm --enable-alpha-plugins --enable-exec

# Validate YAML syntax
kubectl apply --dry-run=client -f apps/myapp/manifest.yaml

# Check ArgoCD sync diff
argocd app diff myapp
```

### Working with Vagrant Test Environment

```bash
# Start full test environment (3 VMs: 1 server, 2 workers)
vagrant up

# Start only server node
vagrant up k3svm1

# Kubeconfig location for vagrant
export KUBECONFIG="$PWD/shared/k3svm1/k3s.yaml"

# SSH into VM
vagrant ssh k3svm1

# Destroy test environment
vagrant destroy -f
```

### Cilium

```bash
# Create hubble portforward
cilium hubble port-forward

# Get last X connections from namespace
hubble observe --namespace media --last 20
```

Both commands can be used during for example the creation of network policies to know, which connections were made.

### Pre-commit Hooks

```bash
# Install pre-commit hooks (recommended for all contributors)
pre-commit install

# Run manually
pre-commit run --all-files
```

Hooks enforce: trailing whitespace removal, line ending normalization, CRLF→LF conversion, tab→space conversion, smartquote fixes, and unencrypted secret detection.

## Key Technologies

- **k3s** - Lightweight Kubernetes distribution
- **ArgoCD** - GitOps continuous delivery
- **Kustomize** - Kubernetes configuration management
- **Helm** - Package manager (deployed via Kustomize helmCharts)
- **SOPS + age** - Secret encryption
- **ksops** - Kustomize plugin for SOPS integration
- **Ansible** - Cluster provisioning and node management
- **Traefik** - Ingress controller
- **Authelia** - SSO/authentication (all exposed services use SSO)
- **Longhorn** - Distributed block storage
- **cert-manager** - TLS certificate management
- **Prometheus + Grafana** - Monitoring (kube-prometheus-stack)
- **Loki** - Log aggregation
- **Velero** - Backup and disaster recovery
- **KubeVIP** - HA control plane and load balancing
- **Cilium** - CNI with network policies

## Important Conventions

### Sync Wave Annotations

All resources use ArgoCD sync waves for ordered deployment:
```yaml
annotations:
  argocd.argoproj.io/sync-wave: "10"
```

### Namespace Management

Most apps include a `k8s.namespace.yaml` with the namespace definition in their kustomization resources.

### Kustomize Secret Hashing

Secrets that need hash suffixes use:
```yaml
annotations:
  kustomize.config.k8s.io/needs-hash: "true"
```

### Repository URL Replacement

ArgoCD Application manifests use a placeholder `repoURL: "example.com"` which is replaced via Kustomize replacements reading from the encrypted secret `argocd-repo-url-public` in the `apps/argo-cd-apps/kustomization.yaml`.

### LoadBalancer

When an ingress has internal.neese-web.de as host use router.entrypoints: web239,websecure239 and as certificate wildcard-cloudflare-production-02.
When an ingress has k8s.neese-web.de as host use router.entrypoints: web,websecure and as certificate wildcard-cloudflare-production-01.

## Documentation

Full documentation is available at: https://madic-creates.github.io/k3s-git-ops/

The documentation covers:
- Installation procedures
- Secret management workflows
- Monitoring setup
- Network configuration
- Update procedures
- Test environment setup

Build docs locally:
```bash
# Install dependencies
pip install -r requirements.txt

# Serve docs locally
mkdocs serve

# Build static site
mkdocs build
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
