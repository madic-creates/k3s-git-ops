# ArgoCD

## Dependencies

ArgoCD does not support "real" dependencies. Therefore, I use the ArgoCD feature [sync waves](https://argo-cd.readthedocs.io/en/stable/user-guide/sync-waves/){target=_blank}. Sync waves determine the order in which ArgoCD apps are installed.

Example:
In my case argo-cd requires a Configuration Management Plugin to be installed first. Without the Configurationmanagementplugin, the argo-cd repo-server container won't start.

## Setting the ArgoCD admin password

This shouldn't be necessary anymore because the helm chart supports setting the password value.

But I will leave it here for reference.

```shell
kubectl patch secret -n argocd argocd-secret -p '{"stringData": { "admin.password": "'$(htpasswd -bnBC 10 "" ${ADMINPASSWORD} | tr -d ':\n')'"}}'
```

### ConfigManagementPlugin

ArgoCD supports [ConfigManagementPlugins](https://argo-cd.readthedocs.io/en/stable/operator-manual/config-management-plugins/){target=_blank} (CMP). These plugins allow you to extend the functionality of ArgoCD, e.g. by adding a new config management tool. In my case I use a CMP to encrypt helm values before kustomize is applied. See [Secret Management](secretmanagement.md#configuration-of-the-ConfigManagementPlugin) for more information.

## Ignoring values

The [`ignoreDifferences`](https://argo-cd.readthedocs.io/en/stable/user-guide/diffing/){target=_blank} field is a powerful feature in Argo CD Application manifests that allows you to specify certain fields in your Kubernetes resources that should be ignored during the comparison between the live state and the desired state defined in the Git repository. I use it to tell Argo CD to e.g. ignore the number of replicas from a deployment or ignore the caBundle value from kube-prometheus-stack webhooks.

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
