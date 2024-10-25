# LoadBalancer

- Ingress Traefik
- KubeVIP
- Certmanager

## KubeVIP

KubeVIP is used in two variants:

- HA for Kubernetes API
- LoadBalancer Provider (cloud-controller)

### HA for Kubernetes API

A kube-vip pod is launched using a DaemonSet to manage a virtual IP (VIP) via ARP.
More information: https://kube-vip.io/docs/installation/daemonset/

Github Releases: https://github.com/kube-vip/kube-vip/releases

#### Updating KubeVIP

Updating the manifests for the ArgoCD app in apps/kubevip-ha

Docker

```shell
export KVVERSION=v0.6.4
docker image pull ghcr.io/kube-vip/kube-vip:$KVVERSION
docker run --rm ghcr.io/kube-vip/kube-vip:$KVVERSION
  manifest \
  daemonset \
  --interface eth1 \
  --address 192.168.33.9 \
  --inCluster \
  --taint \
  --controlplane \
  --services \
  --arp \
  --leaderElection
```

containerd:

```
export KVVERSION=v0.6.4
/usr/local/bin/ctr image pull ghcr.io/kube-vip/kube-vip:$KVVERSION
/usr/local/bin/ctr run --rm --net-host ghcr.io/kube-vip/kube-vip:$KVVERSION vip /kube-vip \
  manifest \
  daemonset \
  --interface eth1 \
  --address 192.168.1.230 \
  --inCluster \
  --taint \
  --controlplane \
  --services \
  --arp \
  --leaderElection \
  --enableNodeLabeling
```

Copy the manifest created to apps/kubevi-ha/kubevip.yaml and remove the creationTimestamps and the status.

## KubeVIP

KubeVIP is installed via Ansible because a Kubernetes manifest needs to be created during installation, which kustomize cannot do. Any further configuration is then handled by kustomize.
