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
