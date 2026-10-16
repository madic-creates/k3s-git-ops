# Open WebUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy a multi-user Open WebUI in front of the existing llama-cpp deployment, authenticated via Authelia OIDC, with transparent Tavily web search for IDE clients.

**Architecture:** New ArgoCD application at sync wave 66 using the official open-webui Helm chart consumed via Kustomize `helmCharts` (Paperless pattern). SQLite on a Longhorn PVC, single replica, `strategy: Recreate`. Standard Ingress on `chat.internal.neese-web.de` with the hostname encrypted via SOPS. CiliumNetworkPolicy with default-deny plus targeted allows to llama-cpp and `world:443`.

**Tech Stack:** k3s 1.31.4, ArgoCD, Kustomize + Helm, Cilium, Longhorn, Traefik, Authelia OIDC, SOPS + age, ksops, Open WebUI Helm chart 14.6.0 (app 0.9.5).

**Spec:** `docs/superpowers/specs/2026-05-20-open-webui-design.md`

> **Note on TDD:** This is infrastructure-as-code; there are no unit tests in the traditional sense. The "test" equivalent for each meaningful unit of work is `kustomize build --enable-helm --enable-alpha-plugins --enable-exec` plus `scripts/kubeconform-validate.sh` and `pre-commit run`. Verification steps are folded into the tasks accordingly.

---

## Task 1: Generate secrets and gather inputs

**Files:** none (interactive prep; values are consumed by Tasks 4 and 8)

This task is not committable. Gather everything up front so secrets never get pasted into chat or commit messages.

- [ ] **Step 1: Obtain Tavily API key**

  Sign in to https://app.tavily.com → API Keys → copy a key (starts with `tvly-`). Store privately for Task 4.

- [ ] **Step 2: Generate the Authelia OIDC client secret**

  Run:
  ```bash
  authelia crypto hash generate pbkdf2 --variant sha512 --iterations 310000 --random
  ```
  Capture both lines from the output:
  - `Password: <plaintext>` — used in Task 4 (`OAUTH_CLIENT_SECRET`)
  - `Digest: $pbkdf2-sha512$310000$...` — used in Task 8 (Authelia values)

- [ ] **Step 3: Generate `WEBUI_SECRET_KEY`**

  Run:
  ```bash
  openssl rand -hex 32
  ```
  Store privately for Task 4. Rotating this value later logs every user out.

- [ ] **Step 4: Copy the existing llama-cpp API key**

  ```bash
  sops --config .sops.yaml -d apps/llama-cpp/secrets.enc.yaml
  ```
  Copy the `API_KEY:` value. Open WebUI calls llama-cpp with this same key. Store privately for Task 4.

---

## Task 2: Create namespace and default-deny network policy

**Files:**
- Create: `apps/open-webui/namespace.yaml`
- Create: `apps/open-webui/k8s.np.open-webui-default-deny.yaml`

- [ ] **Step 1: Create the namespace**

  Write `apps/open-webui/namespace.yaml`:
  ```yaml
  ---
  apiVersion: v1
  kind: Namespace
  metadata:
    name: open-webui
    labels:
      goldilocks.fairwinds.com/enabled: "true"
  ```

- [ ] **Step 2: Create the default-deny CiliumNetworkPolicy**

  Write `apps/open-webui/k8s.np.open-webui-default-deny.yaml`:
  ```yaml
  ---
  # Default Deny Network Policy for namespace.
  # Selecting all pods (empty endpointSelector) puts them under Cilium's
  # default-deny semantics; the targeted policy in k8s.np.open-webui.yaml
  # adds the specific allows we need.
  apiVersion: cilium.io/v2
  kind: CiliumNetworkPolicy
  metadata:
    name: open-webui-default-deny
    namespace: open-webui
  spec:
    endpointSelector: {}
    ingress:
      - {}
    egress:
      - {}
  ```

---

## Task 3: Create the application-specific network policy

**Files:**
- Create: `apps/open-webui/k8s.np.open-webui.yaml`

