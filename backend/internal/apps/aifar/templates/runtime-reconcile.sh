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
  var="$(service_port_var "$1")"
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

agent_host_ip() {
  nacos_host="$(read_env_value "$ENV_DIR/java-common.env" NACOS_HOST "")"
  nacos_connect_host="${nacos_host%:*}"
  if command -v ip >/dev/null 2>&1 && [ -n "$nacos_connect_host" ]; then
    route_ip="$(ip route get "$nacos_connect_host" 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}' || true)"
    if [ -n "$route_ip" ]; then
      printf "%s" "$route_ip"
      return
    fi
  fi
  hostname -I 2>/dev/null | awk '{print $1; exit}'
}

nacos_access_token() {
  nacos_host="$(read_env_value "$ENV_DIR/java-common.env" NACOS_HOST "")"
  nacos_connect_host="${nacos_host%:*}"
  nacos_port="$(read_env_value "$ENV_DIR/java-common.env" NACOS_PORT_WEB "${nacos_host##*:}")"
  nacos_user="$(read_env_value "$ENV_DIR/java-common.env" NACOS_USER nacos)"
  nacos_password="$(read_env_value "$ENV_DIR/java-secrets.env" NACOS_PASSWORD "")"
  if command -v curl >/dev/null 2>&1 && [ -n "$nacos_connect_host" ] && [ -n "$nacos_port" ]; then
    body="$(curl -fsS -X POST "http://${nacos_connect_host}:${nacos_port}/nacos/v1/auth/users/login" -d "username=${nacos_user}&password=${nacos_password}" 2>/dev/null || true)"
    token="$(printf "%s" "$body" | sed -n 's/.*"accessToken"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
    [ -n "$token" ] && printf "%s" "$token"
  fi
}

register_nacos_proxy() {
  service="$1"
  app_name="$(alpha_service_name "$service")"
  [ -n "$app_name" ] || return 0
  nacos_host="$(read_env_value "$ENV_DIR/java-common.env" NACOS_HOST "")"
  nacos_connect_host="${nacos_host%:*}"
  nacos_port="$(read_env_value "$ENV_DIR/java-common.env" NACOS_PORT_WEB "${nacos_host##*:}")"
  nacos_ns="$(read_env_value "$ENV_DIR/java-common.env" NACOS_NS prod)"
  ip="$(agent_host_ip)"
  port="$(service_port "$service")"
  [ -n "$ip" ] || fail "AIFAR agent host IP is empty for $service"
  [ -n "$nacos_connect_host" ] || fail "Nacos host is missing for $service"
  [ -n "$port" ] || fail "AIFAR service port is empty for $service"
  token="$(nacos_access_token || true)"
  token_arg=""
  [ -z "$token" ] || token_arg="&accessToken=$token"
  url="http://${nacos_connect_host}:${nacos_port}/nacos/v1/ns/instance?serviceName=${app_name}&ip=${ip}&port=${port}&namespaceId=${nacos_ns}&ephemeral=false${token_arg}"
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -X DELETE "$url" >/dev/null 2>&1 || true
    curl -fsS -X POST "$url" >/dev/null 2>&1 || fail "register Nacos service proxy failed: $app_name"
    echo "Nacos agent proxy registered: $app_name -> $ip:$port"
  else
    echo "curl is not available; skip Nacos agent proxy registration for $app_name"
  fi
}

command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is not installed"
[ -f "$SPEC_PATH" ] || fail "AIFAR runtime spec is missing"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"

aifar-agent reconcile-runtime --spec "$SPEC_PATH"
open_service_ports gateway oauth permission system file message im contacts meeting web-vue3

for service in gateway oauth permission system file message im contacts meeting; do
  [ -f "$ENV_DIR/$service.env" ] || continue
  register_nacos_proxy "$service"
done
