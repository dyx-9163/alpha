#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_ORDER={{ quote .ServiceOrder }}
NEW_SERVICES={{ quote .NewServices }}
SERVICE_APPLICATIONS={{ quote .ServiceApplications }}
SERVICE_PORTS={{ quote .ServicePorts }}
SERVICE_KINDS={{ quote .ServiceKinds }}
SERVICE_HEALTH_PATHS={{ quote .ServiceHealthPaths }}
SERVICE_AFFINITIES={{ quote .ServiceAffinities }}
GATEWAY_SERVICE={{ quote .GatewayService }}
WEB_SERVICE={{ quote .WebService }}
VERSION={{ quote .Version }}
REVISION={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
SERVICE_INSTALL_SUCCEEDED=0
ORCHESTRATION_MODEL="agent-service-controller-v1"

RUNTIME_DIR="$INSTALL_ROOT/runtime"
APP_DIR="$RUNTIME_DIR/services"
ENV_DIR="$RUNTIME_DIR/env"
LOG_DIR="$RUNTIME_DIR/logs"
AGENT_DIR="$RUNTIME_DIR/agent"
AIFAR_DIR="$INSTALL_ROOT/.aifar"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

{{ serviceAccessHelpers }}

set_env() {
  key="$1"
  value="$2"
  file="$3"
  tmp="${file}.tmp"
  [ -f "$file" ] || touch "$file"
  grep -v "^${key}=" "$file" > "$tmp" || true
  printf "%s=%s\n" "$key" "$value" >> "$tmp"
  mv "$tmp" "$file"
}

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

json_escape() {
  printf "%s" "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

retag_image() {
  image="$1"
  case "$image" in
    *@*) printf "%s" "$image" ;;
    *:*) printf "%s:%s" "${image%:*}" "$REVISION" ;;
    *) printf "%s:%s" "$image" "$REVISION" ;;
  esac
}

pod_name() {
  printf "aifar-pod-admin-%s-%s-r%s" "$1" "$2" "$3" | tr '. _/' '----'
}

catalog_value() {
  pairs="$1"
  wanted="$2"
  for pair in $pairs; do
    case "$pair" in "$wanted="*) printf "%s" "${pair#*=}"; return 0 ;; esac
  done
  return 1
}

service_known() {
  catalog_value "$SERVICE_KINDS" "$1" >/dev/null 2>&1
}

alpha_service_name() {
  catalog_value "$SERVICE_APPLICATIONS" "$1" || true
}

service_kind() {
  catalog_value "$SERVICE_KINDS" "$1" || printf "java"
}

service_port() {
  service="$1"
  value="$(read_env_value "$ENV_DIR/$service.env" AIFAR_SERVICE_PORT "")"
  [ -n "$value" ] || value="$(read_env_value "$ENV_DIR/$service.env" SERVER_PORT "")"
  [ -n "$value" ] || value="$(catalog_value "$SERVICE_PORTS" "$service" || true)"
  [ -n "$value" ] || fail "unsupported service port: $service"
  printf "%s" "$value"
}

