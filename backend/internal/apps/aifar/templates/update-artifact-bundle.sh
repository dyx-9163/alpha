#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
WORK_DIR={{ quote .WorkDir }}
SERVICE_ORDER={{ quote .ServiceOrder }}
CHANGED_SERVICES={{ quote .ChangedServices }}
DESIRED_REPLICAS={{ quote .DesiredReplicas }}
VERSION={{ quote .Version }}
REVISION={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
INGRESS_CONTAINER={{ quote .IngressContainer }}
DEPLOYMENT_CONCURRENCY={{ .Concurrency }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
APP_DIR="$RUNTIME_DIR/docker-apps"
ENV_DIR="$RUNTIME_DIR/env"
PROXY_DIR="$RUNTIME_DIR/service-proxies"
TMP_DIR="$INSTALL_ROOT/.rollout-bundle-$REVISION-$$"
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

service_changed() {
  wanted="$1"
  for service in $CHANGED_SERVICES; do
    [ "$service" = "$wanted" ] && return 0
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

artifact_remote_for_service() {
  case "$1" in
{{- range .Artifacts }}
    {{ .ServiceName }}) printf "%s" {{ quote .ArtifactRemote }} ;;
{{- end }}
    *) return 1 ;;
  esac
}

artifact_file_for_service() {
  case "$1" in
{{- range .Artifacts }}
    {{ .ServiceName }}) printf "%s" {{ quote .ArtifactFile }} ;;
{{- end }}
    *) return 1 ;;
  esac
}

artifact_sha_for_service() {
  case "$1" in
{{- range .Artifacts }}
    {{ .ServiceName }}) printf "%s" {{ quote .ArtifactSHA256 }} ;;
{{- end }}
    *) return 1 ;;
  esac
}

artifact_size_for_service() {
  case "$1" in
{{- range .Artifacts }}
    {{ .ServiceName }}) printf "%s" "{{ .ArtifactSize }}" ;;
{{- end }}
    *) return 1 ;;
  esac
}

desired_replicas_for_service() {
  wanted="$1"
  value=""
  for pair in $DESIRED_REPLICAS; do
    case "$pair" in
      "$wanted="*) value="${pair#*=}" ;;
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
  service="$1"
  artifact_remote="$(artifact_remote_for_service "$service")"
  artifact_file="$(artifact_file_for_service "$service")"
  artifact_sha="$(artifact_sha_for_service "$service")"
  service_dir="$APP_DIR/$service"
  [ -d "$service_dir" ] || fail "service directory is missing: $service"
  [ -f "$artifact_remote" ] || fail "artifact file is missing: $artifact_remote"
  actual="$(sha256sum "$artifact_remote" | awk '{print $1}')"
  [ "$actual" = "$artifact_sha" ] || fail "artifact checksum mismatch for $artifact_file"
  case "$service" in
    web-vue3)
      rm -rf "$service_dir/dist" "$service_dir/html" "$TMP_DIR/$service"
      mkdir -p "$TMP_DIR/$service"
      case "$artifact_file" in
        *.zip) unzip -q "$artifact_remote" -d "$TMP_DIR/$service" ;;
        *.tar|*.tgz|*.tar.gz) tar -xf "$artifact_remote" -C "$TMP_DIR/$service" ;;
        *) fail "unsupported web artifact type: $artifact_file" ;;
      esac
      if [ -d "$TMP_DIR/$service/dist" ]; then
        cp -a "$TMP_DIR/$service/dist" "$service_dir/dist"
      else
        mkdir -p "$service_dir/dist"
        cp -a "$TMP_DIR/$service/." "$service_dir/dist/"
      fi
      ;;
    *)
      cp "$artifact_remote" "$service_dir/app.jar"
      ;;
  esac
}

ensure_service_runtime_env() {
  service="$1"
  service_env="$ENV_DIR/$service.env"
  [ -f "$service_env" ] || fail "service env is missing: $service"
  image="$(read_env_value "$service_env" APP_IMAGE "")"
  [ -n "$image" ] || image="aifar-$service:$REVISION"
  new_image="$(retag_image "$image")"
  set_env APP_IMAGE "$new_image" "$service_env"
  set_env APP_CONTAINER_NAME "$(pod_name "$service" "$REVISION" 1)" "$service_env"
  set_env AIFAR_REVISION "$REVISION" "$service_env"
  if [ "$service" != "web-vue3" ]; then
    port="$(service_port "$service")"
    [ -n "$port" ] || fail "service port is missing: $service"
    set_env SERVER_PORT "$port" "$service_env"
    set_env SPRING_CLOUD_NACOS_DISCOVERY_REGISTER_ENABLED false "$service_env"
  fi
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
  service="$1"
  replica="$2"
  container="$(pod_name "$service" "$REVISION" "$replica")"
  service_env="$ENV_DIR/$service.env"
  compose_env="$ENV_DIR/compose.env"
  image="$(read_env_value "$service_env" APP_IMAGE "")"
  port="$(service_port "$service")"
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
  if [ "$service" = "web-vue3" ]; then
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
    --label "aifar.service=$service" \
    --label "aifar.revision=$REVISION" \
    --label "aifar.release=$REVISION" \
    --label "aifar.pod=$(pod_name "$service" "$REVISION" "$replica")" \
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
  command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required; install or upgrade Docker runtime first"
  [ -f "$spec" ] || fail "AIFAR runtime spec is missing: $spec"
  aifar-agent reconcile-ingress --spec "$spec"
}

