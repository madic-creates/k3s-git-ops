# ArgoCD

ArgoCD serves as the central control plane for the entire cluster, implementing a pull-based deployment model where Argo CD monitors the Git repository and synchronizes the cluster state to match the declared configuration.

## Dependencies

ArgoCD does not support "real" dependencies. Therefore, I use the ArgoCD feature [sync waves](https://argo-cd.readthedocs.io/en/stable/user-guide/sync-waves/){target=_blank}. Sync waves determine the order in which ArgoCD apps are installed.

Example:
In my case argo-cd requires a Configuration Management Plugin to be installed first. Without the Configurationmanagementplugin, the argo-cd repo-server container won't start.

| Sync Wave Range | Purpose | Examples |
| -- | -- | -- |
| 0-5 | Infrastructure Foundation | kubevip-ha, cert-manager |
| 8-15 | Core Services | mariadb, redis, longhorn |
| 12-17 | Network & DNS | pihole, traefik |
| 16-17 | Monitoring | monitoring, loki |
| 20-60 | Applications | emby, nextpvr, semaphore |
| 96-99 | GitOps Platform | argo-cd, argo-cd-apps |

All Argo CD applications are defined in the [apps/argo-cd-apps](https://github.com/madic-creates/k3s-git-ops/tree/main/apps/argo-cd-apps){target=_blank} directory where the argo-cd-apps application manages all individual application definitions.

``` mermaid
graph LR
  A[Start] --> B{Error?};
  B -->|Yes| C[Hmm...];
  C --> D[Debug];
  D --> B;
  B ---->|No| E[Yay!];
```

## Getting ArgoCD admin password

This shouldn't be necessary anymore because the helm chart supports setting the password value. But I will leave it here for reference.

```shell
kubectl patch secret -n argocd argocd-secret -p '{"stringData": { "admin.password": "'$(htpasswd -bnBC 10 "" ${ADMINPASSWORD} | tr -d ':\n')'"}}'
```

### ConfigManagementPlugin

ArgoCD supports [ConfigManagementPlugins](https://argo-cd.readthedocs.io/en/stable/operator-manual/config-management-plugins/){target=_blank} (CMP). These plugins allow you to extend the functionality of ArgoCD, e.g. by adding a new config management tool. In my case I use a CMP to encrypt helm values before kustomize is applied. See [Secret Management](secretmanagement.md#configuration-of-the-configmanagementplugin) for more information.

## Ignoring values

The [`ignoreDifferences`](https://argo-cd.readthedocs.io/en/stable/user-guide/diffing/){target=_blank} field is a powerful feature in Argo CD Application manifests that allows you to specify certain fields in your Kubernetes resources that should be ignored during ArgoCDs comparison between the live state and desired state defined in the Git repository. I use it to tell ArgoCD to e.g. ignore the number of replicas from a deployment or ignore the caBundle value from kube-prometheus-stack webhooks.

### Detailed Explanation:

```yaml
  ignoreDifferences:
    - group: apps
      kind: Deployment
      name: emby
      namespace: media
      jsonPointers:
        - /spec/replicas
```

- **group**: Specifies the API group of the resource

- **kind**: The type of Kubernetes resource to which this rule applies. Here, it is `Deployment`.

- **name**: The name of the specific resource to which the ignore rule applies, in this case, `emby`.

- **namespace**: The namespace in which the resource resides. This indicates where the `Deployment` object `emby` is located.

- **jsonPointers**: Lists specific JSON paths within the resource's manifest to ignore during synchronization. JSON Pointers are a way to reference specific parts of a JSON object. In this example, `- /spec/replicas` tells Argo CD to ignore differences in the `replicas` field of the `Deployment` specification.

### Practical Implications:

Ignoring differences specified in the `jsonPointers` allows certain dynamic fields within Kubernetes resources to change (e.g., the number of replicas during autoscaling) without triggering an unwanted sync or causing the application to appear out of sync in Argo CD. This is particularly useful for configurations where you want Argo CD to overlook certain operational details that are managed externally or dynamically, rather than being strictly controlled through GitOps.
