# Design: Solar Irradiance as an Expected-Yield Reference

Date: 2026-09-07
Issue: td-40206d
Builds on: [2026-09-06-energy-dashboard-design.md](2026-09-06-energy-dashboard-design.md)
Related: [2026-09-07-shelly-plugs-design.md](2026-09-07-shelly-plugs-design.md) (td-17eb5e)

## Goal

Give the energy dashboard a reference for what the PV array *should* be
producing, so that a given output figure can be judged instead of merely read.
250 W at 15:00 is either an overcast afternoon or a failing array, and today
nothing distinguishes the two.

Per-string DC values already catch *relative* faults — pv1 diverging from pv2.
They cannot catch a fault that affects both strings equally: soiling, snow,
curtailment, gradual degradation. An external irradiance reference supplies the
missing "what should it be doing".

## Scope Decision

**Reference only.** Open-Meteo also serves a forecast, and an hourly forecast
would make load shifting actionable — the measured surplus of 0.15 kWh against
3.1 kWh generated says the lever for self-consumption is running the washing
machine while the sun is up, not producing more.

That is deliberately **out of scope here**. Prometheus stores what has happened;
future-timestamped samples do not belong in it, so a forecast panel would need a
second Grafana datasource (Infinity/JSON-API), a new plugin, and a new egress
path — roughly doubling the surface area of a P3 issue. It becomes a follow-up
once td-17eb5e's plugs exist to act on it.

**No standalone sunshine panel.** The inverter already measures irradiance at
the panels directly via per-string DC voltage and current; external data is a
proxy for that. It earns its place only through expected-vs-actual comparison.

## Site Parameters

Supplied by the operator, converted to Open-Meteo's convention (0 = south,
negative = east, positive = west; the operator gave compass bearings, where
0 = north).

| String | Compass bearing | Open-Meteo azimuth | Tilt | Peak power |
|---|---|---|---|---|
| `pv1` | 45° (NE) | **-135** | 40° | 560 Wp |
| `pv2` | 225° (SW) | **+45** | 40° | 560 Wp |

Total 1120 Wp into a Deye SUN-M160G4. `pv3` and `pv4` are not connected and
always read 0 — correct, not a fault.

Tilt, azimuth and peak power are **not** location-identifying and stay in plain
YAML. The site coordinates are handled separately — see "Location Privacy".

**The near-opposite orientation of the two strings is the central fact of this
design.** A single averaged tilt/azimuth would misrepresent both panels: the NE
string peaks late morning, the SW string mid-afternoon. Each string needs its
own irradiance series.

## Verified API Behaviour

Every statement below was executed against `api.open-meteo.com` on 2026-09-07
with the real site coordinates. No API key is required.

**One tilt/azimuth pair per request.** Passing comma-separated values with
duplicated coordinates fails:

```
HTTP 400  {"error":true,"reason":"Value was not of type 'Float' at path 'tilt'."}
```

Two orientations therefore mean **two requests**.

**`current=` is supported and returns 15-minute values.** This is what makes the
exporter simple — no indexing into an hourly array, no timezone arithmetic:

```json
"current_units":{"time":"iso8601","interval":"seconds","global_tilted_irradiance":"W/m²"},
"current":{"time":"2026-09-07T17:15","interval":900,
           "global_tilted_irradiance":468.5,"shortwave_radiation":320.0,"cloud_cover":18}
```

The native interval is 900 s, so **polling faster than every 15 minutes gains
nothing**.

**The grid cell is not the exact site.** Open-Meteo snaps the request to its own
model grid, which resolved roughly 1 km from the site (elevation 64 m).
Acceptable for an irradiance reference; worth knowing before chasing a small
systematic offset. It also means sub-kilometre precision in the stored
coordinates buys nothing.

**GTI already incorporates cloud cover.** Measured today: 448 W/m² at 4% cloud
at midday, against heavily suppressed morning values at 100% cloud. This is the
property that makes the whole design work — see "Why the ratio is a health
signal" below.

### Modelled vs. actual, 2026-09-07