- [ ] **Step 1: Write the network policy**

  ```yaml
  ---
  # Open WebUI application-specific network policy.
  # Selecting the pod via endpointSelector puts it under Cilium default-deny;
  # only explicitly allowed traffic passes.
  apiVersion: cilium.io/v2
  kind: CiliumNetworkPolicy
  metadata:
    name: open-webui
    namespace: open-webui
  spec:
    endpointSelector:
      matchLabels:
        app.kubernetes.io/name: open-webui
    ingress:
      # Traefik forwards browser + IDE-API traffic.
      - fromEndpoints:
          - matchLabels:
              k8s:app.kubernetes.io/instance: traefik-kube-system
              k8s:io.kubernetes.pod.namespace: kube-system
        toPorts:
          - ports:
              - port: "8080"
                protocol: TCP
      # Same-namespace traffic (probes, helm post-install hooks).
      - fromEndpoints:
          - matchLabels:
              k8s:io.kubernetes.pod.namespace: open-webui
    egress:
      # DNS via CoreDNS.
      - toEndpoints:
          - matchLabels:
              k8s:io.cilium.k8s.namespace.labels.kubernetes.io/metadata.name: kube-system
              k8s:io.kubernetes.pod.namespace: kube-system
              k8s:k8s-app: kube-dns
        toPorts:
          - ports:
              - port: "53"
                protocol: UDP
              - port: "53"
                protocol: TCP
      # llama-cpp backend (in-cluster).
      - toEndpoints:
          - matchLabels:
              k8s:app: llama-cpp
              k8s:io.kubernetes.pod.namespace: llama-cpp
        toPorts:
          - ports:
              - port: "8080"
                protocol: TCP
      # External HTTPS: Tavily API + Authelia OIDC discovery (the issuer URL
      # is the public hostname and must resolve via the external route, same
      # pattern Argo CD uses for its OIDC integration).
      - toEntities:
          - world
        toPorts:
          - ports:
              - port: "443"
                protocol: TCP
  ```

---

## Task 4: Create the SOPS-encrypted env secret

**Files:**
- Create: `apps/open-webui/secrets.enc.yaml` (encrypted)

The Read tool is blocked on `*.enc.yaml` paths inside the repo. Use the `/tmp` workflow from CLAUDE.md to author the plaintext, then encrypt into the repo.

- [ ] **Step 1: Write the plaintext secret to `/tmp`**

  ```bash
  cat > /tmp/open-webui-secrets.enc.yaml <<'EOF'
  ---
  apiVersion: v1
  kind: Secret
  metadata:
    name: open-webui-env
    namespace: open-webui
  type: Opaque
  stringData:
    # Auth / OIDC
    WEBUI_AUTH: "true"
    ENABLE_SIGNUP: "false"
    ENABLE_OAUTH_SIGNUP: "true"
    OAUTH_PROVIDER_NAME: "Authelia"
    OAUTH_CLIENT_ID: "openwebui"
    OAUTH_CLIENT_SECRET: "<PASTE_AUTHELIA_CLIENT_SECRET_PLAIN_FROM_TASK_1_STEP_2>"
    OPENID_PROVIDER_URL: "https://auth.k8s.neese-web.de/.well-known/openid-configuration"
    OPENID_REDIRECT_URI: "https://chat.internal.neese-web.de/oauth/oidc/callback"
    OAUTH_SCOPES: "openid email profile groups"
    OAUTH_MERGE_ACCOUNTS_BY_EMAIL: "true"
    OAUTH_UPDATE_PICTURE_ON_LOGIN: "true"
    DEFAULT_USER_ROLE: "pending"

    # Session signing
    WEBUI_SECRET_KEY: "<PASTE_OPENSSL_RAND_HEX_32_FROM_TASK_1_STEP_3>"

    # llama-cpp backend
    ENABLE_OLLAMA_API: "false"
    OPENAI_API_BASE_URLS: "http://llama-cpp.llama-cpp.svc.cluster.local:8080/v1"
    OPENAI_API_KEYS: "<PASTE_LLAMA_CPP_API_KEY_FROM_TASK_1_STEP_4>"

    # Web search via Tavily
    ENABLE_RAG_WEB_SEARCH: "true"
    RAG_WEB_SEARCH_ENGINE: "tavily"
    TAVILY_API_KEY: "<PASTE_TAVILY_API_KEY_FROM_TASK_1_STEP_1>"
    RAG_WEB_SEARCH_RESULT_COUNT: "5"
    RAG_WEB_SEARCH_CONCURRENT_REQUESTS: "10"
    RAG_WEB_SEARCH_TRUST_ENV: "false"

    # Skip embedding/retrieval — Tavily snippets go directly into context
    BYPASS_EMBEDDING_AND_RETRIEVAL: "true"
  EOF
  ```

  Then open `/tmp/open-webui-secrets.enc.yaml` in an editor and replace the four `<PASTE_*>` placeholders with the actual values gathered in Task 1.

