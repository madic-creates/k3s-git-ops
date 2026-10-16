# td-sync Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run the td sync server (github.com/marcus/td) in the k3s home cluster, reachable only from the internal network, with automated version updates via Renovate and file-level backups via restic.

**Architecture:** Two repositories. `madic/docker-td` on forge.geekbundle.org builds the container image (upstream publishes none) via Forgejo Actions; its Dockerfile clones the upstream at a pinned `ARG TD_VERSION` that Renovate maintains. `k3s-git-ops/apps/td-sync/` deploys that image via ArgoCD: one Longhorn PVC, single replica, Traefik ingress on the internal entrypoints, Cilium default-deny, and an hourly restic CronJob that snapshots the SQLite databases consistently before shipping them.

**Tech Stack:** Go (upstream build only), SQLite + Litestream, Docker, Forgejo Actions, Renovate, Kubernetes 1.36 (k3s), Kustomize + ksops + SOPS/age, ArgoCD, Cilium, Longhorn, restic, Traefik.

**Design spec:** `docs/superpowers/specs/2026-09-07-td-sync-server-design.md`
**td issue:** `td-02c66e`

## Global Constraints

- Two working directories. Build repo: `/home/mne-adm/Git/personal/github.com/docker-td` (remote `ssh://git@forge.geekbundle.org/madic/docker-td.git`, branch `main`). Cluster repo: `/home/mne-adm/Git/personal/github.com/k3s-git-ops` (branch `main`).
- **Never bypass ArgoCD.** No `kubectl edit`/`patch`/`apply` on managed objects. Change YAML → commit → push → sync. Read-only `kubectl get/logs/describe/exec` and `kubectl create job --from=cronjob/...` are fine.
- In `k3s-git-ops`, push approved changes **straight to `main`**, no PR. Rebase before pushing — the remote runs ahead.
- No `Co-Authored-By` or "Generated with" trailers in commits.
- End every git commit message with:
  `Claude-Session: https://claude.ai/code/session_01JTJMgDKXYPLzeS5Z7wFtgS`
- Do **not** commit anything under `docs/superpowers/` — specs and plans stay local.
- Never paste decrypted secret values into chat, commit messages, or plan files.
- The `Read` tool is blocked on `*.enc.yaml` inside the repo. Use the two SOPS workflows from `CLAUDE.md`: `EDITOR='sed -i ...' sops --config .sops.yaml <file>` for small edits, or decrypt to `/tmp/*.enc.yaml` → edit → re-encrypt for structural ones. `rm` is not allowlisted; blank temp files with `: > /tmp/file`.
- Image reference in Kubernetes manifests must be `forge.geekbundle.org/madic/td-sync:vX.Y.Z@sha256:...` — **tag *and* digest**. Digest-only pinning gives Renovate no `currentValue` and the image silently stops being tracked.
- Code comments and documentation in English. Conversation with the user in German.
- Namespace: `td-sync`. Host: `td.internal.neese-web.de`. ArgoCD sync wave: `62`.
- Starting upstream version: `v0.65.0`.

---

## File Structure

**Build repo — `/home/mne-adm/Git/personal/github.com/docker-td`**

| File | Responsibility |
|---|---|
| `Dockerfile` | Single source of truth for `TD_VERSION`; clones upstream, builds the binary, assembles the runtime image |
| `.forgejo/workflows/build.yaml` | Reads `TD_VERSION` from the Dockerfile, builds and pushes the image |
| `.forgejo/workflows/renovate.yaml` | Scheduled Renovate run against the Forgejo platform |
| `renovate.json5` | Renovate config: platform, repo, automerge policy, `customManagers:dockerfileVersions` |
| `README.md` | Exists; gets the purpose and the update-chain description |

**Cluster repo — `/home/mne-adm/Git/personal/github.com/k3s-git-ops`**

| File | Responsibility |
|---|---|
| `apps/td-sync/k8s.td-sync.yaml` | Namespace, ServiceAccount, Deployment, Service |
| `apps/td-sync/k8s.pvc.yaml` | Longhorn PVC `longhorn-pvc-td-sync` |
| `apps/td-sync/ingress.enc.yaml` | Traefik Ingress (encrypted — contains the hostname) |
| `apps/td-sync/k8s.np.td-sync-default-deny.yaml` | Cilium default-deny for the namespace |
| `apps/td-sync/k8s.np.td-sync.yaml` | Cilium allow rules for the td-sync pod |
| `apps/td-sync/k8s.backup-cronjob.yaml` | restic CronJob + its Cilium policy |
| `apps/td-sync/backup-env-configuration-td-sync.enc.yaml` | restic env Secret (encrypted) |
| `apps/td-sync/kustomization.yaml` | Resource list, sync-wave, ksops generator |
| `apps/td-sync/kustomize-secret-generator.yaml` | ksops entry for both `.enc.yaml` files |
| `apps/argo-cd-apps/62-td-sync.yaml` | ArgoCD Application |
| `apps/argo-cd-apps/kustomization.yaml:53` | Add `- 62-td-sync.yaml` |
| `apps/monitoring-blackbox-exporter/values.enc.yaml` | Add the `/healthz` probe target |

---

## Task 1: Build repo — Dockerfile

Produces the image definition. The `ARG TD_VERSION` line is what Renovate edits later, so its exact shape matters.

