# Update management

The cluster uses two components to manage updates:

- [Renovate](https://github.com/renovatebot/renovate){target=_blank}
- [System Upgrade Controller](https://github.com/rancher/system-upgrade-controller){target=_blank}

## Renovate

Renovate is configured as a scheduled github workflow. Additionally, the action can be run manually with optional increased debug log or in dry-run mode. As RENOVATE_TOKEN a github personal access token is used.

More information about the required token permissions can be found in the official docs: [https://docs.renovatebot.com/modules/platform/github/]/(https://docs.renovatebot.com/modules/platform/github/){target=_blank}. I keep it here because I had a hard time finding thoses information 🙈

### Rules

Multiple configurations in the [renovate.json](https://github.com/Madic-/k3s-git-ops/blob/main/.github/renovate.json){target=_blank} support renovate in finding updates.

**Kubernetes Manifests**

Renovate identifies Kubernetes manifests depending on the file name. The current regex is `apps\/.+\/k8s\\..*\\.yaml$`. Basically beneath the apps folder every yaml file that begins with k8s.

/// danger
Renovate must not change Kubernetes manifests which contain sops encrypted entries because that would break the file signature. Encrypted Kubernetes manifests aren't allowed to begin with **k8s**!
///

## System Upgrade Controller

The System Upgrade Controller is a Kubernetes controller that manages the upgrade of a Kubernetes cluster. There are only plans configured to automatically upgrade the cluster to the latest k3s version.

The update plans are a separate ArgoCD app because they require CRDs from the system-upgrade-controller. Without those CRDs ArgoCD would not be able to apply the plans and the deployment would fail if they are included in the system-upgrade-controller app.
