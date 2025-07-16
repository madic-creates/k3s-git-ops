#!/bin/bash
## Available CheckMK entrypoint hooks:
## https://github.com/Checkmk/checkmk/blob/master/docker_image/docker-entrypoint.sh
set -eu -o pipefail
CHECK_SSL_CERT_VERSION=2.93.0

echo "Installing and configuring thruk web interface"
apt-get -qq update && apt-get -qq install apt-utils && apt-get install -qqy lsb-release
curl -s "https://labs.consol.de/repo/stable/RPM-GPG-KEY" -o /etc/apt/auth.conf.d/labs.consol.de-RPM-GPG-KEY
echo "deb [signed-by=/etc/apt/auth.conf.d/labs.consol.de-RPM-GPG-KEY] http://labs.consol.de/repo/stable/ubuntu $(lsb_release -cs) main" > /etc/apt/sources.list.d/labs-consol-stable.list
rm -f /etc/apache2/conf-enabled/zzz_omd.conf
apt-get -qq update && apt-get install -qqy thruk nano libipc-run-perl freeipmi jq file bc
apt-get -qq clean all

echo "Use checkmk htpasswd for thruk"
rm -f /etc/thruk/htpasswd && ln -s /opt/omd/sites/cmk/etc/htpasswd /etc/thruk/htpasswd
sed -i 's/^authorized_for_admin=.*/authorized_for_admin=cmkadmin/' /etc/thruk/cgi.cfg
usermod -a -G cmk www-data

echo "Adding CheckMK as Thruk backend"
echo "resource_file = /omd/sites/cmk/etc/nagios/resource.cfg

<Component Thruk::Backend>
    <peer>
        name    = cmk
        type    = livestatus
        <options>
            peer = /opt/omd/sites/cmk/tmp/run/live
            #peer = 127.0.0.1:6557
        </options>
        <configtool>
            core_conf      = /omd/sites/cmk/tmp/nagios/nagios.cfg
            #obj_check_cmd  = /omd/sites/cmk/etc/init.d/nagios check
            #obj_reload_cmd = /omd/sites/cmk/etc/init.d/nagios reload
            #obj_readonly   = check_mk_objects\.cfg
        </configtool>
    </peer>
</Component>" > /etc/thruk/thruk_local.d/thruk_ansible.conf

if [ -n "${THRUK_MENU_CONFIG:-}" ]; then
  echo "Adding entries to thruk menu"
  echo "${THRUK_MENU_CONFIG}" > /etc/thruk/menu_local.conf
else
  echo "Warning: THRUK_MENU_CONFIG environment variable is not set"
fi

echo "Starting apache on port 80"
apache2ctl start &

echo "Configuring ssh for user cmk"
[ -d "/opt/omd/sites/cmk/.ssh" ] || mkdir -p /opt/omd/sites/cmk/.ssh && chown 1000:1000 /opt/omd/sites/cmk/.ssh && chmod 0700 /opt/omd/sites/cmk/.ssh
[ -d "/opt/omd/sites/cmk/.ssh/sockets" ] || mkdir -p /opt/omd/sites/cmk/.ssh/sockets && chown 1000:1000 /opt/omd/sites/cmk/.ssh/sockets
if [ ! -f "/opt/omd/sites/cmk/.ssh/config" ]; then
  echo "Host *
    ControlMaster auto
    ControlPath  ~/.ssh/sockets/%r@%h-%p
    ControlPersist 600
    User nagios
    ForwardAgent yes" > /opt/omd/sites/cmk/.ssh/config
  chown 1000:1000 /opt/omd/sites/cmk/.ssh/config
fi

# SSH Private Key from environment variable
if [ -n "${SSH_PRIVATE_KEY:-}" ]; then
  echo "Creating SSH private key from environment variable"
  echo "${SSH_PRIVATE_KEY}" > /opt/omd/sites/cmk/.ssh/id_ed25519
  chmod 0600 /opt/omd/sites/cmk/.ssh/id_ed25519
  chown 1000:1000 /opt/omd/sites/cmk/.ssh/id_ed25519
else
  echo "Warning: SSH_PRIVATE_KEY environment variable is not set"
fi

# IPMI configuration from environment variable
if [ -n "${IPMI_CONFIG:-}" ]; then
  echo "Creating IPMI config from environment variable"
  mkdir -p /opt/omd/sites/cmk/etc/ipmi-config
  echo "${IPMI_CONFIG}" > /opt/omd/sites/cmk/etc/ipmi-config/ipmi-default.cfg
  chmod 0600 /opt/omd/sites/cmk/etc/ipmi-config/ipmi-default.cfg
  chown -R 1000:1000 /opt/omd/sites/cmk/etc/ipmi-config
else
  echo "Warning: IPMI_CONFIG environment variable is not set"
fi

# Nagios Plugins
echo "Downloading check plugins"
rm -rf /opt/omd/sites/cmk/local/lib/nagios/plugins/*
mkdir -p /opt/omd/sites/cmk/local/lib/nagios/plugins/{check_ssl_cert,check_ipmi_sensor_v3}
curl -sL https://github.com/matteocorti/check_ssl_cert/releases/download/v${CHECK_SSL_CERT_VERSION}/check_ssl_cert-${CHECK_SSL_CERT_VERSION}.tar.gz | tar xzf - --strip-components=1 -C /opt/omd/sites/cmk/local/lib/nagios/plugins/check_ssl_cert/
git clone https://github.com/thomas-krenn/check_ipmi_sensor_v3.git /opt/omd/sites/cmk/local/lib/nagios/plugins/check_ipmi_sensor_v3/
chmod +x /omd/sites/cmk/local/lib/nagios/plugins/check_ipmi_sensor_v3/check_ipmi_sensor
chown -R 1000:1000 /opt/omd/sites/cmk/local/lib/nagios/plugins
if [ -n "${CHECK_MATRIX_VERSION:-}" ]; then
  echo "${CHECK_MATRIX_VERSION}" > /omd/sites/cmk/local/lib/nagios/plugins/check_matrix_version.sh
  chmod +x /omd/sites/cmk/local/lib/nagios/plugins/check_matrix_version.sh
  chown 1000:1000 /omd/sites/cmk/local/lib/nagios/plugins/check_matrix_version.sh
fi
echo "### Finished pre-start script"
