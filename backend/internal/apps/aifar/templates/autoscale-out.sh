#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_NAME={{ quote .ServiceName }}
REVISION={{ quote .ReleaseID }}
REPLICA_ID={{ .ReplicaID }}
CONTAINER_NAME={{ quote .ContainerName }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
MAX_REPLICAS={{ .MaxReplicas }}
CONTROL_PLANE_DESIRED_REPLICAS={{ quote .DesiredReplicas }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
ENV_DIR="$RUNTIME_DIR/env"

fail() {
  echo "$*" >&2
  exit 1
}

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
  fallback="${3:-}"
  if [ -f "$file" ]; then
    value="$(awk -F= -v k="$key" '$1==k {print substr($0, index($0, "=")+1)}' "$file" | tail -n 1)"
    if [ -n "$value" ]; then
      printf "%s" "$value"
      return 0
    fi
  fi
  printf "%s" "$fallback"
}

json_escape() {
  printf "%s" "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

service_order() {
  printf "%s\n" gateway oauth permission system file message im contacts meeting web-vue3
}

alpha_service_name() {
  case "$1" in
    gateway) printf "alpha-gateway" ;;
    oauth) printf "alpha-oauth" ;;
    permission) printf "alpha-permission" ;;
    system) printf "alpha-system" ;;
    file) printf "alpha-file" ;;
    message) printf "alpha-message" ;;
    im) printf "alpha-im" ;;
    contacts) printf "alpha-contacts" ;;
    meeting) printf "alpha-meeting" ;;
    *) printf "" ;;
  esac
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
  compose_env="$ENV_DIR/compose.env"
  cpus="$(read_env_value "$compose_env" APP_CPUS "")"
  memory="$(read_env_value "$compose_env" APP_MEMORY_LIMIT "")"
  resource_file="$(resource_file_for_service "$SERVICE_NAME")"
  [ -f "$resource_file" ] || {
    : > "$resource_file"
    printf "APP_CPUS=%s\n" "$cpus" >> "$resource_file"
    printf "APP_MEMORY_LIMIT=%s\n" "$memory" >> "$resource_file"
    chmod 0644 "$resource_file"
  }
  if [ "$SERVICE_NAME" != "web-vue3" ]; then
    write_jvm_options_if_missing "$ENV_DIR/java-jvm.options"
    write_jvm_options_if_missing "$ENV_DIR/java-jvm.$SERVICE_NAME.options"
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

memory_to_bytes() {
  value="$(printf "%s" "$1" | tr '[:upper:]' '[:lower:]' | tr -d ' ')"
  case "$value" in
    *gib) n="${value%gib}"; awk -v n="$n" 'BEGIN {printf "%.0f", n*1024*1024*1024}' ;;
    *gb) n="${value%gb}"; awk -v n="$n" 'BEGIN {printf "%.0f", n*1000*1000*1000}' ;;
    *mib) n="${value%mib}"; awk -v n="$n" 'BEGIN {printf "%.0f", n*1024*1024}' ;;
    *mb) n="${value%mb}"; awk -v n="$n" 'BEGIN {printf "%.0f", n*1000*1000}' ;;
    *kib) n="${value%kib}"; awk -v n="$n" 'BEGIN {printf "%.0f", n*1024}' ;;
    *kb) n="${value%kb}"; awk -v n="$n" 'BEGIN {printf "%.0f", n*1000}' ;;
    ''|*[!0-9]*) printf "0" ;;
    *) printf "%s" "$value" ;;
  esac
}

service_pod_count() {
  docker ps --filter "label=aifar.app=aifar" \
    --filter "label=aifar.install-root=$INSTALL_ROOT" \
    --filter "label=aifar.component=pod" \
    --filter "label=aifar.service=$SERVICE_NAME" \
    --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null | wc -l | tr -d ' '
}

