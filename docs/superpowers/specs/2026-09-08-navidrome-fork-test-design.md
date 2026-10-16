# Design: Testing the Navidrome Podcast-UX Fork as a Parallel Instance

Date: 2026-09-08
Issue: td-e7c3cf

## Goal

Evaluate `search5/navidrome@feat-podcast-ux-improvements/5420` (upstream PR
navidrome/navidrome#5949, which addresses issue #5420; HEAD
`07cf48cb8c927b5aa062309c352536ef8752a6d4`) in the real
cluster, against real library data, without putting the production Navidrome
instance at risk.

The fork publishes no container image, so one has to be built. It is rebased on
Navidrome `master` — `go 1.27`, mpv-based playback, multi-library support —
which is far ahead of the deployed `ghcr.io/navidrome/navidrome:0.63.2`.

## The Constraint That Shapes Everything

Navidrome runs goose migrations against `navidrome.db` at startup. The branch
carries five podcast migrations:

```
db/migrations/20260427165650_add_podcast.go
db/migrations/20260427184047_add_podcast_downloaded_bytes.go
db/migrations/20260428000000_add_podcast20.go
db/migrations/20260428120000_add_podcast_tier3.go
db/migrations/20260428200000_add_podcast_metadata.go
```

Pointing this build at the production data folder would migrate the database to
a master-era schema. `0.63.2` cannot read that schema, so there would be no way
back short of restoring a backup. Everything below follows from refusing to
accept that risk.

A second consequence, from `core/podcasts/podcasts.go:301` and `:349`: episodes
are downloaded into `${DataFolder}/podcasts/<channelID>/`, and the code registers
a **virtual library whose root is DataFolder**. Running the fork therefore writes
both files and an extra `library` row. There is no feature flag gating this —
`conf/configuration.go` has no podcast setting at all.

## Scope Decision

**In scope:** a second, fully isolated Navidrome deployment in namespace `media`,
running the fork against a point-in-time copy of the production database, reachable
at `music-test.k8s.neese-web.de`.

**Out of scope:** merging test-instance state back into production. Once the copy
is taken the two instances diverge permanently. Playlists, play counts, and
podcast subscriptions created in the test instance are throwaway.

**Out of scope:** building the image in Forgejo CI. That is the established
pattern for the long-lived custom images (`td-sync`, `docker-samba`,
`docker-nzbget`, `docker-radarr`), but this is a one-off evaluation and a local
build gets there faster with a warm cache. If the fork turns into something
permanent, moving the build to `madic/docker-navidrome` is the follow-up.

## Architecture

### 1. Image Build

The fork's `Dockerfile` is a standard buildx multi-stage build. `--target final`
pulls the binary from the `build-alpine` stage; the `osxcross` stage feeds only
the standalone-distribution `build` stage and is never touched by a native amd64
build of `final`. BuildKit is mandatory — the Dockerfile uses heredoc `RUN <<EOT`
blocks and `--mount=type=cache`.

```bash
git clone --depth 1 --branch feat-podcast-ux-improvements/5420 \
  https://github.com/search5/navidrome.git /tmp/navidrome-5420
cd /tmp/navidrome-5420

docker login forge.geekbundle.org
docker buildx build \
  --platform linux/amd64 \
  --target final \
  --build-arg GIT_SHA=07cf48cb \
  --build-arg GIT_TAG=5420-podcast-ux \
  -t forge.geekbundle.org/madic/navidrome-test:5420-07cf48cb \
  --push .
```

`GIT_SHA` and `GIT_TAG` are load-bearing, not cosmetic: they are linked into
`consts.gitSha` / `consts.gitTag` via ldflags and are the only way to confirm in
the running UI which build is live.

The registry package stays **public**. No manifest in this repo uses
`imagePullSecrets` for `forge.geekbundle.org`, and this instance must not become
the exception.

After the push, resolve the digest and pin it in the deployment, matching the
convention used by every other `forge.geekbundle.org` image in the repo:

```bash
docker buildx imagetools inspect \
  forge.geekbundle.org/madic/navidrome-test:5420-07cf48cb --format '{{.Manifest.Digest}}'
```

### 2. Cluster Manifests

The test resources live **inside `apps/navidrome/`**, not in a separate app
directory. The existing ArgoCD Application `33-navidrome` (sync wave 33, source
path `apps/navidrome`) then manages them with no new Application manifest and no
new entry in `apps/argo-cd-apps/kustomization.yaml`. Because that Application has
`prune: true`, teardown is exactly "delete the files and push".

New files:

| File | Purpose |
|---|---|
| `k8s.test.deployment.yaml` | Deployment `navidrome-test` |
| `k8s.test.service.yaml` | Service `navidrome-test` |
| `k8s.np.navidrome-test.yaml` | CiliumNetworkPolicy for the test pod |
| `ingress-test.enc.yaml` | SOPS-encrypted IngressRoutes |

Modified: `kustomization.yaml` (three resources added) and
`kustomize-secret-generator.yaml` (one ksops file added).

