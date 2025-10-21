# Test environment

The test environment is based upon virtual machines and created with Vagrant and libvirt. You need a quiet powerful machine to run the full test environment. It can create up to three systems with the following configuration:

## Virtual Machine Specifications

- **k3svm1**: The server node of the k3s cluster
    - 4096MB RAM
    - 4 CPU threads
    - Role: k3s server (control plane)
- **k3svm2**: The first worker node of the k3s cluster
    - 2048MB RAM
    - 2 CPU threads
    - Role: k3s server (control plane)
- **k3svm3**: The second worker node of the k3s cluster
    - 2048MB RAM
    - 2 CPU threads
    - Role: k3s agent (worker)

**Total resources required:** 8192MB RAM and 8 CPU threads when running all three VMs.

## Architecture Decision

I decided to base the test environment on virtual machines because I also test different network plugins. This is much easier to do with virtual machines than with containers. Another requirement is ansible, which I use to initially install k3s. This also needs to be tested. During deployment Vagrant uses the same ansible playbook to provision the system(s) as the one used for the production environment. Just the vars are a bit different and can be adjusted in the `ansible/inventory/vagrant/group_vars/all/main.yaml` file.

## File Organization

In the subfolder **./shared/${HOSTNAME}/** are files for the configuration of the k3s cluster, like the kubeconfig.

The folder **./shared** gets also mounted to the virtual machines. This is the place where you can put files you want to share with the virtual machines. Though this requires **memory_backing_dir = "/dev/shm"** in **/etc/libvirt/qemu.conf**.

/// warning | libvirt Configuration Required
The shared folder mounting requires the following configuration in **/etc/libvirt/qemu.conf**:
```
memory_backing_dir = "/dev/shm"
```
After changing this setting, restart libvirtd:
```shell
sudo systemctl restart libvirtd
```
///

## Prerequisites

Before setting up the test environment, ensure you have:

- **Virtualization support**: CPU with VT-x/AMD-V enabled in BIOS
- **libvirt**: KVM/QEMU virtualization platform installed
- **Vagrant**: Version 2.3.0 or higher
- **Sufficient resources**: At least 8GB free RAM and 8 CPU threads for full cluster
- **Disk space**: Minimum 20GB free for VM images and data

## Setting up vagrant

