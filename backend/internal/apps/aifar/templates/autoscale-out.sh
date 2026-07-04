#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_NAME={{ quote .ServiceName }}
REVISION={{ quote .ReleaseID }}
REPLICA_ID={{ .ReplicaID }}
CONTAINER_NAME={{ quote .ContainerName }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
MAX_REPLICAS={{ .MaxReplicas }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
ENV_DIR="$RUNTIME_DIR/env"
PROXY_DIR="$RUNTIME_DIR/service-proxies"

fail() {
  echo "$*" >&2
  exit 1
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

reconcile_runtime() {
  spec="$INSTALL_ROOT/runtime/ingress/runtime-spec.json"
  command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required; install or upgrade Docker runtime first"
  [ -f "$spec" ] || fail "AIFAR runtime spec is missing: $spec"
  aifar-agent reconcile-ingress --spec "$spec"
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
[ -f "$INSTALL_ROOT/.aifar/model.json" ] || fail "AIFAR k8s-like model manifest is missing"
grep -q '"model"[[:space:]]*:[[:space:]]*"k8s-like-v1"' "$INSTALL_ROOT/.aifar/model.json" || fail "AIFAR instance is legacy; reinstall with k8s-like orchestration"
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
app_memory_limit="$(read_env_value "$compose_env" APP_MEMORY_LIMIT "")"
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

port="$(service_port "$SERVICE_NAME")"
health_protocol="$(read_env_value "$compose_env" APP_HEALTH_PROTOCOL http)"
health_host="$(read_env_value "$compose_env" APP_HEALTH_HOST 127.0.0.1)"
health_path="$(read_env_value "$compose_env" APP_HEALTH_PATH "")"
health_connect_timeout="$(read_env_value "$compose_env" APP_HEALTH_CONNECT_TIMEOUT 3)"
health_interval="$(read_env_value "$compose_env" APP_HEALTH_INTERVAL 15s)"
health_timeout="$(read_env_value "$compose_env" APP_HEALTH_TIMEOUT 5s)"
health_retries="$(read_env_value "$compose_env" APP_HEALTH_RETRIES 3)"
health_start_period="$(read_env_value "$compose_env" APP_HEALTH_START_PERIOD 30s)"
app_cpus="$(read_env_value "$compose_env" APP_CPUS "")"
restart_policy="$(read_env_value "$compose_env" APP_RESTART_POLICY unless-stopped)"
tz_value="$(read_env_value "$compose_env" TZ system)"
resource_args=""
[ -z "$app_cpus" ] || resource_args="$resource_args --cpus $app_cpus"
[ -z "$app_memory_limit" ] || resource_args="$resource_args --memory $app_memory_limit"

docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
if [ "$SERVICE_NAME" = "web-vue3" ]; then
  [ -n "$health_path" ] || health_path="/"
  env_args="--env-file $service_env"
  health_cmd="wget -q -T $health_connect_timeout -O /dev/null ${health_protocol}://${health_host}:${port}${health_path} || exit 1"
elif [ -n "$health_path" ]; then
  env_args="--env-file $ENV_DIR/java-common.env --env-file $ENV_DIR/java-secrets.env --env-file $service_env"
  health_cmd="curl -fsS --connect-timeout $health_connect_timeout ${health_protocol}://${health_host}:${port}${health_path} >/dev/null || exit 1"
else
  env_args="--env-file $ENV_DIR/java-common.env --env-file $ENV_DIR/java-secrets.env --env-file $service_env"
  health_cmd="curl -sS --connect-timeout $health_connect_timeout -o /dev/null ${health_protocol}://${health_host}:${port}/ || exit 1"
fi

docker run -d \
  --name "$CONTAINER_NAME" \
  --restart no \
  --label aifar.app=aifar \
  --label "aifar.install-root=$INSTALL_ROOT" \
  --label "aifar.component=pod" \
  --label "aifar.service=$SERVICE_NAME" \
  --label "aifar.revision=$REVISION" \
  --label "aifar.release=$REVISION" \
  --label "aifar.pod=$CONTAINER_NAME" \
  --label "aifar.replica=$REPLICA_ID" \
  --label aifar.autoscaled=true \
  --network "$INGRESS_NETWORK" \
  $resource_args \
  --health-cmd "$health_cmd" \
  --health-interval "$health_interval" \
  --health-timeout "$health_timeout" \
  --health-retries "$health_retries" \
  --health-start-period "$health_start_period" \
  -e "APP_CONTAINER_NAME=$CONTAINER_NAME" \
  -e "TZ=$tz_value" \
  $env_args \
  "$image" >/dev/null

if ! wait_container_ready "$CONTAINER_NAME"; then
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fail "new AIFAR service Pod did not become ready: $CONTAINER_NAME"
fi

if ! reconcile_runtime; then
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fail "AIFAR runtime reconcile failed for autoscaled endpoint"
fi

docker update --restart "$restart_policy" "$CONTAINER_NAME" >/dev/null 2>&1 || true
echo "AIFAR service $SERVICE_NAME scaled out with Pod $CONTAINER_NAME"
