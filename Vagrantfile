# -*- mode: ruby -*-
# vi: set ft=ruby :
# code: language=ruby

# Adapted from https://akos.ma/blog/vagrant-k3s-and-virtualbox/

server_ip = "192.168.33.11"
server_memory = "4096"

agents = { "k3svm2" => ["192.168.33.12", "0-1"],
           "k3svm3" => ["192.168.33.13", "0-1"]}
agents_memory = "2048"

Vagrant.configure("2") do |config|
  #config.vm.box = "roboxes/rocky8"
  config.vm.box = "generic/arch"

  # synced_folder with virtiofs requires memory_backing_dir = "/dev/shm" in /etc/libvirt/qemu.conf

  config.vm.define "k3svm1" do |server|
    server.vm.network "private_network", ip: server_ip
    server.vm.synced_folder "./shared", "/vagrant", type: "virtiofs"
    server.vm.provision "ansible" do |ansible|
      ansible.compatibility_mode = "2.0"
      ansible.config_file = "ansible/ansible.cfg"
      ansible.playbook = "ansible/install.yaml"
      ansible.extra_vars = "@ansible/vars/vagrant.yaml"
      ansible.galaxy_role_file = "ansible/requirements.yaml"
      ansible.inventory_path = "ansible/vagrant.inventory"
      ansible.groups = {
        "k3s_control_plane" => ["k3svm1"]
      }
      ansible.become = true
    end
    server.vm.hostname = "k3svm1"
    server.vm.provider :libvirt do |libvirt|
      libvirt.cpus = 4
      libvirt.memory = server_memory
      #libvirt.numa_nodes = [{ :cpus => "0-3", :memory => server_memory, :memAccess => "shared" }]
      libvirt.memorybacking :access, :mode => "shared"
      #libvirt.machine_virtual_size = 30
      #libvirt.channel :type => 'unix', :target_name => 'org.qemu.guest_agent.0', :disabled => false
      #libvirt.cpuset = '1-4,^3,6'
      libvirt.cputopology :sockets => '1', :cores => '4', :threads => '1'
    end
  end

  agents.each do |agent_name, agent_props|
    agent_ip, agent_cpus = agent_props
    config.vm.define agent_name do |agent|
      agent.vm.network "private_network", ip: agent_ip
      agent.vm.synced_folder "./shared", "/vagrant", type: "virtiofs"
      agent.vm.hostname = agent_name
      agent.vm.provision "ansible" do |ansible|
        ansible.compatibility_mode = "2.0"
        ansible.config_file = "ansible/ansible.cfg"
        ansible.playbook = "ansible/install.yaml"
        ansible.extra_vars = "@ansible/vars/vagrant.yaml"
        ansible.galaxy_role_file = "ansible/requirements.yaml"
        ansible.inventory_path = "ansible/vagrant.inventory"
        ansible.groups = {
          "k3s_worker" => ["k3svm[2:3]"]
        }
        ansible.become = true
      end
      agent.vm.provider :libvirt do |libvirt|
        libvirt.cpus = 2
        libvirt.memory = agents_memory
        #libvirt.numa_nodes = [{ :cpus => agent_cpus, :memory => agents_memory, :memAccess => "shared" }]
        libvirt.memorybacking :access, :mode => "shared"
        #libvirt.machine_virtual_size = 30
        #libvirt.channel :type => 'unix', :target_name => 'org.qemu.guest_agent.0', :disabled => false
        #libvirt.cpuset = '1-4,^3,6'
        libvirt.cputopology :sockets => '1', :cores => '2', :threads => '1'
      end
    end
  end

end
