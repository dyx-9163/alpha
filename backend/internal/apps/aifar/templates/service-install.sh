#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_ORDER={{ quote .ServiceOrder }}
NEW_SERVICES={{ quote .NewServices }}
VERSION={{ quote .Version }}
REVISION={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
SERVICE_INSTALL_SUCCEEDED=0
ORCHESTRATION_MODEL="agent-runtime-v2"

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

service_known() {
  case "$1" in
    oauth|permission|system|file|message|im|contacts|meeting|gateway|web-vue3) return 0 ;;
    *) return 1 ;;
  esac
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
  [ -n "$var" ] || fail "unsupported service port: $1"
  read_env_value "$ENV_DIR/compose.env" "$var" ""
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
  if [ "$service" != "web-vue3" ]; then
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
  app_name="$(alpha_service_name "$service")"
  if [ -n "$app_name" ]; then
    set_env SPRING_APPLICATION_NAME "$app_name" "$service_env"
  fi
  port_var="$(service_port_var "$service")"
  if [ "$service" != "web-vue3" ] && [ -n "$port_var" ]; then
    port_value="$(read_env_value "$ENV_DIR/compose.env" "$port_var" "")"
    [ -n "$port_value" ] && set_env SERVER_PORT "$port_value" "$service_env"
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
  if [ "$service" = "web-vue3" ]; then
    path="$(read_env_value "$ENV_DIR/compose.env" APP_WEB_HEALTH_PATH "/")"
    printf "wget -q -T %s -O /dev/null %s://%s:%s%s || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  else
    path="$(read_env_value "$ENV_DIR/$service.env" HEALTH_PATH "")"
    [ -n "$path" ] || path="$(read_env_value "$ENV_DIR/compose.env" APP_BACKEND_HEALTH_PATH "/actuator/health/readiness")"
    [ -n "$path" ] || path="/actuator/health/readiness"
    printf "curl -fsS --connect-timeout %s %s://%s:%s%s >/dev/null || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  fi
}

current_replicas_for_service() {
  docker ps --filter "label=aifar.app=aifar" \
    --filter "label=aifar.install-root=$INSTALL_ROOT" \
    --filter "label=aifar.component=pod" \
    --filter "label=aifar.service=$1" \
    --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null | wc -l | tr -d ' '
}

desired_replicas_from_env() {
  wanted="$1"
  for pair in $(read_env_value "$ENV_DIR/compose.env" AIFAR_DESIRED_REPLICAS ""); do
    case "$pair" in
      "$wanted="*) printf "%s" "${pair#*=}"; return 0 ;;
    esac
  done
  return 1
}

desired_replicas_for_service() {
  service="$1"
  for new_service in $NEW_SERVICES; do
    if [ "$new_service" = "$service" ]; then
      printf "1"
      return
    fi
  done
  if value="$(desired_replicas_from_env "$service")"; then
    case "$value" in ""|*[!0-9]*) value=1 ;; esac
    [ "$value" -ge 0 ] || value=0
    printf "%s" "$value"
    return
  fi
  replicas="$(current_replicas_for_service "$service")"
  case "$replicas" in ""|*[!0-9]*) replicas=1 ;; esac
  [ "$replicas" -ge 0 ] || replicas=0
  if [ "$replicas" -eq 0 ]; then
    replicas=1
  fi
  printf "%s" "$replicas"
}

nacos_ephemeral() {
  value="$(read_env_value "$ENV_DIR/compose.env" AIFAR_NACOS_EPHEMERAL true)"
  case "$(printf "%s" "$value" | tr '[:upper:]' '[:lower:]')" in
    false|0|no|off) printf "false" ;;
    *) printf "true" ;;
  esac
}

write_desired_replicas_env() {
  pairs=""
  for service in $SERVICE_ORDER; do
    [ -f "$ENV_DIR/$service.env" ] || continue
    replicas="$(desired_replicas_for_service "$service")"
    case "$replicas" in ""|*[!0-9]*) replicas=1 ;; esac
    [ "$replicas" -ge 0 ] || replicas=0
    if [ -z "$pairs" ]; then
      pairs="$service=$replicas"
    else
      pairs="$pairs $service=$replicas"
    fi
  done
  set_env AIFAR_DESIRED_REPLICAS "$pairs" "$ENV_DIR/compose.env"
}

