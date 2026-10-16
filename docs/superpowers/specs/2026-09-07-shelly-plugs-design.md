# Design: Shelly Plug M Gen3 Integration (Per-Appliance Breakdown)

Date: 2026-09-07
Issue: td-17eb5e
Builds on: [2026-09-06-energy-dashboard-design.md](2026-09-06-energy-dashboard-design.md)

## Goal

Meter four individual appliances — mini server, washing machine, tumble dryer,
fridge — with Shelly Plug M Gen3 smart plugs, and show them against total
household consumption so it is visible which share of the household load is
accounted for.

**Status: blocked on hardware.** The devices have not arrived. Nothing is
implemented until the Gen3 compatibility check below passes on a real device.

## Device Facts (from vendor documentation, not assumed)

- Shelly Gen3 speaks the same RPC API as Gen2 (`/rpc/Shelly.GetStatus`).
- Over MQTT, Gen2/Gen3 devices publish **JSON objects** on
  `<topic_prefix>/status/switch:0`. There is no mode that publishes one scalar
  per topic.

## Data Path

`webdevops/shelly-plug-exporter` polls the four plugs over HTTP RPC and exposes
`/metrics`; Prometheus scrapes the exporter.

### Why this and not MQTT

The Shellys can publish to MQTT natively and a broker already runs in the
`solar` namespace, which looks like the cheaper path. It is not:

- `mosquitto` currently accepts connections only from inside its namespace and
  runs **anonymous** — the CiliumNetworkPolicy is the entire access control.
  Letting four LAN devices publish into it means exposing the broker to the LAN
  and retrofitting authentication.
- It reverses the data direction. Every other LAN integration here (FRITZ!Box,
  Deye logger) has the cluster pull from the device.
- It needs a *second* `mqtt2prometheus` instance regardless: Shelly publishes
  JSON objects, the existing instance runs in `metric_per_topic` mode, and
  upstream `config.go` rejects a configuration that sets both
  `object_per_topic_config` and `metric_per_topic_config`.

MQTT stays the documented fallback if the exporter turns out not to support
Gen3.

A third option — driving the exporter's `/probe?target=` endpoint with one
ServiceMonitor per plug, mirroring `monitoring-blackbox-exporter` — was
rejected as premature: it makes a single failed plug visible as its own
`up == 0`, but costs four manifests and moves the appliance names into Git.
Revisit if per-plug availability turns out to matter.

## Architecture

New application `apps/monitoring-shelly-exporter/`, following
`apps/monitoring-fritzbox-exporter/` exactly:

| File | Purpose |
|---|---|
| `kustomization.yaml` | namespace `monitoring`, `commonAnnotations` sync-wave `17` |
| `k8s.deployment.yaml` | the exporter, static host list of the four plug IPs |
| `k8s.service.yaml` | ClusterIP on the metrics port |
| `k8s.servicemonitor.yaml` | scrape interval 30s |
| `k8s.np.shelly-exporter.yaml` | egress to the four plug IPs on 80; ingress from Prometheus only |

Plus `apps/argo-cd-apps/17-monitoring-shelly-exporter.yaml` with
`metadata.name: 17-monitoring-shelly-exporter`, `destination.namespace: default`
and no `CreateNamespace`, matching the sibling `monitoring-*` applications.

No Prometheus egress rule is needed: unlike `solar/mqtt2prometheus`, this
exporter runs inside `monitoring`, which the policy's `toEndpoints: - {}` rule
already covers.

## Manual Prerequisites

1. **Fixed IPs** for all four plugs via DHCP reservation in the FRITZ!Box. Not
   optional — the Cilium policy matches CIDRs, so a changed lease silently
   breaks egress.
2. **Set the device name** in each plug ("Waschmaschine", "Trockner",
   "Kühlschrank", "Mini-Server"). The exporter exposes it as the `plugName`
   label, so the dashboard gets readable names without hard-coding them in Git.
3. Leave device authentication **off**. Gen3 ships with it disabled, and the
   plugs are reachable by anything on the LAN regardless; the policy protects
   the cluster→device direction. If it is enabled later, credentials go into a
   SOPS secret alongside the app.

## Metrics

From the exporter (label set verified in upstream `shellyplug/metrics.go`:
`target`, `mac`, `plugName`, plus `id` and `name` on the power/switch/temp
series):

- `shellyplug_power_load_current` — current power in W
- `shellyplug_power_load_total` — cumulative energy in Wh
- `shellyplug_switch_on`, `shellyplug_temperature` — device health
- `shellyplug_info` — carries `plugModel`, `plugGeneration`, `hostname`

Every dashboard query aggregates the infrastructure labels away, as
`max by (plugName) (...)`. A pod restart otherwise creates a second series per
plug and each panel renders it twice — this happened on the energy dashboard
and is the reason the rule is written down here.

## Recording Rules

Added to `apps/monitoring/k8s.prometheusrule.energy.yaml`:

- `home:power_measured:watts` = `sum(shellyplug_power_load_current)`
- `home:power_unaccounted:watts` =
  `clamp_min(home:power_total:watts - home:power_measured:watts, 0)`

`clamp_min` is load-bearing: the plugs and the FRITZ meter are sampled
independently, so timing offsets produce briefly negative differences without
it.

