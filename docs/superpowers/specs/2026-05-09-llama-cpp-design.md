# llama.cpp on k3s with NVIDIA GPU — Design

**Date:** 2026-05-09
**Status:** Approved
**Author:** Claude (brainstormed with @madic-creates)

## Goal

Provide an OpenAI-compatible inference endpoint inside the cluster, served by
[`llama.cpp`](https://github.com/ggml-org/llama.cpp) running on `node04`
(the ZFS node, which has an NVIDIA RTX 3060 Ti / 8 GiB VRAM).

The endpoint must:

- Serve a single fixed model (Qwen2.5-Coder-7B-Instruct, Q4_K_M GGUF) tuned
  for agentic / tool-calling workloads (large context, quantized KV cache,
  parallel serving slots).
- Be reachable from the internal network at
  `https://llama.internal.neese-web.de`.
- Authenticate every request with a shared API key (Bearer token).
- Run on `node04` only (`nodeSelector: node-role/workload=zfs`).
- Use the GPU through the NVIDIA Kubernetes device plugin, with
  `runtimeClassName: nvidia` (the NVIDIA RuntimeClass already exists in the
  cluster).

## Non-Goals

- Multi-model serving / model swapping (would require `llama-swap` — out of
  scope, explicitly deferred).
- Public exposure (cloudflare tunnel) — endpoint stays internal.
- Authelia integration — API key is the only auth surface.
- Replicas > 1 — there is exactly one consumer GPU.

## Architecture

Two new ArgoCD applications, deployed in dependency order via sync-waves:

| Wave | App | Purpose |
|------|-----|---------|
| 24   | `nvidia-device-plugin` | Advertise `nvidia.com/gpu` as a schedulable resource on every node carrying an NVIDIA GPU. |
| 65   | `llama-cpp`            | Run the inference server, consume `nvidia.com/gpu: 1`, expose Ingress. |

Wave 24 matches the existing `intel-gpu` app — both are device plugins.
Wave 65 places `llama-cpp` alongside other workload apps.

### Why a device plugin (and not just runtimeClass passthrough)

The NVIDIA container toolkit alone (via `runtimeClassName: nvidia`) would let a
pod see the GPU, but the scheduler would not know that the GPU is occupied.
Two pods could land on the same node and fight over VRAM. The device plugin
turns the GPU into a regular Kubernetes resource (`nvidia.com/gpu`), so the
scheduler enforces single-tenancy.

## App 1 — `apps/nvidia-device-plugin/`

### Layout

```
apps/nvidia-device-plugin/
  namespace.yaml
  kustomization.yaml
  k8s.np.nvidia-device-plugin-default-deny.yaml
  k8s.np.nvidia-device-plugin.yaml
```

### `kustomization.yaml`

Helm chart from the official NVIDIA repository:

```yaml
helmCharts:
  - name: nvidia-device-plugin
    repo: https://nvidia.github.io/k8s-device-plugin
    version: <pinned, latest stable>
    namespace: nvidia-device-plugin
    releaseName: nvidia-device-plugin
    valuesInline:
      runtimeClassName: nvidia
      nodeSelector:
        feature.node.kubernetes.io/pci-0300_10de.present: "true"
      # Default behaviour is fine: one device per GPU, no MIG, no time-slicing.
```

The `nodeSelector` uses an existing NFD label (PCI vendor `10de` = NVIDIA),
so adding more NVIDIA nodes in the future requires no changes here.

`runtimeClassName: nvidia` is required because the device-plugin DaemonSet
itself needs the NVIDIA runtime to talk to the driver.

### Network policies

- `k8s.np.nvidia-device-plugin-default-deny.yaml` — Cilium default-deny
  ingress + egress for namespace `nvidia-device-plugin`.
- `k8s.np.nvidia-device-plugin.yaml` — allow only DNS egress + kubelet
  reachability. The plugin communicates with the kubelet over the device
  plugin Unix socket (`/var/lib/kubelet/device-plugins/`), which is hostPath,
  not network — so very little egress is actually needed.

### ArgoCD Application

`apps/argo-cd-apps/24-nvidia-device-plugin.yaml`, sync-wave `"24"`, mirroring
`24-intel-gpu.yaml`. Add to `apps/argo-cd-apps/kustomization.yaml`.

### Renovate

Helm chart and image tags will be picked up by the existing Renovate manager
config. Treat as a critical app like Longhorn/Traefik — add `apps/nvidia-device-plugin/**`
to the `automerge: false` `packageRules` block in `.github/renovate.json5` so
that updates require manual review (a broken device plugin is cluster-wide).

## App 2 — `apps/llama-cpp/`

### Layout

```
apps/llama-cpp/
  namespace.yaml
  kustomization.yaml
  kustomize-secret-generator.yaml  # ksops config, references secrets.enc.yaml
  secrets.enc.yaml                 # SOPS-encrypted Secret manifest (input to ksops)
  k8s.deployment.yaml              # Deployment + Service
  llama-cpp.ingress.enc.yaml       # SOPS-encrypted (hostname matches encrypted_regex)
  k8s.np.llama-cpp-default-deny.yaml
  k8s.np.llama-cpp.yaml
```

### `kustomization.yaml`

```yaml
resources:
  - namespace.yaml
  - k8s.deployment.yaml
  - llama-cpp.ingress.enc.yaml
  - k8s.np.llama-cpp-default-deny.yaml
  - k8s.np.llama-cpp.yaml

# ksops decrypts secrets.enc.yaml at build time and injects the Secret as if
# it were a regular resource. secrets.enc.yaml is NOT in `resources:`.
generators:
  - kustomize-secret-generator.yaml

commonAnnotations:
  argocd.argoproj.io/sync-wave: "65"
```

### Deployment (`k8s.deployment.yaml`)

Plain Deployment + ClusterIP Service in one file (consistent with `paperless-gpt`).

Key fields:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: llama-cpp
  namespace: llama-cpp
spec:
  replicas: 1
  revisionHistoryLimit: 2
  strategy:
    type: Recreate          # GPU is single-tenant; rolling would deadlock.
  selector:
    matchLabels:
      app: llama-cpp
  template:
    metadata:
      labels:
        app: llama-cpp
    spec:
      runtimeClassName: nvidia
      nodeSelector:
        node-role/workload: zfs
      containers:
        - name: llama-cpp
          image: ghcr.io/ggml-org/llama.cpp:server-cuda-bXXXX  # pinned, Renovate-tracked
          args:
            - --model
            - /models/Qwen2.5-Coder-7B-Instruct-Q4_K_M.gguf
            - --host
            - 0.0.0.0
            - --port
            - "8080"
            - --n-gpu-layers
            - "99"
            - --ctx-size
            - "32768"
            - --cache-type-k
            - q8_0
            - --cache-type-v
            - q8_0
            - --parallel
            - "1"
            - --flash-attn
            - --api-key-file
            - /etc/llama-cpp/api-key
            - --alias
            - qwen2.5-coder-7b
            - --jinja
          ports:
            - containerPort: 8080
              name: http
          resources:
            requests:
              cpu: "2"
              memory: 8Gi
              nvidia.com/gpu: 1
            limits:
              cpu: "4"
              memory: 12Gi
              nvidia.com/gpu: 1
          volumeMounts:
            - name: models
              mountPath: /models
              readOnly: true
            - name: api-key
              mountPath: /etc/llama-cpp
              readOnly: true
      initContainers:
        # Fail fast with a clear error if the operator forgot to stage the
        # GGUF on node04, or if the file got truncated. Cheaper than a
        # CrashLoop on the main container, which buries the cause in
        # llama.cpp logs.
        - name: verify-model
          image: alpine:3.20
          command:
            - sh
            - -c
            - |
              set -eu
              MODEL=/models/Qwen2.5-Coder-7B-Instruct-Q4_K_M.gguf
              EXPECTED_SHA256=<pinned at implementation time>
              test -f "$MODEL" || { echo "model file missing: $MODEL"; exit 1; }
              # Quick sanity: GGUF magic is 'GGUF' (0x47475546) at offset 0
              head -c4 "$MODEL" | grep -q '^GGUF' || {
                echo "not a GGUF file (header mismatch)"; exit 1; }
              echo "$EXPECTED_SHA256  $MODEL" | sha256sum -c -
          volumeMounts:
            - name: models
              mountPath: /models
              readOnly: true
          readinessProbe:
            httpGet: { path: /health, port: http }
            initialDelaySeconds: 30
            periodSeconds: 10
          livenessProbe:
            httpGet: { path: /health, port: http }
            initialDelaySeconds: 120  # model load can take a while on cold cache
            periodSeconds: 30
            failureThreshold: 6
      volumes:
        - name: models
          hostPath:
            path: /zfs/llama-cpp/models
            type: Directory
        - name: api-key
          secret:
            secretName: llama-cpp-env
            defaultMode: 0400
            items:
              - key: API_KEY
                path: api-key
---
apiVersion: v1
kind: Service
metadata:
  name: llama-cpp
  namespace: llama-cpp
spec:
  selector:
    app: llama-cpp
  ports:
    - port: 8080
      targetPort: http
      protocol: TCP
  type: ClusterIP
```

### Ingress (`llama-cpp.ingress.enc.yaml`)

Plain `networking.k8s.io/v1 Ingress`, mirroring `cluster-tv`'s pattern, but
**without** Authelia forward-auth (auth happens at the API layer):

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: llama-cpp
  namespace: llama-cpp
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web239,websecure239
    traefik.ingress.kubernetes.io/router.tls: "true"
    traefik.ingress.kubernetes.io/router.middlewares: traefik-redirect@kubernetescrd
spec:
  ingressClassName: traefik
  tls:
    - hosts:
        - llama.internal.neese-web.de
      secretName: wildcard-cloudflare-production-02
  rules:
    - host: llama.internal.neese-web.de
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: llama-cpp
                port:
                  number: 8080
```

The `wildcard-cloudflare-production-02` TLS secret is the wildcard cert for
`*.internal.neese-web.de`, reflected into namespaces by the existing
`reflector` app.

### Secrets

Same pattern as `paperless-gpt` and other apps: a SOPS-encrypted Secret
manifest plus a ksops generator that decrypts it during `kustomize build`.

`apps/llama-cpp/secrets.enc.yaml` (encrypted in the repo, the `data:` field
matches the SOPS encryption regex):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: llama-cpp-env
  namespace: llama-cpp
type: Opaque
stringData:
  API_KEY: <openssl rand -hex 32, generated once during Phase 2>
```

`apps/llama-cpp/kustomize-secret-generator.yaml` (plain text):

```yaml
apiVersion: viaduct.ai/v1
kind: ksops
metadata:
  name: llama-cpp-secret-generator
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: ksops
files:
  - secrets.enc.yaml
```

The `kustomization.yaml` references the ksops config under `generators:`,
not under `resources:`. `secrets.enc.yaml` is *not* a `resources:` entry —
ksops produces the Secret at build time.

### Network policies

- `k8s.np.llama-cpp-default-deny.yaml` — Cilium default-deny ingress + egress
  for namespace `llama-cpp`.
- `k8s.np.llama-cpp.yaml`:
  - allow ingress from Traefik on port 8080
  - allow DNS egress (kube-system → kube-dns)
  - no other egress (the model is local; no internet access needed at runtime)

### ArgoCD Application

`apps/argo-cd-apps/65-llama-cpp.yaml`, sync-wave `"65"`, copied from
`40-paperless-gpt.yaml` pattern. Add to
`apps/argo-cd-apps/kustomization.yaml`.

## Manual Bootstrap (one-time, documented)

Before ArgoCD syncs `llama-cpp`, the model file must exist on `node04`:

```bash
ssh node04
sudo mkdir -p /zfs/llama-cpp/models
cd /zfs/llama-cpp/models
sudo huggingface-cli download bartowski/Qwen2.5-Coder-7B-Instruct-GGUF \
    Qwen2.5-Coder-7B-Instruct-Q4_K_M.gguf \
    --local-dir .
sudo chmod 644 *.gguf
```

If the file is missing, the pod will `CrashLoopBackOff` until it appears —
ArgoCD will mark the app `Degraded`, which is the desired feedback.

## Verification

After both apps reconcile:

```bash
# Device plugin advertises GPU
kubectl get node node04 -o jsonpath='{.status.allocatable.nvidia\.com/gpu}'
# expected: 1

# Pod is Running and Ready
kubectl get pod -n llama-cpp -l app=llama-cpp

# OpenAI endpoint replies with the loaded model
API_KEY=$(sops --config .sops.yaml -d apps/llama-cpp/secrets.enc.yaml \
            | yq '.stringData.API_KEY')
curl -sS -H "Authorization: Bearer $API_KEY" \
    https://llama.internal.neese-web.de/v1/models | jq

# Inference smoke test
curl -sS -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    https://llama.internal.neese-web.de/v1/chat/completions \
    -d '{"model":"qwen2.5-coder-7b","messages":[{"role":"user","content":"hi"}]}'

# Tool-calling smoke test (verifies --jinja path and OpenAI tools schema)
curl -sS -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    https://llama.internal.neese-web.de/v1/chat/completions \
    -d '{
      "model": "qwen2.5-coder-7b",
      "messages": [{"role":"user","content":"What is the weather in Berlin?"}],
      "tools": [{
        "type": "function",
        "function": {
          "name": "get_weather",
          "description": "Get the current weather for a city",
          "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string"}},
            "required": ["city"]
          }
        }
      }]
    }' | jq '.choices[0].message.tool_calls'