| Hour | NE GTI | SW GTI | Expected at PR=1.0 |
|---|---|---|---|
| 09:00 | 145.6 | 81.2 | 127 W |
| 12:00 | 448.3 | 511.4 | 537 W |
| 14:00 | 334.7 | 758.0 | 612 W |
| 16:00 | 168.4 | 793.3 | 539 W |
| 19:00 | 89.2 | 289.1 | 212 W |

Ideal day total **4.56 kWh** against roughly **3.1 kWh** actually generated,
giving a performance ratio of about **0.68** — a believable real-world figure
for a fixed, steeply mounted split-orientation array.

The 3.1 kWh figure comes from an earlier session's log, not from a query run
during this design: the calibration query against Prometheus was blocked by the
sandbox because the endpoint document carries an inline credential. **Confirming
0.68 against a clear day is an implementation step, not an established fact.**

## Location Privacy

**The site coordinates must never appear in plain text anywhere in this
repository.** They identify the operator's home address to within a few metres,
and the repository is public.

They are therefore stored in `apps/solar/secrets.enc.yaml`, the SOPS-encrypted
Secret that already exists in this app for the inverter serial number. Its
`stringData` block matches the `encrypted_regex` for `.*.enc.yaml` in
`.sops.yaml`, so two new keys are encrypted automatically with no config change:

```yaml
stringData:
  serial_number: <existing>
  latitude: <encrypted>
  longitude: <encrypted>
```

The exporter reads them as environment variables via `secretKeyRef`
(`OPENMETEO_LATITUDE`, `OPENMETEO_LONGITUDE`), exactly as
`monitoring-fritzbox-exporter` reads its credentials.

Three consequences that are easy to get wrong:

1. **The exporter must not emit coordinates as metric labels.** Open-Meteo
   echoes `latitude`/`longitude` in every response and the naive thing is to
   pass them through. A Prometheus label is readable by anyone with dashboard
   access and is copied into every backup of the TSDB.
2. **No coordinates in log output**, including on error. Log the failing
   parameter *names*, never the query string.
3. **No coordinates in this spec, in commit messages, or in
   `docs/energy-metrics-api.md`** — that file is handed to an external agent
   which needs the array geometry to reason about the generation curve but has
   no use for the location.

Because the model grid snaps to roughly a kilometre anyway, storing fewer
decimal places costs no accuracy — but rounding is defence in depth, not a
substitute for encryption.

## Architecture

```
api.open-meteo.com
        │  2 × HTTPS GET every 15 min (one per string orientation)
        ▼
openmeteo-exporter          Deployment, namespace solar, sync wave 66
        │  /metrics, served from memory
        ▼
Prometheus                  ServiceMonitor
        │
        ▼
recording rules             namespace monitoring, energy.rules group
        │
        ▼
energy dashboard
```

### Why an exporter Deployment and not a CronJob to pushgateway

Pushgateway serves its last value forever. A dead CronJob would keep showing
yesterday's irradiance as "expected now", making a healthy array look broken or
a broken one look fine. That is the same reason pushgateway was rejected for the
Deye inverter, and it is worse here: the entire purpose of this series is to
judge health, so a stale reference is actively misleading rather than merely
absent.

An exporter gets the right failure semantics for free — `up == 0` alerts, and
the series disappear with the pod instead of lying.

### Why a stock image with a mounted script, and not a built image

The exporter is ~50 lines of standard-library Python. Building and publishing a
container image for that would add a release pipeline and one more thing for
Renovate to track. `apps/solar` already has the pattern this needs: a
`configMapGenerator` that appends a content hash to the ConfigMap name so a
config change actually restarts the pod (used today for `mosquitto.conf` and the
mqtt2prometheus config, both of which read their configuration only at startup).

The script uses **standard library only** — no `pip install` at container start,
which would make the pod depend on a package index being reachable at boot.

### Why namespace `solar` and not `monitoring`

It is a PV data source and belongs beside the inverter bridge it references.
The rules and dashboard stay in `monitoring` with the rest of the energy logic.