#### The label must differ

The production Service selects `app.kubernetes.io/name: navidrome`. If the test
pod carried that same label, Traefik would load-balance production traffic onto
an unvetted fork build — silently, with no error anywhere. The test Deployment
therefore uses `app.kubernetes.io/name: navidrome-test` throughout: pod template
labels, Deployment selector, and Service selector.

That label change also means the existing CiliumNetworkPolicy `navidrome` does
not cover the test pod — its `endpointSelector` matches the production label. A
second policy is required, otherwise the default-deny posture leaves the test pod
unreachable and unable to resolve DNS.

#### Deployment differences from production

Everything else is copied from `k8s.deployment.yaml` unchanged — same
`securityContext` (`runAsUser: 1001`, `runAsGroup: 1002`, `fsGroup: 1002`), same
`nodeSelector` (`node-role/workload: zfs`), same `dnsConfig` (`ndots: 1`), same
`strategy: Recreate`.

| Setting | Production | Test | Why |
|---|---|---|---|
| `image` | `ghcr.io/navidrome/navidrome:0.63.2` | `forge.geekbundle.org/madic/navidrome-test@sha256:…` | the fork build |
| `config-vol` hostPath | `/vol_raidz1/container-scheduler/navidrome` | `…/navidrome-test` | schema isolation |
| `music-vol` mount | read-write | **`readOnly: true`** | the fork must not be able to touch the library |
| `ND_SCANSCHEDULE` | `@every 1h` | `"0"` | the copied DB already holds the scanned library |

`ND_MUSICFOLDER`, `ND_LOGLEVEL`, `ND_REVERSEPROXYWHITELIST`, `ND_ENABLEUSEREDITING`,
`ND_ENABLESHARING` and `TZ` are carried over as-is.

#### Secrets are reused, deliberately

The test Deployment references the **existing** `navidrome-secrets` Secret. This
is a correctness requirement, not convenience: `ND_PASSWORDENCRYPTIONKEY` must
match the key that encrypted the passwords in the copied database, or no user can
log in. Reusing the Secret makes that impossible to get wrong. No new
`*.enc.yaml` secret file is created.

#### Ingress

`ingress-test.enc.yaml` mirrors the three production IngressRoutes on host
`music-test.k8s.neese-web.de`, entrypoint `websecure`:

| Path rule | Middlewares |
|---|---|
| `PathPrefix('/')` | `traefik/redirect`, `authelia/forwardauth-authelia` |
| `PathPrefix('/rest/') && !Query('c','NavidromeUI')` | `traefik/redirect`, `authelia/forwardauth-authelia-basic-forward-auth` |
| `PathPrefix('/share/')` | `traefik/redirect` |

All three point at Service `navidrome-test` port 4533, and carry over
`tls: {}` and `observability.accessLogs: true` from the production routes.

The host falls under `wildcard-cloudflare-production-01`, which Traefik already
serves on the `web`/`websecure` entrypoints — no Certificate resource and no
cert-manager change is needed.

The file is encrypted because `.sops.yaml`'s `encrypted_regex` covers hostnames,
matching how `ingress.enc.yaml` is handled today. Like that file, it is emitted
through ksops rather than listed under `resources:`.

#### Network policy

`k8s.np.navidrome-test.yaml` is `k8s.np.navidrome.yaml` with the
`endpointSelector` changed to `app.kubernetes.io/name: navidrome-test`. The rules
themselves need no change:

- **Ingress** from Traefik (`kube-system`) and Authelia (`authelia`) on 4533
- **Egress** to CoreDNS on 53/UDP+TCP
- **Egress** to `world` on 80 and 443 — which already covers podcast RSS feeds and
  episode downloads, so podcast fetching works without a policy change

### 3. Data Copy

Copying a live SQLite database with `cp` yields a torn snapshot: the `-wal` and
`-shm` sidecars are not captured atomically alongside the main file. SQLite's
`.backup` command is online-safe and produces a consistent single file.

On the ZFS node:

```bash
D=/vol_raidz1/container-scheduler
mkdir -p "$D/navidrome-test"
sqlite3 "$D/navidrome/navidrome.db" ".backup '$D/navidrome-test/navidrome.db'"
chown -R 1001:1002 "$D/navidrome-test"
```

Only `navidrome.db` is copied. Cache directories are left behind; the instance
rebuilds them.

If shell access to the node is undesirable, the same three commands run as a
one-off Job with both host paths mounted — permitted by the GitOps rules as a
cluster-state operation not represented in Git. The Navidrome image is
`alpine:3.22`-based and ships `sqlite`, so it needs no other image.

From this point the two databases diverge permanently. See Scope Decision.

### 4. Verification

In order — each step gates the next:

1. `kubectl logs -n media deploy/navidrome-test` — the five podcast migrations
   apply cleanly and the server reports it is listening on 4533
2. Log in at `music-test.k8s.neese-web.de` with production credentials. Success
   proves the encryption key and the database copy are consistent with each other.