## Dashboard

A new **"Geräte"** row in the existing `Energie — Verbrauch & PV` dashboard
rather than a separate one — the comparison against total consumption is the
whole point, and it only reads directly if it sits next to the totals.

1. **Stacked time series**: the four appliances plus "Rest (nicht gemessen)".
   The stack height is total household consumption, so the unexplained share is
   readable without arithmetic. A `timeseries` panel handles the five frames;
   see the note under 2 for why Bar chart cannot.
2. **Energy per appliance per day** (kWh) from
   `increase(shellyplug_power_load_total[1d])`, with the same midnight-aligned
   1d step as the PV panel. Two consequences carried over from building that
   panel, both of which cost a debugging round there:
   - It produces no bar until a full day boundary has passed. Grafana aligns
     range-query start and end down to multiples of the step, so with a 1d step
     every evaluation point is a midnight.
   - Use a `timeseries` panel with `custom.drawStyle: "bars"`, **not** the Bar
     chart panel. Bar chart supports only one dataset ("using multiple datasets
     can result in unexpected behavior" — Grafana docs), and four appliances
     means four data frames, which renders as an empty panel.
3. **Four stat tiles** with current power per appliance.

Colours: five series (four appliances plus the remainder) need a palette run
through `dataviz`'s validator for both light and dark, as was done for the
generation/import pair.

### The remainder is sound

An earlier draft warned that the remainder would inherit "export blindness"
from the FRITZ plug. That premise was wrong: the plug reports signed power, so
`home:power_total:watts` is the true household load in both directions and the
remainder needs no caveat. See the correction section in the energy dashboard
spec.

## Alerting

`up == 0` on the exporter for 10 minutes. Nothing per appliance.

A "fridge has drawn 0 W for hours" alert would be genuinely useful — it catches
a failed appliance — but defrost and compressor cycles make the threshold
guesswork without data. Design it separately after a few weeks of history.

## Open Questions to Resolve Before Implementing

1. **Does the exporter read a Gen3 device at all?** It advertises generation 1
   and 2. Check by pointing it at one plug and confirming `shellyplug_info`
   reports `plugGeneration="3"` with a non-zero
   `shellyplug_power_load_current`. If it fails, fall back to the MQTT path.
2. **Which host flag applies?** The exporter has separate
   `--shelly.host.shellyplug`, `--shelly.host.shellyplus` and
   `--shelly.host.shellypro` options and it is not obvious which covers a
   Plug M Gen3. Determine empirically; do not guess.
3. **Image tag.** Pin a concrete released tag from the upstream registry at
   implementation time; do not use `latest`.

## Testing

- `kustomize build apps/monitoring-shelly-exporter --enable-helm --enable-alpha-plugins --enable-exec`
- `scripts/kubeconform-validate.sh apps/monitoring-shelly-exporter apps/monitoring`
- `promtool check rules` on the extended PrometheusRule, and the dashboard
  expressions extracted into a synthetic rules file, as before.
- Post-deploy: confirm four distinct `plugName` values appear and that
  `home:power_measured:watts` is plausible against `home:power_total:watts`.

## Out of Scope

- Switching the plugs on or off from the cluster. Read-only.
- Per-appliance anomaly alerting.
- Replacing the FRITZ!Smart Energy as the grid-import meter.

---

## Addendum, 2026-09-11: eleven plugs

Seven more Shelly Plug M Gen3 joined the four: Spuelmaschine (.91),
Kuehlschrank (.89), Router (.189), Arbeitsplatz 1 (.66), Arbeitsplatz 2 (.173),
Wohn Fernseher (.108), Wohn Verstaerker (.150). Same model and firmware
(2.0.0) as the existing plugs, and the exporter's shared password is accepted,
so no secret changed.

**Personal names stay out of Git.** Two plugs carry first names on the device.
In the repository they are only "Arbeitsplatz 1" and "Arbeitsplatz 2"; the
real name reaches Grafana at runtime through the `name` label. Consequently no
panel or rule may match a plug by name.

**Identity is the MAC.** Plugs are selected into groups by `address` (the same
IPs as the host list and the egress policy) and aggregated by `mac`. `name`
was the identity before and failed: the fourth plug was renamed from
"Kuehlschrank" to "Tiefkuehlschrank" on 2026-09-11, and the next plug to arrive
was a real fridge called "Kuehlschrank", so by name the freezer's first hours
merged into the fridge. `home:power_measured:watts` and
`ShellyExporterDeviceMissing` were moved to `by (mac)` for the same reason; the
alert threshold is now 11.

**Four groups, not eleven colours.** The categorical palette has eight
validated slots, four already carry fixed meanings, and under the all-pairs
check that a stacked chart requires even the full eight fail (CVD dE 1.6,
normal-vision dE 7.1). The stacked power chart and the daily energy bars
therefore show four groups by load profile -- Waschen & Spuelen, Wohnzimmer,
Kuehlen, IT & Netz -- in the four passing slots, and each plug appears as a
labelled bar whose colour marks its group. The operator chose this over
per-plug small multiples. Grouping by load profile rather than room keeps the
continuous loads -- Kuehlen, IT & Netz -- readable as the bulk of base load.
