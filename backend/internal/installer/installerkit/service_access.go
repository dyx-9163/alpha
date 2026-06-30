package installerkit

const serviceAccessHelpers = `
open_firewall_ports() {
  if ! command -v firewall-cmd >/dev/null 2>&1; then
    echo "warning: firewall-cmd not found, skip firewall port opening"
    return 0
  fi
  if ! $SUDO firewall-cmd --state >/dev/null 2>&1; then
    echo "warning: firewalld is not running, skip firewall port opening"
    return 0
  fi
  for port in "$@"; do
    if $SUDO firewall-cmd --permanent --add-port="$port/tcp" >/dev/null 2>&1; then
      echo "firewall opened tcp port: $port"
    else
      echo "warning: failed to open firewall tcp port: $port"
    fi
  done
  if ! $SUDO firewall-cmd --reload >/dev/null 2>&1; then
    echo "warning: failed to reload firewalld"
  fi
}

selinux_port_has_type() {
  semanage port -l 2>/dev/null | awk -v want_type="$1" -v want_port="$2" '
    $1 == want_type && $2 == "tcp" {
      for (i = 3; i <= NF; i++) {
        gsub(",", "", $i)
        n = split($i, port_range, "-")
        if ((n == 1 && port_range[1] == want_port) || (n == 2 && want_port >= port_range[1] && want_port <= port_range[2])) {
          found = 1
        }
      }
    }
    END { exit(found ? 0 : 1) }
  '
}

allow_selinux_ports() {
  port_type="$1"
  shift
  if ! command -v getenforce >/dev/null 2>&1; then
    echo "warning: getenforce not found, skip SELinux port rules"
    return 0
  fi
  if [ "$(getenforce 2>/dev/null || echo Disabled)" = "Disabled" ]; then
    echo "SELinux is disabled, skip port rules"
    return 0
  fi
  if ! command -v semanage >/dev/null 2>&1; then
    echo "warning: semanage not found, skip SELinux port rules"
    return 0
  fi
  for port in "$@"; do
    if selinux_port_has_type "$port_type" "$port"; then
      echo "SELinux already allows tcp port $port as $port_type"
    elif $SUDO semanage port -a -t "$port_type" -p tcp "$port" >/dev/null 2>&1; then
      echo "SELinux allowed tcp port $port as $port_type"
    elif $SUDO semanage port -m -t "$port_type" -p tcp "$port" >/dev/null 2>&1; then
      echo "SELinux updated tcp port $port as $port_type"
    else
      echo "warning: failed to add SELinux rule for tcp port $port as $port_type"
    fi
  done
}
`

func ServiceAccessHelpers() string {
	return serviceAccessHelpers
}