- [ ] **Step 2: Encrypt to the repo path**

  ```bash
  sops --config .sops.yaml -e /tmp/open-webui-secrets.enc.yaml > apps/open-webui/secrets.enc.yaml
  ```

- [ ] **Step 3: Verify round-trip**

  ```bash
  sops --config .sops.yaml -d apps/open-webui/secrets.enc.yaml | diff - /tmp/open-webui-secrets.enc.yaml
  ```
  Expected: no output (files identical).

- [ ] **Step 4: Wipe the plaintext temp file**

  ```bash
  : > /tmp/open-webui-secrets.enc.yaml
  ```
  `rm` is not allowlisted; truncate-in-place is sufficient.

---

## Task 5: Create the encrypted Ingress

**Files:**
- Create: `apps/open-webui/ingress.enc.yaml` (encrypted)

- [ ] **Step 1: Write the plaintext ingress to `/tmp`**

  ```bash
  cat > /tmp/open-webui-ingress.enc.yaml <<'EOF'
  ---
  apiVersion: networking.k8s.io/v1
  kind: Ingress
  metadata:
    name: open-webui
    namespace: open-webui
    annotations:
      traefik.ingress.kubernetes.io/router.entrypoints: web239,websecure239
      traefik.ingress.kubernetes.io/router.tls: "true"
      traefik.ingress.kubernetes.io/router.middlewares: traefik-redirect@kubernetescrd
  spec:
    ingressClassName: traefik
    tls:
      - hosts:
          - chat.internal.neese-web.de
        secretName: wildcard-cloudflare-production-02
    rules:
      - host: chat.internal.neese-web.de
        http:
          paths:
            - path: /
              pathType: Prefix
              backend:
                service:
                  name: open-webui
                  port:
                    number: 80
  EOF
  ```

  The service port `80` matches the chart default (Service port 80 → containerPort 8080).

- [ ] **Step 2: Encrypt and verify**

  ```bash
  sops --config .sops.yaml -e /tmp/open-webui-ingress.enc.yaml > apps/open-webui/ingress.enc.yaml
  sops --config .sops.yaml -d apps/open-webui/ingress.enc.yaml | diff - /tmp/open-webui-ingress.enc.yaml
  : > /tmp/open-webui-ingress.enc.yaml
  ```
  Expected: diff produces no output.

---

## Task 6: Create the ksops generator and Kustomize Helm chart

**Files:**
- Create: `apps/open-webui/kustomize-secret-generator.yaml`
- Create: `apps/open-webui/kustomization.yaml`

- [ ] **Step 1: Write the ksops generator**

  ```yaml
  ---
  apiVersion: viaduct.ai/v1
  kind: ksops
  metadata:
    name: open-webui-secret-generator
    annotations:
      config.kubernetes.io/function: |
        exec:
          path: ksops
  files:
    - secrets.enc.yaml
    - ingress.enc.yaml
  ```

- [ ] **Step 2: Write the kustomization**

  ```yaml
  ---
  namespace: open-webui

  resources:
    - namespace.yaml
    - k8s.np.open-webui-default-deny.yaml
    - k8s.np.open-webui.yaml

  # ksops decrypts secrets.enc.yaml and ingress.enc.yaml at build time and
  # injects the resources as if they were regular resources. The encrypted
  # files are intentionally NOT in `resources:`.
  generators:
    - kustomize-secret-generator.yaml

  commonAnnotations:
    argocd.argoproj.io/sync-wave: "66"

  helmCharts:
    - name: open-webui
      repo: https://helm.openwebui.com/
      version: 14.6.0
      releaseName: open-webui
      namespace: open-webui
      includeCRDs: false
      valuesInline:
        ollama:
          enabled: false
        enableOpenaiApi: true
        ingress:
          enabled: false
        persistence:
          enabled: true
          size: 10Gi
          storageClass: longhorn
          accessModes:
            - ReadWriteOnce
        replicaCount: 1
        strategy:
          type: Recreate
        service:
          type: ClusterIP
          port: 80
        extraEnvFrom:
          - secretRef:
              name: open-webui-env
  ```