**Files:**
- Create: `/home/mne-adm/Git/personal/github.com/docker-td/Dockerfile`
- Modify: `/home/mne-adm/Git/personal/github.com/docker-td/README.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `Dockerfile` containing exactly one line matching `^ARG TD_VERSION="(.*)"$`. Task 2's workflow extracts the version with that pattern. Runtime image contains `/usr/local/bin/td-sync`, `/usr/local/bin/litestream`, `/entrypoint.sh`, `/etc/litestream.yml`, and the `sqlite3` binary. Listens on 8080, data in `/data`, Litestream file replica in `/backups`.

- [ ] **Step 1: Verify the base image tags exist before writing anything**

The upstream Dockerfile pins `golang:1.27-alpine` and `litestream/litestream:0.3.13`. If either tag is missing, the build fails with an unhelpful error. Check first:

```bash
for ref in "library/golang:1.27-alpine" "litestream/litestream:0.3.13" "library/alpine:3.21"; do
  repo="${ref%:*}"; tag="${ref##*:}"
  tok=$(curl -s "https://auth.docker.io/token?service=registry.docker.io&scope=repository:${repo}:pull" \
        | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
  code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $tok" \
         -H 'Accept: application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.docker.distribution.manifest.v2+json' \
         "https://registry-1.docker.io/v2/${repo}/manifests/${tag}")
  echo "$ref -> HTTP $code"
done
```

Expected: `HTTP 200` for all three.

If `golang:1.27-alpine` returns 404, pick the highest available `golang:1.2x-alpine` that satisfies the upstream `go.mod` (check with `curl -s https://raw.githubusercontent.com/marcus/td/v0.65.0/go.mod | head -5`) and use that instead. Note the substitution in the commit message. Do **not** fall back to `golang:alpine` — an unpinned tag defeats Renovate's Dockerfile manager.

- [ ] **Step 2: Write the Dockerfile**

```dockerfile
# Build the td sync server from upstream sources.
#
# Upstream (github.com/marcus/td) publishes release binaries but no container
# image, so we build our own. The version below is the single source of truth:
# Renovate bumps it, and .forgejo/workflows/build.yaml derives the image tag
# from it.
#
# This differs from upstream's deploy/Dockerfile in two ways:
#   1. We clone the sources here instead of building from the build context,
#      which keeps TD_VERSION in one Renovate-maintainable line.
#   2. sqlite is added to the runtime image. The backup CronJob in k3s-git-ops
#      reuses this image as an initContainer to produce consistent `.backup`
#      copies of the WAL databases before restic ships them.

# renovate: datasource=github-releases depName=marcus/td
ARG TD_VERSION="v0.65.0"

# --- Build stage ---
FROM golang:1.27-alpine AS builder

# A global ARG is not visible inside a stage unless re-declared without a value.
ARG TD_VERSION

RUN apk add --no-cache git

WORKDIR /src
RUN git clone --depth 1 --branch "${TD_VERSION}" https://github.com/marcus/td.git .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/td-sync ./cmd/td-sync

# --- Litestream stage ---
FROM litestream/litestream:0.3.13 AS litestream

# --- Runtime stage ---
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata wget sqlite

COPY --from=litestream /usr/local/bin/litestream /usr/local/bin/litestream
COPY --from=builder /out/td-sync /usr/local/bin/td-sync
COPY --from=builder /src/deploy/litestream.yml /etc/litestream.yml
COPY --from=builder /src/deploy/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

VOLUME /data
EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
```

- [ ] **Step 3: Verify the version line matches what the workflow and Renovate expect**

Two consumers depend on this line's exact shape. Check both.

The workflow's extraction:

```bash
cd /home/mne-adm/Git/personal/github.com/docker-td
sed -n 's/^ARG TD_VERSION="\(.*\)"/\1/p' Dockerfile | head -1
```

Expected output: `v0.65.0` — exactly one line, no quotes, nothing else.

Renovate's `customManagers:dockerfileVersions` preset regex:

```bash
python3 - <<'PY'
import re, pathlib
rx = re.compile(
    r"# renovate: datasource=(?P<datasource>[a-zA-Z0-9-._]+?) depName=(?P<depName>[^\s]+?)"
    r"(?: (?:lookupName|packageName)=(?P<packageName>[^\s]+?))?"
    r"(?: versioning=(?P<versioning>[^\s]+?))?"
    r"(?: extractVersion=(?P<extractVersion>[^\s]+?))?"
    r"(?: registryUrl=(?P<registryUrl>[^\s]+?))?"
    r"\s(?:ENV|ARG)\s+[A-Za-z0-9_]+?_VERSION[ =][\"']?(?P<currentValue>.+?)[\"']?\s"
)
m = rx.search(pathlib.Path("Dockerfile").read_text())
print(m.groupdict() if m else "NO MATCH - Renovate will not track TD_VERSION")
PY
```

Expected: `{'datasource': 'github-releases', 'depName': 'marcus/td', ..., 'currentValue': 'v0.65.0'}`.

If this prints `NO MATCH`, the comment and ARG line are malformed — fix them before continuing. A silent non-match here means the whole update automation is dead on arrival, and nothing later in this plan will surface it.

- [ ] **Step 4: Write the README**

```markdown
# docker-td

Container image for the [td](https://github.com/marcus/td) sync server.

Upstream publishes release binaries but no container image, so this repo builds
one from `deploy/Dockerfile`'s recipe, with sqlite added to the runtime image
for consistent SQLite backups.

Image: `forge.geekbundle.org/madic/td-sync`

## Updating

`ARG TD_VERSION` in the `Dockerfile` is the single source of truth. The update
chain runs by itself:

1. Renovate (`.forgejo/workflows/renovate.yaml`) bumps `ARG TD_VERSION`
2. Branch automerge lands it on `main`
3. `.forgejo/workflows/build.yaml` builds and pushes `td-sync:<version>`
4. Renovate in `madic-creates/k3s-git-ops` bumps tag and digest in
   `apps/td-sync/k8s.td-sync.yaml`

Minor bumps of `marcus/td` are **not** automerged — td is on `0.x`, where a
minor bump may break. They land as a pull request for review.

Deployment lives in `madic-creates/k3s-git-ops` under `apps/td-sync/`.
```

- [ ] **Step 5: Commit**

```bash
cd /home/mne-adm/Git/personal/github.com/docker-td
git add Dockerfile README.md
git commit -m "feat: add Dockerfile building td-sync from pinned upstream tag

Upstream publishes no container image. The Dockerfile clones marcus/td at
ARG TD_VERSION so Renovate can maintain the version in one line, and adds
sqlite to the runtime image for consistent SQLite backups.

Claude-Session: https://claude.ai/code/session_01JTJMgDKXYPLzeS5Z7wFtgS"
```

Do not push yet — Task 2 adds the workflow, and pushing the Dockerfile alone would land a commit that nothing builds.

---

## Task 2: Build repo — Forgejo Actions build workflow

**Files:**
- Create: `/home/mne-adm/Git/personal/github.com/docker-td/.forgejo/workflows/build.yaml`

**Interfaces:**
- Consumes: `Dockerfile` from Task 1, specifically the `^ARG TD_VERSION="(.*)"$` line.
- Produces: `forge.geekbundle.org/madic/td-sync:<version>` and `:latest` in the Forgejo container registry. Task 4 needs the `<version>` and the manifest digest.

**Why no `docker/*` actions:** `uses:` references resolve against the Forgejo instance's `DEFAULT_ACTIONS_URL`, which defaults to `code.forgejo.org` where the `docker/*` actions do not exist. Plain `docker` CLI steps work regardless. `actions/checkout` is the one exception — it is mirrored on both code.forgejo.org and GitHub.

- [ ] **Step 1: Write the workflow**

```yaml
---
name: Build td-sync

# Path-filtered so Renovate's base-image bumps and TD_VERSION bumps both
# trigger a build, but README-only commits do not.
on:
  push:
    branches: [main]
    paths:
      - "Dockerfile"
      - ".forgejo/workflows/build.yaml"
  workflow_dispatch:

concurrency:
  group: ${{ github.workflow }}
  cancel-in-progress: false

env:
  REGISTRY: forge.geekbundle.org
  IMAGE_NAME: madic/td-sync

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      # The Dockerfile is the single source of truth for the version, so the
      # image tag is derived from it rather than duplicated here.
      - name: Resolve td version from Dockerfile
        id: version
        run: |
          set -euo pipefail
          version=$(sed -n 's/^ARG TD_VERSION="\(.*\)"/\1/p' Dockerfile | head -1)
          if [ -z "$version" ]; then
            echo "Could not read ARG TD_VERSION from Dockerfile" >&2
            exit 1
          fi
          echo "version=$version" >> "$GITHUB_OUTPUT"
          echo "Building td-sync $version"

      - name: Log in to the Forgejo container registry
        run: |
          set -euo pipefail
          echo "${{ secrets.GITHUB_TOKEN }}" \
            | docker login "$REGISTRY" -u "${{ github.actor }}" --password-stdin

      - name: Build and push
        run: |
          set -euo pipefail
          image="$REGISTRY/$IMAGE_NAME"
          version="${{ steps.version.outputs.version }}"
          docker build -t "$image:$version" -t "$image:latest" .
          docker push "$image:$version"
          docker push "$image:latest"

      # Printed so the digest can be copied into k3s-git-ops without a
      # separate registry query.
      - name: Report the pushed digest
        run: |
          set -euo pipefail
          docker inspect --format='{{index .RepoDigests 0}}' \
            "$REGISTRY/$IMAGE_NAME:${{ steps.version.outputs.version }}"
```

- [ ] **Step 2: Commit and push both Task 1 and Task 2**

```bash
cd /home/mne-adm/Git/personal/github.com/docker-td
git add .forgejo/workflows/build.yaml
git commit -m "ci: build and push td-sync image via Forgejo Actions

Derives the image tag from ARG TD_VERSION in the Dockerfile. Uses plain
docker CLI steps rather than docker/* actions, which do not resolve against
Forgejo's default actions URL.

Claude-Session: https://claude.ai/code/session_01JTJMgDKXYPLzeS5Z7wFtgS"
git push origin main
```

- [ ] **Step 3: Watch the first build**

Open the Actions tab of `madic/docker-td` on forge.geekbundle.org. The push to `main` touched `Dockerfile` and the workflow file, so the build should start on its own. If it does not, trigger it manually via `workflow_dispatch`.

Three things are unverified on this first run and each fails loudly:

| Failure | Meaning | Fix |
|---|---|---|
| No job is queued | No runner picked up the `ubuntu-latest` label | Confirm the runner is online and carries that label |
| `docker login` denied | Actions token lacks package-write | Create a PAT with `write:packages`, store it as repo secret `REGISTRY_TOKEN`, replace `secrets.GITHUB_TOKEN` with `secrets.REGISTRY_TOKEN` in the workflow |
| `docker: not found` / cannot connect to daemon | Runner has no Docker access | Runner-side configuration; out of scope for this plan — report back |

- [ ] **Step 4: Verify the image landed in the registry**

From the workstation:

```bash
TOK=$(curl -s "https://forge.geekbundle.org/v2/token?scope=repository:madic/td-sync:pull&service=container_registry" \
      | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s -H "Authorization: Bearer $TOK" \
     "https://forge.geekbundle.org/v2/madic/td-sync/tags/list"
```

Expected: `{"name":"madic/td-sync","tags":["latest","v0.65.0"]}`

- [ ] **Step 5: Capture the digest for Task 4**

```bash
TOK=$(curl -s "https://forge.geekbundle.org/v2/token?scope=repository:madic/td-sync:pull&service=container_registry" \
      | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -sI -H "Authorization: Bearer $TOK" \
     -H 'Accept: application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json' \
     "https://forge.geekbundle.org/v2/madic/td-sync/manifests/v0.65.0" \
  | grep -i '^docker-content-digest'
```

Write down the `sha256:...` value. Task 4 needs it.

Note: take the digest of the **version tag**, not `latest`. They point at the same manifest today, but `latest` moves and the digest must stay pinned to the version.

---

## Task 3: Build repo — Renovate

**Files:**
- Create: `/home/mne-adm/Git/personal/github.com/docker-td/renovate.json5`
- Create: `/home/mne-adm/Git/personal/github.com/docker-td/.forgejo/workflows/renovate.yaml`

**Interfaces:**
- Consumes: the `# renovate:` / `ARG TD_VERSION` pair from Task 1.
- Produces: commits on `main` that bump `ARG TD_VERSION` or the base image tags, which trigger Task 2's workflow.

- [ ] **Step 1: Create the two Forgejo secrets**

In the repo settings of `madic/docker-td` → Actions → Secrets:

| Secret | Value |
|---|---|
| `RENOVATE_TOKEN` | Forgejo PAT. Scopes: `repo` (read+write), `user` (read), `issue` (read+write), `organization` (read), `read:packages` |
| `GITHUB_COM_TOKEN` | GitHub PAT, public-repo read-only, no scopes needed beyond default |

`GITHUB_COM_TOKEN` is not optional. The `github-releases` datasource queries github.com even though the platform is Forgejo, and unauthenticated requests hit the rate limit quickly — Renovate then silently reports no updates rather than failing.

- [ ] **Step 2: Write the Renovate config**

```json5
{
  $schema: 'https://docs.renovatebot.com/renovate-schema.json',
  onboarding: false,
  requireConfig: 'ignored',
  extends: [
    'config:recommended',
    // Matches `# renovate: datasource=... depName=...` followed by
    // `ARG <NAME>_VERSION="..."`. This is what tracks TD_VERSION.
    'customManagers:dockerfileVersions',
  ],
  platform: 'forgejo',
  endpoint: 'https://forge.geekbundle.org/api/v1',
  repositories: [
    'madic/docker-td',
  ],
  gitAuthor: 'Renovate Bot <bot@renovateapp.com>',
  prConcurrentLimit: 0,
  prHourlyLimit: 0,
  automerge: true,
  automergeType: 'branch',
  // Forgejo supports platform-native automerge only from v10; Renovate's own
  // branch automerge works regardless.
  platformAutomerge: false,
  ignoreTests: true,
  major: {
    automerge: false,
  },
  minor: {
    automerge: true,
  },
  patch: {
    automerge: true,
  },
  packageRules: [
    {
      // td is on 0.x, where a minor bump is allowed to break things. Do not
      // rely on Renovate classifying 0.x minors as major - say it explicitly.
      matchDepNames: [
        'marcus/td',
      ],
      matchUpdateTypes: [
        'minor',
      ],
      automerge: false,
    },
  ],
  minimumReleaseAge: '5 days',
  minimumReleaseAgeBehaviour: 'timestamp-optional',
  internalChecksFilter: 'strict',
}
```

- [ ] **Step 3: Write the Renovate workflow**

```yaml
---
name: Renovate

