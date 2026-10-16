# td-sync Server im k3s-Cluster — Design

- **Datum:** 2026-09-07
- **td-Issue:** td-02c66e
- **Upstream:** https://github.com/marcus/td (v0.65.0)
- **Ops-Guide:** `docs/sync-server-ops-guide.md` im Upstream-Repo

## Ziel

Den td-Sync-Server im bestehenden k3s-Cluster betreiben, damit der lokale
`td`-CLI seine Issues über mehrere Geräte und AI-Sessions hinweg synchronisieren
kann. Erreichbarkeit ausschließlich intern (LAN/VPN). Versions-Updates laufen
automatisiert über Renovate.

## Recherche-Befunde, die das Design bestimmen

Drei Eigenschaften von td-sync weichen von dem ab, was der Ops-Guide auf den
ersten Blick nahelegt:

1. **Kein publiziertes Container-Image.** `marcus/td` released ausschließlich
   Binaries (v0.65.0: linux/darwin, amd64/arm64). Weder GHCR noch Docker Hub
   führen ein Image. Der Guide baut selbst aus `deploy/Dockerfile`.

2. **Litestream deckt nur `server.db` ab.** `deploy/litestream.yml` listet genau
   eine DB. Die eigentlichen Issue-Daten liegen in Per-Projekt-Datenbanken unter
   `SYNC_PROJECT_DATA_DIR` (`/data/projects`), die dynamisch angelegt werden.
   Litestream 0.3.x kennt keine Globs, kann sie also nicht erfassen. Das
   mitgelieferte Backup schützt die Metadaten, nicht den Inhalt.

3. **Login erfordert zwingend E-Mail.** `internal/email/` implementiert nur
   `cloudflare.go`, `log.go` und `memory.go` — kein SMTP. Das im Cluster
   vorhandene Postfix-Relay ist ohne Upstream-Änderung nicht nutzbar. Der
   Bestätigungslink wird nicht im Terminal angezeigt; der Login schließt erst ab,
   wenn der Link aufgerufen wurde.

Weiter relevant:

- `/metricz` liefert JSON (`internal/api/metrics.go`, `MetricsSnapshot`), kein
  Prometheus-Exposition-Format.
- `/healthz` gibt 500 zurück, wenn die DB nicht erreichbar ist.
- Der Entrypoint startet Litestream unbedingt und wrappt `td-sync` darin
  (`litestream replicate -exec "td-sync"`). Ein Deployment ohne Litestream
  erforderte ein Command-Override.
- td taggt sauber semver (`v0.65.0`, `v0.64.0`, …) — für Renovate unproblematisch.
- Die Forge-Registry gibt auf `/v2/` zwar 401, beantwortet aber den
  Standard-Bearer-Challenge anonym. Gegen `madic/docker-samba` getestet: die
  Tag-Liste kommt ohne Credentials zurück. Renovates Docker-Datasource macht
  genau diesen Flow, kann die Registry also lesen.

## Getroffene Entscheidungen

| Entscheidung | Gewählt | Alternative(n) | Begründung |
|---|---|---|---|
| Erreichbarkeit | Nur intern | Cloudflare Tunnel, öffentliches Traefik | Sync wird nur zuhause bzw. über VPN gebraucht; kleinste Angriffsfläche |
| Image-Build | Forgejo Actions in `madic/docker-td` → `forge.geekbundle.org` | GitHub-Fork + Actions → ghcr.io; lokal bauen; initContainer lädt Release-Binary | Deckt sich mit `madic/docker-samba`, `docker-radarr`, `docker-nzbget`; Repo existiert bereits |
| Backup | Longhorn-PVC + restic-CronJob | Nur Longhorn; zusätzlich Litestream→versitygw (S3) | Nur restic erfasst die Projekt-DBs; Off-Site-Mirror nach Wasabi läuft bereits |
| Auth-Mail | `SYNC_EMAIL_PROVIDER=log` | Cloudflare Email API; SMTP-Provider upstream beitragen | Login passiert selten (Credentials landen in `~/.config/td/auth.json`); null Zusatzinfrastruktur |
| Update-Automatisierung | Renovate im Build-Repo (Forgejo Actions) + bestehendes Renovate in k3s-git-ops | Cron-Poll im Build-Workflow; manuell | Trackt neben `TD_VERSION` auch die Base-Images; Review-Gate an beiden Stellen |

## Architektur

Zwei getrennte Repositories:

