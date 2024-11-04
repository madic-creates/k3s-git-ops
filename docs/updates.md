# Update management

The cluster uses two components to manage updates:

- [renovatebot](https://github.com/renovatebot/renovate){target=_blank}
- [System Upgrade Controller](https://github.com/rancher/system-upgrade-controller){target=_blank}

## Renovatebot

Renovate is configured as a scheduled github workflow. Additionally, the action can be run manually with optional increased debug log or in dry-run mode. As RENOVATE_TOKEN a github personal access token is used.

More information about the required token permissions can be found in the official docs: [https://docs.renovatebot.com/modules/platform/github/]/(https://docs.renovatebot.com/modules/platform/github/){target=_blank}. I keep it here because I had a hard time finding thoses information 🙈

## System Upgrade Controller

The System Upgrade Controller is a Kubernetes controller that manages the upgrade of a Kubernetes cluster. There are only plans configured to automatically upgrade the cluster to the latest k3s version.

The update plans are a separate ArgoCD app because they require CRDs from the system-upgrade-controller. Without those CRDs ArgoCD would not be able to apply the plans and the deployment would fail if they are included in the system-upgrade-controller app.
