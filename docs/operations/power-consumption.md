# Power Consumption Audit

Findings of a power audit of the four cluster nodes (October 2026, td-08070d). No change to the cluster came out of it directly. The measurable reductions are tracked as separate td issues, listed at the end.

## Measurement setup

- There is one meter for the whole cluster: the Shelly plug named `Server`. It measures a power strip with **all four nodes** on it, not node04 alone.
- node01-03 have no meter of their own, so a saving on the NUCs cannot be measured today. The NUC rail needs its own plug first.
- The Shelly readings are scraped by Prometheus (see [Monitoring](../monitoring.md)).

## Baseline

4 hours at 30 s resolution (481 samples):

| Statistic | Watts |
|---|---|
| median | 201.1 |
| p05 | 191.7 |
| p95 | 213.4 |
| min / max | 187.9 / 238.3 |
| standard deviation | 7.4 |

About 75 % of the draw is a base load that does not depend on CPU load. The 5-95 % band is only 22 W wide.

A regression of the Shelly power against the CPU load of all four nodes has R² = 0.021, with some coefficients negative. That is no signal at all. The total cannot be split per node from correlation alone. It needs a controlled experiment, see below.

## Per-node load experiment

td-866186 put one node at a time under full CPU load and read the change on the `Server` plug. Each node got 6 minutes of load: one niced shell busy loop per hardware thread, stopped by `timeout`. Every load window had a 6-minute idle window before and after it. The first 60 s of each window were dropped, which leaves 10 samples per window at the 30 s scrape interval.

| Window | Mean (W) | Min (W) | Max (W) |
|---|---|---|---|
| idle | 191.7 | 185.3 | 199.2 |
| load node01 | 240.6 | 229.8 | 277.0 |
| idle | 201.7 | 186.8 | 223.2 |
| load node02 | 240.6 | 232.1 | 253.1 |
| idle | 193.6 | 185.0 | 209.3 |
| load node03 | 229.5 | 225.7 | 234.7 |
| idle | 196.5 | 187.8 | 209.5 |
| load node04 | 232.8 | 226.8 | 241.2 |
| idle | 202.0 | 191.2 | 232.7 |

The delta is the load window minus the mean of the idle windows on either side:

| Node | CPU | Delta under full load |
|---|---|---|
| node01 | i5-10210U, 4C/8T | +44 W |
| node02 | i5-10210U, 4C/8T | +43 W |
| node03 | i5-10210U, 4C/8T | +34 W |
| node04 | Ryzen 5 3600, 6C/12T | +34 W |

What this means:

- The NUC BIOS raises the RAPL limits far above the 15 W TDP of the i5-10210U: PL1 (long term) is 30 W, PL2 (short term) is 64 W. A loaded NUC therefore draws 35-45 W more at the wall. That is the PL1 package limit plus voltage regulator and power supply losses.
- At idle, the three NUCs together are only a small part of the ~195 W total. Under load, each of them can add as much as node04.
- node04 adds the least relative to its size. A scalar shell loop does not reach the Ryzen package power limit; AVX-heavy work would draw more.
- The idle windows vary by 10 W (191.7-202.0 W). Treat the deltas as ±5 W.
- This measures how much each node adds under load, not how the idle draw is split. Splitting the idle draw needs one node at a time powered off, or a separate plug on the NUC rail (td-8da11b).

## node04 (AMD, storage and GPU node)

- CPU: 94 % idle, `amd-pstate-epp`, governor `powersave`, EPP `balance_performance`. Deepest C-state (C2/CC6) residency 88 %.
- Estimated share of the total: 155-170 W. This is an estimate, not a measurement.
- Static consumers that governor tuning cannot reduce, together about 45-50 W:
    - LSI SAS2008 HBA (`mpt3sas`, link downgraded to PCIe 5 GT/s x4): typically 9-14 W.
    - RTX 3060 Ti: 13.9 W in P8 (measured with `nvidia-smi`).
    - 4x WD 10 TB disks: about 20-24 W.
- The disks are 1.3 % busy each, but see 4-6 write operations per second all the time, because the k3s and Longhorn datasets live in the same ZFS pool. Spinning the disks down is not possible without moving that data.
- SATA link power management is `med_power_with_dipm`, PCIe ASPM policy is `default`. There is no `modprobe.d` or udev tuning.

### GPU idle power

Scaling all llama-cpp Deployments to 0 replicas did **not** lower the GPU draw: it stayed at 13.83 W in P8 with 1 MiB VRAM in use and no process on the card. The nvidia driver stays loaded and PCIe runtime PM for the card (`07:00.0`) is `on`, so the card never drops below P8.

Scaling llama-cpp down therefore saves no power. The llama-cpp Deployments are scaled by hand on demand, so 0 live replicas is a normal state. ArgoCD ignores `replicas` for these Deployments (see `apps/argo-cd-apps/65-llama-cpp.yaml`).