---

## Task 7: Verify the kustomization builds and validates

**Files:** none (verification only)

- [ ] **Step 1: Build the kustomization**

  ```bash
  kustomize build apps/open-webui --enable-helm --enable-alpha-plugins --enable-exec > /tmp/open-webui-rendered.yaml
  ```
  Expected: zero errors.

- [ ] **Step 2: Spot-check the rendered manifests**

  ```bash
  grep -E '^(apiVersion|kind|  name):' /tmp/open-webui-rendered.yaml | head -40
  ```
  Expected to see at minimum: Namespace `open-webui`, two CiliumNetworkPolicy (`open-webui-default-deny`, `open-webui`), Secret `open-webui-env`, Ingress `open-webui`, Deployment `open-webui`, Service `open-webui`, PersistentVolumeClaim.

- [ ] **Step 3: Confirm the Deployment label matches the NetworkPolicy selector**

  ```bash
  yq 'select(.kind == "Deployment") | .spec.template.metadata.labels' /tmp/open-webui-rendered.yaml
  ```
  Expected: includes `app.kubernetes.io/name: open-webui`. If the chart uses a different label, update `k8s.np.open-webui.yaml`'s `endpointSelector.matchLabels` to match the actual label, then re-run Step 1 and Step 3.

- [ ] **Step 4: Schema validation**

  ```bash
  scripts/kubeconform-validate.sh apps/open-webui
  ```
  Expected: zero errors.

- [ ] **Step 5: Confirm the Secret was substituted from SOPS**

  ```bash
  yq 'select(.kind == "Secret" and .metadata.name == "open-webui-env") | .data | keys' /tmp/open-webui-rendered.yaml
  ```
  Expected: the list of env var names (base64-encoded values, not the `<PASTE_*>` placeholders).

- [ ] **Step 6: Clean up**

  ```bash
  : > /tmp/open-webui-rendered.yaml
  ```

---

## Task 8: Register the OIDC client in Authelia

**Files:**
- Modify: `apps/authelia/values.enc.yaml` (encrypted)

- [ ] **Step 1: Decrypt Authelia values to `/tmp`**

  ```bash
  sops --config .sops.yaml -d apps/authelia/values.enc.yaml > /tmp/authelia-values.enc.yaml
  ```

- [ ] **Step 2: Append the openwebui client block**

  Open `/tmp/authelia-values.enc.yaml`. Locate the existing list `identity_providers.oidc.clients:` (existing entries include `argocd`, `grafana`, `semaphore`). Append the following as a new list item, matching the surrounding indentation **exactly** (the existing entries are indented 16 spaces; the YAML below assumes that — adjust if the file uses different indentation):

  ```yaml
              - client_id: openwebui
                client_name: Open WebUI
                client_secret: <PASTE_PBKDF2_DIGEST_FROM_TASK_1_STEP_2>
                claims_policy: default_claims_policy
                redirect_uris:
                  - https://chat.internal.neese-web.de/oauth/oidc/callback
                scopes:
                  - openid
                  - profile
                  - email
                  - groups
                grant_types:
                  - authorization_code
                response_types:
                  - code
                authorization_policy: two_factor
                token_endpoint_auth_method: client_secret_post
  ```

  Substitute the `<PASTE_PBKDF2_DIGEST_*>` placeholder with the **Digest** value (the `$pbkdf2-sha512$...` string), **not** the plaintext.

- [ ] **Step 3: Re-encrypt and verify**

  ```bash
  sops --config .sops.yaml -e /tmp/authelia-values.enc.yaml > apps/authelia/values.enc.yaml
  sops --config .sops.yaml -d apps/authelia/values.enc.yaml | grep -A 2 "client_id: openwebui"
  ```
  Expected: the new client block appears in the decrypted output.