stop_old_pods() {
  service="$1"
  old="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$service" --format '{{ "{{" }}.Names{{ "}}" }}|{{ "{{" }}.Label "aifar.revision"{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$old" ] && return 0
  sleep "$DRAIN_SECONDS"
  printf "%s\n" "$old" | while IFS='|' read -r name revision; do
    [ -n "$name" ] || continue
    [ "$revision" != "$REVISION" ] || continue
    docker rm -f "$name" >/dev/null 2>&1 || true
  done
}

cleanup_failed_service() {
  service="$1"
  pods="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$service" --filter "label=aifar.revision=$REVISION" --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$pods" ] || docker rm -f $pods >/dev/null 2>&1 || true
}

rollout_service() {
  service="$1"
  echo "AIFAR Deployment rollout started: $service -> $REVISION"
  apply_artifact "$service"
  ensure_service_runtime_env "$service"
  image="$(read_env_value "$ENV_DIR/$service.env" APP_IMAGE "")"
  [ -n "$image" ] || fail "service image is empty: $service"
  docker build -t "$image" "$APP_DIR/$service"
  desired="$(desired_replicas_for_service "$service")"
  replica=1
  while [ "$replica" -le "$desired" ]; do
    if ! start_pod "$service" "$replica"; then
      cleanup_failed_service "$service"
      fail "new AIFAR Pod did not become ready for $service replica $replica"
    fi
    replica=$((replica + 1))
  done
  if ! reconcile_runtime; then
    cleanup_failed_service "$service"
    fail "AIFAR runtime reconcile failed: $service"
  fi
  stop_old_pods "$service"
  echo "AIFAR Deployment rollout completed: $service -> $REVISION"
}

run_parallel_group() {
  services="$1"
  max="$DEPLOYMENT_CONCURRENCY"
  case "$max" in ""|*[!0-9]*) max=1 ;; esac
  [ "$max" -ge 1 ] || max=1
  running=0
  pids=""
  for service in $services; do
    (
      rollout_service "$service"
    ) &
    pids="$pids $!"
    running=$((running + 1))
    if [ "$running" -ge "$max" ]; then
      for pid in $pids; do
        wait "$pid"
      done
      pids=""
      running=0
    fi
  done
  for pid in $pids; do
    wait "$pid"
  done
}

write_model_manifest() {
  mkdir -p "$INSTALL_ROOT/.aifar"
  cat > "$INSTALL_ROOT/.aifar/last-rollout.json" <<JSON
{
  "model": "${ORCHESTRATION_MODEL}",
  "kind": "rollout-bundle",
  "services": "$(printf "%s" "$CHANGED_SERVICES")",
  "revision": "${REVISION}",
  "version": "${VERSION}",
  "createdAt": "${CREATED_AT}",
  "configHash": "${CONFIG_HASH}",
  "deploymentConcurrency": ${DEPLOYMENT_CONCURRENCY}
}
JSON
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
[ -d "$APP_DIR" ] || fail "AIFAR runtime app directory is missing"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
[ -f "$INSTALL_ROOT/.aifar/model.json" ] || fail "AIFAR k8s-like model manifest is missing"
grep -q '"model"[[:space:]]*:[[:space:]]*"k8s-like-v1"' "$INSTALL_ROOT/.aifar/model.json" || fail "AIFAR instance is legacy; reinstall with k8s-like orchestration"

mkdir -p "$TMP_DIR"
non_entry=""
for service in $SERVICE_ORDER; do
  service_changed "$service" || continue
  case "$service" in
    gateway|web-vue3) ;;
    *) non_entry="$non_entry $service" ;;
  esac
done
[ -z "$non_entry" ] || run_parallel_group "$non_entry"
service_changed gateway && rollout_service gateway
service_changed web-vue3 && rollout_service web-vue3
write_model_manifest
rm -rf "$TMP_DIR"
echo "AIFAR Deployment bundle rollout completed: $CHANGED_SERVICES -> $REVISION"
