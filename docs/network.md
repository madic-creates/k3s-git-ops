# Network Architecture

This cluster relies on a tightly integrated networking stack that combines Cilium for pod connectivity and security, KubeVIP for highly-available virtual IP addresses, and Traefik as the ingress controller fronted by Kubernetes `LoadBalancer` services. The following sections document how traffic moves through the environment and which manifests are responsible for each layer.

## Simplified Topology Diagram

```
                         Home LAN (192.168.1.0/24)
                                      |
                          +-------------------------+
                          | Router / Default GW     |
                          +-------------------------+
                                      |
                          +-------------------------+
                          | VIP 192.168.1.230       |
                          | kube-vip DaemonSet      |
                          | (Kubernetes API)        |
                          +-------------------------+
                                      |
          +---------------------------+---------------------------+
          |                                                           |
 +----------------------+                                 +----------------------+
 | Control-plane node 1 |                                 | Control-plane node 2 |
 | eno1 / vmbr0         |                                 | eno1 / vmbr0         |
 +----------------------+                                 +----------------------+
          |                                                           |
          +---------------------------+---------------------------+
                                      |
                          +-------------------------+
                          | kube-vip Cloud Ctrl     |
                          | VIP pool 192.168.1.231-239 |
                          +-------------------------+
                                      |
               +----------------------+----------------------+
               |                                             |
   +-----------------------+                     +-----------------------+
   | Service: Traefik      |                     | Service: Traefik-vip239 |
   | VIP 192.168.1.231     |                     | VIP 192.168.1.239       |
   +-----------------------+                     +-----------------------+
               |                                             |
               +----------------------+----------------------+
                                      |
                          +-------------------------+
                          | Cilium Data Plane       |
                          | Pod CIDR 10.42.0.0/16   |
                          | WireGuard encrypted     |
                          +-------------------------+
                                      |
                          +-------------------------+
                          | Workload Pods & Hubble  |
                          +-------------------------+
```

## Underlay Topology

- All control-plane and worker nodes connect to the home LAN on `192.168.1.0/24`.
- A dedicated virtual IP `192.168.1.230` exposes the Kubernetes API and floats across control-plane nodes.
- Load-balancer service IPs are assigned from `192.168.1.231-192.168.1.239`, ensuring they never collide with DHCP-managed addresses on the LAN.

Keeping these addresses outside of the DHCP scope prevents gratuitous ARP conflicts when KubeVIP announces ownership of a VIP.

## Pod Networking with Cilium

Cilium is deployed via Argo CD (`apps/argo-cd-apps-helm/00-cilium.yaml`) and replaces both kube-proxy and the default flannel CNI that ships with K3s.

- **eBPF data plane:** `kubeProxyReplacement: true` enables kube-proxy-free service routing, reducing hops and relying on eBPF programs instead of iptables.
- **Pod CIDR:** The IPAM operator manages the `10.42.0.0/16` pod network (`clusterPoolIPv4PodCIDRList`).
- **Encryption:** WireGuard is enabled (`encryption.type: wireguard`), so pod-to-pod traffic inside the cluster is transparently encrypted at Layer 3.
- **Devices:** The agents are bound to the physical interfaces `eno1 vmbr0` so all relevant NICs participate in datapath processing.
- **Tunneling:** The GENEVE tunnel protocol allows encrypted overlay traffic across different subnets while preserving eBPF optimisation.
- **Observability:** Hubble Relay and Hubble UI are enabled for flow inspection.

> Note
> Cilium's experimental Layer 2 announcement feature is disabled (`l2announcements.enabled: false`) so that VIP ownership can be handled entirely by KubeVIP. This guarantees the external IP can move to the node hosting the active backend pod, something Cilium cannot yet do.

### Network Policy

Every ArgoCD App comes with its own sets of CiliumNetworkPolicies, allowing only the required incoming and outgoing pod connections.

## KubeVIP Roles

KubeVIP is installed in two distinct modes, both orchestrated by Argo CD:

1. **Control-plane virtual IP (`apps/kubevip-ha`)**
   - Runs as a DaemonSet on nodes labelled `node-role.kubernetes.io/master` or `node-role.kubernetes.io/control-plane`.
   - Advertises `192.168.1.230` on interface `eno1`, presenting a single stable endpoint for `https://<vip>:6443`.
   - Leader election is configured with rapid timeouts (`vip_leaseduration: 5`, `vip_renewdeadline: 3`, `vip_retryperiod: 1`) to minimise failover time.

2. **Cloud controller (`apps/kubevip-cloud-controller`)**
   - Watches Kubernetes `Service` objects of type `LoadBalancer` and annotates them with a VIP from the configured pool.
   - Runs as a singleton deployment using image `ghcr.io/kube-vip/kube-vip-cloud-provider:<tag>`.
   - Collaborates with Cilium: KubeVIP assigns the IP, updates Service status, and advertises the VIP, while Cilium continues to handle pod networking and service routing inside the cluster.

### Updating KubeVIP

Follow the instructions in `docs/loadbalancer.md` to regenerate manifests when upgrading to newer KubeVIP releases. Always update the DaemonSet image tag and the cloud-controller deployment in lockstep.

## Load Balancing Workflow

1. You declare a service (for example Traefik) as `type: LoadBalancer`.
2. The kube-vip cloud controller allocates a free IP from the `cidr-global` range defined in the `kubevip` ConfigMap and records it in the Service status.
3. The kube-vip DaemonSet elects the node that should host the VIP—preferentially the node running the backend pods—and advertises the address on the local network using ARP.
4. Client traffic from the LAN reaches that node, where KubeVIP performs the load-balancing decision and hands the connection to the target pod.

This layered approach yields fast failovers, keeps the control plane highly available, and avoids the need for external load balancer appliances.

## Traefik Virtual IP Mapping

The Traefik release in `apps/traefik/kustomization.yaml` exposes two independent LoadBalancer frontends backed by kube-vip:

- `traefik` (LoadBalancer IP `192.168.1.231`) publishes the `web` and `websecure` entrypoints for general ingress traffic.
- `traefik-vip239` (LoadBalancer IP `192.168.1.239`) publishes the dedicated `web239` and `websecure239` entrypoints for workloads that must live on the second VIP.

Ingress manifests remain otherwise unchanged; assigning them to the secondary IP only requires the annotation `traefik.ingress.kubernetes.io/router.entrypoints: web239,websecure239`. Traefik listens on separate container ports for these entrypoints, so kube-vip can pin each Service to the correct address without additional node-level networking changes.

## Extending the Network Stack

- **New VIP ranges:** Update the `cidr-global` literal in `apps/kubevip-cloud-controller/kustomization.yaml` and document the range in your network inventory.
- **Additional interfaces:** Modify the `devices` setting in the Helm values if you add NICs to your nodes.
- **Fine-grained policy:** Start from the sample manifests under `apps/cilium-network-policies/` and deploy them via Argo CD to enforce zero-trust defaults.
- **Testing:** Use Hubble UI or `cilium status`/`cilium connectivity test` on a node to validate connectivity after changes.

Service IP address management is configured through the `kubevip` ConfigMap generated in `apps/kubevip-cloud-controller/kustomization.yaml`. When adding an IP range, update the `cidr-global` literal and keep it aligned with the DHCP exclusions on your router or firewall.

For component-specific procedures (such as regenerating KubeVIP manifests), refer to [LoadBalancer](loadbalancer.md).

## Troubleshooting

`hubble observe` provides real-time insight in the network flow and allows filtering by e.g. dropped frames.

```shell
cilium hubble port-forward&
hubble observe --last 20 --namespace kube-system --verdict DROPPED -f
```
