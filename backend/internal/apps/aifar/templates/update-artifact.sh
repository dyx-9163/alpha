#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
WORK_DIR={{ quote .WorkDir }}
SERVICE_ORDER={{ quote .ServiceOrder }}
SERVICE_NAME={{ quote .ServiceName }}
DESIRED_REPLICAS={{ quote .DesiredReplicas }}
ARTIFACT_REMOTE={{ quote .ArtifactRemote }}
ARTIFACT_FILE={{ quote .ArtifactFileName }}
ARTIFACT_SHA256={{ quote .ArtifactSHA256 }}
ARTIFACT_SIZE={{ .ArtifactSize }}
VERSION={{ quote .Version }}
REVISION={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
INGRESS_NETWORK={{ quote .IngressNetwork }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
APP_DIR="$RUNTIME_DIR/docker-apps"
ENV_DIR="$RUNTIME_DIR/env"
TMP_DIR="$INSTALL_ROOT/.rollout-$REVISION-$$"
DRAIN_SECONDS="${DRAIN_SECONDS:-30}"
ORCHESTRATION_MODEL="k8s-like-v1"

fail() {
  echo "ERROR: $*" >&2
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

set_env() {
  key="$1"
  value="$2"
  file="$3"
  tmp="$file.tmp"
  [ -f "$file" ] || touch "$file"
  grep -v "^${key}=" "$file" > "$tmp" || true
  printf "%s=%s\n" "$key" "$value" >> "$tmp"
  mv "$tmp" "$file"
}

check_agent_dependency() {
  command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required; install or upgrade Docker runtime first"
  aifar-agent status >/dev/null 2>&1 || fail "aifar-agent service is not reachable; install or upgrade Docker runtime first"
}

strip_web_nginx_runtime_routes() {
  service_dir="$1"
  nginx_conf="$service_dir/nginx/default.conf"
  [ -f "$nginx_conf" ] || return 0
  tmp="$nginx_conf.tmp"
  awk '
    /^[[:space:]]*location[[:space:]]+\/api\/[[:space:]]*\{/ || /^[[:space:]]*location[[:space:]]+\/im\/ws\/[[:space:]]*\{/ {
      skip = 1
      depth = 0
    }
    skip {
      line = $0
      opens = gsub(/\{/, "{", line)
      line = $0
      closes = gsub(/\}/, "}", line)
      depth += opens - closes
      if (depth <= 0) {
        skip = 0
      }
      next
    }
    { print }
  ' "$nginx_conf" > "$tmp"
  mv "$tmp" "$nginx_conf"
}

service_known() {
  for service in $SERVICE_ORDER; do
    [ "$service" = "$SERVICE_NAME" ] && return 0
  done
  return 1
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

desired_replicas_for_service() {
  value=""
  for pair in $DESIRED_REPLICAS; do
    case "$pair" in
      "$SERVICE_NAME="*) value="${pair#*=}" ;;
    esac
  done
  case "$value" in ""|*[!0-9]*) value=1 ;; esac
  [ "$value" -ge 1 ] || value=1
  printf "%s" "$value"
}

pod_name() {
  service="$1"
  revision="$2"
  replica="$3"
  printf "aifar-pod-admin-%s-%s-r%s" "$service" "$revision" "$replica" | tr '. _/' '----'
}

retag_image() {
  image="$1"
  case "$image" in
    *@*) printf "%s" "$image" ;;
    *:*) printf "%s:%s" "${image%:*}" "$REVISION" ;;
    *) printf "%s:%s" "$image" "$REVISION" ;;
  esac
}

apply_artifact() {
  service_dir="$APP_DIR/$SERVICE_NAME"
  [ -d "$service_dir" ] || fail "service directory is missing: $SERVICE_NAME"
  [ -f "$ARTIFACT_REMOTE" ] || fail "artifact file is missing: $ARTIFACT_REMOTE"
  actual="$(sha256sum "$ARTIFACT_REMOTE" | awk '{print $1}')"
  [ "$actual" = "$ARTIFACT_SHA256" ] || fail "artifact checksum mismatch for $ARTIFACT_FILE"
  case "$SERVICE_NAME" in
    web-vue3)
      rm -rf "$service_dir/dist" "$service_dir/html" "$TMP_DIR"
      mkdir -p "$TMP_DIR/web"
      case "$ARTIFACT_FILE" in
        *.zip) unzip -q "$ARTIFACT_REMOTE" -d "$TMP_DIR/web" ;;
        *.tar|*.tgz|*.tar.gz) tar -xf "$ARTIFACT_REMOTE" -C "$TMP_DIR/web" ;;
        *) fail "unsupported web artifact type: $ARTIFACT_FILE" ;;
      esac
      if [ -d "$TMP_DIR/web/dist" ]; then
        cp -a "$TMP_DIR/web/dist" "$service_dir/dist"
      else
        mkdir -p "$service_dir/dist"
        cp -a "$TMP_DIR/web/." "$service_dir/dist/"
      fi
      strip_web_nginx_runtime_routes "$service_dir"
      ;;
    *)
      mkdir -p "$service_dir"
      cp "$ARTIFACT_REMOTE" "$service_dir/app.jar"
      ;;
  esac
}