- [ ] **Step 4: Confirm Authelia still builds**

  ```bash
  kustomize build apps/authelia --enable-helm --enable-alpha-plugins --enable-exec > /dev/null
  ```
  Expected: zero errors.

- [ ] **Step 5: Wipe plaintext temp file**

  ```bash
  : > /tmp/authelia-values.enc.yaml
  ```

---

## Task 9: Write the WEBSEARCH.md runbook

**Files:**
- Create: `apps/open-webui/WEBSEARCH.md`

- [ ] **Step 1: Write the runbook**

  Save the following as `apps/open-webui/WEBSEARCH.md` (content is in English per the repo convention):

  ````markdown
  # Open WebUI — Web Search Setup

  This document explains the web-search wiring in this app and the post-deploy
  steps required to expose it transparently to IDE clients (Continue.dev) via
  the OpenAI-compatible API.

  ## Backend wiring

  Open WebUI uses Tavily as the search backend. Configuration lives in the SOPS
  secret `open-webui-env`:

  - `ENABLE_RAG_WEB_SEARCH=true` — enables the web-search feature.
  - `RAG_WEB_SEARCH_ENGINE=tavily` — selects Tavily as the provider.
  - `TAVILY_API_KEY` — bearer key from https://app.tavily.com.
  - `RAG_WEB_SEARCH_RESULT_COUNT=5` — snippets fetched per query.
  - `RAG_WEB_SEARCH_CONCURRENT_REQUESTS=10` — parallel fetches.

  ## Embedding bypass

  `BYPASS_EMBEDDING_AND_RETRIEVAL=true` disables the embedding/retrieval layer
  inside Open WebUI. Tavily snippets are inserted into the prompt context
  verbatim, with no semantic chunking. This keeps the inference pipeline simple
  and avoids running an embedding model on CPU (llama-cpp owns the GPU
  exclusively and does not serve embeddings).

  If you later want RAG over uploaded documents, the bypass must be turned off
  and an embedding model deployed separately (e.g., a second llama.cpp instance
  with `--embedding`, or a dedicated embedding service). That is out of scope
  for the current setup.

  ## Virtual model `qwen2.5-coder-websearch`

  Open WebUI does not enable web search per request by default. To make web
  search transparent for IDE clients, create a virtual model with web search
  toggled on:

  1. Log in to https://chat.internal.neese-web.de as an admin user.
  2. Workspace → Models → "Create a New Model".
  3. Base Model: `qwen2.5-coder-7b` (exposed by the llama-cpp backend).
  4. Model ID: `qwen2.5-coder-websearch`.
  5. Capabilities → Web Search: **enabled**.
  6. Save.

  IDE clients target this virtual model. Every request goes through the Tavily
  lookup automatically; the client does not need to know about web search.

  > **Note:** this virtual model lives in the SQLite database on the Longhorn
  > PVC. It is not represented in Git. If the PVC is destroyed, recreate the
  > model by following these steps.

  ## IDE client setup (Continue.dev example)

  Open WebUI → Settings → Account → API Keys → "Create new key".

  In `~/.continue/config.json`:

  ```jsonc
  "models": [{
    "title": "qwen-websearch",
    "provider": "openai",
    "model": "qwen2.5-coder-websearch",
    "apiBase": "https://chat.internal.neese-web.de/api",
    "apiKey": "sk-<your personal Open WebUI key>"
  }]
  ```

  ## Tavily Free tier

  The Free plan covers 1000 calls/month. Track usage in the Tavily dashboard.
  If you approach the limit, lower `RAG_WEB_SEARCH_RESULT_COUNT` and
  `RAG_WEB_SEARCH_CONCURRENT_REQUESTS`, or upgrade the plan.
  ````

---

## Task 10: Commit the app folder + Authelia update

**Files staged:** `apps/open-webui/**`, `apps/authelia/values.enc.yaml`

- [ ] **Step 1: Stage**

  ```bash
  git add apps/open-webui apps/authelia/values.enc.yaml
  ```

