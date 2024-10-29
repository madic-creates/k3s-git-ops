# Secret Management

In this repository I use two ways to encrypt secrets, both utilizing sops and age.

- [Kubernetes Secrets / Manifests are encrypted](#kubernetes-secrets-manifests-via-ksops) via [KSOPS](https://github.com/viaduct-ai/kustomize-sops){target=_blank} (described in this document)
- [Kustomize managed Helm values](#kustomize-managed-helm-values) are encrypted via sops and decrypted by an ArgoCD ConfigManagementPlugin

age is the recommended encryption tool for sops as it is more secure and easier to use than gpg.

## Requirements

- [ksops](https://github.com/viaduct-ai/kustomize-sops){target=_blank}
- [age](https://github.com/FiloSottile/age){target=_blank}

## Preparation

For both variants we need two age keypairs. One for local use and one for ArgoCD.

```shell
# the folder for the age keypairs, consumed by sops
mkdir -p "$HOME/.config/sops/age"
# local age keypair
age-keygen -o "$HOME/.config/sops/age/keys.txt"
# argocd age keypair
age-keygen -o "$HOME/.config/sops/age/argo-cd.txt"
```

Example output:

```shell
cat "$HOME/.config/sops/age/keys.txt"
# created: 2024-05-28T07:23:28+02:00
# public key: age***
AGE-SECRET-KEY-19***
```

ArgoCD needs the private key of the local keypair to decrypt the secrets. So we create a kubernetes secret with the private key that ArgoCD gets mounted.

```shell
cat "$HOME/.config/sops/age/argo-cd.txt" | kubectl create secret generic sops-age --namespace=argocd \
--from-file=keys.txt=/dev/stdin
```

Adjusted helm values to mount the sops-age secret into the argocd-server pod:

```yaml title="values.yaml"
repoServer:
  volumes:
    - name: sops-age
      secret:
        secretName: sops-age
  volumeMounts:
    - mountPath: /.config/sops/age
      name: sops-age
      readOnly: true
  env:
    - name: SOPS_AGE_KEY_FILE
      value: /.config/sops/age/keys.txt
```

## Repository configuration

The secrets need to be encrypted with both public keys. The ArgoCD key is used to decrypt the secrets in the ArgoCD cluster and the local key is used to de- and encrypt the secrets locally.

Create a `.sops.yaml` file in the repository root. Example:

```yaml title=".sops.yaml"
creation_rules:
  - path_regex: .*.enc.yaml
    encrypted_regex: "^(data|stringData|email|dnsNames|.*(H|h)osts?|hostname|username|password|url|issuer|clientSecret|argocdServerAdminPassword|oidc.config|commonName|literals)$"
    age: age1d2g7tgqpfvxulsusn3m608h60h2hne7yqwv5nh5nd24z6h0hgq0skjkhw8,age1q522xtgjrmvr43w7um5rh02ta3yfns635680hz4m7uhw0nfqj5zqgxnz27
  - path_regex: secrets/argo-cd.age
    age: age1d2g7tgqpfvxulsusn3m608h60h2hne7yqwv5nh5nd24z6h0hgq0skjkhw8
```

The .sops file for this repository differs from this example.

## Kubernetes Secrets / Manifests via KSOPS

To use sops with ArgoCD, you need to mount ksops and the sops-age key into the argocd-server pod. The following helm values start the ksops container as an initContainer, copies the ksops and kustomize binaries into the custom-tools volume and mounts the binaries from the custom-tools volume into the repo server container. The sops-age key is also mounted into the repo server container.


```yaml title="values.yaml"
configs:
  cm:
    kustomize.buildOptions: "--enable-helm --enable-alpha-plugins --enable-exec"
repoServer:
  # Use init containers to configure custom tooling
  # https://argoproj.github.io/argo-cd/operator-manual/custom_tools/
  volumes:
    - name: custom-tools
      emptyDir: {}
  initContainers:
    - name: install-ksops
      image: viaductoss/ksops:v4.3.1
      command: ["/bin/sh", "-c"]
      args:
        - echo "Installing KSOPS...";
          mv ksops /custom-tools/;
          mv kustomize /custom-tools/;
          echo "Done.";
      volumeMounts:
        - mountPath: /custom-tools
          name: custom-tools
  volumeMounts:
    # ksops packages it's own kustomize binary with ksops integration, overrides the argocd kustomize binary
    - mountPath: /usr/local/bin/kustomize
      name: custom-tools
      subPath: kustomize
    - mountPath: /usr/local/bin/ksops
      name: custom-tools
      subPath: ksops
    - mountPath: /.config/sops/age
      name: sops-age
  env:
    - name: XDG_CONFIG_HOME
      value: /.config
```

### Adjusting kustomize configuration

To tell kustomize to use ksops for decryption, we need to add a **generators** configuration to the `kustomization.yaml` file:

```yaml title="kustomization.yaml"
generators:
  - kustomize-secret-generator.yaml
```

```yaml title="kustomize-secret-generator.yaml"
---
apiVersion: viaduct.ai/v1
kind: ksops
metadata:
  name: secret-generator
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: ksops
files:
  - backup-secrets.enc.yaml
```

The **backup-secrets.enc.yaml** is just a normal kubernetes manifest but with sops encrypted values: [Example emby backup-secrets.enc.yaml](https://github.com/Madic-/k3s-git-ops/blob/main/apps/emby/backup-secrets.enc.yaml){target=_blank}. Do not add thouse manifests to the resource list of the kustomization.yaml because this would result in a duplicate resource error.

## Kustomize managed Helm values

This repository makes heavy use of kustomize rendering helm charts. Kustomize can manage helm values either directly in the kustomization.yaml or in a separate file. The helm values can contain sensitive values and it's not possible to encrypt values in the kustomization.yaml file directly so we need to use a separate helm values file. The workflow basically is to create an encrypted **values.enc.yaml**, tell kustomize to get the helm values from **values.yaml** and use an ArgoCD ConfigManagementPlugin to decrypt the **values.enc.yaml** to values.yaml. The ConfigManagementPlugin gets executed when ArgoCD finds a values.enc.yaml before kustomize renders the kubernetes manifests.

### Configuration of the ConfigManagementPlugin

If ArgoCD finds a values.enc.yaml in an application directory, argo-cd runs the CMP cmp-sops-decrypt, which decryptes the file to values.yaml, and then runs kustomize.

The ConfigManagementPlugin is configured as a separate [argo-cd app deployment](https://github.com/Madic-/k3s-git-ops/tree/main/apps/argo-cd-configmanagementplugins){target=_blank} and needs to be run as a sidecar container for the argo-cd repo-server so the deployment of it needs to be extended. The helm values I adjusted:

```yaml title="values.yaml"
repoServer:
  volumes:
    - name: custom-tools
      emptyDir: {}
    - name: cmp-tmp
      emptyDir: {}
    - name: cmp-sops-plugin
      configMap:
        name: argocd-cmp-sops-plugin
  initContainers:
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

## En- and decrypting helm values and manifests

To encrypt a file inplace, use the following command:

```shell
sops -e -i secret.enc.yaml
```

To decrypt a file inplace, use the following command:

```shell
sops -d -i secret.enc.yaml
```

If you're working with VSCode I can recommend the extension [@signageos/vscode-sops](https://marketplace.visualstudio.com/items?itemName=signageos.signageos-vscode-sops){target=_blank} which automatically decrypts and encrypts secrets on save.

It can also automatically encrypt files which are not yet encrypted. To enable this feature, add the following to your `settings.json`:

```json
{
  "sops.creationEnabled": true
}
```

This will automatically encrypt files which match the `creation_rules` in the `.sops.yaml` file.