open_service_ports() {
  ports=""
  for service in "$@"; do
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

resource_file_for_service() {
  printf "%s/resource.%s.env" "$ENV_DIR" "$1"
}

resource_value() {
  service="$1"
  key="$2"
  fallback="$3"
  file="$(resource_file_for_service "$service")"
  read_env_value "$file" "$key" "$fallback"
}

write_jvm_options_if_missing() {
  file="$1"
  [ -f "$file" ] && return 0
  initial="$(read_env_value "$ENV_DIR/compose.env" JVM_INITIAL_RAM_PERCENTAGE 20)"
  max="$(read_env_value "$ENV_DIR/compose.env" JVM_MAX_RAM_PERCENTAGE 70)"
  cat > "$file" <<EOF
-XX:+UseContainerSupport
-XX:InitialRAMPercentage=${initial}
-XX:MaxRAMPercentage=${max}
-XX:+ExitOnOutOfMemoryError
EOF
  chmod 0644 "$file"
}

ensure_runtime_config_files() {
  service="$1"
  compose_env="$ENV_DIR/compose.env"
  cpus="$(read_env_value "$compose_env" APP_CPUS "")"
  memory="$(read_env_value "$compose_env" APP_MEMORY_LIMIT "")"
  resource_file="$(resource_file_for_service "$service")"
  [ -f "$resource_file" ] || {
    : > "$resource_file"
    set_env APP_CPUS "$cpus" "$resource_file"
    set_env APP_MEMORY_LIMIT "$memory" "$resource_file"
    chmod 0644 "$resource_file"
  }
  if [ "$(service_kind "$service")" != "web" ]; then
    write_jvm_options_if_missing "$ENV_DIR/java-jvm.options"
    write_jvm_options_if_missing "$ENV_DIR/java-jvm.$service.options"
  fi
}

java_start_command() {
  cat <<'EOF'
opts_file="/opt/aifar/runtime/env/java-jvm.${AIFAR_SERVICE_NAME}.options"
[ -f "$opts_file" ] || opts_file="/opt/aifar/runtime/env/java-jvm.options"
java_opts=""
[ -f "$opts_file" ] && java_opts="$(tr '\n' ' ' < "$opts_file")"
jar="aifar-${AIFAR_SERVICE_NAME}.jar"
[ -f app.jar ] && jar="app.jar"
if [ ! -f "$jar" ]; then
  jar="$(find . -maxdepth 1 -type f -name '*.jar' 2>/dev/null | head -n 1 | sed 's#^\./##')"
fi
[ -n "$jar" ] && [ -f "$jar" ] || { echo "AIFAR jar is missing for ${AIFAR_SERVICE_NAME}" >&2; exit 1; }
exec java $java_opts --add-opens=java.base/java.lang=ALL-UNNAMED --add-opens=java.base/java.lang.reflect=ALL-UNNAMED --add-opens=java.base/java.lang.invoke=ALL-UNNAMED --add-opens=java.base/java.math=ALL-UNNAMED --add-opens=java.base/sun.net.util=ALL-UNNAMED --add-opens=java.base/java.io=ALL-UNNAMED --add-opens=java.base/java.net=ALL-UNNAMED --add-opens=java.base/java.nio=ALL-UNNAMED --add-opens=java.base/java.security=ALL-UNNAMED --add-opens=java.base/java.text=ALL-UNNAMED --add-opens=java.base/java.time=ALL-UNNAMED --add-opens=java.base/java.util=ALL-UNNAMED --add-opens=java.base/jdk.internal.module=ALL-UNNAMED --add-opens=java.base/sun.security.util=ALL-UNNAMED -Dfile.encoding=utf8 -Djava.security.egd=file:/dev/./urandom -jar "$jar"
EOF
}

check_agent_dependency() {
  command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required; install or upgrade Docker runtime first"
  aifar-agent status >/dev/null 2>&1 || fail "aifar-agent service is not reachable; install or upgrade Docker runtime first"
}

write_service_env() {
  service="$1"
  service_env="$ENV_DIR/$service.env"
  image="$(retag_image "aifar-$service:latest")"
  : > "$service_env"
  set_env APP_IMAGE "$image" "$service_env"
  set_env APP_CONTAINER_NAME "$(pod_name "$service" "$REVISION" 1)" "$service_env"
  set_env AIFAR_SERVICE_PROXY "aifar-agent" "$service_env"
  set_env AIFAR_REVISION "$REVISION" "$service_env"
  set_env AIFAR_SERVICE_KIND "$(service_kind "$service")" "$service_env"
  set_env AIFAR_AFFINITY_POLICY "$(catalog_value "$SERVICE_AFFINITIES" "$service" || printf round-robin)" "$service_env"
  set_env HEALTH_PATH "$(catalog_value "$SERVICE_HEALTH_PATHS" "$service" || true)" "$service_env"
  app_name="$(alpha_service_name "$service")"
  if [ -n "$app_name" ]; then
    set_env SPRING_APPLICATION_NAME "$app_name" "$service_env"
  fi
  port_value="$(catalog_value "$SERVICE_PORTS" "$service" || true)"
  [ -n "$port_value" ] && set_env AIFAR_SERVICE_PORT "$port_value" "$service_env"
  if [ "$(service_kind "$service")" != "web" ] && [ -n "$port_value" ]; then
    set_env SERVER_PORT "$port_value" "$service_env"
    set_env SPRING_CLOUD_NACOS_DISCOVERY_REGISTER_ENABLED false "$service_env"
  fi
  chmod 0644 "$service_env"
}

health_cmd_for_service() {
  service="$1"
  port="$(service_port "$service")"
  protocol="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PROTOCOL http)"
  host="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_HOST 127.0.0.1)"
  timeout="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_CONNECT_TIMEOUT 3)"
  if [ "$(service_kind "$service")" = "web" ]; then
    path="$(catalog_value "$SERVICE_HEALTH_PATHS" "$service" || true)"
    [ -n "$path" ] || path="$(read_env_value "$ENV_DIR/compose.env" APP_WEB_HEALTH_PATH "/")"
    printf "wget -q -T %s -O /dev/null %s://%s:%s%s || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  else
    path="$(read_env_value "$ENV_DIR/$service.env" HEALTH_PATH "")"
    [ -n "$path" ] || path="$(catalog_value "$SERVICE_HEALTH_PATHS" "$service" || true)"
    [ -n "$path" ] || path="$(read_env_value "$ENV_DIR/compose.env" APP_BACKEND_HEALTH_PATH "/actuator/health/readiness")"
    [ -n "$path" ] || path="/actuator/health/readiness"
    printf "curl -fsS --connect-timeout %s %s://%s:%s%s >/dev/null || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  fi
}

write_model_manifest() {
  mkdir -p "$AIFAR_DIR"
  cat > "$AIFAR_DIR/last-service-install.json" <<JSON
{
  "model": "${ORCHESTRATION_MODEL}",
  "kind": "service-install",
  "services": "${NEW_SERVICES}",
  "revision": "${REVISION}",
  "version": "${VERSION}",
  "createdAt": "${CREATED_AT}",
  "configHash": "${CONFIG_HASH}",
  "network": "${INGRESS_NETWORK}"
}
JSON
}

cleanup_failed_service_install() {
  [ "$SERVICE_INSTALL_SUCCEEDED" = "1" ] && return 0
  for service in $NEW_SERVICES; do
    container="$(pod_name "$service" "$REVISION" 1)"
    docker rm -f "$container" >/dev/null 2>&1 || true
  done
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
check_agent_dependency
[ -d "$APP_DIR" ] || fail "AIFAR runtime app directory is missing"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
[ -f "$INSTALL_ROOT/.aifar/model.json" ] || fail "AIFAR service-controller model manifest is missing"
grep -q '"model"[[:space:]]*:[[:space:]]*"agent-service-controller-v1"' "$INSTALL_ROOT/.aifar/model.json" || fail "AIFAR_RUNTIME_MIGRATION_REQUIRED: migrate AIFAR to agent-service-controller-v1"

trap 'cleanup_failed_service_install' EXIT INT TERM
java_start_command > "$ENV_DIR/java-entrypoint.sh"
chmod 0755 "$ENV_DIR/java-entrypoint.sh"
for service in $NEW_SERVICES; do
  service_known "$service" || fail "unsupported AIFAR service: $service"
  [ -d "$APP_DIR/$service" ] || fail "AIFAR service app directory is missing: $service"
  existing="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$service" --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$existing" ] || fail "AIFAR service already has Pod(s): $service"
  write_service_env "$service"
  ensure_runtime_config_files "$service"
  image="$(read_env_value "$ENV_DIR/$service.env" APP_IMAGE "")"
  [ -n "$image" ] || fail "service image is empty: $service"
  docker build -t "$image" "$APP_DIR/$service"
done

open_service_ports $NEW_SERVICES

write_model_manifest
SERVICE_INSTALL_SUCCEEDED=1
trap - EXIT INT TERM
echo "AIFAR services installed: $NEW_SERVICES"