- [ ] **Step 2: Run pre-commit hooks**

  ```bash
  pre-commit run --files \
    apps/open-webui/namespace.yaml \
    apps/open-webui/kustomization.yaml \
    apps/open-webui/kustomize-secret-generator.yaml \
    apps/open-webui/k8s.np.open-webui-default-deny.yaml \
    apps/open-webui/k8s.np.open-webui.yaml \
    apps/open-webui/secrets.enc.yaml \
    apps/open-webui/ingress.enc.yaml \
    apps/open-webui/WEBSEARCH.md \
    apps/authelia/values.enc.yaml
  ```
  If any hook auto-fixes whitespace or formatting, re-stage and rerun until clean.

- [ ] **Step 3: Commit**

  ```bash
  git commit -m "$(cat <<'EOF'
  feat(open-webui): add Helm-based deployment with Authelia OIDC and Tavily websearch

  Sync wave 66, single-replica, SQLite on Longhorn PVC, Ingress on
  chat.internal.neese-web.de. OIDC client `openwebui` appended to Authelia.
  See apps/open-webui/WEBSEARCH.md for the post-deploy virtual-model steps.

  Spec: docs/superpowers/specs/2026-05-20-open-webui-design.md

  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

---

## Task 11: Create the ArgoCD Application manifest

**Files:**
- Create: `apps/argo-cd-apps/66-open-webui.yaml`

- [ ] **Step 1: Write the Application**

  ```yaml
  ---
  apiVersion: argoproj.io/v1alpha1
  kind: Application
  metadata:
    name: 66-open-webui
    namespace: argocd
    finalizers:
      - resources-finalizer.argocd.argoproj.io
    annotations:
      argocd.argoproj.io/sync-wave: "66"
  spec:
    project: default
    source:
      repoURL: "example.com"
      path: apps/open-webui
      targetRevision: HEAD
    destination:
      server: "https://kubernetes.default.svc"
      namespace: default
    syncPolicy:
      syncOptions:
        - ApplyOutOfSyncOnly=true
      automated:
        prune: true
        selfHeal: true
  ```

  The placeholder `repoURL: "example.com"` is rewritten by the existing Kustomize replacement in `apps/argo-cd-apps/kustomization.yaml` (sourced from the encrypted `argocd-repo-url-public` Secret).

---

## Task 12: Register the Application in the argo-cd-apps kustomization

**Files:**
- Modify: `apps/argo-cd-apps/kustomization.yaml`

- [ ] **Step 1: Add the new resource entry**

  Open `apps/argo-cd-apps/kustomization.yaml`. In the `resources:` list, locate the last wave-65 entry (`- 65-unifi-controller.yaml`). Insert the new entry on its own line **before** `- 70-descheduler.yaml`:

  ```yaml
    - 66-open-webui.yaml
  ```

  Resulting context should look like:
  ```yaml
    - 65-unifi-controller.yaml
    #- 65-prox01-watchtower.yaml
    #- 65-wardrowbe.yaml
    - 66-open-webui.yaml
    - 70-descheduler.yaml
  ```

- [ ] **Step 2: Verify argo-cd-apps still builds**

  ```bash
  kustomize build apps/argo-cd-apps --enable-helm --enable-alpha-plugins --enable-exec > /tmp/argocd-apps-rendered.yaml
  ```
  Expected: zero errors.

- [ ] **Step 3: Confirm the repoURL replacement targets the new Application**

  ```bash
  yq 'select(.metadata.name == "66-open-webui") | .spec.source.repoURL' /tmp/argocd-apps-rendered.yaml
  ```
  Expected: a real git URL (not the literal string `example.com`).

- [ ] **Step 4: Clean up**

  ```bash
  : > /tmp/argocd-apps-rendered.yaml
  ```

---

## Task 13: Commit the ArgoCD Application registration

**Files staged:** `apps/argo-cd-apps/66-open-webui.yaml`, `apps/argo-cd-apps/kustomization.yaml`

- [ ] **Step 1: Stage, lint, commit**

  ```bash
  git add apps/argo-cd-apps/66-open-webui.yaml apps/argo-cd-apps/kustomization.yaml
  pre-commit run --files apps/argo-cd-apps/66-open-webui.yaml apps/argo-cd-apps/kustomization.yaml
  git commit -m "$(cat <<'EOF'
  feat(argo-cd-apps): deploy open-webui at sync wave 66

  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