The driver's runtime D3 power management cannot help on this board (td-709827). Powering the card off at idle needs D3cold, which requires ACPI `_PR3` power resources on the PCIe root port. The root port of the card (`00:03.1`, `\_SB_.PCI0.GPP8`) has none. The driver (`nvidia-open-lts` 595.71.05) already runs with `DynamicPowerManagement=3` (auto) and reports `Runtime D3 status: Disabled by default` in `/proc/driver/nvidia/gpus/0000:07:00.0/power`. Forcing `NVreg_DynamicPowerManagement=0x02` would not change that.

The card stays installed, so about 14 W in P8 is the idle floor of the GPU on node04.

## node01-03 (Intel NUC10i5FNH, i5-10210U)

- `intel_pstate`, governor `powersave`, EPP `balance_performance`, turbo enabled.
- `intel_idle` with C1-C10 available. SATA link power management `min_power_with_partial`, PCIe ASPM policy `default`.
- No WLAN or Bluetooth module. All display connectors are disconnected.
- CPU busy: node01 12.7 %, node02 11.7 %, node03 8.5 %.
- Interrupts: 16-27k/s, context switches 27-50k/s, 32-36 veth interfaces per node.
- **The package never goes below PC3.** `pmc_core` shows 0 residency in package C6 to C10 and 0 for `slp_s0` on node01 and node02. The cores reach CC10, but the package does not. See below.

### Why the NUC package stays at PC3

td-74ae82 tested the usual blockers one at a time on node01 and node02, measuring the `pmc_core` package residency over 20-30 s windows. None of them moved PC6 off 0:

| Suspect | Test | Result |
|---|---|---|
| BIOS package C-state limit | `MSR_PKG_CST_CONFIG_CONTROL` (0xE2) = `0x1e008008` | Limit nibble `8` = PC10 allowed. Not a limit. |
| GL9755 SD card reader (`02:00.0`, link with ASPM disabled, stuck in D0) | Removed the device through sysfs, root port went to D3hot | No change |
| Display on node02 (a monitor on DP-1 kept pipe A lit, `DC3 -> DC5` count 0) | Unplugged monitor and USB keyboard, DC5 entries started | No change |
| I219-V GbE link (`e1000e`, 1 Gb/s) | `eno1` down for 20 s | No change |
| iGPU render power state | `rc6_residency_ms` | 100 % RC6. Not a blocker. |

The most likely explanation is the wakeup rate. This is an inference, not a confirmed cause: no test reduced the wakeup rate and showed PC6 appear. node01 takes about 11,300 local timer interrupts per second, roughly 1,400 per thread. That is above the 1,000 Hz kernel tick even with `NO_HZ_FULL`, so the timers come from userspace (k3s, etcd, Cilium, kubelet, containerd). Each thread idles into CC10 on its own, but all eight threads are almost never idle at the same time for long enough. MSR 0xE2 has C-state auto-demotion enabled, so the hardware demotes those short package idles to PC3.

If that is right, this is the cost of running a Kubernetes control-plane node, not a misconfiguration. PCIe ASPM policy `powersupersave` and EPP `power` were not tested (td-831b31). The saving from PC6+ on a 15 W NUC is estimated at 1-2 W per node at best; it has not been measured yet (td-3135c2, needs the NUC plug from td-8da11b). That is not worth cutting cluster services for, so no change was made.

The node02 display finding still matters: a monitor left on a node keeps the display out of its power states.

Left over from the tests: the GL9755 SD reader on node01 and node02 is still removed through sysfs until a `pci rescan` or reboot (td-c6b050), node03 was not measured because it asks for a sudo password (td-64e4c9), and the monitor and keyboard on node02 are still unplugged (td-9aa0c8).

### Longhorn backup target poll on node01

`longhorn backup ls --volume-only s3://...` runs about every 300 s and takes 100-200 % CPU (2 cores) on node01. Folding the Shelly series on a 300 s period shows a median of 207.9 W in the bucket where the poll runs, against about 200.4 W in all other nine buckets (48 samples per bucket). The peak is about 7 W, but the duty cycle is about 1/10, so the mean cost is only 0.7-1.5 W. This is worth fixing as a configuration issue, not as a power saving.

## Follow-up issues

| td issue | Topic |
|---|---|
| td-d17e4e | Fix the recurring Longhorn `backup ls` poll on node01 |
| td-709827 | Test runtime PM for the RTX 3060 Ti on node04 (done: not supported, no D3cold) |
| td-8da11b | Add a 4th Shelly plug to meter the NUC rail |
| td-74ae82 | Unblock package C-states on the NUCs (done: most likely blocked by the k3s timer wakeup rate, no fix) |
| td-831b31 | Test PCIe ASPM `powersupersave` and EPP `power` on node01 |
| td-3135c2 | Measure the NUC PC6+ saving with the NUC plug |
| td-c6b050 | Restore the GL9755 SD reader on node01 and node02 |
| td-64e4c9 | Check package C-states on node03 |
| td-9aa0c8 | Decide whether monitor and keyboard go back on node02 |
| td-866186 | Controlled per-node power experiment (done: load deltas above, idle split still open) |
