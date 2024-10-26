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

## ConfigManagementPlugins

ArgoCD supports [ConfigManagementPlugins](https://argo-cd.readthedocs.io/en/stable/operator-manual/config-management-plugins/){target=_blank} (CMP). These plugins allow you to extend the functionality of ArgoCD, e.g. by adding a new config management tool.

SOPS, or ksops, don't support encrypting helm valueFiles. But this repository makes heavy use of kustomize rendering helm charts. And these are configured via provided helm values.yaml files. But these files can contain sensitive values. So they are encrypted in this repository as values.enc.yaml. Therefore, I use a CMP to decrypt the files before applying them via kustomize.

If ArgoCD finds a values.enc.yaml in an application directory, argo-cd runs the CMP cmp-sops-decrypt, which decryptes the file to values.yaml, and then runs kustomize.

The ConfigManagementPlugin can be found as an argo-cd app deployment below **apps/argo-cd-configmanagementplugins/** and needs to be run as a sidecar container for the argo-cd repo-server so the deployment of it needs to be extended. The helm values I adjusted:

```yaml
repoServer:
  volumes:
    - name: custom-tools
      emptyDir: {}
    - name: sops-age
      secret:
        secretName: sops-age
    - name: cmp-tmp
      emptyDir: {}
    - name: cmp-sops-plugin
      configMap:
        name: argocd-cmp-sops-plugin
  initContainers:
    - name: install-ksops
      image: viaductoss/ksops:v4.3.1
      command:
        - /bin/sh
        - -c
      args:
        - echo "Installing KSOPS..."; mv ksops /custom-tools/; mv kustomize /custom-tools/; echo "Done.";
      volumeMounts:
        - mountPath: /custom-tools
          name: custom-tools
    - name: install-sops
      image: ghcr.io/getsops/sops:v3.8.1-alpine
      command:
        - /bin/sh
        - -c
      args:
        - echo "Installing SOPS..."; cp /usr/local/bin/sops /custom-tools/; echo "Done.";
      volumeMounts:
        - mountPath: /custom-tools
          name: custom-tools
    - name: install-helm
      image: alpine/helm:3.15.1
      command:
        - /bin/sh
        - -c
      args:
        - echo "Installing helm..."; cp /usr/bin/helm /custom-tools/; echo "Done.";
      volumeMounts:
        - mountPath: /custom-tools
          name: custom-tools
  extraContainers:
    - name: cmp-sops-plugin
      command:
        - "/var/run/argocd/argocd-cmp-server"
      image: alpine:3.20.0
      imagePullPolicy: IfNotPresent
      securityContext:
        runAsNonRoot: true
        runAsUser: 999
      volumeMounts:
        - mountPath: /var/run/argocd
          name: var-files
        - mountPath: /home/argocd/cmp-server/plugins
          name: plugins
        - mountPath: /home/argocd/cmp-server/config/plugin.yaml
          subPath: plugin.yaml
          name: cmp-sops-plugin
        - mountPath: /tmp
          name: cmp-tmp
        - mountPath: /usr/local/bin/kustomize
          name: custom-tools
          subPath: kustomize
        - mountPath: /usr/local/bin/ksops
          name: custom-tools
          subPath: ksops
        - mountPath: /usr/local/bin/sops
          name: custom-tools
          subPath: sops
        - mountPath: /usr/local/bin/helm
          name: custom-tools
          subPath: helm
        - mountPath: /.config/sops/age
          name: sops-age
          readOnly: true
  volumeMounts:
    - mountPath: /usr/local/bin/kustomize
      name: custom-tools
      subPath: kustomize
    - mountPath: /usr/local/bin/ksops
      name: custom-tools
      subPath: ksops
    - mountPath: /.config/sops/age
      name: sops-age
      readOnly: true
```

This basically builds the plugin container with all required tools on-demand.

Of course, the configuration could be way shorter if a container, that already includes the following binaries, would be used 🤷

- kustomize (from ksops)
- ksops
- sops
- helm

More information about my journey to decrypt values.yaml can be found in the following ksops issue on github: [Support kustomize helmCharts valuesFile](https://github.com/viaduct-ai/kustomize-sops/issues/242){target=_blank}.

More about secret management in the [Secret Management](secretmanagement.md) section.

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
