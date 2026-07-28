#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_ORDER={{ quote .ServiceOrder }}
SERVICE_NAME={{ quote .ServiceName }}
REPLICAS={{ .Replicas }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
TASK_ID={{ quote .TaskID }}
CONTROL_PLANE_DESIRED_REPLICAS={{ quote .DesiredReplicas }}
TARGET_DESIRED_REPLICAS={{ quote .TargetDesiredReplicas }}
[ -n "$TARGET_DESIRED_REPLICAS" ] || TARGET_DESIRED_REPLICAS="$SERVICE_NAME=$REPLICAS"

RUNTIME_DIR="$INSTALL_ROOT/runtime"
ENV_DIR="$RUNTIME_DIR/env"
LOG_DIR="$RUNTIME_DIR/logs"
AGENT_DIR="$RUNTIME_DIR/agent"
TASK_TOKEN="$(printf "%s" "${TASK_ID:-manual}" | tr -c 'A-Za-z0-9._-' '_')"
CANONICAL_ENV="$ENV_DIR/compose.env"
CANONICAL_SPEC="$AGENT_DIR/runtime-spec.json"
STAGED_ENV="$CANONICAL_ENV.$TASK_TOKEN.staged"
STAGED_SPEC="$CANONICAL_SPEC.$TASK_TOKEN.staged"
ROLLBACK_ENV="$CANONICAL_ENV.$TASK_TOKEN.rollback"
ROLLBACK_SPEC="$CANONICAL_SPEC.$TASK_TOKEN.rollback"
COMPOSE_ENV="$STAGED_ENV"
PROMOTING=0
COMMITTED=0
HAD_SPEC=0

fail() {
  echo "ERROR: $*" >&2
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

alpha_service_name() {
  configured="$(read_env_value "$ENV_DIR/$1.env" SPRING_APPLICATION_NAME "")"
  [ -z "$configured" ] || { printf "%s" "$configured"; return; }
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
  service="$1"
  value="$(read_env_value "$ENV_DIR/$service.env" AIFAR_SERVICE_PORT "")"
  [ -n "$value" ] || value="$(read_env_value "$ENV_DIR/$service.env" SERVER_PORT "")"
  [ -n "$value" ] && { printf "%s" "$value"; return; }
  var="$(service_port_var "$service")"
  [ -n "$var" ] || fail "unsupported service port: $1"
  read_env_value "$COMPOSE_ENV" "$var" ""
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

desired_replicas_from_control_plane() {
  wanted="$1"
  for pair in $CONTROL_PLANE_DESIRED_REPLICAS; do
    case "$pair" in
      "$wanted="*) printf "%s" "${pair#*=}"; return 0 ;;
    esac
  done
  return 1
}

desired_replicas_from_targets() {
  wanted="$1"
  for pair in $TARGET_DESIRED_REPLICAS; do
    case "$pair" in
      "$wanted="*) printf "%s" "${pair#*=}"; return 0 ;;
    esac
  done
  return 1
}

desired_replicas_for_service() {
  service="$1"
  value="$(desired_replicas_from_targets "$service" || true)"
  if [ -n "$value" ]; then
    printf "%s" "$value"
    return
  fi
  value="$(desired_replicas_from_control_plane "$service" || true)"
  case "$value" in ""|*[!0-9]*) value=1 ;; esac
  [ "$value" -ge 0 ] || value=0
  printf "%s" "$value"
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
  set_env AIFAR_DESIRED_REPLICAS "$pairs" "$COMPOSE_ENV"
}

nacos_ephemeral() {
  value="$(read_env_value "$COMPOSE_ENV" AIFAR_NACOS_EPHEMERAL true)"
  case "$(printf "%s" "$value" | tr '[:upper:]' '[:lower:]')" in
    false|0|no|off) printf "false" ;;
    *) printf "true" ;;
  esac
}

health_cmd_for_service() {
  service="$1"
  port="$(service_port "$service")"
  protocol="$(read_env_value "$COMPOSE_ENV" APP_HEALTH_PROTOCOL http)"
  host="$(read_env_value "$COMPOSE_ENV" APP_HEALTH_HOST 127.0.0.1)"
  timeout="$(read_env_value "$COMPOSE_ENV" APP_HEALTH_CONNECT_TIMEOUT 3)"
  if [ "$service" = "web-vue3" ]; then
    path="$(read_env_value "$COMPOSE_ENV" APP_WEB_HEALTH_PATH "/")"
    printf "wget -q -T %s -O /dev/null %s://%s:%s%s || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  else
    path="$(read_env_value "$ENV_DIR/$service.env" HEALTH_PATH "")"
    [ -n "$path" ] || path="$(read_env_value "$COMPOSE_ENV" APP_BACKEND_HEALTH_PATH "/actuator/health/readiness")"
    [ -n "$path" ] || path="/actuator/health/readiness"
    printf "curl -fsS --connect-timeout %s %s://%s:%s%s >/dev/null || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  fi
}

