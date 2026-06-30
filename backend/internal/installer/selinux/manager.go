package selinux

import "text/template"

const (
	TemplateFuncName = "serviceAccessHelpers"

	PortTypeDocker = "docker_port_t"
	PortTypeHTTP   = "http_port_t"
	PortTypeMySQL  = "mysqld_port_t"
	PortTypeRedis  = "redis_port_t"
)

const shellHelpers = `
: "${SUDO:=}"

open_firewall_ports() {
  [ "$#" -gt 0 ] || return 0
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

selinux_is_enabled() {
  if ! command -v getenforce >/dev/null 2>&1; then
    return 1
  fi
  [ "$(getenforce 2>/dev/null || echo Disabled)" != "Disabled" ]
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
  [ "$#" -gt 0 ] || return 0
  if ! selinux_is_enabled; then
    echo "SELinux is disabled or unavailable, skip port rules"
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

set_selinux_fcontext() {
  context_type="$1"
  path_pattern="$2"
  if ! selinux_is_enabled; then
    echo "SELinux is disabled or unavailable, skip file context rule"
    return 0
  fi
  if ! command -v semanage >/dev/null 2>&1; then
    echo "warning: semanage not found, skip file context rule for $path_pattern"
    return 0
  fi
  if $SUDO semanage fcontext -a -t "$context_type" "$path_pattern" >/dev/null 2>&1; then
    echo "SELinux file context added: $path_pattern -> $context_type"
  elif $SUDO semanage fcontext -m -t "$context_type" "$path_pattern" >/dev/null 2>&1; then
    echo "SELinux file context updated: $path_pattern -> $context_type"
  else
    echo "warning: failed to set SELinux file context for $path_pattern as $context_type"
  fi
}

restore_selinux_context() {
  [ "$#" -gt 0 ] || return 0
  if ! selinux_is_enabled; then
    echo "SELinux is disabled or unavailable, skip restorecon"
    return 0
  fi
  if ! command -v restorecon >/dev/null 2>&1; then
    echo "warning: restorecon not found, skip context restore"
    return 0
  fi
  for path in "$@"; do
    if [ -e "$path" ]; then
      if $SUDO restorecon -R "$path" >/dev/null 2>&1; then
        echo "SELinux context restored: $path"
      else
        echo "warning: failed to restore SELinux context: $path"
      fi
    fi
  done
}

set_selinux_boolean() {
  boolean_name="$1"
  boolean_value="$2"
  if ! selinux_is_enabled; then
    echo "SELinux is disabled or unavailable, skip boolean $boolean_name"
    return 0
  fi
  if ! command -v setsebool >/dev/null 2>&1; then
    echo "warning: setsebool not found, skip boolean $boolean_name"
    return 0
  fi
  if $SUDO setsebool -P "$boolean_name" "$boolean_value" >/dev/null 2>&1; then
    echo "SELinux boolean set: $boolean_name=$boolean_value"
  else
    echo "warning: failed to set SELinux boolean: $boolean_name=$boolean_value"
  fi
}

print_recent_selinux_denials() {
  if ! selinux_is_enabled; then
    return 0
  fi
  if command -v ausearch >/dev/null 2>&1; then
    echo "recent SELinux AVC denials:"
    $SUDO ausearch -m AVC,USER_AVC -ts recent 2>/dev/null || true
  fi
}
`

func ServiceAccessHelpers() string {
	return shellHelpers
}

func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		TemplateFuncName: ServiceAccessHelpers,
	}
}

func AddTemplateFuncs(base template.FuncMap) template.FuncMap {
	funcs := make(template.FuncMap, len(base)+1)
	for name, fn := range base {
		funcs[name] = fn
	}
	for name, fn := range TemplateFuncs() {
		funcs[name] = fn
	}
	return funcs
}
