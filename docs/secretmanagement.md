# Secret Management

In this repository I use two ways to encrypt secrets, both utilizing sops and age.

- Kubernetes Secrets / Manifests are encrypted via [KSOPS](https://github.com/viaduct-ai/kustomize-sops){target=_blank} (described in this document)
- Helm values are encrypted via sops and decrypted by an ArgoCD ConfigManagementPlugin (described in [ArgoCD](argocd#configmanagementplugins)

age is the recommended encryption tool for sops as it is more secure and easier to use than gpg.

## Requirements

- [ksops](https://github.com/viaduct-ai/kustomize-sops){target=_blank}
- [age](https://github.com/FiloSottile/age){target=_blank}

## Preparation

```bash
mkdir -p "$HOME/.config/sops/age"
age-keygen -o "$HOME/.config/sops/age/keys.txt"
age-keygen -o "$HOME/.config/sops/age/argo-cd.txt"
```

This creates two new age keypairs in the folder `$HOME/.config/sops/age/`.

The first is for personal use and the second is for ArgoCD.

```bash
cat "$HOME/.config/sops/age/keys.txt"
# created: 2024-05-28T07:23:28+02:00
# public key: age***
AGE-SECRET-KEY-19***
```

Create the sops-age kubernetes secret for later use in argocd:

```bash
cat "$HOME/.config/sops/age/argo-cd.txt" | kubectl create secret generic sops-age --namespace=argocd \
--from-file=keys.txt=/dev/stdin
```

## ArgoCD configuration

To use sops with ArgoCD, you need to mount ksops and the sops-age key into the argocd-server pod using initContainers. Adjust the helm deployment accordingly:

```yaml
configs:
  cm:
    kustomize.buildOptions: "--enable-helm --enable-alpha-plugins --enable-exec"
repoServer:
  # Use init containers to configure custom tooling
  # https://argoproj.github.io/argo-cd/operator-manual/custom_tools/
  volumes:
    - name: custom-tools
      emptyDir: {}
    - name: sops-age
      secret:
        secretName: sops-age
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
    # ksops packages it's own kustomize binary with ksops integration overrides the argocd kustomize binary
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
    - name: SOPS_AGE_KEY_FILE
      value: /.config/sops/age/keys.txt
```

## Repository configuration

The secrets need to be encrypted with both public keys. The ArgoCD key is used to decrypt the secrets in the ArgoCD cluster and the personal key is used to de- and encrypt the secrets locally.

Create a `.sops.yaml` file in the repository root:

```yaml
creation_rules:
  - path_regex: .*.enc.yaml
    encrypted_regex: "^(data|stringData|email|dnsNames|.*(H|h)osts?|hostname|username|password|url|issuer|clientSecret|argocdServerAdminPassword|oidc.config|commonName|literals)$"
    age: age1d2g7tgqpfvxulsusn3m608h60h2hne7yqwv5nh5nd24z6h0hgq0skjkhw8,age1q522xtgjrmvr43w7um5rh02ta3yfns635680hz4m7uhw0nfqj5zqgxnz27
  - path_regex: secrets/argo-cd.age
    age: age1d2g7tgqpfvxulsusn3m608h60h2hne7yqwv5nh5nd24z6h0hgq0skjkhw8
```

## Usage

To encrypt a secret inplace, use the following command:

```bash
sops -e -i secret.enc.yaml
```

To decrypt a secret inplace, use the following command:

```bash
sops -d -i secret.enc.yaml
```

If you're working with VSCode I can recommend the extension [@signageos/vscode-sops](https://marketplace.visualstudio.com/items?itemName=signageos.signageos-vscode-sops) which automatically decrypts and encrypts secrets on save.

It can also automatically encrypt files which are not yet encrypted. To enable this feature, add the following to your `settings.json`:

```json
{
  "sops.creationEnabled": true
}
```

This will automatically encrypt files which match the `creation_rules` in the `.sops.yaml` file.
