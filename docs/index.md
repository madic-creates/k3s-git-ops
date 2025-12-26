# K3S

/// warning | Warning
This documentation is at a very early stage.
///

This documentation provides an overview of my K3s GitOps home lab repository, explaining the high-level architecture, core principles, and system capabilities. The repository implements a fully automated Kubernetes home lab using GitOps practices with ArgoCD as the deployment orchestrator.

For detailed information about specific subsystems, see GitOps Platform for ArgoCD configuration, Infrastructure Services for foundational components, Authentication & Identity for SSO implementation, Monitoring & Observability for the monitoring stack, Applications for user-facing services, and Backup & Data Protection for data protection strategies.

## GitOps principles

The cluster operates on GitOps principles with ArgoCD serving as the primary deployment controller. All infrastructure and application configurations are stored as code in the GitHub repository, with ArgoCD continuously monitoring for changes and maintaining the desired cluster state.

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

| Feature Category | Components | Key Capabilities |
| --- | --- | --- |
| High Availability | KubeVIP, Traefik, Cilium | HA control plane, load balancing, VIP management, Network Policies |
| Authentication | Authelia, LLDAP | Single Sign-On, LDAP directory, forward authentication |
| Storage | Longhorn | Distributed block storage |
| Monitoring | kube-prometheus-stack, Loki, CheckMK | Metrics collection, log aggregation, alerting |
| Backup | Restic | Cluster backups, application data protection |
| Media Services | Emby, NextPVR | Media streaming, TV recording, transcoding |
| Automation | RenovateBot, Semaphore | Dependency updates, playbook execution |
| Security | SOPS, cert-manager | Secret encryption, TLS certificate management |

## ToDos

- ✅ Backup
    * ✅ Notification on failure
- ✅ Encryption of secrets
    * ✅ Rework documentation
- [ ] Extend [Monitoring](monitoring.md) beyond kube-prometheus-stack defaults
- ✅ Migrate renovate to github actions
