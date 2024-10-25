# Test environment

The test environment is based upon virtual machines and created with Vagrant and libvirt. You need a quiet powerful machine to run the full test environment. It can creat up to three systems with the following configuration:

- **k3svm1**: The server node of the k3s cluster
    - 4096MB RAM
    - 4 CPU threads
- **k3svm2**: The first worker node of the k3s cluster
    - 2048MB RAM
    - 2 CPU threads
- **k3svm3**: The second worker node of the k3s cluster
    - 2048MB RAM
    - 2 CPU threads

I decided to base the test environemnt on virtual machines because I also test different network plugins. This is much easier to do with virtual machines than with containers. Another requirement is ansible, which I use to initially install k3s. This also needs to be tested. During deployment Vagrant uses the same ansible playbook to provision the system(s) as the one used for the production environment. Just the vars are a bit different and can be adjusted in the ansible/vars/vagrant.yaml file.

In the subfolder **./shared/<HOSTNAME>/** are files for the configuration of the k3s cluster, like the kubeconfig.

The folder **./shared** gets also mounted to the virtual machines. This is the place where you can put files you want to share with the virtual machines. Though this requires **memory_backing_dir = "/dev/shm"** in **/etc/libvirt/qemu.conf**.

## Setting up vagrant

At first install vagrant for your system. This wont be covered here. Please refer to the [vagrant documentation](https://developer.hashicorp.com/vagrant/docs/installation){target=_blank}.

Add the libvirt plugin:

```shell
vagrant plugin install vagrant-libvirt
```

Start the complete test environment:

```shell
vagrant up
```

Start only the server node (which is enough for many tests):

```shell
vagrant up k3svm1
```

After the installation is finished, it can still take some time till the k3s service is up and running.

The ansible **ansible/install.yaml** playbook, which vagrant automatically runs, will copy the kubeconfig from the server node to shared/k3svm1/k3s.yaml. You can use this file to access the test k3s cluster.

```shell
export KUBECONFIG="$PWD/shared/k3svm1/k3s.yaml"
```

Start the worker nodes separately if needed. Ansible will automaitcally join the worker nodes to the k3s cluster.

```shell
vagrant up k3svm2
vagrant up k3svm3
```

Once your virtual machine is up and running, you can log in to it:

```shell
vagrant ssh k3svm1
```

Destroy the test environment:

```shell
vagrant destroy -f
```

## Notes

Sometimes vagrant has conflicts with os packages resutling in message like this:

*conflicting dependencies date (= 3.2.2) and date (= 3.3.4)*

Set the following environment variable to ignore gem versions:

```shell
export VAGRANT_DISABLE_STRICT_DEPENDENCY_ENFORCEMENT=1
```