write_runtime_spec() {
  spec="$STAGED_SPEC"
  gateway_port="$(read_env_value "$COMPOSE_ENV" GATEWAY_PORT 38000)"
  web_port="$(read_env_value "$COMPOSE_ENV" WEB_VUE3_PORT 8080)"
  nacos_ns="$(read_env_value "$ENV_DIR/java-common.env" NACOS_NS prod)"
  tz_value="$(read_env_value "$COMPOSE_ENV" TZ system)"
  [ -n "$INGRESS_NETWORK" ] || INGRESS_NETWORK="$(read_env_value "$COMPOSE_ENV" AIFAR_NETWORK aifar-network)"
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
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:latest")"
    revision="$(read_env_value "$service_env" AIFAR_REVISION "$(read_env_value "$COMPOSE_ENV" AIFAR_REVISION current)")"
    replicas="$(desired_replicas_for_service "$service")"
    case "$replicas" in ""|*[!0-9]*) replicas=1 ;; esac
    [ "$replicas" -ge 0 ] || replicas=0
    port="$(service_port "$service")"
    health_cmd="$(health_cmd_for_service "$service")"
    cpus="$(resource_value "$service" APP_CPUS "$(read_env_value "$COMPOSE_ENV" APP_CPUS "")")"
    memory="$(resource_value "$service" APP_MEMORY_LIMIT "$(read_env_value "$COMPOSE_ENV" APP_MEMORY_LIMIT "")")"
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
    printf '      "podRevision": "%s",\n' "$(json_escape "$revision")" >> "$spec"
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
    printf '      "resources": {"cpus":"%s","memory":"%s"},\n' "$(json_escape "$cpus")" "$(json_escape "$memory")" >> "$spec"
    printf '      "healthCheck": {"command":"%s","interval":"%s","timeout":"%s","retries":%s,"startPeriod":"%s"}\n' \
      "$(json_escape "$health_cmd")" \
      "$(json_escape "$(read_env_value "$COMPOSE_ENV" APP_HEALTH_INTERVAL 15s)")" \
      "$(json_escape "$(read_env_value "$COMPOSE_ENV" APP_HEALTH_TIMEOUT 5s)")" \
      "$(read_env_value "$COMPOSE_ENV" APP_HEALTH_RETRIES 3)" \
      "$(json_escape "$(read_env_value "$COMPOSE_ENV" APP_HEALTH_START_PERIOD 30s)")" >> "$spec"
    printf '    }' >> "$spec"
  done
  cat >> "$spec" <<JSON
  ],
  "services": [
JSON
  first_service=1
  for service in $SERVICE_ORDER; do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    port="$(service_port "$service")"
    app_name="$(alpha_service_name "$service")"
    if [ "$first_service" = "1" ]; then
      first_service=0
    else
      printf ",\n" >> "$spec"
    fi
    affinity="$(read_env_value "$service_env" AIFAR_AFFINITY_POLICY round-robin)"
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

case "$REPLICAS" in ""|*[!0-9]*) fail "replicas must be a non-negative integer" ;; esac
[ "$REPLICAS" -ge 0 ] || fail "replicas must be a non-negative integer"
for pair in $TARGET_DESIRED_REPLICAS; do
  target_service="${pair%%=*}"
  target_replicas="${pair#*=}"
  [ -n "$target_service" ] || fail "scale target service is required"
  case "$target_replicas" in ""|*[!0-9]*) fail "replicas must be a non-negative integer" ;; esac
  [ -f "$ENV_DIR/$target_service.env" ] || fail "AIFAR service env is missing: $target_service"
done
command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
[ -f "$ENV_DIR/$SERVICE_NAME.env" ] || fail "AIFAR service env is missing: $SERVICE_NAME"
[ -f "$CANONICAL_ENV" ] || fail "AIFAR runtime compose env is missing"

cleanup() {
  rc=$?
  trap - EXIT HUP INT TERM
  if [ "$PROMOTING" = "1" ] && [ "$COMMITTED" != "1" ]; then
    cp "$ROLLBACK_ENV" "$CANONICAL_ENV" 2>/dev/null || true
    if [ "$HAD_SPEC" = "1" ]; then
      cp "$ROLLBACK_SPEC" "$CANONICAL_SPEC" 2>/dev/null || true
    else
      rm -f "$CANONICAL_SPEC"
    fi
  fi
  rm -f "$STAGED_ENV" "$STAGED_ENV.tmp" "$STAGED_SPEC" "$ROLLBACK_ENV" "$ROLLBACK_SPEC"
  exit "$rc"
}
trap cleanup EXIT HUP INT TERM

cp "$CANONICAL_ENV" "$STAGED_ENV"
cp "$CANONICAL_ENV" "$ROLLBACK_ENV"
if [ -f "$CANONICAL_SPEC" ]; then
  cp "$CANONICAL_SPEC" "$ROLLBACK_SPEC"
  HAD_SPEC=1
fi

write_desired_replicas_env
spec="$(write_runtime_spec)"
aifar-agent reconcile-runtime --spec "$spec"
PROMOTING=1
mv "$STAGED_ENV" "$CANONICAL_ENV"
mv "$STAGED_SPEC" "$CANONICAL_SPEC"
COMMITTED=1
echo "AIFAR services desired replicas set: $TARGET_DESIRED_REPLICAS"
