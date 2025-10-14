# Networking

## Replacing flannel with cilium - live

In the beginning the cluster used flannel as cni. I wanted to use cilium so I tested a live migration from flannel to cilium.
In short: Only do this if you realy know what you are doing.
This is a complex task and requires linux and kubernetes networking knowledge.
The following guide is just a starting point. There were many small problems afterwards.

### Creating test environment

In repository root

```shell
vagrant up
# Wait vor all hosts becoming ready
cd apps/overlays/
export KUBECONFIG="$(realpath ../../shared/k3svm1/k3s.yaml)"
kustomize build --enable-alpha-plugins --enable-exec ./kubevip-cloud-controller | kubectl apply -f -
kustomize build --enable-alpha-plugins --enable-exec ./kubevip-ha | kubectl apply -f -
kustomize build --enable-alpha-plugins --enable-exec ./traefik | kubectl apply -f -
kustomize build --enable-alpha-plugins --enable-exec ./debug-container | kubectl apply -f -
kustomize build --enable-alpha-plugins --enable-exec ./whoami | kubectl apply -f -
```

### Migration phase

On all server nodes:

```shell
systemctl stop k3s.service
```

On all agent nodes:

```shell
systemctl stop k3s-agent.service
```

On server nodes add the following parameters in **/etc/rancher/k3s/config.yaml**:

```shell
nano /etc/rancher/k3s/config.yaml
flannel-backend: none
disable-kube-proxy: true
```

On all k3s servers / agents:

```shell
ip link delete flannel.1
ip link delete flannel-wg
```

On all server nodes:

```shell
systemctl start k3s.service
```

On all k3s agents:

```shell
systemctl start k3s-agent.service
```

On localhost or wherever [cilium cli is installed](https://docs.cilium.io/en/stable/gettingstarted/k8s-install-default/#install-the-cilium-cli){target=_blank}:

### Installing cilium

Delete the following deployments in this order:

  * kubevip-cloud-controller

```shell
# The CIDR List must match the cidr range from the previous cni!
# <deployment>
helm repo add cilium https://helm.cilium.io/
helm install cilium cilium/cilium --version 1.17.6 \
   --namespace kube-system \
   -f apps/argo-cd-apps-helm/values.cilium.yaml
cilium status --wait
cilium connectivity test
# Cleanup cilium connectivity test
kubectl delete ns cilium-test-1
# Restart unmanaged pods
kubectl get pods --all-namespaces -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name,HOSTNETWORK:.spec.hostNetwork --no-headers=true | grep '<none>' | awk '{print "-n "$1" "$2}' | xargs -L 1 -r kubectl delete pod
```

Reboot all nodes to re-create all host firewall rules and restart pods!

Don't do this:

```shell
kubectl delete pods --all --all-namespaces
```