replicas_for_service() {
  service="$1"
  if [ "$service" = "$SERVICE_NAME" ]; then
    printf "%s" "$REPLICA_ID"
    return
  fi
  if value="$(desired_replicas_from_pairs "$service" "$CONTROL_PLANE_DESIRED_REPLICAS")"; then
    case "$value" in ""|*[!0-9]*) value=1 ;; esac
    [ "$value" -ge 0 ] || value=0
    printf "%s" "$value"
    return
  fi
  if value="$(desired_replicas_from_env "$service")"; then
    case "$value" in ""|*[!0-9]*) value=1 ;; esac
    [ "$value" -ge 0 ] || value=0
    printf "%s" "$value"
    return
  fi
  replicas="$(docker ps --filter "label=aifar.app=aifar" \
    --filter "label=aifar.install-root=$INSTALL_ROOT" \
    --filter "label=aifar.component=pod" \
    --filter "label=aifar.service=$service" \
    --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null | wc -l | tr -d ' ')"
  case "$replicas" in ""|*[!0-9]*) replicas=1 ;; esac
  [ "$replicas" -ge 0 ] || replicas=0
  if [ "$replicas" -eq 0 ]; then
    replicas=1
  fi
  printf "%s" "$replicas"
}

desired_replicas_from_pairs() {
  wanted="$1"
  pairs="$2"
  for pair in $pairs; do
    case "$pair" in
      "$wanted="*) printf "%s" "${pair#*=}"; return 0 ;;
    esac
  done
  return 1
}

desired_replicas_from_env() {
  wanted="$1"
  desired_replicas_from_pairs "$wanted" "$(read_env_value "$ENV_DIR/compose.env" AIFAR_DESIRED_REPLICAS "")"
}

write_desired_replicas_env() {
  pairs=""
  for service in $(service_order); do
    [ -f "$ENV_DIR/$service.env" ] || continue
    replicas="$(replicas_for_service "$service")"
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

nacos_ephemeral() {
  value="$(read_env_value "$ENV_DIR/compose.env" AIFAR_NACOS_EPHEMERAL true)"
  case "$(printf "%s" "$value" | tr '[:upper:]' '[:lower:]')" in
    false|0|no|off) printf "false" ;;
    *) printf "true" ;;
  esac
}

container_status() {
  docker inspect --format '{{ "{{" }}.State.Status{{ "}}" }}' "$1" 2>/dev/null || true
}

container_health() {
  docker inspect --format '{{ "{{" }}if .State.Health{{ "}}" }}{{ "{{" }}.State.Health.Status{{ "}}" }}{{ "{{" }}end{{ "}}" }}' "$1" 2>/dev/null || true
}

wait_container_ready() {
  container="$1"
  timeout="$(read_env_value "$ENV_DIR/compose.env" APP_STARTUP_TIMEOUT 300)"
  case "$timeout" in ""|*[!0-9]*) timeout=300 ;; esac
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    status="$(container_status "$container")"
    health="$(container_health "$container")"
    if [ "$status" = "running" ] && { [ -z "$health" ] || [ "$health" = "healthy" ]; }; then
      return 0
    fi
    sleep 3
  done
  docker logs --tail 120 "$container" >&2 || true
  return 1
}