```

## Placeholders to resolve at implementation time

- `nvidia-device-plugin` Helm chart `version:` — pin to current stable from
  https://nvidia.github.io/k8s-device-plugin
- `ghcr.io/ggml-org/llama.cpp:server-cuda-bXXXX` image tag — pin to current
  stable build (the project releases per-commit tags `b<NNNN>`).
- `API_KEY` value — generated once via `openssl rand -hex 32` during
  Phase 2 implementation.
- `EXPECTED_SHA256` in the `verify-model` initContainer — pin to the
  upstream-published SHA256 of the chosen GGUF file. Source of truth:
  https://huggingface.co/bartowski/Qwen2.5-Coder-7B-Instruct-GGUF (the
  per-file checksum is shown next to each download). Compute locally with
  `sha256sum` after staging on `node04` and confirm the values match before
  pinning.

## Risks and Open Questions

- **VRAM budget on RTX 3060 Ti / 8 GiB.**
  - Model (Q4_K_M of 7B): ~4.6 GiB
  - KV cache @ 32k context, q8_0 quantized, single slot: ~2 GiB
  - Compute buffers + flash-attn workspace: ~0.5–1 GiB
  - **Total: ~7.0 GiB.** ~1 GiB headroom. Monitor via `nvidia-smi` after
    deploy; if OOM under load, drop `--ctx-size` to `24576` (saves ~0.5 GiB)
    or move to Q3_K_M (saves another ~0.6 GiB).
- **Why Q4_K_M and not Q5_K_M.** Agentic loops need long context for tool
  output history; the 0.9 GiB freed by Q4_K_M (vs Q5_K_M) is what makes
  32k context feasible at all. Quality drop is small for code tasks.
- **`--parallel 1` chosen deliberately.** With `--parallel >1`, llama.cpp
  splits the configured context across slots, so the effective per-request
  context drops proportionally. The agentic use case prioritises long
  context per request over concurrency — there is exactly one human user
  anyway. Concurrency can be increased later if a real second consumer
  appears, at the cost of per-request context.
- **`--flash-attn`** requires CUDA build (which we are using) and Compute
  Capability ≥ 7.0. RTX 3060 Ti is CC 8.6 — supported.
- **First request latency.** Cold model load is ~10–20 s on this hardware.
  `livenessProbe.initialDelaySeconds = 120` covers it.
- **Agentic limits.** Qwen2.5-Coder-7B is decent at short tool-use loops
  (1–5 tool calls) but degrades on long-horizon agent sessions. For
  Aider/Cline-style work on large repos, expect to hit context limits or
  loops. Acceptable tradeoff for an 8 GiB card; revisit if a 16+ GiB GPU
  appears.
- **No backup of `/zfs/llama-cpp/models`.** Acceptable: GGUF files are
  re-downloadable from HuggingFace and the path is on a ZFS dataset.
- **Renovate noise on `llama.cpp` image.** The project tags every commit
  (e.g. `b5821`, `b5822` …). Without throttling, this would produce many PRs.
  Mitigation: rely on the global `minimumReleaseAge: '5 days'`, and consider
  adding `apps/llama-cpp/**` to the `automerge: false` rules so updates land
  with a manual click rather than auto-merge.

## Implementation Phases

To be expanded by `writing-plans`:

1. **Phase 1 — `nvidia-device-plugin` app:** create files, encrypt nothing
   (no secrets), add ArgoCD Application, update kustomization, update Renovate
   rules, commit, let ArgoCD sync, verify `nvidia.com/gpu: 1` is allocatable
   on `node04`.
2. **Phase 2 — `llama-cpp` app:** create files, generate API key, encrypt
   `secrets.enc.yaml` and `llama-cpp.ingress.enc.yaml`, add ArgoCD Application,
   update kustomization. Before pushing: place the GGUF file on `node04`.
   Push, let ArgoCD sync, verify `/v1/models` and `/v1/chat/completions`.
