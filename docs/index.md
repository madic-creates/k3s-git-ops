# K3S

## Important used technologies

- [K3S](https://k3s.io/)
This is the foundation of the infrastructure. It's an easy to deploy and maintain kubernetes distribution

- [ArgoCD](https://argoproj.github.io/argo-cd/) is a tool to manage kubernetes clusters the GitOPs way

- [Kustomize](https://kustomize.io/) is used to manage the kubernetes manifests within ArgoCD

- [Ansible](https://www.ansible.com/) prepares the machines for the k3s installation and installs k3s

- [Vagrant](https://www.vagrantup.com/) manages the [test environment](testenv.md)