## Metrics Contract

The exporter emits **measurements and site configuration only** — never
derived quantities. No performance ratio, no expected watts. That keeps the
model in git-tracked PromQL where changing it is a commit rather than a pod
restart.

| Metric | Type | Unit | Notes |
|---|---|---|---|
| `solar_irradiance_tilted_wm2{string="pv1"\|"pv2"}` | gauge | W/m² | GTI at that string's orientation |
| `solar_irradiance_global_horizontal_wm2` | gauge | W/m² | GHI, orientation-independent context |
| `solar_cloud_cover_ratio` | gauge | 0..1 | context |
| `solar_array_peak_power_watts{string=...}` | gauge | W | static 560 per string |
| `solar_openmeteo_last_success_timestamp_seconds` | gauge | s | staleness signal |
| `solar_openmeteo_request_failures_total` | counter | | |

`solar_array_peak_power_watts` is exported rather than written as a literal in
PromQL so the rules read as a formula over labelled data. Adding a third panel
later becomes a config change, not a rule edit.

GHI and cloud cover are orientation-independent and are taken from the first of
the two requests only.

### Serving details

Metrics port **9779**, named `metrics`. ServiceMonitor scrapes every **60 s**
with a 10 s timeout. The scrape is a pure in-memory read — it never triggers an
API call — so the interval is free to be short and is unrelated to the 15-minute
poll. A `readinessProbe` on the metrics port is an HTTP probe here, unlike
fritzbox-exporter's deliberate TCP probe, precisely because serving costs
nothing.

### Behaviour on API failure

The last good values are kept in memory and continue to be served;
`solar_openmeteo_last_success_timestamp_seconds` stops advancing and
`SolarIrradianceStale` catches it. The exporter must **not** exit on a failed
poll — a transient API error should not take the reference offline — but it must
also not silently serve week-old data without saying so, which is what the
timestamp metric is for.

## Recording Rules

Added to the existing `energy.rules` group in
`apps/monitoring/k8s.prometheusrule.energy.yaml`.

Every raw metric is wrapped in `max by (string)`, per that file's own documented
rule about exporter pod restarts creating a new series per pod.

```promql
# PR = 1.0 physical ceiling
solar:ideal_power:watts =
  sum(
    max by (string) (solar_irradiance_tilted_wm2) / 1000
      * on (string) group_left max by (string) (solar_array_peak_power_watts)
  )

# The single performance-ratio literal in the whole design
solar:expected_power:watts = solar:ideal_power:watts * 0.68

solar:performance_ratio =
  home:power_generation:watts / clamp_min(solar:ideal_power:watts, 1)
```

`clamp_min(..., 1)` guards the division at night when the ceiling is zero.

The `0.68` carries a comment stating it was calibrated from one day
(4.56 kWh modelled against ~3.1 kWh actual) and must be revisited after several
clear days.

### Why the ratio is a health signal

Because Open-Meteo's GTI already includes cloud cover, `actual / ideal` stays
roughly flat across weather. A cloudy day lowers both the numerator and the
denominator. A *drop in the ratio* therefore points at the array, not the sky —
which is exactly the fault class the per-string DC values cannot catch.

## Alerting

```
SolarArrayUnderperforming
  solar:performance_ratio < 0.40 and solar:ideal_power:watts > 300
  for: 2h        severity: warning

SolarIrradianceExporterDown
  up{job="openmeteo-exporter"} == 0
  for: 30m       severity: warning

SolarIrradianceStale
  time() - solar_openmeteo_last_success_timestamp_seconds > 3600
  for: 30m       severity: warning
```

`0.40` sits between the 0.68 baseline and the ~0.34 that losing one of two
strings would produce. The `ideal_power > 300` guard keeps dawn, dusk and deep
overcast out of the comparison, where a small absolute error produces a wild
ratio. `for: 2h` keeps passing weather out.

**Both constants are provisional.** They are derived from a single day, which is
precisely the kind of unverified assumption the comments in
`k8s.prometheusrule.energy.yaml` exist to warn about. They must be re-evaluated
once a few weeks of `solar:performance_ratio` history exists, and the rule file
must say so.

