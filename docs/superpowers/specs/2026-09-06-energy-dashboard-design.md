# Design: Energy Dashboard (Household Consumption + PV Generation)

Date: 2026-09-06
Issue: td-b80c1f

## Goal

A Grafana dashboard showing household electricity consumption and photovoltaic
generation together, so that current power flow, self-sufficiency and daily
energy yield can be read at a glance.

Data sources:

- **FRITZ!Smart Energy 250** smart plug, read via the FRITZ!Box — reports
  **signed** power at the grid connection: positive while importing, negative
  while exporting PV surplus.
- **Deye SUN-M160G4-EU-Q0** micro inverter, read locally from its Solarman
  WiFi logger stick — reports current PV generation.

## Reconnaissance Findings (verified 2026-09-06)

These were measured against the live devices, not assumed:

- Logger stick reachable at `192.168.1.136`, TCP port `8899` open.
- Stick status page (`/status.html`, basic auth `admin`/`admin`) reports
  inverter serial `2406160BE0`, logger serial `3873566823`, and live values
  (`webdata_now_p`, `webdata_today_e`, `webdata_total_e`).
- **Solarman V5 / Modbus works on this unit.** Holding registers read
  successfully via `pysolarmanv5`:
  - `0x0056` = 3980 → 398.0 W AC active power (matches the status page)
  - `0x005B` = 2462 → 246.2 V AC voltage
  - `0x005D` = 4997 → 49.97 Hz grid frequency
  - `0x0075`, `0x0076` = 458, 436 → DC string voltages
  This matters because public reports claim some 4th-generation Deye micro
  inverters ship with firmware that disables Modbus. This one does not.
- The FRITZ!Box (`192.168.1.1`) initially offered only the users `fritz7965`
  and `Madic`; the `monitoring` user referenced by the existing encrypted
  secret no longer existed. The operator has since recreated it, and the
  credentials now authenticate against the AHA interface.
- The smart plug is a **FRITZ!Smart Energy 250**, AIN `16000 0036823` with the
  powermeter on sub-unit `16000 0036823-1`. Live reading at the time of
  writing: 200 W draw, 241.3 V, lifetime counter 15 523 kWh — a lifetime
  counter that size confirms it meters the house rather than a single
  appliance.
- The container image `madic/fritzbox_exporter` is **not present on Docker
  Hub**. The existing app (then at `apps/prox01/fritzbox-exporter/`, since moved) was commented out in
  `apps/argo-cd-apps/kustomization.yaml:59` and is not deployable as-is.

## Measurement Semantics

The FRITZ!Smart Energy plug measures grid draw, i.e. what is left after the PV
generation has already been consumed. Therefore:

```
grid_import    = fritz_ha_multimeter_power_W      (measured)
generation     = deye_ac_active_power_w           (measured)
total_load     = grid_import + generation         (derived)
self_suff. [%] = generation / total_load * 100    (derived)
```

### Correction (2026-09-07): the plug is NOT direction-blind

This section previously claimed the plug measured magnitude only, so that
exported surplus read as positive draw and inflated both total load and
self-sufficiency. **That was wrong and was never verified.**

Measured over two days: 122 of 1672 samples are negative, minimum -202 W. The
FRITZ!Smart Energy 250 reports signed power — negative while exporting.

Consequences:

- `total_load = grid + generation` is correct in both directions after all,
  because the grid term carries the sign. No correction needed.
- Self-sufficiency as `generation / total_load` **overflows** its own range: at
  414 W generation against 396 W load it reads 105%, and up to 537% as load
  approaches zero. It is now capped with `clamp_max(..., 1)`; whatever exceeds
  100% is surplus, not self-sufficiency, and is reported separately by the
  surplus panels. The dashboard applies `clamp_max` a second time at query
  time, because a recording rule never rewrites the samples it already wrote —
  without that, the historical 105% and 537% readings would keep rendering.
- Exported surplus is derived inline as
  `clamp_min(-home:power_grid_import:watts, 0)` rather than recorded as its own
  rule. A rule added later carries no history, and the surplus panels have to
  show past days.
- Two power panels carried `min: 0`, silently clipping the exported half of the
  curve. Removed.
- The "Plausibility check" row existed only to make invisible export visible.
  It survives as a plain unstacked overlay, where export is now directly
  readable as the area below zero.

## Architecture

Four new components across two ArgoCD applications.

