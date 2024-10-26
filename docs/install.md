# Installation

## Requirements

- git
- ansible
- kubectl
- helm

## Preparation

- Clone this repository

```shell
git clone https://github.com/Madic-/k3s-git-ops
```

- Configure [pre-commit-hooks](pre-commit-hooks.md)
- Adapt ansible vars to your needs. For this, copy the "example.yaml" to e.g. "own.yaml" and use that as ansible extra-vars file.

```shell
cd ansible
cp vars/example.yaml vars/own.yaml
```

A short explanation of the vars can be found in the vars file. Because they tend to change, I wont document them here.

### Ansible inventory

This playbook requires the following host groups:

- k3s_control_plane
- k3s_worker

Example:

```ini
[k3s_control_plane]
k3svm1

[k3s_worker]
k3svm2
k3svm3
```

## Installation

Run ansible from within the ansible folder

```shell
cd ansible
ansible-playbook install.yaml -i inventory --extra-vars "@vars/own.yaml" --diff
```

## Removal

```shell
cd ansible
ansible-playbook remove.yaml -i inventory -l node01.example.com -K --diff
```

## ArgoCD Installieren

```shell
kubectl kustomize apps/argo-cd --enable-helm | kubectl apply -f -
kubectl kustomize apps/overlays/vagrant | kubectl apply -f -
```

Get the ArgoCD Login password for the admin user:

```shell
kubectl get secret argocd-initial-admin-secret -n argocd -o jsonpath="{.data.password}" | base64 -d
```

Remove ArgoCD

```shell
kubectl kustomize bootstrap --enable-helm | kubectl delete -f -
```
