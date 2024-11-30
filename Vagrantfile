Vagrant.configure("2") do |config|
  config.vm.box = "generic/arch"

  server = {
    "k3svm1" => {
      ip: "192.168.33.11",
      memory: 4096,
      cpus: 4,
      groups: { "k3s_server" => ["k3svm1"] }
    },
    "k3svm2" => {
      ip: "192.168.33.12",
      memory: 2048,
      cpus: 2,
      groups: { "k3s_agent" => ["k3svm2"] }
    },
    "k3svm3" => {
      ip: "192.168.33.13",
      memory: 2048,
      cpus: 2,
      groups: { "k3s_agent" => ["k3svm3"] }
    }
  }

  all_groups = {}
  running_machines = ARGV.select { |arg| server.keys.include?(arg) }

  server.each do |name, config_params|
    config.vm.define name do |node|
      node.vm.network "private_network", ip: config_params[:ip]
      node.vm.synced_folder "./shared", "/vagrant", type: "virtiofs"
      node.vm.hostname = name

      node.vm.provider :libvirt do |libvirt|
        libvirt.cpus = config_params[:cpus]
        libvirt.memory = config_params[:memory]
        libvirt.memorybacking :access, :mode => "shared"
        libvirt.cputopology :sockets => '1', :cores => config_params[:cpus].to_s, :threads => '1'
      end

      # Accumulate groups for Ansible provisioning
      config_params[:groups].each do |group, hosts|
        all_groups[group] ||= []
        all_groups[group].concat(hosts)
      end

      provision_all = running_machines.empty? || (running_machines.size == server.keys.size)
      provision_single = !running_machines.empty? && running_machines.include?(name)

      # Configure Ansible provisioner
      if provision_all || provision_single
        node.vm.provision :ansible do |ansible|
          ansible.limit = "all"
          ansible.compatibility_mode = "2.0"
          ansible.config_file = "ansible/ansible.cfg"
          ansible.playbook = "ansible/install.yaml"
          ansible.extra_vars = "@ansible/vars/vagrant.yaml"
          ansible.galaxy_role_file = "ansible/requirements.yaml"
          ansible.groups = all_groups
          ansible.become = true
        end
      end
    end
  end
end