write_runtime_spec() {
  spec="$AGENT_DIR/runtime-spec.json"
  gateway_port="$(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)"
  web_port="$(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)"
  nacos_ns="$(read_env_value "$ENV_DIR/java-common.env" NACOS_NS prod)"
  tz_value="$(read_env_value "$ENV_DIR/compose.env" TZ system)"
  mkdir -p "$AGENT_DIR"
  cat > "$spec" <<JSON
{
  "version": "runtime-v2",
  "instanceId": "admin",
  "installRoot": "${INSTALL_ROOT}",
  "network": "${INGRESS_NETWORK}",
  "deployments": [
JSON
  first_deployment=1
  for service in $SERVICE_ORDER; do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$REVISION")"
    service_revision="$(read_env_value "$service_env" AIFAR_REVISION "$REVISION")"
    replicas="$(desired_replicas_for_service "$service")"
    port="$(service_port "$service")"
    health_cmd="$(health_cmd_for_service "$service")"
    app_cpus="$(resource_value "$service" APP_CPUS "$(read_env_value "$ENV_DIR/compose.env" APP_CPUS "")")"
    app_memory_limit="$(resource_value "$service" APP_MEMORY_LIMIT "$(read_env_value "$ENV_DIR/compose.env" APP_MEMORY_LIMIT "")")"
    deployment_name="$(alpha_service_name "$service")"
    log_dir="$LOG_DIR/$service"
    mkdir -p "$log_dir"
    if [ "$first_deployment" = "1" ]; then
      first_deployment=0
    else
      printf ",\n" >> "$spec"
    fi
    printf '    {\n' >> "$spec"
    printf '      "serviceName": "%s",\n' "$service" >> "$spec"
    printf '      "deploymentName": "%s",\n' "$(json_escape "$deployment_name")" >> "$spec"
    printf '      "image": "%s",\n' "$(json_escape "$image")" >> "$spec"
    printf '      "podRevision": "%s",\n' "$(json_escape "$service_revision")" >> "$spec"
    printf '      "replicas": %s,\n' "$replicas" >> "$spec"
    printf '      "ports": [{"name":"http","containerPort":%s}],\n' "$port" >> "$spec"
    if [ "$service" = "web-vue3" ]; then
      printf '      "envFiles": ["%s"],\n' "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/logs","readOnly":false},{"source":"%s","target":"/var/log/nginx","readOnly":false}],\n' "$(json_escape "$log_dir")" "$(json_escape "$log_dir")" >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_LOG_DIR":"/opt/aifar/logs","LOG_DIR":"/opt/aifar/logs","TZ":"%s"},\n' "$(json_escape "$tz_value")" >> "$spec"
    else
      printf '      "envFiles": ["%s","%s","%s"],\n' "$(json_escape "$ENV_DIR/java-common.env")" "$(json_escape "$ENV_DIR/java-secrets.env")" "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/runtime/env","readOnly":true},{"source":"%s","target":"/opt/aifar/logs","readOnly":false},{"source":"%s","target":"/data/aifarsoft/javaApi/aifar-%s/log","readOnly":false}],\n' "$(json_escape "$ENV_DIR")" "$(json_escape "$log_dir")" "$(json_escape "$log_dir")" "$(json_escape "$service")" >> "$spec"
      printf '      "entrypoint": ["/bin/sh"],\n' >> "$spec"
      printf '      "command": ["/opt/aifar/runtime/env/java-entrypoint.sh"],\n' >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_SERVICE_NAME":"%s","AIFAR_LOG_DIR":"/opt/aifar/logs","LOG_DIR":"/opt/aifar/logs","TZ":"%s"},\n' "$service" "$(json_escape "$tz_value")" >> "$spec"
    fi
    printf '      "resources": {"cpus":"%s","memory":"%s"},\n' "$(json_escape "$app_cpus")" "$(json_escape "$app_memory_limit")" >> "$spec"
    printf '      "healthCheck": {"command":"%s","interval":"%s","timeout":"%s","retries":%s,"startPeriod":"%s"}\n' \
      "$(json_escape "$health_cmd")" \
      "$(json_escape "$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_INTERVAL 15s)")" \
      "$(json_escape "$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_TIMEOUT 5s)")" \
      "$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_RETRIES 3)" \
      "$(json_escape "$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_START_PERIOD 30s)")" >> "$spec"
    printf '    }' >> "$spec"
  done
  cat >> "$spec" <<JSON
  ],
  "services": [
JSON
  first_service=1
  for service in $SERVICE_ORDER; do
    [ -f "$ENV_DIR/$service.env" ] || continue
    port="$(service_port "$service")"
    app_name="$(alpha_service_name "$service")"
    if [ "$first_service" = "1" ]; then
      first_service=0
    else
      printf ",\n" >> "$spec"
    fi
    affinity="round-robin"
    case "$service" in gateway|file) affinity="stable" ;; esac
    printf '    {"name":"%s","appName":"%s","listenPort":%s,"targetPort":%s,"affinityPolicy":"%s"}' "$service" "$app_name" "$port" "$port" "$affinity" >> "$spec"
  done
  cat >> "$spec" <<JSON
  ],
  "ingress": {
    "mode": "web-nginx",
    "gatewayService": "gateway",
    "webService": "web-vue3",
    "gatewayPort": ${gateway_port},
    "webPort": ${web_port}
  },
  "nacos": {
    "namespace": "${nacos_ns}",
    "group": "DEFAULT_GROUP",
    "ephemeral": $(nacos_ephemeral),
    "agentIPStrategy": "auto"
  }
}
JSON
  printf "%s" "$spec"
}

reconcile_runtime() {
  check_agent_dependency
  write_desired_replicas_env
  spec="$(write_runtime_spec)"
  aifar-agent reconcile-runtime --spec "$spec"
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
[ -f "$INSTALL_ROOT/.aifar/model.json" ] || fail "AIFAR agent-runtime-v2 model manifest is missing"
grep -q '"model"[[:space:]]*:[[:space:]]*"agent-runtime-v2"' "$INSTALL_ROOT/.aifar/model.json" || fail "AIFAR_RUNTIME_REINSTALL_REQUIRED: reinstall AIFAR with agent-runtime-v2"

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

if ! reconcile_runtime; then
  cleanup_failed_service_install
  fail "AIFAR runtime reconcile failed after service installation"
fi

open_service_ports $NEW_SERVICES

write_model_manifest
SERVICE_INSTALL_SUCCEEDED=1
trap - EXIT INT TERM
echo "AIFAR services installed: $NEW_SERVICES"