`SolarIrradianceExporterDown` uses `up`, not metric absence — consistent with
`SolarExporterDown` and `FritzboxExporterDown`. Unlike the inverter, this
exporter has no legitimate nightly outage, so a 30m window is safe.

The two staleness alerts are complementary and neither is redundant:
`...ExporterDown` catches a dead pod (at which point the timestamp metric is
absent and cannot fire), while `...Stale` catches the harder case of a *healthy*
pod that has lost its upstream and is quietly serving old values.

## Network Policy

The exporter is the **first internet-dependent source in the energy stack** —
every other one (inverter logger, FRITZ!Box) is LAN-local.

A new `CiliumNetworkPolicy` selecting only
`app.kubernetes.io/name: openmeteo-exporter`:

- ingress: Prometheus from `monitoring`, on the metrics port
- egress: DNS to kube-dns, and HTTPS to `api.open-meteo.com`

### Known risk: toFQDNs may not be load-bearing in this cluster

Cilium's `toFQDNs` only works when the DNS request passes through Cilium's DNS
proxy, which requires a `toPorts` rule carrying `rules.dns.matchPattern` on the
DNS egress. **No policy in this repository has such a rule.** The existing
`toFQDNs` entries (cert-manager, CoreDNS, pihole-01, pihole-03) may therefore
never have been load-bearing — cert-manager's, for instance, is backstopped by a
`toCIDRSet` covering the same addresses.

Plan: use `toFQDNs` *together with* a proper DNS-proxy rule, scoped to this
exporter's endpoint only. Cilium policies apply per endpoint selector, so
`deye-inverter-mqtt` and `mqtt2prometheus` keep their current DNS path
untouched and cannot be broken by this change.

**Fallback if egress does not come up:** `toEntities: world` on port 443, the
pattern proven in `apps/epicgames-freegames`. Broader than ideal for a pod that
talks to exactly one API, but known to work. Verify with
`hubble observe --namespace solar` before concluding either way.

## Dashboard Changes

`apps/monitoring/ks8.grafana-dashboards.energy.yaml`. Panel titles are German,
matching the existing dashboard; comments and code stay English.

| Panel | Kind | Content |
|---|---|---|
| **Erzeugung gegen Erwartung** | timeseries (new) | `home:power_generation:watts` against `solar:expected_power:watts` — the core deliverable |
| **Anlagenleistung** | stat (new) | `solar:performance_ratio * 100`, in % |
| **Anlagengesundheit** (existing row) | timeseries | gains `max by (string) (solar_irradiance_tilted_wm2)`, so a diverging string reads against its own reference |
| **Datenquellen** (existing) | | gains `up{job="openmeteo-exporter"}` |

The ConfigMap holding the dashboard must go through `configMapGenerator` so the
content hash rolls the Grafana sidecar, per the existing pattern.

## Credential Hygiene

`docs/energy-metrics-api.md` contains a live Grafana service-account token, says
in its own header that it must not be committed, and **is not gitignored**. The
repository is public and no pre-commit hook recognises a Grafana token, so a
single `git add docs/` would leak it.

- Add `docs/energy-metrics-api.md` to `.gitignore`.
- Document the array parameters from this design in that file's
  "The physical setup" section, which currently says only "two panels".
- **The file itself is never committed.** Only the `.gitignore` line is.

## Files

```
apps/solar/openmeteo-exporter.py            new   ~50 lines, stdlib only
apps/solar/k8s.openmeteo-exporter.yaml      new   Deployment + Service + ServiceMonitor
apps/solar/k8s.np.openmeteo-exporter.yaml   new   CiliumNetworkPolicy
apps/solar/kustomization.yaml               edit  + configMapGenerator, + resources
apps/solar/secrets.enc.yaml                 edit  + latitude, longitude (SOPS)
apps/monitoring/k8s.prometheusrule.energy.yaml    edit  3 rules, 3 alerts
apps/monitoring/ks8.grafana-dashboards.energy.yaml edit 2 new panels, 2 amended
.gitignore                                  edit  + docs/energy-metrics-api.md
docs/energy-metrics-api.md                  edit  array parameters — NEVER COMMITTED
```