| Component | Purpose | Location |
|---|---|---|
| `pdreker/fritz_exporter` | TR-064 + AHA → `fritz_ha_*` metrics | `apps/monitoring-fritzbox-exporter/` (replaces the dead `madic/` image) |
| `eclipse-mosquitto` | MQTT broker, cluster-internal only | `apps/solar/` |
| `kbialek/deye-inverter-mqtt` | Solarman V5 against `192.168.1.136:8899`, metric group `micro`, protocol `tcp` | `apps/solar/` |
| `hikhvar/mqtt2prometheus` | MQTT topics → `/metrics` endpoint | `apps/solar/` |

### Why this data path

`kbialek/deye-inverter-mqtt` explicitly supports the M160 micro inverter
(metric group `micro`) and is the best-maintained option, but publishes only to
MQTT. Two alternatives were considered and rejected:

- **Custom Go exporter** speaking Solarman V5 directly: one pod instead of
  three, but creates a permanently self-maintained component and image build
  for a niche protocol.
- **`deye-inverter-mqtt` plugin pushing to the existing Pushgateway**: also one
  pod, but Pushgateway retains the last value indefinitely. For a live power
  dashboard, stale values silently presented as current is the worst available
  failure mode.

The chosen path costs two extra containers and keeps normal Prometheus pull
semantics with no self-written code. The broker is reusable for future smart
home integrations.

### Grouping

The three solar components are deployed as a **single** ArgoCD application
(`apps/argo-cd-apps/66-solar.yaml`, namespace `solar`, sync wave 66). None of
them delivers value without the other two, so splitting them into separate
applications would add ordering constraints without any benefit.

### Scraping

One ServiceMonitor per exporter. Because `apps/monitoring/values.yaml:114` sets
`serviceMonitorSelectorNilUsesHelmValues: false`, ServiceMonitors in any
namespace are *discovered* without further Prometheus configuration.

Discovery is not reachability. The Prometheus CiliumNetworkPolicy carries a
`toEndpoints: - {}` egress rule commented "scrape metrics from all namespaces",
but in a namespaced CiliumNetworkPolicy an empty endpoint selector only matches
endpoints in the policy's own namespace. Every scrape target outside
`monitoring` therefore needs an explicit egress rule in
`apps/monitoring/k8s.np.monitoring.yaml` — which is why a rule for
`databases/mariadb:9104` already exists there, and why one was added for
`solar/mqtt2prometheus:9641`. The fritzbox exporter needs none: it runs in
`monitoring` itself.

### Network policies

Following the repository's default-deny + per-app-allow Cilium pattern:

- `deye-inverter-mqtt`: egress to `192.168.1.136:8899`
- `fritz_exporter`: egress to `192.168.1.1:49000` (TR-064) and `:80` (AHA)
- Both: ingress on the metrics port from Prometheus only
- `mosquitto`: ingress from `deye-inverter-mqtt` and `mqtt2prometheus` only

### Polling

Deye poll interval 30 s (upstream default is 60 s), Prometheus scrape interval
30 s. If the logger stick becomes unstable at that rate, fall back to 60 s.

## Manual Prerequisite

This cannot be automated and must be done by the operator before deployment:

1. Create a FRITZ!Box user with the permissions **"Smart-Home-Geräte steuern"**
   and **"FRITZ!Box-Einstellungen"**.
2. Enable *Heimnetz → Netzwerk → Netzwerkeinstellungen → Zugriff für
   Anwendungen zulassen* (TR-064).
3. Store username and password in the existing SOPS secret
   `apps/monitoring-fritzbox-exporter/secrets.enc.yaml`.

## Metrics

From `pdreker/fritz_exporter` (verified in upstream source
`fritzexporter/fritzcapabilities.py`, class `HomeAutomation`):

- `fritz_ha_multimeter_power_W` — current power in W
- `fritz_ha_multimeter_energy_Wh` — cumulative energy in Wh
- `fritz_ha_temperature_C`, `fritz_ha_device_present`
- Labels: `serial`, `friendly_name`, `ain`, `device_name`, `device_id`,
  `manufacturer`, `productname`

From the Deye chain. `deye-inverter-mqtt` publishes one MQTT topic per metric,
so `mqtt2prometheus` is configured in `metric_per_topic_config` mode: the topic
suffix is matched against `mqtt_name` and the raw payload is the value.

The topic list was read from the upstream sensor definitions
(`src/deye_sensors.py`, entries carrying the `micro` group) rather than guessed.
It yields 31 topics, of which 27 are mapped to 15 Prometheus metrics:

- `ac/active_power`, `ac/l1/{voltage,current}`, `ac/freq`
- `day_energy`, `total_energy`
- `radiator_temp`, `uptime`, `operating_power`, `dc/total_power`
- `dc/pv{1..4}/{voltage,current,power,day_energy}`
- `status` (availability topic) → `deye_logger_online`

