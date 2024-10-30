# K3S

/// warning | Warning
This documentation is at a very early stage.
///

## Important used technologies

- [K3S](https://k3s.io/){target=_blank}
This is the foundation of the infrastructure. It's an easy to deploy and maintain kubernetes distribution

- [ArgoCD](https://argoproj.github.io/argo-cd/){target=_blank} is a tool to manage kubernetes clusters the GitOPs way

- [Kustomize](https://kustomize.io/){target=_blank} is used to manage the kubernetes manifests within ArgoCD

- [Ansible](https://www.ansible.com/){target=_blank} prepares the machines for the k3s installation and installs k3s

- [Vagrant](https://www.vagrantup.com/){target=_blank} manages the [test environment](testenv.md)

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

## ToDos

- ✅ Backup
    * ✅ Notification on failure
- ✅ Encryption of secrets
    * ✅ Rework documentation
- [ ] Extend [Monitoring](monitoring.md) beyond kube-prometheus-stack defaults
- [ ] Migrate renovate to github actions