ensure_service_runtime_env() {
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  [ -f "$service_env" ] || fail "service env is missing: $SERVICE_NAME"
  image="$(read_env_value "$service_env" APP_IMAGE "")"
  [ -n "$image" ] || image="aifar-$SERVICE_NAME:$REVISION"
  new_image="$(retag_image "$image")"
  set_env APP_IMAGE "$new_image" "$service_env"
  set_env APP_CONTAINER_NAME "$(pod_name "$SERVICE_NAME" "$REVISION" 1)" "$service_env"
  set_env AIFAR_REVISION "$REVISION" "$service_env"
  if [ "$SERVICE_NAME" != "web-vue3" ]; then
    port="$(service_port "$SERVICE_NAME")"
    [ -n "$port" ] || fail "service port is missing: $SERVICE_NAME"
    set_env SERVER_PORT "$port" "$service_env"
    set_env SPRING_CLOUD_NACOS_DISCOVERY_REGISTER_ENABLED false "$service_env"
  fi
}

build_image() {
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  image="$(read_env_value "$service_env" APP_IMAGE "")"
  [ -n "$image" ] || fail "service image is empty: $SERVICE_NAME"
  docker build -t "$image" "$APP_DIR/$SERVICE_NAME"
}

container_status() {
  docker inspect --format '{{ "{{" }}.State.Status{{ "}}" }}' "$1" 2>/dev/null || true
}

container_health() {
  docker inspect --format '{{ "{{" }}if .State.Health{{ "}}" }}{{ "{{" }}.State.Health.Status{{ "}}" }}{{ "{{" }}end{{ "}}" }}' "$1" 2>/dev/null || true
}

wait_pod_ready() {
  container="$1"
  timeout="$(read_env_value "$ENV_DIR/compose.env" APP_STARTUP_TIMEOUT 300)"
  case "$timeout" in ""|*[!0-9]*) timeout=300 ;; esac
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    status="$(container_status "$container")"
    health="$(container_health "$container")"
    if [ "$status" = "running" ] && { [ -z "$health" ] || [ "$health" = "healthy" ]; }; then
      echo "Pod ready: $container"
      return 0
    fi
    sleep 5
  done
  docker logs --tail 120 "$container" >&2 || true
  return 1
}