The four per-string `total_energy` topics are deliberately not mapped: lifetime
per-string counters add series without answering a question the dashboard asks.

Per-string values share one metric name each and are distinguished by a
`string="pv1".."pv4"` label, so a single panel can graph all four.

## Recording Rules

Derived values live in a `PrometheusRule`, not in dashboard expressions, so
that every panel uses an identical definition:

- `home:power_grid_import:watts` = `sum(fritz_ha_multimeter_power_W)`
- `home:power_generation:watts` = `sum(deye_ac_active_power_watts) or vector(0)`
- `home:power_total:watts` = grid import + generation
- `home:autarky:ratio` = generation / `clamp_min(total, 1)`

`or vector(0)` on generation is load-bearing: the inverter powers down at night
and its series disappear, which would otherwise make total household load
vanish along with it.

Grid import is pinned to the plug's AIN (`ain=~"16000.?0036823.*"`) so that a
second smart plug paired later cannot be silently summed into household
consumption. The regex is deliberately loose in two places: the FRITZ!Box lists
the device both as `16000 0036823` and as the sub-unit `16000 0036823-1`, and
only the sub-unit carries the powermeter; the `.?` also absorbs the space in
the AIN in case the exporter normalises it away. The same selector is applied
to `fritz_ha_multimeter_energy_Wh` in the dashboard's daily bar chart.

The AIN is a local DECT device identifier, not a credential — it is useless
without FRITZ!Box authentication — so it lives in plaintext alongside the LAN
IPs the repository already publishes, rather than in a SOPS secret that a
PrometheusRule could not read anyway.

## Dashboard

File: `apps/monitoring/ks8.grafana-dashboards.energy.yaml` (ConfigMap, label
`grafana_dashboard: "1"`, matching the existing dashboard pattern).
Title: `Energie — Verbrauch & PV`. Default range 24 h, refresh 30 s.

**Row 1 — Snapshot.** Four stat panels: generation, grid import, total load,
self-sufficiency. The four numbers the dashboard gets opened for.

**Row 2 — Core chart.** Stacked time series of grid import + generation. The
stack height *is* total load and the yellow share *is* self-sufficiency, so
both are readable without arithmetic. Below it, self-sufficiency in % as its
own curve.

**Row 3 — Energy rather than power.** Yield today and total, plus a bar chart
of PV kWh against grid-import kWh per day over 14 days
(`increase(fritz_ha_multimeter_energy_Wh[1d])`). This answers "is it worth it",
which the power curves cannot.

**Row 4 — Plausibility check.** Grid import and generation overlaid, *not*
stacked. Where grid import rises together with PV output instead of falling,
the export periods described under "Known limitation" become visible.

**Row 5 — System health** (collapsed by default). DC voltage and current per
MPPT string (a shaded or failed panel shows up immediately), AC voltage and
grid frequency (excessive grid voltage makes the inverter throttle — the most
common explanation for unexpectedly low output), inverter temperature, and
scrape status of both sources.

## Alerting

Three alerts, routed through the existing Alertmanager chain:

- `SolarExporterDown` / `FritzboxExporterDown`: `up == 0` for 10 min.
- `EnergyGridImportMissing`: the pinned AIN matches nothing while the exporter
  is up, i.e. the plug was re-paired and the dashboard would silently show
  generation only.

The design originally called for a staleness alert on "no MQTT message received
for 10 min". That was dropped: the inverter legitimately stops publishing every
night, so it would have fired nightly. `up == 0` on the exporter pod carries the
same signal without the seasonal false positives, and the short
`mqtt2prometheus` cache timeout (5 min) already prevents stale values from being
served as current — the series simply disappears instead.

A "no production at midday" alert is deliberately omitted: without solar
position data it is a winter-long noise source.

## Testing

- `kustomize build apps/solar --enable-helm --enable-alpha-plugins --enable-exec`
- `scripts/kubeconform-validate.sh apps/solar` and the same for the rewritten
  `apps/monitoring-fritzbox-exporter`
- `pre-commit run --all-files`
- Dashboard JSON validated by building the ConfigMap and confirming the Grafana
  sidecar loads it without error.
- Post-deploy: confirm both `/metrics` endpoints return the expected series,
  and cross-check `deye_ac_active_power_w` against the logger stick's own
  status page.

## Out of Scope

- Solarman cloud API integration (not needed — local access works).
- Cost/tariff calculations.
- Any write access to the inverter (read-only Modbus only).
