# Load Balancer Stack

The cluster exposes services to the home network through a combination of Traefik, KubeVIP, and Cilium. See [Network Architecture](network.md) for the full dataplane overview; this page focuses on the operational details for the load-balancing components.

## Building Blocks

- **Traefik** acts as the ingress controller and terminates TLS for HTTP(S) workloads.
- **KubeVIP** provides virtual IP addresses for both the Kubernetes API and `LoadBalancer` services.
- **Cilium** supplies the pod network, kube-proxy-free service routing, and transparent encryption but does not announce external VIPs in this setup.

## Control-Plane Virtual IP

The DaemonSet in `apps/kubevip-ha/` keeps the Kubernetes API reachable under `https://192.168.1.230:6443`. Nodes labelled as control-plane members compete for leadership; the winner advertises the VIP on interface `eno1` using ARP.

To rotate the static manifest:

1. Pull the desired `ghcr.io/kube-vip/kube-vip` image.
2. Generate a new DaemonSet manifest with the `manifest daemonset` helper.
3. Copy the output into `apps/kubevip-ha/k8s.kubevip.yaml`, trimming `creationTimestamp` and `status` fields.

Example using containerd:

```
export KVVERSION=v0.9.2
/usr/local/bin/ctr image pull ghcr.io/kube-vip/kube-vip:${KVVERSION}
/usr/local/bin/ctr run --rm --net-host ghcr.io/kube-vip/kube-vip:${KVVERSION} vip /kube-vip \
  manifest daemonset \
  --interface eno1 \
  --address 192.168.1.230 \
  --inCluster \
  --taint \
  --controlplane \
  --services \
  --arp \
  --leaderElection \
  --enableNodeLabeling
```

> GitHub releases: [https://github.com/kube-vip/kube-vip/releases](https://github.com/kube-vip/kube-vip/releases){target=_blank}

## Service Load Balancers

The deployment in `apps/kubevip-cloud-controller/` installs the kube-vip cloud controller manager. It watches every Service of type `LoadBalancer` and assigns a VIP from the range defined in the generated `kubevip` ConfigMap (`cidr-global=192.168.1.231-192.168.1.239` by default). Because Cilium L2 announcements are disabled, the kube-vip DaemonSet takes responsibility for advertising the VIP on whichever node currently hosts the matching backend pod — KubeVIP allows the address to "follow" the pod. Cilium currently doesn't have this feature.

When you create a new external-facing service (for example Traefik or kube-prometheus-stack's Alertmanager):

1. Set the Service `type: LoadBalancer`.
2. Optional: pin a specific address by setting `spec.loadBalancerIP` to an IP inside the configured range.
3. Watch the Service until the `EXTERNAL-IP` field displays a value from the pool.
4. Configure DNS or clients to reach the assigned address.

Although KubeVIP owns the VIP, the per-pod data plane is still provided by Cilium in hybrid DSR mode, so backend pods see the original client IP and respond directly, improving throughput.

## Updating the Cloud Controller

The deployment references `ghcr.io/kube-vip/kube-vip-cloud-provider:v0.0.12`. To upgrade:

1. Update the image tag in `apps/kubevip-cloud-controller/k8s.kube-vip-cloud-controller.yaml`.
2. Apply the same change to any overlays in `apps/overlays/kubevip-cloud-controller/`.
3. Commit and push; Argo CD will roll out the new deployment.

## Troubleshooting Checklist

- `kubectl -n kube-system get pods -l app.kubernetes.io/name=kube-vip-ds` — verify the control-plane DaemonSet is running on each leader-capable node.
- `kubectl -n kube-system logs deploy/kube-vip-cloud-provider` — confirm Services receive VIP assignments.
- `kubectl -n kube-system get configmap kubevip -o yaml` — verify the configured CIDR range matches your intended pool.
- `ip addr show dev eno1 | rg 192.168.1.23` on a node — check which node currently owns the VIP.
- `cilium service list` — confirm the service map contains the expected frontend (VIP) and backend pods.

If ARP announcements stop, restart the kube-vip DaemonSet pod on the current leader and verify that no conflicting DHCP lease is advertising the same IP.