## Testing

1. Run `openmeteo-exporter.py` locally against the live API; confirm the two
   GTI values differ as expected for NE vs SW at the current hour.
1b. `grep -rn` the working tree for the coordinate digits before committing, and
   confirm `/metrics` output and pod logs contain neither.
2. `kustomize build apps/solar --enable-helm --enable-alpha-plugins --enable-exec`
3. `scripts/kubeconform-validate.sh apps/solar apps/monitoring`
4. `pre-commit run --all-files`
5. After sync: `up{job="openmeteo-exporter"} == 1`.
6. Confirm egress actually works — `hubble observe --namespace solar` — before
   assuming `toFQDNs` took effect.
7. On the next clear day, check `solar:performance_ratio` lands near 0.68 and
   adjust the literal if not.

## Out of Scope

- Forecast and load-shifting panels (follow-up issue)
- Historical irradiance backfill for past days
- Any new Grafana plugin or datasource
- Modelling temperature coefficients, shading, or angle-of-incidence losses;
  all of that is absorbed into the single performance-ratio constant

---

## Addendum, 2026-09-07 evening: per-string, after first contact with reality

The design above compares **aggregate** generation against an aggregate
expectation. Deployment showed that cannot work here, and the deployed system
differs accordingly.

**What the first measurements showed.** At 18:10 local, pv1 was at 0.67 of its
modelled irradiance while pv2 was at 0.19, with both producing roughly the same
absolute power. The operator confirmed the cause: pv2 faces south-west and is
shaded by a tree from the early afternoon. The operator also confirmed the
string mapping — pv1 is the north-east panel, pv2 the south-west one — which
until then had only been inferred from the order of a sentence.

**Why the aggregate design was wrong.** The shading is permanent and
predictable, so an aggregate expected curve overstates afternoon output every
single day. A reference that is reliably wrong at the same time daily teaches
the reader to ignore the panel — the exact failure the "ideal ceiling only"
option was rejected for. The aggregate ratio was already 0.291, below the 0.40
threshold, with only the `ideal_power > 300` guard keeping the alert quiet.

**What changed.**

- Three per-string recording rules: `solar:ideal_power_by_string:watts`,
  `solar:expected_power_by_string:watts`, `solar:performance_ratio_by_string`.
- `SolarArrayUnderperforming` replaced by `SolarStringUnderperforming`: per
  string, and only between 07:00 and 12:00 UTC, before the shading begins.
  `hour()` is UTC, so the window drifts an hour across DST — harmless when the
  shading starts in the afternoon, and PromQL cannot express local time.
- Panel 18 plots each string against its own model; panel 19 shows the ratio
  per string.
- The aggregate rules are kept: they carry history and still answer "how is the
  whole array doing right now".

**One basis difference to keep in mind.** Per-string actuals are DC, because
there is no per-string AC value; the aggregate uses AC. A DC ratio excludes
inverter conversion loss and the inverter's own draw, so it reads slightly
higher for identical conditions. The 0.68 constant is reused for now and needs
its own calibration.

**Verified after deployment.** For pv1, expected 40.6 W against an actual
40.1 W — the reference sits on the measurement, which validates the API, the
geometry, the peak-power figures and the constant for an unshaded string.

**Two deployment defects, both fixed.** DNS failed because `ndots:5` makes the
resolver try three search-domain permutations first, which a narrow
`matchName` rule caused Cilium to answer REFUSED, and musl treats REFUSED as
fatal (commit 102c830b). Scrapes then timed out because Prometheus's own egress
allowlist did not include the new exporter — a namespaced CiliumNetworkPolicy's
`toEndpoints: - {}` only matches its own namespace (commit f702f104). The
second one is deceptive: the target's ingress rule is correct and its BPF byte
counters sitting at zero are what proves the packets never arrived.