on:
  schedule:
    - cron: "0 4 * * *"
  workflow_dispatch:
    inputs:
      dryRun:
        description: "Dry run (no commits)"
        required: true
        default: "false"
        type: choice
        options:
          - "false"
          - "true"

concurrency:
  group: renovate
  cancel-in-progress: false

jobs:
  renovate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Run Renovate
        run: |
          set -euo pipefail
          if [ "${{ inputs.dryRun }}" = "true" ]; then
            export RENOVATE_DRY_RUN=full
          fi
          npx --yes renovate
        env:
          RENOVATE_CONFIG_FILE: renovate.json5
          RENOVATE_TOKEN: ${{ secrets.RENOVATE_TOKEN }}
          # The github-releases datasource queries github.com regardless of
          # platform; without a token it rate-limits and reports no updates.
          GITHUB_COM_TOKEN: ${{ secrets.GITHUB_COM_TOKEN }}
          LOG_LEVEL: info
```

- [ ] **Step 4: Commit and push**

```bash
cd /home/mne-adm/Git/personal/github.com/docker-td
git add renovate.json5 .forgejo/workflows/renovate.yaml
git commit -m "ci: track td and base image versions with Renovate

Runs on the Forgejo platform against madic/docker-td. TD_VERSION is matched by
the stock customManagers:dockerfileVersions preset. Minor bumps of marcus/td
are excluded from automerge because td is on 0.x.

Claude-Session: https://claude.ai/code/session_01JTJMgDKXYPLzeS5Z7wFtgS"
git push origin main
```

The path filter in `build.yaml` means this push does **not** trigger a rebuild. That is intended.

- [ ] **Step 5: Verify with a dry run**

Trigger `renovate.yaml` via `workflow_dispatch` with `dryRun: true`.

Read the job log and confirm all four dependencies are found:

| Manager | Dependency | Current |
|---|---|---|
| `custom.regex` | `marcus/td` | `v0.65.0` |
| `dockerfile` | `golang` | `1.27-alpine` |
| `dockerfile` | `litestream/litestream` | `0.3.13` |
| `dockerfile` | `alpine` | `3.21` |

If `marcus/td` is missing, the `# renovate:` comment does not match — go back to Task 1 Step 3.
If the log shows GitHub rate-limit errors, `GITHUB_COM_TOKEN` is wrong or missing.

Expected in the log: `DRY-RUN` markers and no branches created.

---

## Task 4: Cluster repo — core manifests and first rollout

The largest task, and deliberately so: namespace, PVC, deployment, service, ingress and network policies have no independent value. A reviewer accepts or rejects "td-sync answers /healthz behind the internal ingress" as one unit.

**Files:**
- Create: `apps/td-sync/k8s.td-sync.yaml`
- Create: `apps/td-sync/k8s.pvc.yaml`
- Create: `apps/td-sync/ingress.enc.yaml`
- Create: `apps/td-sync/k8s.np.td-sync-default-deny.yaml`
- Create: `apps/td-sync/k8s.np.td-sync.yaml`
- Create: `apps/td-sync/kustomization.yaml`
- Create: `apps/td-sync/kustomize-secret-generator.yaml`
- Create: `apps/argo-cd-apps/62-td-sync.yaml`
- Modify: `apps/argo-cd-apps/kustomization.yaml:53`