At first install vagrant for your system. This wont be covered here. Please refer to the [vagrant documentation](https://developer.hashicorp.com/vagrant/docs/installation){target=_blank}.

Add the libvirt plugin:

```shell
vagrant plugin install vagrant-libvirt
```

/// tip | Using Docker Container (Recommended)
If you have, like me, dependency problems with the libvirt plugin, you can use the [vagrant-libvirt](https://vagrant-libvirt.github.io/vagrant-libvirt/installation.html#docker--podman){target=_blank} docker container. I **recommend** you to use my extended [vagrant-libvirt-ansible](https://github.com/madic-creates/vagrant-libvirt-ansible){target=_blank} container with added ansible. Ansible is required for provisioning and by default not installed in the standard vagrant-libvirt container.

To use the container:
```shell
docker run -it --rm \
  -v /var/run/libvirt/:/var/run/libvirt/ \
  -v ~/.vagrant.d:/root/.vagrant.d \
  -v $(pwd):$(pwd) \
  -w $(pwd) \
  --network host \
  ghcr.io/madic-creates/vagrant-libvirt-ansible:latest \
  vagrant up
```
///

## Starting the Test Environment

### Full Cluster Deployment

Start the complete test environment with all three nodes:

```shell
vagrant up
```

This will:

1. Create three VMs (k3svm1, k3svm2, k3svm3)
2. Run the Ansible playbook `ansible/playbooks/install.yaml`
3. Install k3s on the server node
4. Join worker nodes to the cluster
5. Copy kubeconfig to `shared/k3svm1/k3s.yaml`

/// note | Provisioning Time
The initial provisioning can take 10-15 minutes depending on your system resources and internet connection. After the Vagrant provisioning completes, allow an additional 2-3 minutes for k3s to fully initialize.
///

### Single Node Deployment

Start only the server node (which is enough for many tests):

```shell
vagrant up k3svm1
```

This creates a single-node cluster that is sufficient for testing:

- Application deployments
- ArgoCD functionality
- Kustomize configurations
- Secret management with SOPS
- Basic networking

### Accessing the Cluster

After the installation is finished, it can still take some time till the k3s service is up and running.

The ansible **ansible/playbooks/install.yaml** playbook, which vagrant automatically runs, will copy the kubeconfig from the server node to `shared/k3svm1/k3s.yaml`. You can use this file to access the test k3s cluster.

Set the KUBECONFIG environment variable:

```shell
export KUBECONFIG="$PWD/shared/k3svm1/k3s.yaml"
```

Verify cluster access:

```shell
kubectl get nodes
kubectl get pods -A
```

### Adding additional Nodes

Start the worker nodes separately if needed. Ansible will automatically join the worker nodes to the k3s cluster.

```shell
vagrant up k3svm2
vagrant up k3svm3
```

/// tip | When to Use Worker Nodes
Worker nodes are useful for testing:

- Multi-node scheduling
- Pod anti-affinity rules
- Node selectors and taints
- Storage replication (Longhorn)
- High availability scenarios
///

### SSH Access

Once your virtual machine is up and running, you can log in to it:

```shell
vagrant ssh k3svm1
```

Useful commands inside the VM:

```shell
# Check k3s service status
sudo systemctl status k3s

# View k3s logs on control-plane
sudo journalctl -u k3s -f

# View k3s logs on worker
sudo journalctl -u k3s-agent -f

# Check node resources
kubectl top nodes
```

### Destroying the Environment

Destroy the test environment and reclaim resources:

```shell
vagrant destroy -f
```

To destroy a specific VM:

```shell
vagrant destroy k3svm2 -f
```

## Testing Workflows

### Deploying Applications

Once the cluster is running, you can test application deployments:

```shell
# Build kustomization locally
kubectl kustomize apps/myapp --enable-helm

# Apply directly (for testing without ArgoCD)
kubectl kustomize apps/myapp --enable-helm | kubectl apply -f -

# Check deployment status
kubectl get pods -n myapp-namespace
kubectl describe pod <pod-name> -n myapp-namespace
```

### Testing ArgoCD

Deploy ArgoCD to the test cluster:

```shell
# Deploy ArgoCD
kubectl kustomize apps/argo-cd --enable-helm | kubectl apply -f -

# Wait for ArgoCD to be ready
kubectl wait --for=condition=available --timeout=300s deployment/argocd-server -n argocd

# Get admin password
kubectl get secret argocd-initial-admin-secret -n argocd -o jsonpath="{.data.password}" | base64 -d
```

Port-forward to access ArgoCD UI:

```shell
kubectl port-forward svc/argocd-server -n argocd 8080:443
```

### Testing Network Policies

When testing Cilium network policies, use Hubble:

```shell
# Install Cilium CLI in the VM
vagrant ssh k3svm1
curl -L --remote-name-all https://github.com/cilium/cilium-cli/releases/latest/download/cilium-linux-amd64.tar.gz
sudo tar xzvfC cilium-linux-amd64.tar.gz /usr/local/bin
rm cilium-linux-amd64.tar.gz

# Enable Hubble port-forward
cilium hubble port-forward &

# Observe traffic from your namespace
hubble observe --namespace myapp --last 100
```

### Vagrant Snapshots

Create snapshots for quick rollback:

```shell
# Take a snapshot
vagrant snapshot save k3svm1 clean-state

# List snapshots
vagrant snapshot list

# Restore snapshot
vagrant snapshot restore k3svm1 clean-state

# Delete snapshot
vagrant snapshot delete k3svm1 clean-state
```

## Troubleshooting

### Vagrant Issues

Sometimes vagrant has conflicts with OS packages resulting in messages like this:

*conflicting dependencies date (= 3.2.2) and date (= 3.3.4)*

Set the following environment variable to ignore gem versions or directly use my [vagrant-libvirt-ansible](https://github.com/madic-creates/vagrant-libvirt-ansible){target=_blank} container:

```shell
export VAGRANT_DISABLE_STRICT_DEPENDENCY_ENFORCEMENT=1
```

### VM Won't Start

Check libvirt status:

```shell
sudo systemctl status libvirtd
sudo virsh list --all
```

View libvirt logs:

```shell
sudo journalctl -u libvirtd -f
```

### k3s Service Not Starting

SSH into the VM and check k3s status:

```shell
vagrant ssh k3svm1
sudo systemctl status k3s
sudo journalctl -u k3s --no-pager -n 50
```

### Kubeconfig Not Working

Ensure the kubeconfig file exists:

```shell
ls -la shared/k3svm1/k3s.yaml
```

If missing, manually copy from VM:

```shell
vagrant ssh k3svm1 -c "sudo cat /etc/rancher/k3s/k3s.yaml" | sed "s/127.0.0.1/$(vagrant ssh-config k3svm1 | grep HostName | awk '{print $2}')/g" > shared/k3svm1/k3s.yaml
```

### Cleaning Up

If VMs are in an inconsistent state:

```shell
# Stop all VMs
vagrant halt

# Destroy all VMs
vagrant destroy -f

# Clean libvirt domains
sudo virsh list --all
sudo virsh undefine k3svm1
sudo virsh undefine k3svm2
sudo virsh undefine k3svm3

# Remove storage
sudo virsh vol-list default
sudo virsh vol-delete k3svm1.img default
```

## Performance Optimization

### Reducing Resource Usage

For limited hardware, modify the Vagrantfile to reduce resources:

```ruby
# Example modifications
config.vm.define "k3svm1" do |node|
  node.vm.provider :libvirt do |libvirt|
    libvirt.memory = 2048  # Reduced from 4096
    libvirt.cpus = 2       # Reduced from 4
  end
end
```
