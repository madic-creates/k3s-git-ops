# LoadBalancer

- Ingress Traefik
- KubeVIP
- Certmanager

## KubeVIP

KubeVIP kommt in zwei Varianten zum Einsatz:

- HA für Kubernetes API
- LoadBalancer Provider (cloud-controller)

### HA für Kubernetes API

Mit Hilfe eines Daemonset wird ein kube-vip pod gestartet, der per ARP eine virtuelle IP (VIP) verwaltet.
Mehr Informationen: https://kube-vip.io/docs/installation/daemonset/

Github Releases: https://github.com/kube-vip/kube-vip/releases

#### KubeVIP aktualisieren:

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

Das damit erstellte Manifest nach apps/kubevi-ha/kubevip.yaml kopieren und die creationTimestamps und den status entfernen.

## KubeVIP

KubeVIP wird per ansible installiert weil ein Kubernetes Manifest während der Installaton erstellt werden muss.
Das kann kustomize nicht. Jede weitere Konfiguration übernimmt dann wieder kustomize.
