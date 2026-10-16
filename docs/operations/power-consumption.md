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

A regression of the Shelly power against the CPU load of all four nodes has R² = 0.021, with some coefficients negative. That is no signal at all. The total cannot be split per node from correlation alone. It needs a controlled experiment (load or power off one node at a time).

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

What remains is the wakeup rate. node01 takes about 11,300 local timer interrupts per second, roughly 1,400 per thread. That is above the 1,000 Hz kernel tick even with `NO_HZ_FULL`, so the timers come from userspace (k3s, etcd, Cilium, kubelet, containerd). Each thread idles into CC10 on its own, but all eight threads are almost never idle at the same time for long enough. MSR 0xE2 has C-state auto-demotion enabled, so the hardware demotes those short package idles to PC3.

This is the cost of running a Kubernetes control-plane node, not a misconfiguration. No ASPM, EPP or udev setting changes it. The saving from PC6+ on a 15 W NUC would be about 1-2 W per node at best. That is not worth cutting cluster services for, so no change was made.

The node02 display finding still matters: a monitor left on a node keeps the display out of its power states.

### Longhorn backup target poll on node01

`longhorn backup ls --volume-only s3://...` runs about every 300 s and takes 100-200 % CPU (2 cores) on node01. Folding the Shelly series on a 300 s period shows a median of 207.9 W in the bucket where the poll runs, against about 200.4 W in all other nine buckets (48 samples per bucket). The peak is about 7 W, but the duty cycle is about 1/10, so the mean cost is only 0.7-1.5 W. This is worth fixing as a configuration issue, not as a power saving.

## Follow-up issues

| td issue | Topic |
|---|---|
| td-d17e4e | Fix the recurring Longhorn `backup ls` poll on node01 |
| td-709827 | Test runtime PM for the RTX 3060 Ti on node04 (done: not supported, no D3cold) |
| td-8da11b | Add a 4th Shelly plug to meter the NUC rail |
| td-74ae82 | Unblock package C-states on the NUCs (done: blocked by the k3s timer wakeup rate, no fix) |
| td-866186 | Controlled per-node power experiment |
