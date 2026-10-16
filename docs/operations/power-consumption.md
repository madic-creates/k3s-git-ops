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
- **Package C-states are blocked.** `pmc_core` on node02 shows 0 residency in package C6 to C10 and 0 for `slp_s0`. The cores reach CC10, but the package never goes below PC3. Likely candidates are the ASPM policy, the EPP setting and devices that block LTR.

### Longhorn backup target poll on node01

`longhorn backup ls --volume-only s3://...` runs about every 300 s and takes 100-200 % CPU (2 cores) on node01. Folding the Shelly series on a 300 s period shows a median of 207.9 W in the bucket where the poll runs, against about 200.4 W in all other nine buckets (48 samples per bucket). The peak is about 7 W, but the duty cycle is about 1/10, so the mean cost is only 0.7-1.5 W. This is worth fixing as a configuration issue, not as a power saving.

## Follow-up issues

| td issue | Topic |
|---|---|
| td-d17e4e | Fix the recurring Longhorn `backup ls` poll on node01 |
| td-709827 | Test runtime PM for the RTX 3060 Ti on node04 (done: not supported, no D3cold) |
| td-8da11b | Add a 4th Shelly plug to meter the NUC rail |
| td-74ae82 | Unblock package C-states on the NUCs (depends on td-8da11b) |
| td-866186 | Controlled per-node power experiment |