```
forge.geekbundle.org/madic/docker-td              (Build, außerhalb k3s-git-ops)
  ├─ Dockerfile                    ARG TD_VERSION  ← Renovate bumpt das
  ├─ renovate.json5                platform=forgejo
  └─ .forgejo/workflows/
       ├─ build.yaml               push auf main → Image bauen und pushen
       └─ renovate.yaml            Cron → Renovate
                    │
                    │ forge.geekbundle.org/madic/td-sync:vX.Y.Z@sha256:…
                    ▼
k3s-git-ops/apps/td-sync/                          (Deployment)
  └─ von ArgoCD via apps/argo-cd-apps/62-td-sync.yaml ausgerollt
```

Lokaler Klon des Build-Repos: `/home/mne-adm/Git/personal/github.com/docker-td`
(Remote `ssh://git@forge.geekbundle.org/madic/docker-td.git`, bisher nur ein
README). Vorlage für Aufbau und Renovate-Config ist
`/home/mne-adm/Git/personal/github.com/container-restic`.

### Build-Repo `madic/docker-td`

#### Dockerfile

Anders als im Ops-Guide klont das Dockerfile den Upstream **selbst**. Damit ist
`TD_VERSION` eine einzelne, von Renovate pflegbare Zeile, und `entrypoint.sh`
sowie `litestream.yml` kommen unverändert aus dem Checkout — es gibt keine
Kopien davon zu pflegen.