**Interfaces:**
- Consumes: image `forge.geekbundle.org/madic/td-sync:v0.65.0@sha256:<digest from Task 2 Step 5>`.
- Produces: Service `td-sync.td-sync.svc.cluster.local:8080`; Deployment labelled `app.kubernetes.io/name: td-sync` in namespace `td-sync` (Task 7's `podAffinity` matches on this); PVC `longhorn-pvc-td-sync` (Task 7 mounts it); URL `https://td.internal.neese-web.de` (Tasks 5 and 8 use it).

- [ ] **Step 1: Write the PVC**

`apps/td-sync/k8s.pvc.yaml`:

```yaml
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: longhorn-pvc-td-sync
  namespace: td-sync
  labels:
    app.kubernetes.io/name: td-sync
spec:
  storageClassName: longhorn
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 2Gi
```

- [ ] **Step 2: Write the workload manifests**

`apps/td-sync/k8s.td-sync.yaml`:

```yaml
---
apiVersion: v1
kind: Namespace
metadata:
  name: td-sync
  labels:
    goldilocks.fairwinds.com/enabled: "true"
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: td-sync
  namespace: td-sync
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: td-sync
  namespace: td-sync
spec:
  revisionHistoryLimit: 3
  replicas: 1
  # SQLite tolerates exactly one writer and the Longhorn PVC is ReadWriteOnce.
  # A RollingUpdate would briefly run two pods; Recreate keeps it at one.
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app.kubernetes.io/name: td-sync
  template:
    metadata:
      labels:
        app.kubernetes.io/name: td-sync
    spec:
      serviceAccount: td-sync
      # The upstream image sets no USER and would run as root. fsGroup makes
      # the Longhorn volume writable for UID 1000 instead.
      securityContext:
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
        runAsNonRoot: true
      containers:
        - name: td-sync
          # https://github.com/marcus/td
          # Built by forge.geekbundle.org/madic/docker-td
          # Tag AND digest: a digest-only reference gives Renovate no
          # currentValue and the image stops being tracked.
          image: forge.geekbundle.org/madic/td-sync:v0.65.0@sha256:REPLACE_WITH_DIGEST_FROM_TASK_2
          imagePullPolicy: IfNotPresent
          ports:
            - containerPort: 8080
              name: http
          env:
            - name: SYNC_LISTEN_ADDR
              value: ":8080"
            # Must match the ingress host: it is embedded in the login
            # verification links.
            - name: SYNC_BASE_URL
              value: "https://td.internal.neese-web.de"
            - name: SYNC_SERVER_DB_PATH
              value: "/data/server.db"
            - name: SYNC_PROJECT_DATA_DIR
              value: "/data/projects"
            # Flipped to "false" in Task 5 once the first account exists.
            - name: SYNC_ALLOW_SIGNUP
              value: "true"
            # td supports cloudflare, log and memory only - no SMTP, so the
            # in-cluster Postfix relay cannot be used. The verification link
            # is read from this pod's log.
            - name: SYNC_EMAIL_PROVIDER
              value: "log"
            - name: SYNC_LOG_FORMAT
              value: "json"
            - name: SYNC_LOG_LEVEL
              value: "info"
            - name: SYNC_SHUTDOWN_TIMEOUT
              value: "60s"
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 15
            periodSeconds: 30
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
            privileged: false
          volumeMounts:
            - name: data
              mountPath: /data
              subPath: data
            # The entrypoint always starts Litestream. Giving /backups a real
            # mount means the file replica of server.db survives restarts and
            # is picked up by the restic job.
            - name: data
              mountPath: /backups
              subPath: backups
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: longhorn-pvc-td-sync
---
apiVersion: v1
kind: Service
metadata:
  name: td-sync
  namespace: td-sync
  labels:
    app.kubernetes.io/name: td-sync
spec:
  type: ClusterIP
  ports:
    - port: 8080
      name: http
      targetPort: http
  selector:
    app.kubernetes.io/name: td-sync
```

Replace `REPLACE_WITH_DIGEST_FROM_TASK_2` with the digest captured in Task 2 Step 5.

- [ ] **Step 3: Write the network policies**

`apps/td-sync/k8s.np.td-sync-default-deny.yaml`:

```yaml
---
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: td-sync-default-deny
  namespace: td-sync
spec:
  endpointSelector: {}  # Empty selector = matches ALL pods in this namespace
  # Default deny
  ingress:
    - {}
  egress:
    - {}
```

`apps/td-sync/k8s.np.td-sync.yaml`:

```yaml
---
# td-sync Network Policy
#
# td-sync talks to nobody: the `log` email provider sends no mail, Litestream
# replicates to the local filesystem only, and there are no outbound calls.
# DNS egress is all it needs.
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: td-sync
  namespace: td-sync
spec:
  endpointSelector:
    matchLabels:
      app.kubernetes.io/name: td-sync
  ingress:
  # Allow ingress from Traefik. The blackbox probe added in Task 8 also
  # arrives through Traefik, so it needs no separate rule.
  - fromEndpoints:
    - matchLabels:
        k8s:app.kubernetes.io/instance: traefik-kube-system
        k8s:io.kubernetes.pod.namespace: kube-system
    toPorts:
    - ports:
      - port: "8080"
        protocol: TCP
  egress:
  # Allow DNS resolution via CoreDNS
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
```

- [ ] **Step 4: Write the encrypted ingress**

The hostname is encrypted by `.sops.yaml`'s `encrypted_regex` (it matches `hosts?` and `host`). Write plaintext first, then encrypt in place.

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
mkdir -p apps/td-sync
cat > apps/td-sync/ingress.enc.yaml <<'EOF'
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: td-sync
  namespace: td-sync
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: web239,websecure239
    traefik.ingress.kubernetes.io/router.tls: "true"
    # Deliberately no authelia-forwardauth middleware: the td CLI
    # authenticates with a bearer token against /v1/... and cannot follow an
    # SSO redirect. td brings its own auth (device flow + API keys).
    traefik.ingress.kubernetes.io/router.middlewares: traefik-redirect@kubernetescrd
spec:
  ingressClassName: traefik
  tls:
    - hosts:
        - td.internal.neese-web.de
      secretName: wildcard-cloudflare-production-02
  rules:
    - host: td.internal.neese-web.de
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: td-sync
                port:
                  number: 8080
EOF
sops --config .sops.yaml -e -i apps/td-sync/ingress.enc.yaml
```

Verify it round-trips and that the hostname is actually encrypted:

```bash
sops --config .sops.yaml -d apps/td-sync/ingress.enc.yaml | grep -c 'td.internal.neese-web.de'
grep -c 'td.internal.neese-web.de' apps/td-sync/ingress.enc.yaml
```

Expected: `2` from the first command (tls host + rule host), `0` from the second. A non-zero second result means the hostname is sitting in the repo in plaintext — fix before committing.

- [ ] **Step 5: Write the kustomization files**

`apps/td-sync/kustomize-secret-generator.yaml`:

```yaml
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
  - ingress.enc.yaml
```

`apps/td-sync/kustomization.yaml`:

```yaml
---
commonAnnotations:
  argocd.argoproj.io/sync-wave: "62"

resources:
  - k8s.td-sync.yaml
  - k8s.pvc.yaml
  - k8s.np.td-sync-default-deny.yaml
  - k8s.np.td-sync.yaml

generators:
  - kustomize-secret-generator.yaml
```

Task 7 adds the backup CronJob and its secret to both files.

- [ ] **Step 6: Write the ArgoCD Application**

`apps/argo-cd-apps/62-td-sync.yaml`:

```yaml
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: 62-td-sync
  namespace: argocd
  finalizers:
    - resources-finalizer.argocd.argoproj.io
  annotations:
    argocd.argoproj.io/sync-wave: "62"
spec:
  project: default
  source:
    repoURL: "example.com"
    path: apps/td-sync
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

`repoURL: "example.com"` is not a mistake — it is a placeholder replaced by a Kustomize replacement reading the encrypted `argocd-repo-url-public` secret. Every Application in this directory uses it.

- [ ] **Step 7: Register the Application**

In `apps/argo-cd-apps/kustomization.yaml`, insert `  - 62-td-sync.yaml` between the `60-semaphore.yaml` and `64-downloader-lidarr.yaml` lines (around line 53):

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
sed -i 's|^  - 64-downloader-lidarr.yaml$|  - 62-td-sync.yaml\n  - 64-downloader-lidarr.yaml|' \
  apps/argo-cd-apps/kustomization.yaml
grep -n -B1 -A1 '62-td-sync' apps/argo-cd-apps/kustomization.yaml
```

Expected: `62-td-sync.yaml` sitting between `60-semaphore.yaml` and `64-downloader-lidarr.yaml`.

- [ ] **Step 8: Validate before committing**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
kustomize build apps/td-sync --enable-helm --enable-alpha-plugins --enable-exec
```

Expected: Namespace, ServiceAccount, Deployment, Service, PVC, two CiliumNetworkPolicies and the Ingress, all carrying `argocd.argoproj.io/sync-wave: "62"`.

Then the schema check:

```bash
scripts/kubeconform-validate.sh apps/td-sync apps/argo-cd-apps
```

Expected: no failures. `CiliumNetworkPolicy` schemas come from the CRDs-catalog; if they are reported as skipped that is expected (`-ignore-missing-schemas`).

Then the full hook suite:

```bash
pre-commit run --all-files
```

- [ ] **Step 9: Commit and push**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
git add apps/td-sync apps/argo-cd-apps/62-td-sync.yaml apps/argo-cd-apps/kustomization.yaml
git commit -m "feat(td-sync): deploy the td sync server on the internal ingress

Single-replica deployment on a Longhorn PVC, reachable only via
td.internal.neese-web.de. No Authelia middleware: the td CLI uses bearer
tokens and cannot follow an SSO redirect. Signup stays open until the first
account exists.

Claude-Session: https://claude.ai/code/session_01JTJMgDKXYPLzeS5Z7wFtgS"
git pull --rebase origin main
git push origin main
```

- [ ] **Step 10: Verify the rollout**

ArgoCD polls; do not wait passively for more than a couple of minutes.

```bash
kubectl annotate -n argocd application 99-argocd-apps argocd.argoproj.io/refresh=hard --overwrite
sleep 30
kubectl get application -n argocd 62-td-sync \
  -o jsonpath='SYNC={.status.sync.status} HEALTH={.status.health.status}{"\n"}'
```

Expected: `SYNC=Synced HEALTH=Healthy`.

"Synced" alone does not prove your commit was applied — ArgoCD may be reporting against a stale revision. Confirm the revision:

```bash
kubectl get application -n argocd 62-td-sync -o jsonpath='{.status.sync.revision}{"\n"}'
git rev-parse HEAD
```

These must match. If not, re-annotate with `refresh=hard` and check again.

Then the pod:

```bash
kubectl get pods -n td-sync
kubectl logs -n td-sync deploy/td-sync --tail=30
```

Expected: one `Running` pod, `1/1` ready. The log should show Litestream starting and td-sync listening on `:8080`.

Then the TLS certificate. The ingress references `wildcard-cloudflare-production-02`, which is not created by this app — Reflector copies it from the `certmanager` namespace into every namespace, and a brand-new namespace gets it only after Reflector notices:

```bash
kubectl get secret -n td-sync wildcard-cloudflare-production-02 \
  -o jsonpath='{.metadata.annotations.reflector\.v1\.k8s\.emberstack\.com/reflects}{"\n"}'
```

Expected: `certmanager/wildcard-cloudflare-production-02`.

If the secret is missing, wait a minute and retry — Reflector is eventually consistent. A persistently missing secret makes Traefik serve its default self-signed certificate, which looks like a certificate problem but is really a reflection problem.

- [ ] **Step 11: Verify the endpoint end to end**

```bash
curl -sS https://td.internal.neese-web.de/healthz
```

Expected: `{"status":"ok"}`

If DNS does not resolve, the internal wildcard is not served to this host — that is a resolver question, not a manifest bug. Confirm the service itself is fine before chasing it:

```bash
kubectl run td-sync-check --rm -i --restart=Never --image=curlimages/curl:latest -n td-sync -- \
  curl -sS http://td-sync.td-sync.svc.cluster.local:8080/healthz
```

---

## Task 5: First account and closing signup

**Files:**
- Modify: `apps/td-sync/k8s.td-sync.yaml` (the `SYNC_ALLOW_SIGNUP` value)

**Interfaces:**
- Consumes: the running service from Task 4.
- Produces: credentials in `~/.config/td/auth.json`; a server with signup disabled.

- [ ] **Step 1: Point the CLI at the server**

```bash
td config set sync.url https://td.internal.neese-web.de
```

- [ ] **Step 2: Start the login**

```bash
td auth login
```

Enter the email address when prompted. The command blocks and polls — leave it running and use a second terminal for the next step. No code appears in the terminal; the login completes only after the emailed link is opened.

- [ ] **Step 3: Retrieve the verification link from the pod log**

`SYNC_EMAIL_PROVIDER=log` writes the mail to stdout instead of sending it.

```bash
kubectl logs -n td-sync deploy/td-sync --tail=100 | grep -iE 'verify|login|token|http'
```

Open the printed URL in a browser on the internal network. Links expire after 15 minutes; if it has lapsed, re-run `td auth login`.

- [ ] **Step 4: Confirm the login completed**

The `td auth login` terminal should report success. Verify:

```bash
test -f ~/.config/td/auth.json && echo "credentials stored"
```

- [ ] **Step 5: Close signup**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
sed -i '/name: SYNC_ALLOW_SIGNUP/{n;s/value: "true"/value: "false"/}' apps/td-sync/k8s.td-sync.yaml
grep -A1 'SYNC_ALLOW_SIGNUP' apps/td-sync/k8s.td-sync.yaml
```

Expected: `value: "false"`.

Also update the stale comment above it:

```bash
sed -i 's|# Flipped to "false" in Task 5 once the first account exists.|# Signup is closed; the only account was created during rollout.|' \
  apps/td-sync/k8s.td-sync.yaml
```

- [ ] **Step 6: Validate, commit and push**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
scripts/kubeconform-validate.sh apps/td-sync
git add apps/td-sync/k8s.td-sync.yaml
git commit -m "feat(td-sync): disable public signup

The single account exists; leaving registration open serves no purpose.

Claude-Session: https://claude.ai/code/session_01JTJMgDKXYPLzeS5Z7wFtgS"
git pull --rebase origin main
git push origin main
```

- [ ] **Step 7: Verify signup is actually closed**

Wait for the sync, then confirm the pod restarted with the new value:

```bash
kubectl annotate -n argocd application 62-td-sync argocd.argoproj.io/refresh=hard --overwrite
sleep 30
kubectl get deploy -n td-sync td-sync \
  -o jsonpath='{range .spec.template.spec.containers[0].env[?(@.name=="SYNC_ALLOW_SIGNUP")]}{.value}{"\n"}{end}'
```

Expected: `false`.

---

## Task 6: Verify migration, then connect the real projects

The spec flags this as genuinely unknown: it is not documented whether `td sync-project create` uploads existing local history or only replicates events from that point on. Find out on throwaway data before risking the real backlog.

**Files:** none in either repo. This task produces knowledge and connected projects.

**Interfaces:**
- Consumes: the authenticated CLI from Task 5.
- Produces: a documented answer about history migration, and sync-connected projects.

- [ ] **Step 1: Back up the existing local state first**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
tar czf ~/todos-backup-$(date +%Y%m%d-%H%M%S).tar.gz .todos/
ls -la ~/todos-backup-*.tar.gz
```

`.todos/` is gitignored, so git is not a safety net here.

- [ ] **Step 2: Create a throwaway project with known history**

```bash
mkdir -p /tmp/claude-1000/td-sync-migration-test
cd /tmp/claude-1000/td-sync-migration-test
td init
td create "Migration probe one" --type chore --priority P3
td create "Migration probe two" --type chore --priority P3
td log -i "$(td list --json | python3 -c 'import sys,json;print(json.load(sys.stdin)[0]["id"])')" "a log line written before any sync"
td list
```

Note the two issue IDs and confirm the log line is attached.

- [ ] **Step 3: Connect it and sync**

```bash
cd /tmp/claude-1000/td-sync-migration-test
td sync-project create "migration-probe"
td sync
```

- [ ] **Step 4: Check whether the pre-existing history arrived**

Pull into a clean checkout:

```bash
mkdir -p /tmp/claude-1000/td-sync-migration-verify
cd /tmp/claude-1000/td-sync-migration-verify
td init
td sync-project join   # select "migration-probe"
td sync
td list
```

Two possible outcomes, both actionable:

| Result | Meaning | What to do |
|---|---|---|
| Both issues **and** the pre-sync log line appear | Full history is uploaded | Connect the real projects directly in Step 5 |
| Issues missing, or present without the pre-sync log line | Only post-connection events replicate | Stop. Report to the user before touching real projects — connecting them would leave the backlog server-side invisible |

Record the outcome:

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
td log -i td-02c66e --decision "Migration probe result: <full history uploaded | only new events replicate>"
```

- [ ] **Step 5: Connect the real projects (only if Step 4 showed full history)**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
td sync-project create "k3s-git-ops"
td sync
td list
```

Confirm the issue count matches what `td list` showed before the sync.

- [ ] **Step 6: Clean up the probe**

```bash
: > /tmp/claude-1000/td-sync-migration-test/.todos/README 2>/dev/null || true
```

Leave the server-side `migration-probe` project in place — it costs nothing and is useful for testing future upgrades.

---

## Task 7: Backup CronJob

**Files:**
- Create: `apps/td-sync/k8s.backup-cronjob.yaml`
- Create: `apps/td-sync/backup-env-configuration-td-sync.enc.yaml`
- Modify: `apps/td-sync/kustomization.yaml`
- Modify: `apps/td-sync/kustomize-secret-generator.yaml`

**Interfaces:**
- Consumes: PVC `longhorn-pvc-td-sync` and the pod label `app.kubernetes.io/name: td-sync` from Task 4; the td-sync image (for its `sqlite3` binary).
- Produces: restic snapshots tagged `td-sync` in the shared repository.

**Why an initContainer:** a plain `restic backup` over `/data` reads `foo.db`, `foo.db-wal` and `foo.db-shm` sequentially, not atomically. A concurrent write yields a torn, unrestorable snapshot. The initContainer produces consistent copies with `sqlite3 .backup` into an emptyDir, and restic ships only that — mirroring the existing MariaDB job, where an initContainer dumps and restic ships the dump.

- [ ] **Step 1: Create the restic env secret from an existing one**

The repository credentials, ntfy settings and pushgateway URL are identical across all backup jobs. Copy an existing secret and change only the four job-specific fields. Never retype the shared values.

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
sops --config .sops.yaml -d apps/monitoring/backup-grafana-secrets.enc.yaml > /tmp/td-sync-backup.enc.yaml
```

Now edit `/tmp/td-sync-backup.enc.yaml` with the Read and Edit tools (Read is permitted under `/tmp/**`) and change exactly these five values, leaving `RESTIC_REPOSITORY`, `RESTIC_PASSWORD`, `NTFY_*` and `PUSHGATEWAY_*` untouched:

| Field | New value |
|---|---|
| `metadata.name` | `backup-env-configuration-td-sync` |
| `metadata.namespace` | `td-sync` |
| `RESTIC_SOURCE` | `/backup/td-sync` |
| `RESTIC_HOSTNAME` | `td-sync` |
| `RESTIC_ADDITIONAL_BACKUP_PARAMETERS` | `--tag td-sync` |

Keep the `kustomize.config.k8s.io/needs-hash: "true"` annotation.

The grafana job excludes `lost+found` because it backs up a PVC root. Ours backs up an emptyDir, so no exclude is needed.

Then encrypt into place and wipe the plaintext:

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
sops --config .sops.yaml -e /tmp/td-sync-backup.enc.yaml > apps/td-sync/backup-env-configuration-td-sync.enc.yaml
: > /tmp/td-sync-backup.enc.yaml
```

Verify it decrypts and that no secret leaked into the repo in plaintext:

```bash
sops --config .sops.yaml -d apps/td-sync/backup-env-configuration-td-sync.enc.yaml \
  | grep -E 'name:|namespace:|RESTIC_SOURCE|RESTIC_HOSTNAME|RESTIC_ADDITIONAL'
grep -c 'stringData' apps/td-sync/backup-env-configuration-td-sync.enc.yaml
```

Expected: the six lines above with the new values, and `1` for the second command with the values below it unreadable.

- [ ] **Step 2: Write the CronJob**

`apps/td-sync/k8s.backup-cronjob.yaml`:

```yaml
---
# Hourly td-sync backup via restic.
#
# Co-located with the td-sync pod via podAffinity because the underlying
# Longhorn PVC is ReadWriteOnce - both pods must run on the same node.
#
# The PVC is mounted read-write, unlike the Grafana job: sqlite3 needs write
# access to the -shm file when opening a WAL database, so a read-only mount
# fails.
apiVersion: batch/v1
kind: CronJob
metadata:
  name: backup-td-sync
  namespace: td-sync
spec:
  # :35 to stay clear of the Grafana job at :20.
  schedule: "35 * * * *"
  concurrencyPolicy: Forbid
  failedJobsHistoryLimit: 1
  successfulJobsHistoryLimit: 1
  startingDeadlineSeconds: 3600
  jobTemplate:
    spec:
      backoffLimit: 2
      activeDeadlineSeconds: 540
      template:
        metadata:
          labels:
            app.kubernetes.io/name: backup-td-sync
            app.kubernetes.io/component: backup
        spec:
          automountServiceAccountToken: false
          restartPolicy: Never
          # Match td-sync's UID/GID so both the sqlite snapshot and restic can
          # read the volume without privilege escalation.
          securityContext:
            runAsUser: 1000
            runAsGroup: 1000
            fsGroup: 1000
            runAsNonRoot: true
          affinity:
            podAffinity:
              requiredDuringSchedulingIgnoredDuringExecution:
                - labelSelector:
                    matchLabels:
                      app.kubernetes.io/name: td-sync
                  namespaces:
                    - td-sync
                  topologyKey: kubernetes.io/hostname
          initContainers:
            # Produce consistent copies of every SQLite database. Copying the
            # live db/-wal/-shm triplet with restic is not atomic and can yield
            # an unrestorable snapshot.
            - name: sqlite-snapshot
              image: forge.geekbundle.org/madic/td-sync:v0.65.0@sha256:REPLACE_WITH_DIGEST_FROM_TASK_2
              imagePullPolicy: IfNotPresent
              command:
                - /bin/sh
                - -c
                - |
                  set -eu
                  mkdir -p /snapshot/projects
                  echo "$(date) snapshotting server.db"
                  sqlite3 /data/server.db ".backup '/snapshot/server.db'"
                  for db in /data/projects/*.db; do
                    [ -e "$db" ] || continue
                    name=$(basename "$db")
                    echo "$(date) snapshotting projects/$name"
                    sqlite3 "$db" ".backup '/snapshot/projects/$name'"
                  done
                  echo "$(date) snapshot complete"
                  ls -la /snapshot /snapshot/projects
              volumeMounts:
                - name: td-sync
                  mountPath: /data
                  subPath: data
                - name: snapshot
                  mountPath: /snapshot
              securityContext:
                allowPrivilegeEscalation: false
                capabilities:
                  drop:
                    - ALL
                privileged: false
          containers:
            - name: restic
              image: ghcr.io/madic-creates/container-restic:latest@sha256:5882ed8d36b4381ee2e6a7dad1a0c99f0341653a262209d655c69cee89770e62
              imagePullPolicy: IfNotPresent
              command:
                - /bin/sh
                - /usr/local/bin/backup.sh
              env:
                # Give restic a writable HOME so it can keep its repo cache
                # under $HOME/.cache/restic between runs.
                - name: HOME
                  value: /home/restic
              envFrom:
                - secretRef:
                    name: backup-env-configuration-td-sync
              volumeMounts:
                - name: backup-script
                  mountPath: /usr/local/bin/backup.sh
                  subPath: backup.sh
                - name: snapshot
                  mountPath: /backup/td-sync
                  readOnly: true
                - name: home
                  mountPath: /home/restic
              securityContext:
                allowPrivilegeEscalation: false
                capabilities:
                  drop:
                    - ALL
                privileged: false
          volumes:
            - name: td-sync
              persistentVolumeClaim:
                claimName: longhorn-pvc-td-sync
            - name: snapshot
              emptyDir:
                sizeLimit: 2Gi
            # Mirrored into every namespace by Reflector; the source ConfigMap
            # lives in the longhorn namespace.
            - name: backup-script
              configMap:
                name: crontab-backup-script
            - name: home
              emptyDir:
                medium: Memory
                sizeLimit: 200Mi
---
apiVersion: cilium.io/v2
kind: CiliumNetworkPolicy
metadata:
  name: backup-td-sync
  namespace: td-sync
spec:
  endpointSelector:
    matchLabels:
      app.kubernetes.io/name: backup-td-sync
  egress:
    # DNS
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
    # Prometheus Pushgateway (metrics)
    - toEndpoints:
        - matchLabels:
            k8s:io.cilium.k8s.namespace.labels.kubernetes.io/metadata.name: monitoring
            k8s:io.kubernetes.pod.namespace: monitoring
            k8s:app.kubernetes.io/name: prometheus-pushgateway
      toPorts:
        - ports:
            - port: "9091"
              protocol: TCP
    # Local restic REST-server (primary backup target)
    - toEndpoints:
        - matchLabels:
            k8s:io.cilium.k8s.namespace.labels.kubernetes.io/metadata.name: backup
            k8s:io.kubernetes.pod.namespace: backup
            k8s:app.kubernetes.io/name: rest-server
      toPorts:
        - ports:
            - port: "8000"
              protocol: TCP
    # ntfy notifications on failure (only HTTPS to the internet)
    - toEntities:
        - world
      toPorts:
        - ports:
            - port: "443"
              protocol: TCP
```

Replace `REPLACE_WITH_DIGEST_FROM_TASK_2` with the same digest used in Task 4.

- [ ] **Step 3: Wire both new files into Kustomize**

`apps/td-sync/kustomization.yaml` — add the CronJob to `resources`:

```yaml
---
commonAnnotations:
  argocd.argoproj.io/sync-wave: "62"

resources:
  - k8s.td-sync.yaml
  - k8s.pvc.yaml
  - k8s.np.td-sync-default-deny.yaml
  - k8s.np.td-sync.yaml
  - k8s.backup-cronjob.yaml

generators:
  - kustomize-secret-generator.yaml
```

`apps/td-sync/kustomize-secret-generator.yaml` — add the secret:

```yaml
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
  - ingress.enc.yaml
  - backup-env-configuration-td-sync.enc.yaml
```

- [ ] **Step 4: Validate**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
kustomize build apps/td-sync --enable-helm --enable-alpha-plugins --enable-exec \
  | grep -E '^kind:|^  name:'
```

Expected to include `kind: CronJob` with `name: backup-td-sync`, and a `kind: Secret` whose name starts with `backup-env-configuration-td-sync-` followed by a hash suffix (from `needs-hash: "true"`).

If the Secret name carries **no** hash suffix, the `needs-hash` annotation was lost while editing — go back to Step 1.

```bash
scripts/kubeconform-validate.sh apps/td-sync
pre-commit run --all-files
```

- [ ] **Step 5: Commit and push**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
git add apps/td-sync
git commit -m "feat(td-sync): back up the SQLite databases with restic

Litestream only replicates server.db; the per-project databases holding the
actual issues are not covered. An initContainer produces consistent sqlite
.backup copies before restic ships them, because copying a live WAL triplet
is not atomic.

Claude-Session: https://claude.ai/code/session_01JTJMgDKXYPLzeS5Z7wFtgS"
git pull --rebase origin main
git push origin main
```

- [ ] **Step 6: Trigger a run without waiting for the schedule**

Creating a Job from a CronJob is not ArgoCD-managed, so this is allowed:

```bash
kubectl annotate -n argocd application 62-td-sync argocd.argoproj.io/refresh=hard --overwrite
sleep 30
kubectl get cronjob -n td-sync backup-td-sync
kubectl create job -n td-sync backup-td-sync-manual-01 --from=cronjob/backup-td-sync
```

- [ ] **Step 7: Verify both containers did their job**

```bash
kubectl wait --for=condition=complete --timeout=300s -n td-sync job/backup-td-sync-manual-01
kubectl logs -n td-sync job/backup-td-sync-manual-01 -c sqlite-snapshot
kubectl logs -n td-sync job/backup-td-sync-manual-01 -c restic
```

Expected from `sqlite-snapshot`: `snapshotting server.db`, one line per project database, `snapshot complete`, and an `ls` listing showing non-zero file sizes.

Expected from `restic`: a completed `restic backup` with files added and a snapshot ID.

If `sqlite-snapshot` reports `attempt to write a readonly database`, the `/data` mount ended up read-only — check the volumeMount in Step 2.

- [ ] **Step 8: Confirm the snapshot is in the repository**

```bash
kubectl logs -n td-sync job/backup-td-sync-manual-01 -c restic | grep -iE 'snapshot|processed|Added'
```

Expected: a snapshot ID and a non-zero count of processed files. A snapshot of zero files means the emptyDir was empty and the initContainer silently produced nothing — re-check Step 7.

---

## Task 8: Blackbox probe

**Files:**
- Modify: `apps/monitoring-blackbox-exporter/values.enc.yaml`

**Interfaces:**
- Consumes: `https://td.internal.neese-web.de/healthz` from Task 4.
- Produces: a `probe_success` timeseries for td-sync.

`/metricz` returns JSON rather than Prometheus exposition format and is deliberately not scraped. `/healthz` returns 500 when the database is unreachable, which is the signal worth alerting on.

- [ ] **Step 1: Regenerate the local values.yaml before touching anything**

`values.yaml` is gitignored and generated from `values.enc.yaml`. A stale copy is the usual cause of confusing failures here — and `apps/monitoring-blackbox-exporter/values.yaml` currently exists as a **0-byte file**, which the validation script will happily use instead of decrypting.

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
sops --config .sops.yaml -d apps/monitoring-blackbox-exporter/values.enc.yaml \
  > apps/monitoring-blackbox-exporter/values.yaml
wc -c apps/monitoring-blackbox-exporter/values.yaml
```

Expected: a few thousand bytes, not 0.

- [ ] **Step 2: Add the probe target**

This is a structural edit to an encrypted file, so use Workflow B from `CLAUDE.md`.

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
sops --config .sops.yaml -d apps/monitoring-blackbox-exporter/values.enc.yaml \
  > /tmp/blackbox.enc.yaml
```

Edit `/tmp/blackbox.enc.yaml` with the Read and Edit tools. Under `serviceMonitor.targets`, append an entry after the last `http-0N` entry (currently `http-06`), before the `cert-special-01` block:

```yaml
        - name: http-07
          url: https://td.internal.neese-web.de/healthz
```

Two conventions of this file matter here:

- **Target names are deliberately generic.** The file's own comment says so: only `url` and `hostname` are covered by the SOPS `encrypted_regex`, so a descriptive `name:` would sit in the repo in plaintext. Use the next free sequential number, not `td-sync`. (`http-04` is absent; do not reuse it.)
- **Do not add a `module` key.** `serviceMonitor.defaults.module: http_2xx` already applies, and that module handles the HTTPS handshake and follows redirects.

Then re-encrypt, regenerate the local values, and wipe the temp file:

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
sops --config .sops.yaml -e /tmp/blackbox.enc.yaml \
  > apps/monitoring-blackbox-exporter/values.enc.yaml
: > /tmp/blackbox.enc.yaml
sops --config .sops.yaml -d apps/monitoring-blackbox-exporter/values.enc.yaml \
  > apps/monitoring-blackbox-exporter/values.yaml
```

- [ ] **Step 3: Verify the target survived the round-trip**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
sops --config .sops.yaml -d apps/monitoring-blackbox-exporter/values.enc.yaml \
  | grep -A1 'name: http-07'
grep -c 'td.internal.neese-web.de' apps/monitoring-blackbox-exporter/values.enc.yaml
```

Expected: the `http-07` target with its URL from the first command, and `0` from the second — the URL must be encrypted, not sitting in the repo in plaintext.

A SOPS MAC mismatch here usually means a comment ended up inside an encrypted sequence. Move any comment you added out of the `targets` list.

- [ ] **Step 4: Validate**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
scripts/kubeconform-validate.sh apps/monitoring-blackbox-exporter
kustomize build apps/monitoring-blackbox-exporter --enable-helm --enable-alpha-plugins --enable-exec \
  | grep -c 'td.internal.neese-web.de'
```

Expected: validation passes and the grep returns a non-zero count — the ServiceMonitor for `http-07` carries the URL as its `target` scrape parameter.

- [ ] **Step 5: Commit and push**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
git add apps/monitoring-blackbox-exporter/values.enc.yaml
git commit -m "feat(monitoring): probe td-sync health endpoint

/metricz returns JSON rather than Prometheus format, so /healthz via blackbox
is the useful signal - it returns 500 when the database is unreachable.

Claude-Session: https://claude.ai/code/session_01JTJMgDKXYPLzeS5Z7wFtgS"
git pull --rebase origin main
git push origin main
```

`values.yaml` is gitignored and must not appear in the commit — confirm with `git status` before pushing.

- [ ] **Step 6: Verify the probe is green**

```bash
kubectl annotate -n argocd application 17-monitoring-blackbox-exporter argocd.argoproj.io/refresh=hard --overwrite
sleep 60
kubectl get servicemonitor -n monitoring | grep http-07
```

Then query Prometheus:

```bash
kubectl exec -n monitoring sts/prometheus-kube-prometheus-stack-prometheus -c prometheus -- \
  wget -qO- 'http://localhost:9090/api/v1/query?query=probe_success%7Binstance%3D~%22.*td.internal.*%22%7D'
```

Expected: a result with `"value":[<timestamp>,"1"]`.

A value of `0` means the probe runs but the endpoint is unreachable from the monitoring namespace — check DNS resolution of the internal hostname from inside the cluster before blaming the manifest.

---

## Task 9: End-to-end verification of the update chain

The automation is the part most likely to be quietly broken, because nothing fails loudly when Renovate simply finds nothing. Prove the chain works while the context is fresh.

**Files:** none. This task produces evidence.

**Interfaces:**
- Consumes: everything from Tasks 1-8.

- [ ] **Step 1: Confirm k3s-git-ops Renovate can see the image**

The `kubernetes` manager is configured for `/apps/.+/k8s\..*\.yaml$/`, which matches `apps/td-sync/k8s.td-sync.yaml`. Confirm the reference has both a tag and a digest:

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
grep -n 'td-sync:v' apps/td-sync/k8s.td-sync.yaml
```

Expected: two lines (Deployment and backup initContainer), both of the form
`forge.geekbundle.org/madic/td-sync:v0.65.0@sha256:...`.

A reference without `:vX.Y.Z` before the `@` is the silent-failure case this whole design guards against.

- [ ] **Step 2: Confirm Renovate resolves the Forge registry anonymously**

```bash
TOK=$(curl -s "https://forge.geekbundle.org/v2/token?scope=repository:madic/td-sync:pull&service=container_registry" \
      | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
curl -s -H "Authorization: Bearer $TOK" \
     "https://forge.geekbundle.org/v2/madic/td-sync/tags/list"
```

Expected: the tag list including `v0.65.0`. This is the same bearer-challenge flow Renovate's docker datasource performs.

- [ ] **Step 3: Trigger the k3s-git-ops Renovate manually**

Run the `Run renovatebot for updates` workflow in `madic-creates/k3s-git-ops` via `workflow_dispatch` with `dryRun: true` and `logLevel: debug`.

Search the log for `td-sync`. Expected: the dependency is extracted with `currentValue: v0.65.0` and a `currentDigest`. Whether an update is available does not matter — being **found** is the assertion.

If td-sync does not appear at all, the manager file pattern did not match. Re-check the filename is exactly `k8s.td-sync.yaml`.

- [ ] **Step 4: Record the verified state**

```bash
cd /home/mne-adm/Git/personal/github.com/k3s-git-ops
td log -i td-02c66e "Update chain verified end to end: Renovate extracts marcus/td from docker-td's Dockerfile, Forge registry tag listing works anonymously, and k3s-git-ops Renovate extracts td-sync with tag+digest."
td handoff td-02c66e \
  --done "docker-td build repo (Dockerfile, build + renovate workflows, renovate.json5); apps/td-sync deployed via ArgoCD wave 62; first account created and signup closed; restic backup CronJob with sqlite snapshot initContainer; blackbox probe on /healthz; update chain verified" \
  --remaining "None for this scope" \
  --decision "See docs/superpowers/specs/2026-09-07-td-sync-server-design.md" \
  --uncertain "Whether the in-cluster forgejo-runner (65-forgejo-runner.yaml, commented out since 2025-12-15) should be removed from the repo as dead weight"
td review td-02c66e
```

---

## Notes for the reviewer

- **The design spec is the reference.** `docs/superpowers/specs/2026-09-07-td-sync-server-design.md` carries the reasoning behind every choice, including the three upstream findings that shaped it.
- **Three things were verified during design, not assumed:** the Forge registry answers the anonymous bearer challenge; `customManagers:dockerfileVersions` matches the chosen ARG shape; td tags are clean semver.
- **Three things remain unverified until execution:** the Forgejo Actions token has package-write; `golang:1.27-alpine` exists (Task 1 Step 1 checks this first); and whether td uploads pre-existing local history (Task 6 answers it before any real data is at risk).
- **The in-cluster forgejo-runner is disabled** (`apps/argo-cd-apps/kustomization.yaml:59`, commit `4a4bdc28`, 2025-12-15). Builds run on a runner outside the cluster. Cleaning up that dead entry is out of scope here.