---

## Task 14: Update Renovate to disable auto-merge for Open WebUI

**Files:**
- Modify: `.github/renovate.json5`

- [ ] **Step 1: Add `apps/open-webui/**` to the no-automerge packageRule**

  Open `.github/renovate.json5`. Find the first entry under `packageRules` — the one with `matchFileNames` listing critical apps (`apps/mariadb/**`, `apps/traefik/**`, ..., `apps/llama-cpp/**`) and `automerge: false`. Add `'apps/open-webui/**'` to that array as a new line at the end, before the closing `]`.

  The relevant rule should end up looking like:
  ```jsonc
      {
        matchFileNames: [
          'apps/mariadb/**',
          'apps/traefik/**',
          'apps/kubevip-ha/**',
          'apps/paperless-ai/**',
          'apps/pihole/**',
          'apps/monitoring-checkmk-server/**',
          'apps/argo-cd-apps-helm/**',
          'apps/nvidia-device-plugin/**',
          'apps/llama-cpp/**',
          'apps/open-webui/**',
        ],
        automerge: false,
      },
  ```

- [ ] **Step 2: Validate JSON5 syntax**

  Quickest check: if `node`/`npx` is available, run:
  ```bash
  npx --yes json5 -c .github/renovate.json5 > /dev/null
  ```
  Otherwise, rely on the pre-commit run in Step 3 — the existing hooks will fail on broken syntax.

- [ ] **Step 3: Stage, lint, commit**

  ```bash
  git add .github/renovate.json5
  pre-commit run --files .github/renovate.json5
  git commit -m "$(cat <<'EOF'
  renovate: disable automerge for open-webui

  Open WebUI is pre-1.0 and ships breaking config changes between minor
  releases. Every update must go through a manually-reviewed PR.

  Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
  EOF
  )"
  ```

---

## Task 15: Push and watch ArgoCD sync

**Files:** none (deploy step)

- [ ] **Step 1: Push to origin**

  ```bash
  git push origin main
  ```

- [ ] **Step 2: Wait for the new Application to appear**

  ```bash
  kubectl -n argocd get application 66-open-webui -w
  ```
  Wait for `SYNC STATUS=Synced` and `HEALTH STATUS=Healthy`. If after about two minutes nothing happens, force a refresh:
  ```bash
  kubectl annotate -n argocd application 66-open-webui \
    argocd.argoproj.io/refresh=hard --overwrite
  ```

- [ ] **Step 3: Confirm Authelia re-registered the OIDC client**

  Authelia's chart annotates its Deployment with a checksum of its config and restarts on change. Verify the pod is fresh:
  ```bash
  kubectl -n authelia get pods -l app.kubernetes.io/name=authelia \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.startTime}{"\n"}{end}'
  ```
  Expected: timestamp within the last few minutes. If the pod did not restart, force one:
  ```bash
  kubectl -n authelia rollout restart deploy/authelia
  kubectl -n authelia rollout status deploy/authelia
  ```
  Then tail logs:
  ```bash
  kubectl -n authelia logs -l app.kubernetes.io/name=authelia --tail=100 \
    | grep -iE 'openwebui|oidc|client'
  ```
  Expected: the `openwebui` client appears among the registered clients with no configuration errors.

- [ ] **Step 4: Confirm the Open WebUI pod is running**

  ```bash
  kubectl -n open-webui get pods,svc,pvc,ingress
  kubectl -n open-webui logs deploy/open-webui --tail=80
  ```
  Expected: pod `Running` (1/1), PVC `Bound`, Service ClusterIP, Ingress with host `chat.internal.neese-web.de`. Logs should be free of OIDC discovery errors or Tavily auth errors.

---

## Task 16: First-login admin bootstrap

**Files:** none (manual UI step)

> **Critical:** the first OIDC login becomes admin. Do this yourself before sharing the hostname with anyone else.

- [ ] **Step 1: Resolve DNS**

  Confirm `chat.internal.neese-web.de` resolves from your client to a cluster ingress IP:
  ```bash
  dig +short chat.internal.neese-web.de
  ```
  Expected: a non-empty A record pointing into the home network. If it fails, check Pi-hole / local DNS records.

