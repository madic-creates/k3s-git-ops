# GitOps managed Kubernetes Home Cluster

*Faciliating ArgoCD and supported via RenovateBot* :robot:

<div align="center">

[![k3s](https://img.shields.io/badge/k3s-v1.30.5-blue?style=for-the-badge&logo=kubernetes&logoColor=white)](https://k3s.io/)
[![pre-commit](https://img.shields.io/badge/pre--commit-enabled?logo=pre-commit&logoColor=white&style=for-the-badge&color=brightgreen)](https://github.com/pre-commit/pre-commit)
[![renovate](https://img.shields.io/badge/renovate-enabled?style=for-the-badge&logo=renovatebot&logoColor=white&color=brightgreen)](https://github.com/renovatebot/renovate)

</div>

## 🌎 Overview

This repository is a playground for my Kubernetes Home Cluster.

It uses [ArgoCD](https://github.com/argoproj/argo-cd) as a GitOPs platform to automate the deployment and keep the cluster in a consistent state.

For more information take a lookt at my [docs](https://madic-.github.io/k3s-git-ops/).

If you're getting a certificate error when opening the docs (happens in Firefox), thats because of the hyphen in my username. Hyphens aren't allowed at the end of DNS names. As a workaround you can use a chromium based browser.

See also the following GitHub discussion: [Hyphen at the end of usernames](https://github.com/orgs/community/discussions/143105)

## Features

Excerpt of features this cluster provides:

- Provision nodes, including k3s, via ansible
- GitOps based cluster management with ArgoCD
- Encrypted secrets with [sops](https://github.com/mozilla/sops)
- Every exposed service uses SSO with [Authelia](https://www.authelia.com/)
- File backups from persistant volumes
  - Backup any folder to a restic supported storage backend
  - Delete old backups (Daily, Weekly, Monthly, Always Keep Last)
  - ntfy.sh notification on failure
  - prometheus pushgateway metrics
- KubeDoom: Killing whoami containers with a shotgun
- High Avaliability ControlPlane and LoadBalancer via KubeVIP
- Monitoring via kube-prometheus-stack
- Logging via loki
- Alerting via alertmanager to a selfhosted ntfy
- Storage managed via longhorn
- Vagrant based virtual test environment