3. `kubectl get endpoints -n media navidrome` returns **only** the production pod
   IP. This is the check that the label separation held.
4. Subscribe to a podcast feed and download an episode; confirm files land under
   `/vol_raidz1/container-scheduler/navidrome-test/podcasts/`
5. Exercise the actual subject of the evaluation — the PR's podcast UX changes

### 5. Teardown

Delete the four new files, revert the two modified files, push. ArgoCD prunes the
Deployment, Service, CiliumNetworkPolicy and IngressRoutes.

`/vol_raidz1/container-scheduler/navidrome-test/` is not managed by ArgoCD and
must be removed by hand. Depending on how many episodes were downloaded it may
hold a non-trivial amount of data.

The production instance is never modified at any point, so there is nothing to
roll back on that side.

## Risks

| Risk | Mitigation |
|---|---|
| Test pod absorbs production traffic | distinct `app.kubernetes.io/name` label; verified explicitly in step 3 |
| Fork writes to the music library | `readOnly: true` on the music mount |
| Users cannot log in to the test instance | production `navidrome-secrets` reused, so `ND_PASSWORDENCRYPTIONKEY` matches |
| Unvetted build exposed externally | Authelia forward-auth on `/` and `/rest/`, same as production |
| Torn database copy | `sqlite3 .backup` instead of `cp` |
| Podcast downloads fill the pool | ZFS pool, monitored; removed at teardown |

## Addendum 2026-09-08: The Branch Does Not Compile

The first build attempt failed. Root cause, reproduced both in Docker and with a
native `go build`:

```
core/podcasts/podcasts.go:8:2:   "net" imported and not used
core/podcasts/podcasts.go:10:2:  "net/url" imported and not used
core/podcasts/podcasts.go:315:12: undefined: validateURL
core/podcasts/podcasts.go:539:12: undefined: validateURL
```

`validateURL` is called twice but never defined. The only definition in the tree
is `conf/configuration.go:906`, a different two-argument function in another
package. The base branch `feat/podcast` contains no reference to `validateURL`
at all, so this is an incomplete commit by the PR author, not a bad rebase.

The function was never deleted — it was never written. Four commits within 65
seconds on 2026-09-07, all web-editor edits, add everything around it:
`cb193a0e8a` the two imports, `037fcfd8d7` the call in `fetchAndParse`,
`a2aaf3e9d6` `/podcast/preview` behind `adminOnlyMiddleware`, `07cf48cb8c` the
call in `doDownload`. That burst is a security hardening pass, so an SSRF guard
was the intent — which is what the `net` import is for.

No reusable helper exists in the tree: `conf.validateURL` is unexported with a
different signature, the plugin host client's check is a method coupled to
per-plugin permissions, and `core/artwork/agent_images.go:35` rolls its own
inline. A local function matches the codebase convention.

`apps/navidrome/navidrome-pr5949.patch` supplies it, mirroring the
scheme and host checks of `conf.validateURL` and the address check of
`isPrivateOrLoopback` in `plugins/host_httpclient.go`. Like that one it inspects
IP literals only and does not stop DNS rebinding.

This leaves the branch's own suite at 78 passed / 23 failed; every failure is the
guard rejecting `httptest` fixtures on `127.0.0.1`. A scheme-only variant passes
101/101 but provides no SSRF protection. The guard was kept because the test pod's
CiliumNetworkPolicy permits egress to `world`, which in Cilium includes the home
LAN — so an attacker-supplied feed URL would otherwise reach it. Neither the
Docker build nor pre-commit runs those tests.

Consequence for the evaluation: **what runs in the cluster is PR + patch, not the
PR verbatim.**

The build step therefore gains a `git apply` between clone and build.

### Second defect: existing databases cannot be migrated

The first deploy of the patched image still failed, this time at startup:

```
Failed to apply new migrations
error: found 5 missing migrations before current version 20260703013908
```

The five podcast migrations carry April 2026 timestamps while the branch is
rebased on master, whose schema is already past them. `db/db.go:107` calls
`goose.UpContext(ctx, db, migrationsFolder)` with no options, and goose refuses
out-of-order migrations by default.

Reproduced as a controlled experiment rather than assumed: a database driven to
the current schema, then stripped of the five podcast versions and their tables,
reproduces the fatal error exactly with an unpatched binary. With
`goose.WithAllowMissing()` all five apply, the eight `podcast_*` tables appear,
the versions are recorded, and the pre-existing tables are untouched.

The patch adds that option. It is safe here only because the five migrations are
self-contained — they create and alter `podcast_*` tables they own and touch
nothing else. Upstream should renumber them instead; the flag relaxes ordering
for every migration, not just these.

This is the more serious of the two defects for the PR: the compile error blocks
anyone building it, but the migration ordering blocks every existing installation
from upgrading.

## Open Questions

None. Everything the design depends on was verified against the branch tree at
`07cf48cb`.
