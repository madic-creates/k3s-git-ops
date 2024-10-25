# Installation

## Requriements

- ansible
- kubectl
- helm

## Installation

You need to adapt the ansible vars to your needs. For this, copy the "example.yaml" to e.g. "own.yaml" and use that as ansible extra-vars file.

```shell
cd ansible
cp vars/example.yaml vars/own.yaml
ansible-playbook install.yaml -i inventory --extra-vars "@vars/own.yaml" --diff
```

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

### Ansible variables

The variables are documented in the `ansible/vars/vagrant.yaml` file.

Because they tend to change, I wont document them here.

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

ArgoCD Login Passwort für den Benutzer admin:

```shell
kubectl get secret argocd-initial-admin-secret -n argocd -o jsonpath="{.data.password}" | base64 -d
```

ArgoCD Entfernen

```shell
kubectl kustomize bootstrap --enable-helm | kubectl delete -f -
```
