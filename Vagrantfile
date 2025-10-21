# code: language=ruby

Vagrant.configure("2") do |config|
  # Specify the base box to use, in this case an Arch Linux box
  config.vm.box = "generic/arch"

  # Define configurations for multiple virtual machines with their specific settings
  server = {
    "k3svm1" => {
      ip: "192.168.33.11",
      memory: 4096,
      cpus: 4,
      groups: { "k3s_primary_server" => ["k3svm1"] }
    },
    "k3svm2" => {
      ip: "192.168.33.12",
      memory: 2048,
      cpus: 2,
      groups: { "k3s_secondary_server" => ["k3svm2"] }
    },
    "k3svm3" => {
      ip: "192.168.33.13",
      memory: 2048,
      cpus: 2,
      groups: { "k3s_agent" => ["k3svm3"] }
    }
  }

  # Initialize a hash to store all group configurations for Ansible
  all_groups = {}
  # Determine which machines are specified as command line arguments
  running_machines = ARGV.select { |arg| server.keys.include?(arg) }
  provision_done = false  # Variable to track if provisioning is done

  # Iterate over each machine configuration and define it in Vagrant
  server.each do |name, config_params|
    config.vm.define name do |node|
      # Configure network settings and hostname for the VM
      node.vm.network "private_network", ip: config_params[:ip]
      # synced_folder with virtiofs requires memory_backing_dir = "/dev/shm" in /etc/libvirt/qemu.conf
      node.vm.synced_folder "./shared", "/vagrant", type: "virtiofs"
      node.vm.hostname = name

      # Use Libvirt provider to specify CPU, memory, and other settings
      node.vm.provider :libvirt do |libvirt|
        libvirt.cpus = config_params[:cpus]
        libvirt.memory = config_params[:memory]
        libvirt.memorybacking :access, :mode => "shared"
        libvirt.cputopology :sockets => '1', :cores => config_params[:cpus].to_s, :threads => '1'
      end

      # Collect group configurations for Ansible provisioning
      config_params[:groups].each do |group, hosts|
        all_groups[group] ||= []
        all_groups[group].concat(hosts)
      end

      # Determine whether to provision all VMs or specific ones based on arguments
      provision_all = running_machines.empty?
      provision_single = !running_machines.empty? && running_machines.include?(name)

      # Install Python on each VM individually
      node.vm.provision "python-install", type: "shell", inline: <<-SHELL
        pacman -Sy --noconfirm archlinux-keyring
        pacman -Sy --noconfirm python openssl
      SHELL

      # Configure Ansible provisioner (only once for all VMs)
      if (provision_all && !provision_done) || provision_single
        node.vm.provision :ansible do |ansible|
          ansible.limit = "all"
          ansible.compatibility_mode = "2.0"
          ansible.config_file = "ansible/ansible.cfg"
          ansible.playbook = "ansible/playbooks/install.yaml"
          ansible.extra_vars = "@ansible/inventory/vagrant/group_vars/all/main.yaml"
          ansible.galaxy_role_file = "ansible/requirements.yaml"
          ansible.groups = all_groups
          ansible.become = true
          #ansible.raw_arguments = ['-vvv']
          ansible.verbose = true
        end
        provision_done = true  # Set the flag to true after provisioning the first time
      end
    end
  end
end
