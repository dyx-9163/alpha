#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_ORDER={{ quote .ServiceOrder }}
SERVICE_NAME={{ quote .ServiceName }}
REPLICAS={{ .Replicas }}
INGRESS_NETWORK={{ quote .IngressNetwork }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
ENV_DIR="$RUNTIME_DIR/env"
LOG_DIR="$RUNTIME_DIR/logs"

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

current_replicas_for_service() {
  docker ps -a --filter "label=aifar.app=aifar" \
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
  if [ "$service" = "$SERVICE_NAME" ]; then
    printf "%s" "$REPLICAS"
    return
  fi
  if value="$(desired_replicas_from_env "$service")"; then
    case "$value" in ""|*[!0-9]*) value=1 ;; esac
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

nacos_ephemeral() {
  value="$(read_env_value "$ENV_DIR/compose.env" AIFAR_NACOS_EPHEMERAL true)"
  case "$(printf "%s" "$value" | tr '[:upper:]' '[:lower:]')" in
    false|0|no|off) printf "false" ;;
    *) printf "true" ;;
  esac
}

health_cmd_for_service() {
  service="$1"
  port="$(service_port "$service")"
  protocol="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PROTOCOL http)"
  host="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_HOST 127.0.0.1)"
  path="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PATH "")"
  timeout="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_CONNECT_TIMEOUT 3)"
  if [ "$service" = "web-vue3" ]; then
    [ -n "$path" ] || path="/"
    printf "wget -q -T %s -O /dev/null %s://%s:%s%s || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  elif [ -n "$path" ]; then
    printf "curl -fsS --connect-timeout %s %s://%s:%s%s >/dev/null || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  else
    printf "curl -sS --connect-timeout %s -o /dev/null %s://%s:%s/ || exit 1" "$timeout" "$protocol" "$host" "$port"
  fi
}

write_runtime_spec() {
  spec="$INSTALL_ROOT/runtime/agent/runtime-spec.json"
  gateway_port="$(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)"
  web_port="$(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)"
  nacos_ns="$(read_env_value "$ENV_DIR/java-common.env" NACOS_NS prod)"
  tz_value="$(read_env_value "$ENV_DIR/compose.env" TZ system)"
  [ -n "$INGRESS_NETWORK" ] || INGRESS_NETWORK="$(read_env_value "$ENV_DIR/compose.env" AIFAR_NETWORK aifar-network)"
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
  for service in $SERVICE_ORDER; do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:latest")"
    revision="$(read_env_value "$service_env" AIFAR_REVISION "$(read_env_value "$ENV_DIR/compose.env" AIFAR_REVISION current)")"
    replicas="$(desired_replicas_for_service "$service")"
    case "$replicas" in ""|*[!0-9]*) replicas=1 ;; esac
    [ "$replicas" -ge 0 ] || replicas=0
    port="$(service_port "$service")"
    health_cmd="$(health_cmd_for_service "$service")"
    cpus="$(resource_value "$service" APP_CPUS "$(read_env_value "$ENV_DIR/compose.env" APP_CPUS "")")"
    memory="$(resource_value "$service" APP_MEMORY_LIMIT "$(read_env_value "$ENV_DIR/compose.env" APP_MEMORY_LIMIT "")")"
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
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/logs","readOnly":false}],\n' "$(json_escape "$log_dir")" >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_LOG_DIR":"/opt/aifar/logs","LOG_DIR":"/opt/aifar/logs","TZ":"%s"},\n' "$(json_escape "$tz_value")" >> "$spec"
    else
      printf '      "envFiles": ["%s","%s","%s"],\n' "$(json_escape "$ENV_DIR/java-common.env")" "$(json_escape "$ENV_DIR/java-secrets.env")" "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/runtime/env","readOnly":true},{"source":"%s","target":"/opt/aifar/logs","readOnly":false}],\n' "$(json_escape "$ENV_DIR")" "$(json_escape "$log_dir")" >> "$spec"
      printf '      "entrypoint": ["/bin/sh"],\n' >> "$spec"
      printf '      "command": ["/opt/aifar/runtime/env/java-entrypoint.sh"],\n' >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_SERVICE_NAME":"%s","AIFAR_LOG_DIR":"/opt/aifar/logs","LOG_DIR":"/opt/aifar/logs","TZ":"%s"},\n' "$service" "$(json_escape "$tz_value")" >> "$spec"
    fi
    printf '      "resources": {"cpus":"%s","memory":"%s"},\n' "$(json_escape "$cpus")" "$(json_escape "$memory")" >> "$spec"
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

case "$REPLICAS" in ""|*[!0-9]*) fail "replicas must be a non-negative integer" ;; esac
[ "$REPLICAS" -ge 0 ] || fail "replicas must be a non-negative integer"
command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
[ -f "$ENV_DIR/$SERVICE_NAME.env" ] || fail "AIFAR service env is missing: $SERVICE_NAME"

write_desired_replicas_env
spec="$(write_runtime_spec)"
aifar-agent reconcile-runtime --spec "$spec"
echo "AIFAR service $SERVICE_NAME desired replicas set to $REPLICAS"