write_runtime_spec() {
  spec="$INSTALL_ROOT/runtime/agent/runtime-spec.json"
  gateway_port="$(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)"
  web_port="$(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)"
  nacos_ns="$(read_env_value "$ENV_DIR/java-common.env" NACOS_NS prod)"
  tz_value="$(read_env_value "$ENV_DIR/compose.env" TZ system)"
  mkdir -p "$INSTALL_ROOT/runtime/agent"
  cat > "$spec" <<JSON
{
  "version": "runtime-v2",
  "instanceId": "admin",
  "installRoot": "${INSTALL_ROOT}",
  "network": "${INGRESS_NETWORK}",
  "deployments": [
JSON
  first_deployment=1
  for service in $(service_order); do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$REVISION")"
    service_revision="$(read_env_value "$service_env" AIFAR_REVISION "$REVISION")"
    replicas="$(replicas_for_service "$service")"
    port="$(service_port "$service")"
    if [ "$service" = "web-vue3" ]; then
      health_path="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PATH "/")"
      health_cmd="wget -q -T $(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_CONNECT_TIMEOUT 3) -O /dev/null $(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PROTOCOL http)://$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_HOST 127.0.0.1):${port}${health_path} || exit 1"
    else
      health_path="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PATH "")"
      if [ -n "$health_path" ]; then
        health_cmd="curl -fsS --connect-timeout $(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_CONNECT_TIMEOUT 3) $(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PROTOCOL http)://$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_HOST 127.0.0.1):${port}${health_path} >/dev/null || exit 1"
      else
        health_cmd="curl -sS --connect-timeout $(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_CONNECT_TIMEOUT 3) -o /dev/null $(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PROTOCOL http)://$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_HOST 127.0.0.1):${port}/ || exit 1"
      fi
    fi
    app_cpus="$(resource_value "$service" APP_CPUS "$(read_env_value "$ENV_DIR/compose.env" APP_CPUS "")")"
    app_memory_limit="$(resource_value "$service" APP_MEMORY_LIMIT "$(read_env_value "$ENV_DIR/compose.env" APP_MEMORY_LIMIT "")")"
    deployment_name="$(alpha_service_name "$service")"
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
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","TZ":"%s"},\n' "$(json_escape "$tz_value")" >> "$spec"
    else
      printf '      "envFiles": ["%s","%s","%s"],\n' "$(json_escape "$ENV_DIR/java-common.env")" "$(json_escape "$ENV_DIR/java-secrets.env")" "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/runtime/env","readOnly":true}],\n' "$(json_escape "$ENV_DIR")" >> "$spec"
      printf '      "entrypoint": ["/bin/sh"],\n' >> "$spec"
      printf '      "command": ["/opt/aifar/runtime/env/java-entrypoint.sh"],\n' >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_SERVICE_NAME":"%s","TZ":"%s"},\n' "$service" "$(json_escape "$tz_value")" >> "$spec"
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
  for service in $(service_order); do
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
  spec="$INSTALL_ROOT/runtime/agent/runtime-spec.json"
  check_agent_dependency
  write_desired_replicas_env
  write_runtime_spec >/dev/null
  [ -f "$spec" ] || fail "AIFAR runtime spec is missing: $spec"
  aifar-agent reconcile-runtime --spec "$spec"
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
check_agent_dependency
[ -f "$INSTALL_ROOT/.aifar/model.json" ] || fail "AIFAR agent-runtime-v2 model manifest is missing"
grep -q '"model"[[:space:]]*:[[:space:]]*"agent-runtime-v2"' "$INSTALL_ROOT/.aifar/model.json" || fail "AIFAR_RUNTIME_REINSTALL_REQUIRED: reinstall AIFAR with agent-runtime-v2"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
[ -f "$ENV_DIR/$SERVICE_NAME.env" ] || fail "AIFAR service env is missing: $SERVICE_NAME"

count="$(service_pod_count)"
case "$count" in ""|*[!0-9]*) count=0 ;; esac
if [ "$count" -ge "$MAX_REPLICAS" ]; then
  fail "AIFAR service $SERVICE_NAME already reached max replicas: $MAX_REPLICAS"
fi

compose_env="$ENV_DIR/compose.env"
service_env="$ENV_DIR/$SERVICE_NAME.env"
image="$(read_env_value "$service_env" APP_IMAGE "")"
[ -n "$image" ] || fail "AIFAR service image is empty: $SERVICE_NAME"
ensure_runtime_config_files
app_memory_limit="$(resource_value "$SERVICE_NAME" APP_MEMORY_LIMIT "$(read_env_value "$compose_env" APP_MEMORY_LIMIT "")")"
required_bytes="$(memory_to_bytes "$app_memory_limit")"
if [ "$required_bytes" -le 0 ]; then
  fail "AIFAR autoscale requires a memory limit for $SERVICE_NAME"
fi
available_bytes="$(awk '/MemAvailable/ {print $2 * 1024}' /proc/meminfo 2>/dev/null | cut -d. -f1)"
[ -n "$available_bytes" ] || available_bytes=0
reserve_bytes=$((required_bytes / 5))
if [ "$available_bytes" -lt $((required_bytes + reserve_bytes)) ]; then
  fail "host memory is insufficient for a new $SERVICE_NAME replica"
fi

java_start_command > "$ENV_DIR/java-entrypoint.sh"
chmod 0755 "$ENV_DIR/java-entrypoint.sh"
if ! reconcile_runtime; then
  fail "AIFAR runtime reconcile failed for autoscaled endpoint"
fi

echo "AIFAR service $SERVICE_NAME scaled out with Pod $CONTAINER_NAME"