```dockerfile
# renovate: datasource=github-releases depName=marcus/td
ARG TD_VERSION="v0.65.0"

FROM golang:1.27-alpine AS builder
ARG TD_VERSION
RUN apk add --no-cache git
WORKDIR /src
RUN git clone --depth 1 --branch "${TD_VERSION}" https://github.com/marcus/td.git .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/td-sync ./cmd/td-sync

FROM litestream/litestream:0.3.13 AS litestream

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

Unterschiede zum Upstream-`deploy/Dockerfile`:

- Der Upstream baut aus dem Build-Context; wir klonen im Builder-Stage.
- `sqlite` kommt zusätzlich ins Runtime-Image (Begründung im Backup-Abschnitt).

`ARG TD_VERSION` vor dem ersten `FROM` ist ein globales ARG und muss im
Builder-Stage ohne Wert erneut deklariert werden, damit es dort sichtbar ist.

Beim ersten Build zu prüfen: `golang:1.27-alpine` und
`litestream/litestream:0.3.13` müssen als Tags existieren. Danach hält Renovates
nativer Dockerfile-Manager sie aktuell.

#### Build-Workflow

`.forgejo/workflows/build.yaml`, `runs-on: ubuntu-latest` (der Runner läuft
außerhalb des Clusters). Trigger: Push auf `main` mit Pfadfilter auf
`Dockerfile` und die Workflow-Datei, plus `workflow_dispatch`.

Bewusst **keine `docker/*`-Actions**, sondern nackte `docker`-CLI-Steps:
`uses:`-Referenzen lösen auf Forgejo gegen `DEFAULT_ACTIONS_URL` auf, und ob die
`docker/*`-Actions dort liegen, ist instanzabhängig. Nur `actions/checkout` wird
verwendet — das ist sowohl auf code.forgejo.org als auch auf GitHub vorhanden.

Ablauf:

1. Checkout
2. `TD_VERSION` aus dem Dockerfile lesen (`sed`), als Step-Output setzen
3. `docker login forge.geekbundle.org` mit `${{ github.actor }}` und dem
   automatisch bereitgestellten Actions-Token
4. `docker build`, taggen mit `:${TD_VERSION}` und `:latest`, beides pushen

Der Registry-Login über den automatischen Actions-Token ist beim ersten Lauf zu
verifizieren; setzt voraus, dass Package-Write für Actions aktiv ist.

#### Renovate im Build-Repo

`.forgejo/workflows/renovate.yaml` (Cron, täglich) fährt Renovate mit
`platform: forgejo` und `endpoint: https://forge.geekbundle.org/api/v1` gegen
`madic/docker-td`.

`renovate.json5` übernimmt die Konventionen aus `container-restic`
(`onboarding: false`, `automergeType: 'branch'`, `ignoreTests: true`,
`major.automerge: false`, `minimumReleaseAge: '5 days'`,
`minimumReleaseAgeBehaviour: 'timestamp-optional'`,
`internalChecksFilter: 'strict'`) und erweitert `extends` um den Preset
**`customManagers:dockerfileVersions`**. Dessen Regex matcht genau das oben
gewählte Muster (`# renovate: …` gefolgt von `ARG …_VERSION="…"`) — ein eigener
Custom-Manager ist nicht nötig.

`platformAutomerge` bleibt aus: Forgejo unterstützt es erst ab v10, und mit
`automergeType: 'branch'` merged Renovate ohnehin selbst.

Zwei Secrets:

| Secret | Zweck |
|---|---|
| `RENOVATE_TOKEN` | Forgejo-PAT. Scopes: `repo` (RW), `user` (R), `issue` (RW), `organization` (R), `read:packages` |
| `GITHUB_COM_TOKEN` | GitHub-PAT (nur öffentliche Repos, read-only). Die `github-releases`-Datasource fragt github.com ab und läuft ohne Token schnell ins Rate-Limit |

Optional, falls Docker Hub für `golang`/`alpine`/`litestream` limitiert:
`hostRules` mit Docker-Hub-Credentials.

#### Die Update-Kette

```
Renovate (Forgejo)  bumpt ARG TD_VERSION   → Branch-Automerge auf main
        ↓
build.yaml          baut, pusht td-sync:vX.Y.Z + :latest
        ↓
Renovate (GitHub)   sieht den neuen Tag in der Forge-Registry
        ↓
PR in k3s-git-ops   aktualisiert Tag + Digest in k8s.td-sync.yaml
```

Damit das letzte Glied greift, muss das Image in `k8s.td-sync.yaml` als
`:vX.Y.Z@sha256:…` referenziert werden, **nicht** digest-only. Die bestehenden
Forge-Images (`docker-samba@sha256:…` usw.) sind digest-only gepinnt; Renovate
hat dort kein `currentValue` und trackt sie deshalb heute nicht. Mit Tag greift
der bereits konfigurierte `kubernetes`-Manager (Pattern
`/apps/.+/k8s\..*\.yaml$/`, passt auf `apps/td-sync/k8s.td-sync.yaml`) und
aktualisiert Tag und Digest gemeinsam.

Major-Updates werden in beiden Repos nicht automerged. Weil td auf `0.x` steht
und ein Minor-Sprung dort brechen darf, bekommt `marcus/td` zusätzlich eine
explizite `packageRule` mit `matchUpdateTypes: ['minor'], automerge: false` —
`v0.65 → v0.66` landet damit als PR zum Anschauen statt automatisch zu mergen.
(Sich darauf zu verlassen, dass Renovate `0.x`-Minor von sich aus wie Major
behandelt, wäre eine ungeprüfte Annahme.)

### Cluster-App

`apps/td-sync/`:

| Datei | Inhalt |
|---|---|
| `k8s.td-sync.yaml` | Namespace, ServiceAccount, Deployment, Service |
| `k8s.pvc.yaml` | `longhorn-pvc-td-sync`, 2Gi, RWO |
| `ingress.enc.yaml` | SOPS-verschlüsselt, Host `td.internal.neese-web.de` |
| `k8s.np.td-sync-default-deny.yaml` | CiliumNetworkPolicy, Default-Deny |
| `k8s.np.td-sync.yaml` | CiliumNetworkPolicy für td-sync |
| `k8s.backup-cronjob.yaml` | restic-CronJob + zugehörige CiliumNetworkPolicy |
| `backup-env-configuration-td-sync.enc.yaml` | restic-Konfiguration, SOPS |
| `kustomization.yaml` | Ressourcen + ksops-Generator, `sync-wave: "62"` |
| `kustomize-secret-generator.yaml` | ksops für `ingress.enc.yaml` und `backup-env-configuration-td-sync.enc.yaml` |

Dazu `apps/argo-cd-apps/62-td-sync.yaml` (Sync-Wave 62, zwischen `60-semaphore`
und `64-downloader-lidarr`) und der Eintrag in
`apps/argo-cd-apps/kustomization.yaml`.

Namespace: `td-sync`, mit Label `goldilocks.fairwinds.com/enabled: "true"`.

#### Deployment

- `replicas: 1`, `strategy: Recreate`. SQLite verträgt keine zwei gleichzeitigen
  Writer, und die Longhorn-PVC ist ReadWriteOnce. Ein RollingUpdate würde
  kurzzeitig zwei Pods erzeugen und scheitern bzw. Daten gefährden.
- Image: `forge.geekbundle.org/madic/td-sync:v0.65.0@sha256:…`
- Port 8080 (`http`)
- Readiness- und Liveness-Probe: `httpGet` auf `/healthz`
- `resources.requests`: 50m CPU / 64Mi RAM. Goldilocks/VPA korrigiert später auf
  Basis echter Werte.
- `securityContext`: `runAsUser: 1000`, `runAsGroup: 1000`, `fsGroup: 1000`,
  `runAsNonRoot: true`; Container: `allowPrivilegeEscalation: false`,
  `capabilities.drop: [ALL]`. Das Upstream-Image setzt kein `USER`, würde also
  als root laufen; `fsGroup` sorgt dafür, dass das Volume für UID 1000 schreibbar
  ist.

Kein `imagePullSecret`: die Forge-Registry erlaubt anonymen Pull, die
bestehenden `madic/docker-*`-Deployments arbeiten ebenfalls ohne.

**Volumes:** eine PVC, zwei Mounts über `subPath`:

| Mount | subPath | Zweck |
|---|---|---|
| `/data` | `data` | `server.db` und `projects/*.db` |
| `/backups` | `backups` | Litestream-File-Replica von `server.db` |

Der Entrypoint startet Litestream unbedingt. Statt das zu umgehen, bekommt
`/backups` einen echten Mount: die File-Replica überlebt Neustarts und wird vom
restic-Job mitgesichert. Sie ist ein konsistenter Zusatz-Snapshot von
`server.db`, ersetzt aber das restic-Backup nicht (Projekt-DBs fehlen).

**Environment:**

```
SYNC_LISTEN_ADDR       = :8080
SYNC_BASE_URL          = https://td.internal.neese-web.de
SYNC_SERVER_DB_PATH    = /data/server.db
SYNC_PROJECT_DATA_DIR  = /data/projects
SYNC_ALLOW_SIGNUP      = true   → nach Erstaccount auf false
SYNC_EMAIL_PROVIDER    = log
SYNC_LOG_FORMAT        = json
SYNC_LOG_LEVEL         = info
SYNC_SHUTDOWN_TIMEOUT  = 60s
```

Keine Werte davon sind geheim; sie stehen im Klartext im Deployment. Nur
Ingress (Domain) und restic-Config werden verschlüsselt.

#### Ingress

Host `td.internal.neese-web.de`, nach dem Muster der internen Apps:

- `traefik.ingress.kubernetes.io/router.entrypoints: web239,websecure239`
- `router.tls: "true"`
- `router.middlewares: traefik-redirect@kubernetescrd`
- `tls.secretName: wildcard-cloudflare-production-02`

**Kein Authelia-ForwardAuth.** Die anderen internen Apps setzen
`authelia-forwardauth-authelia@kubernetescrd`, hier würde es die API brechen: der
td-CLI authentifiziert sich mit Bearer-Token gegen `/v1/...` und kann keinen
SSO-Redirect verarbeiten. td bringt eigene Authentifizierung (Device-Flow +
API-Keys) mit.

#### NetworkPolicies

Default-Deny für den Namespace, dazu eine App-Policy:

- **Ingress:** nur von Traefik (`k8s:app.kubernetes.io/instance: traefik-kube-system`,
  Namespace `kube-system`) auf Port 8080
- **Egress:** nur CoreDNS (53/UDP + 53/TCP)

Mehr braucht td-sync nicht: der `log`-Provider verschickt keine Mails, Litestream
repliziert nur auf das lokale Dateisystem, und es gibt keine Outbound-Calls.

## Backup

restic-CronJob nach dem etablierten Muster des Repos (siehe `docs/backup.md`):
Ziel ist der `rest-server` im Namespace `backup`, der tägliche rclone-Mirror nach
Wasabi und die zentrale Retention-Policy greifen automatisch.

- `podAffinity` auf den td-sync-Pod — die Longhorn-PVC ist RWO, beide Pods müssen
  auf demselben Node laufen
- Das Skript kommt über den Reflector in den Namespace: die ConfigMap
  `crontab-backup-script` in `longhorn` hat `reflection-auto-enabled: "true"`
  ohne Namespace-Einschränkung, wird also überall gespiegelt. Keine Änderung an
  `apps/backup-script/` nötig.
- restic-Container: `ghcr.io/madic-creates/container-restic` (Digest wie in den
  anderen Jobs), `envFrom` auf `backup-env-configuration-td-sync`
- Eigene CiliumNetworkPolicy: DNS, Pushgateway 9091, rest-server 8000,
  `world:443` für ntfy im Fehlerfall

### Konsistenz der SQLite-Snapshots

Ein direkter `restic backup` auf `/data` liest `foo.db`, `foo.db-wal` und
`foo.db-shm` nacheinander, nicht atomar. Läuft parallel ein Write, kann der
Snapshot zerrissen und beim Restore unbrauchbar sein.

Lösung analog zum bestehenden MariaDB-Job (initContainer erzeugt den Dump, restic
versendet ihn):

1. **initContainer** mit unserem eigenen `td-sync`-Image führt für `server.db`
   und jede `projects/*.db` ein `sqlite3 <db> ".backup /snapshot/<name>.db"` aus,
   Ziel ist ein `emptyDir`.
2. **restic-Container** sichert ausschließlich dieses `emptyDir`.

Deshalb `sqlite` im Dockerfile: kein `apk add` zur Laufzeit (das bräuchte Egress
zum Alpine-CDN und eine entsprechend weite NetworkPolicy), kein Fremd-Image, und
das Image ist ohnehin unter unserer Kontrolle.

Die PVC wird im Backup-Pod **read-write** gemountet. SQLite braucht beim Öffnen
einer WAL-Datenbank Schreibzugriff auf die `-shm`-Datei; das `readOnly: true` des
Grafana-Jobs funktioniert hier nicht.

## Monitoring

`/metricz` liefert JSON und ist nicht direkt scrapebar. Für den ersten Ausbau:

- Blackbox-Exporter-Probe auf `https://td.internal.neese-web.de/healthz` — der
  Exporter läuft bereits (`apps/monitoring-blackbox-exporter/`)
- die bestehenden generischen Pod-/Deployment-Alerts

Ein `json_exporter` für `/metricz` wäre nachrüstbar, ist für einen
Single-User-Sync-Server aber unverhältnismäßig.

## Rollout

1. Build-Repo `docker-td` befüllen (Dockerfile, Workflows, `renovate.json5`),
   Secrets setzen, Build auslösen, Digest notieren
2. `apps/td-sync/` und den ArgoCD-Application-Eintrag committen und pushen,
   zunächst mit `SYNC_ALLOW_SIGNUP=true`
3. ArgoCD-Sync abwarten, `/healthz` prüfen
4. `td config set sync.url https://td.internal.neese-web.de`
5. `td auth login`, Bestätigungslink aus
   `kubectl logs -n td-sync deploy/td-sync` holen und im Browser aufrufen
6. `SYNC_ALLOW_SIGNUP=false` committen und syncen lassen
7. Migration verifizieren (siehe unten), dann pro Projekt
   `td sync-project create "<name>"` und `td sync`
8. Renovate-Kette einmal manuell durchspielen (`workflow_dispatch` auf
   `renovate.yaml`), um zu sehen, dass `TD_VERSION` erkannt wird

### Migration des bestehenden Bestands

**Vor Schritt 7 zu klären:** ob `td sync-project create` + `td sync` den
vorhandenen lokalen `.todos/`-Bestand vollständig hochlädt oder erst ab dem
Zeitpunkt der Verknüpfung neue Events repliziert. Der Sync-Setup-Guide sagt dazu
nichts.

Vorgehen: zuerst gegen ein Wegwerf-Projekt mit ein paar Test-Issues prüfen, ob
die Historie ankommt. Erst danach die echten Projekte anbinden. `.todos/` wird
vorher gesichert.

## Bewusst nicht im Scope

- Multi-User-Betrieb, Einladungen, Rollen (`td sync-project invite`)
- Öffentliche Erreichbarkeit über Cloudflare Tunnel
- Litestream-Replikation nach versitygw/S3
- `json_exporter` für `/metricz`
- Ein SMTP-Provider als Upstream-Beitrag
- **Der in-cluster forgejo-runner.** `apps/argo-cd-apps/kustomization.yaml:59`
  hat `65-forgejo-runner.yaml` auskommentiert (Commit `4a4bdc28`, 2025-12-15,
  *„Disable forgejo runner for now"*), Namespace und ArgoCD-Application
  existieren nicht. Gebaut wird auf einem Runner außerhalb des Clusters. Ob die
  Karteileiche im Repo bleibt oder aufgeräumt wird, ist eine eigene Entscheidung.
