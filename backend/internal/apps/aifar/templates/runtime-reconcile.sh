#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SPEC_PATH={{ quote .SpecPath }}

ENV_DIR="$INSTALL_ROOT/runtime/env"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

{{ serviceAccessHelpers }}

read_env_value() {
  file="$1"
  key="$2"
  fallback="$3"
  if [ -f "$file" ]; then
    value="$(awk -F= -v k="$key" '$1==k {sub(/^[^=]*=/,""); print; exit}' "$file" 2>/dev/null || true)"
    if [ -n "$value" ]; then
      printf "%s" "$value"
      return
    fi
  fi
  printf "%s" "$fallback"
}

alpha_service_pairs() {
  cat <<'EOF'
gateway alpha-gateway
oauth alpha-oauth
permission alpha-permission
system alpha-system
file alpha-file
message alpha-message
im alpha-im
contacts alpha-contacts
meeting alpha-meeting
EOF
}

alpha_service_name() {
  service="$1"
  alpha_service_pairs | awk -v s="$service" '$1==s {print $2; exit}'
}

service_port_var() {
  case "$1" in
    gateway) printf "GATEWAY_PORT" ;;
    oauth) printf "OAUTH_PORT" ;;
    permission) printf "PERMISSION_PORT" ;;
    system) printf "SYSTEM_PORT" ;;
    file) printf "FILE_PORT" ;;
    message) printf "MESSAGE_PORT" ;;
    im) printf "IM_PORT" ;;
    contacts) printf "CONTACTS_PORT" ;;
    meeting) printf "MEETING_PORT" ;;
    web-vue3) printf "WEB_VUE3_PORT" ;;
    *) printf "" ;;
  esac
}

service_port() {
  service="$1"
  value="$(read_env_value "$ENV_DIR/$service.env" AIFAR_SERVICE_PORT "")"
  [ -n "$value" ] || value="$(read_env_value "$ENV_DIR/$service.env" SERVER_PORT "")"
  [ -n "$value" ] && { printf "%s" "$value"; return; }
  var="$(service_port_var "$service")"
  [ -n "$var" ] || return 0
  read_env_value "$ENV_DIR/compose.env" "$var" ""
}

open_service_ports() {
  ports=""
  for service in "$@"; do
    [ -f "$ENV_DIR/$service.env" ] || continue
    port="$(service_port "$service")"
    [ -n "$port" ] || continue
    ports="$ports $port"
  done
  [ -n "$ports" ] || return 0
  # shellcheck disable=SC2086
  open_firewall_ports $ports
  # shellcheck disable=SC2086
  allow_selinux_ports http_port_t $ports
}

command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is not installed"
[ -f "$SPEC_PATH" ] || fail "AIFAR runtime spec is missing"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"

aifar-agent reconcile-runtime --spec "$SPEC_PATH"
runtime_services=""
for pair in $(read_env_value "$ENV_DIR/compose.env" AIFAR_DESIRED_REPLICAS ""); do
  runtime_services="$runtime_services ${pair%%=*}"
done
# shellcheck disable=SC2086
open_service_ports $runtime_services