start_pod() {
  replica="$1"
  container="$(pod_name "$SERVICE_NAME" "$REVISION" "$replica")"
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  compose_env="$ENV_DIR/compose.env"
  image="$(read_env_value "$service_env" APP_IMAGE "")"
  port="$(service_port "$SERVICE_NAME")"
  app_memory_limit="$(read_env_value "$compose_env" APP_MEMORY_LIMIT "")"
  app_cpus="$(read_env_value "$compose_env" APP_CPUS "")"
  restart_policy="$(read_env_value "$compose_env" APP_RESTART_POLICY unless-stopped)"
  health_protocol="$(read_env_value "$compose_env" APP_HEALTH_PROTOCOL http)"
  health_host="$(read_env_value "$compose_env" APP_HEALTH_HOST 127.0.0.1)"
  health_path="$(read_env_value "$compose_env" APP_HEALTH_PATH "")"
  health_connect_timeout="$(read_env_value "$compose_env" APP_HEALTH_CONNECT_TIMEOUT 3)"
  health_interval="$(read_env_value "$compose_env" APP_HEALTH_INTERVAL 15s)"
  health_timeout="$(read_env_value "$compose_env" APP_HEALTH_TIMEOUT 5s)"
  health_retries="$(read_env_value "$compose_env" APP_HEALTH_RETRIES 3)"
  health_start_period="$(read_env_value "$compose_env" APP_HEALTH_START_PERIOD 30s)"
  tz_value="$(read_env_value "$compose_env" TZ system)"
  resource_args=""
  [ -z "$app_cpus" ] || resource_args="$resource_args --cpus $app_cpus"
  [ -z "$app_memory_limit" ] || resource_args="$resource_args --memory $app_memory_limit"

  docker rm -f "$container" >/dev/null 2>&1 || true
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
    --name "$container" \
    --restart no \
    --label aifar.app=aifar \
    --label "aifar.install-root=$INSTALL_ROOT" \
    --label "aifar.component=pod" \
    --label "aifar.service=$SERVICE_NAME" \
    --label "aifar.revision=$REVISION" \
    --label "aifar.release=$REVISION" \
    --label "aifar.pod=$(pod_name "$SERVICE_NAME" "$REVISION" "$replica")" \
    --label "aifar.replica=$replica" \
    --network "$INGRESS_NETWORK" \
    $resource_args \
    --health-cmd "$health_cmd" \
    --health-interval "$health_interval" \
    --health-timeout "$health_timeout" \
    --health-retries "$health_retries" \
    --health-start-period "$health_start_period" \
    -e "APP_CONTAINER_NAME=$container" \
    -e "TZ=$tz_value" \
    $env_args \
    "$image" >/dev/null
  wait_pod_ready "$container" || return 1
  docker update --restart "$restart_policy" "$container" >/dev/null 2>&1 || true
}

reconcile_runtime() {
  spec="$INSTALL_ROOT/runtime/ingress/runtime-spec.json"
  check_agent_dependency
  [ -f "$spec" ] || fail "AIFAR runtime spec is missing: $spec"
  aifar-agent reconcile-ingress --spec "$spec"
}

stop_old_pods() {
  old="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$SERVICE_NAME" --format '{{ "{{" }}.Names{{ "}}" }}|{{ "{{" }}.Label "aifar.revision"{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$old" ] && return 0
  sleep "$DRAIN_SECONDS"
  printf "%s\n" "$old" | while IFS='|' read -r name revision; do
    [ -n "$name" ] || continue
    [ "$revision" != "$REVISION" ] || continue
    docker rm -f "$name" >/dev/null 2>&1 || true
  done
}

write_model_manifest() {
  mkdir -p "$INSTALL_ROOT/.aifar"
  cat > "$INSTALL_ROOT/.aifar/last-rollout.json" <<JSON
{
  "model": "${ORCHESTRATION_MODEL}",
  "kind": "rollout",
  "service": "${SERVICE_NAME}",
  "revision": "${REVISION}",
  "version": "${VERSION}",
  "createdAt": "${CREATED_AT}",
  "configHash": "${CONFIG_HASH}",
  "artifact": {
    "file": "${ARTIFACT_FILE}",
    "sha256": "${ARTIFACT_SHA256}",
    "size": ${ARTIFACT_SIZE}
  }
}
JSON
}

cleanup_failed_rollout() {
  pods="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$SERVICE_NAME" --filter "label=aifar.revision=$REVISION" --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$pods" ] || docker rm -f $pods >/dev/null 2>&1 || true
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
check_agent_dependency
service_known || fail "unsupported AIFAR service: $SERVICE_NAME"
[ -d "$APP_DIR" ] || fail "AIFAR runtime app directory is missing"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
[ -f "$INSTALL_ROOT/.aifar/model.json" ] || fail "AIFAR k8s-like model manifest is missing"
grep -q '"model"[[:space:]]*:[[:space:]]*"k8s-like-v1"' "$INSTALL_ROOT/.aifar/model.json" || fail "AIFAR instance is legacy; reinstall with k8s-like orchestration"

trap 'cleanup_failed_rollout' INT TERM
apply_artifact
ensure_service_runtime_env
build_image
desired="$(desired_replicas_for_service)"
replica=1
while [ "$replica" -le "$desired" ]; do
  if ! start_pod "$replica"; then
    cleanup_failed_rollout
    fail "new AIFAR Pod did not become ready for $SERVICE_NAME replica $replica"
  fi
  replica=$((replica + 1))
done
if ! reconcile_runtime; then
  cleanup_failed_rollout
  fail "AIFAR runtime reconcile failed: $SERVICE_NAME"
fi
stop_old_pods
write_model_manifest
echo "AIFAR Deployment rollout completed: $SERVICE_NAME -> $REVISION"