- [ ] **Step 2: Open the UI**

  Browse to https://chat.internal.neese-web.de. You should be redirected to Authelia. Complete the OIDC flow with your normal credentials + 2FA.

- [ ] **Step 3: Verify you are admin**

  In Open WebUI → Settings → Account, your role should be `admin`. If it shows `pending`, the admin bootstrap did not trigger — check Open WebUI logs and confirm `DEFAULT_USER_ROLE=pending` is set (the first user is still promoted to admin regardless of this default).

---

## Task 17: Create the virtual model `qwen2.5-coder-websearch`

**Files:** none (Open WebUI admin UI)

- [ ] **Step 1: Confirm llama-cpp is reachable from Open WebUI**

  In Open WebUI → Workspace → Models you should see `qwen2.5-coder-7b` appearing from the OpenAI connection. If it does not, verify reachability from the pod:
  ```bash
  kubectl -n open-webui exec deploy/open-webui -- \
    sh -c 'curl -sS http://llama-cpp.llama-cpp.svc.cluster.local:8080/v1/models \
      -H "Authorization: Bearer $OPENAI_API_KEYS"'
  ```
  Expected: a JSON object with a `data` array listing the qwen model.

- [ ] **Step 2: Create the virtual model**

  Workspace → Models → "Create a New Model":
  - Base Model: `qwen2.5-coder-7b`
  - Model ID: `qwen2.5-coder-websearch`
  - Capabilities → Web Search: **enabled**
  - Save.

- [ ] **Step 3: Smoke-test in the UI**

  Start a new chat, select `qwen2.5-coder-websearch`, ask a current-events question (e.g., "What is the current stable Kubernetes version?"). Expected: the response references web sources fetched via Tavily.

---

## Task 18: Generate an API key and configure Continue.dev

**Files:** none on cluster side; client-side `~/.continue/config.json` only.

- [ ] **Step 1: Generate a personal API key**

  Open WebUI → Settings → Account → API Keys → "Create new key". Copy the key (starts with `sk-`).

- [ ] **Step 2: Configure Continue.dev**

  Edit `~/.continue/config.json` on your workstation. Add the following entry to the `models:` array:
  ```jsonc
  {
    "title": "qwen-websearch",
    "provider": "openai",
    "model": "qwen2.5-coder-websearch",
    "apiBase": "https://chat.internal.neese-web.de/api",
    "apiKey": "sk-..."
  }
  ```

- [ ] **Step 3: Verify from VSCode**

  Reload Continue, select `qwen-websearch`, ask a question that requires recent information. Confirm the answer references current web sources, indicating Tavily ran in the request path.

---

## Self-Review (already applied)

Spec coverage:

- Architecture / components: Tasks 2, 3, 6.
- Hostname & TLS: Task 5.
- OIDC client registration: Task 8.
- Helm values + chart wiring: Task 6.
- Env vars (SOPS secret): Task 4.
- Network Policy: Tasks 2 + 3.
- File layout: Tasks 2–6 + 9.
- ArgoCD Application + registration: Tasks 11 + 12.
- Renovate policy: Task 14.
- First-deploy operational procedures: Tasks 1, 15, 16.
- Virtual model creation: Task 17.
- Tavily usage monitoring: documented in WEBSEARCH.md (Task 9).
- Browser & IDE request flows: exercised in Tasks 16 + 18.

Type consistency:

- Secret name `open-webui-env` consistent across Task 4 metadata and Task 6 `extraEnvFrom.secretRef.name`.
- Hostname `chat.internal.neese-web.de` consistent across Tasks 4, 5, 8, 9, 15, 16.
- Sync wave `66` consistent across Tasks 6, 11, 12.
- Service port 80 → Ingress backend port 80 (Tasks 5 + 6).
- PVC size 10Gi (Task 6) matches spec.
- Pod label `app.kubernetes.io/name: open-webui` (Task 3 selector) is verified in Task 7 Step 3 against the rendered Deployment, with a documented fallback if the chart uses a different label.

No remaining `TBD`/`TODO`/`later` placeholders. Every `<PASTE_*>` value points back to where it was gathered in Task 1.
