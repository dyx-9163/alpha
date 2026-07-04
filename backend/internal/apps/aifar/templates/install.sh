#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
WORK_DIR={{ quote .WorkDir }}
ARCHIVE={{ quote .ArchiveRemote }}
SERVICE_ORDER={{ quote .ServiceOrder }}
VERSION={{ quote .Version }}
REVISION={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
INGRESS_NETWORK={{ quote .IngressNetwork }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
APP_DIR="$RUNTIME_DIR/docker-apps"
IMAGE_DIR="$RUNTIME_DIR/docker-images"
ENV_DIR="$RUNTIME_DIR/env"
INGRESS_DIR="$RUNTIME_DIR/ingress"
AIFAR_DIR="$INSTALL_ROOT/.aifar"
TMP_DIR="$INSTALL_ROOT/.extract-$REVISION-$$"

TIMEZONE={{ quote .Options.Timezone }}
APP_CPUS={{ quote .Options.AppCPUs }}
APP_MEMORY_LIMIT={{ quote .Options.AppMemoryLimit }}
GATEWAY_PORT={{ quote .Options.GatewayPort }}
WEB_VUE3_PORT={{ quote .Options.WebPort }}
NACOS_PORT_WEB={{ quote .Options.NacosWebPort }}
NACOS_PORT_API={{ quote .Options.NacosAPIPort }}
NACOS_CONNECT_HOST={{ quote .Options.NacosHost }}
NACOS_USER={{ quote .Options.NacosUser }}
NACOS_PASSWORD={{ quote .Options.NacosPassword }}
NACOS_NS={{ quote .Options.NacosNamespace }}
NACOS_REGISTRATION_MODE="agent-proxy"
ORCHESTRATION_MODEL="k8s-like-v1"

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

sanitize_name() {
  printf "%s" "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9_.-]/-/g; s/^[._-]*//; s/[._-]*$//'
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
  port_var="$(service_port_var "$1")"
  [ -n "$port_var" ] || { printf "0"; return; }
  read_env_value "$ENV_DIR/compose.env" "$port_var" "0"
}

{{ serviceAccessHelpers }}

load_docker_images() {
  [ -d "$IMAGE_DIR" ] || return 0
  for image_tar in "$IMAGE_DIR"/*.tar; do
    [ -f "$image_tar" ] || continue
    docker load -i "$image_tar"
  done
}

require_local_image() {
  image="$1"
  docker image inspect "$image" >/dev/null 2>&1 || fail "required offline Docker image is missing after docker load: $image"
}

tcp_probe() {
  host="$1"
  port="$2"
  if command -v nc >/dev/null 2>&1; then
    nc -z -w 3 "$host" "$port" >/dev/null 2>&1
    return $?
  fi
  if command -v bash >/dev/null 2>&1; then
    if command -v timeout >/dev/null 2>&1; then
      timeout 5 bash -c "</dev/tcp/$host/$port" >/dev/null 2>&1
      return $?
    fi
    bash -c "</dev/tcp/$host/$port" >/dev/null 2>&1
    return $?
  fi
  return 2
}

http_probe() {
  url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --connect-timeout 5 "$url" >/dev/null 2>&1
    return $?
  fi
  if command -v wget >/dev/null 2>&1; then
    wget -q -T 5 -O /dev/null "$url" >/dev/null 2>&1
    return $?
  fi
  return 2
}

check_nacos_dependency() {
  url="http://${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}/nacos/v1/console/health/readiness"
  if http_probe "$url"; then
    echo "Nacos dependency is ready at ${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}"
    return 0
  fi
  if tcp_probe "$NACOS_CONNECT_HOST" "$NACOS_PORT_WEB"; then
    echo "Nacos dependency is reachable at ${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}"
    return 0
  fi
  fail "Nacos dependency readiness check failed at $url"
}

resolve_system_timezone() {
  case "$TIMEZONE" in
    ""|"system"|"SYSTEM"|"System")
      detected=""
      if command -v timedatectl >/dev/null 2>&1; then
        detected="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
      fi
      if [ -z "$detected" ] && [ -f /etc/timezone ]; then
        detected="$(head -n 1 /etc/timezone 2>/dev/null || true)"
      fi
      TIMEZONE="${detected:-UTC}"
      ;;
  esac
}

ensure_network() {
  docker network inspect "$INGRESS_NETWORK" >/dev/null 2>&1 || docker network create "$INGRESS_NETWORK" >/dev/null
}

write_compose_env() {
  source_env="$APP_DIR/.env"
  compose_env="$ENV_DIR/compose.env"
  : > "$compose_env"
  set_env ORCHESTRATION_MODEL "$ORCHESTRATION_MODEL" "$compose_env"
  set_env AIFAR_REVISION "$REVISION" "$compose_env"
  set_env AIFAR_NETWORK "$INGRESS_NETWORK" "$compose_env"
  set_env TZ "$TIMEZONE" "$compose_env"
  set_env APP_CPUS "$APP_CPUS" "$compose_env"
  set_env APP_MEMORY_LIMIT "$APP_MEMORY_LIMIT" "$compose_env"
  set_env APP_RESTART_POLICY "$(read_env_value "$source_env" APP_RESTART_POLICY unless-stopped)" "$compose_env"
  set_env APP_HEALTH_PROTOCOL "$(read_env_value "$source_env" APP_HEALTH_PROTOCOL http)" "$compose_env"
  set_env APP_HEALTH_HOST "$(read_env_value "$source_env" APP_HEALTH_HOST 127.0.0.1)" "$compose_env"
  set_env APP_HEALTH_PATH "$(read_env_value "$source_env" APP_HEALTH_PATH '')" "$compose_env"
  set_env APP_HEALTH_CONNECT_TIMEOUT "$(read_env_value "$source_env" APP_HEALTH_CONNECT_TIMEOUT 3)" "$compose_env"
  set_env APP_HEALTH_INTERVAL "$(read_env_value "$source_env" APP_HEALTH_INTERVAL 15s)" "$compose_env"
  set_env APP_HEALTH_TIMEOUT "$(read_env_value "$source_env" APP_HEALTH_TIMEOUT 5s)" "$compose_env"
  set_env APP_HEALTH_RETRIES "$(read_env_value "$source_env" APP_HEALTH_RETRIES 3)" "$compose_env"
  set_env APP_HEALTH_START_PERIOD "$(read_env_value "$source_env" APP_HEALTH_START_PERIOD 30s)" "$compose_env"
  set_env APP_STARTUP_TIMEOUT "$(read_env_value "$source_env" APP_STARTUP_TIMEOUT 300)" "$compose_env"
  set_env APP_STABLE_WINDOW "$(read_env_value "$source_env" APP_STABLE_WINDOW 10)" "$compose_env"
  set_env GATEWAY_PORT "$GATEWAY_PORT" "$compose_env"
  set_env WEB_VUE3_PORT "$WEB_VUE3_PORT" "$compose_env"
  set_env OAUTH_PORT "$(read_env_value "$source_env" OAUTH_PORT 38001)" "$compose_env"
  set_env PERMISSION_PORT "$(read_env_value "$source_env" PERMISSION_PORT 38010)" "$compose_env"
  set_env SYSTEM_PORT "$(read_env_value "$source_env" SYSTEM_PORT 38002)" "$compose_env"
  set_env FILE_PORT "$(read_env_value "$source_env" FILE_PORT 38005)" "$compose_env"
  set_env MESSAGE_PORT "$(read_env_value "$source_env" MESSAGE_PORT 38008)" "$compose_env"
  set_env IM_PORT "$(read_env_value "$source_env" IM_PORT 38031)" "$compose_env"
  set_env CONTACTS_PORT "$(read_env_value "$source_env" CONTACTS_PORT 38032)" "$compose_env"
  set_env MEETING_PORT "$(read_env_value "$source_env" MEETING_PORT 38033)" "$compose_env"
}

write_java_env() {
  common_env="$ENV_DIR/java-common.env"
  secrets_env="$ENV_DIR/java-secrets.env"
  : > "$common_env"
  : > "$secrets_env"
  set_env TZ "$TIMEZONE" "$common_env"
  set_env NACOS_HOST "${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}" "$common_env"
  set_env NACOS_PORT_WEB "$NACOS_PORT_WEB" "$common_env"
  set_env NACOS_PORT_API "$NACOS_PORT_API" "$common_env"
  set_env NACOS_USER "$NACOS_USER" "$common_env"
  set_env NACOS_NS "$NACOS_NS" "$common_env"
  set_env SPRING_CLOUD_NACOS_DISCOVERY_REGISTER_ENABLED "false" "$common_env"
  set_env NACOS_PASSWORD "$NACOS_PASSWORD" "$secrets_env"
  chmod 0644 "$common_env"
  chmod 0600 "$secrets_env"
}

strip_web_nginx_runtime_routes() {
  nginx_conf="$APP_DIR/web-vue3/nginx/default.conf"
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

write_service_envs() {
  for service in $SERVICE_ORDER; do
    [ -d "$APP_DIR/$service" ] || continue
    source_env="$APP_DIR/$service/.env"
    service_env="$ENV_DIR/$service.env"
    image="$(retag_image "$(read_env_value "$source_env" APP_IMAGE "aifar-$service:latest")")"
    : > "$service_env"
    set_env APP_IMAGE "$image" "$service_env"
    set_env APP_CONTAINER_NAME "$(pod_name "$service" "$REVISION" 1)" "$service_env"
    set_env AIFAR_SERVICE_PROXY "aifar-agent" "$service_env"
    app_name="$(alpha_service_name "$service")"
    if [ -n "$app_name" ]; then
      set_env SPRING_APPLICATION_NAME "$app_name" "$service_env"
    fi
    port_var="$(service_port_var "$service")"
    if [ "$service" != "web-vue3" ] && [ -n "$port_var" ]; then
      port_value="$(read_env_value "$ENV_DIR/compose.env" "$port_var" "")"
      [ -n "$port_value" ] && set_env SERVER_PORT "$port_value" "$service_env"
    fi
    chmod 0644 "$service_env"
  done
}

build_images() {
  for service in $SERVICE_ORDER; do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$REVISION")"
    docker build -t "$image" "$APP_DIR/$service"
  done
}

agent_host_ip() {
  if command -v ip >/dev/null 2>&1; then
    route_ip="$(ip route get "$NACOS_CONNECT_HOST" 2>/dev/null | awk '{for (i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}' || true)"
    if [ -n "$route_ip" ]; then
      printf "%s" "$route_ip"
      return
    fi
  fi
  hostname -I 2>/dev/null | awk '{print $1; exit}'
}

nacos_access_token() {
  if command -v curl >/dev/null 2>&1; then
    body="$(curl -fsS -X POST "http://${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}/nacos/v1/auth/users/login" -d "username=${NACOS_USER}&password=${NACOS_PASSWORD}" 2>/dev/null || true)"
    token="$(printf "%s" "$body" | sed -n 's/.*"accessToken"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
    [ -n "$token" ] && printf "%s" "$token"
  fi
}

register_nacos_proxy() {
  service="$1"
  app_name="$(alpha_service_name "$service")"
  [ -n "$app_name" ] || return 0
  ip="$(agent_host_ip)"
  port="$(service_port "$service")"
  [ -n "$ip" ] || fail "AIFAR agent host IP is empty for $service"
  token="$(nacos_access_token || true)"
  token_arg=""
  [ -z "$token" ] || token_arg="&accessToken=$token"
  url="http://${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}/nacos/v1/ns/instance?serviceName=${app_name}&ip=${ip}&port=${port}&namespaceId=${NACOS_NS}&ephemeral=false${token_arg}"
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -X DELETE "$url" >/dev/null 2>&1 || true
    curl -fsS -X POST "$url" >/dev/null 2>&1 || fail "register Nacos service proxy failed: $app_name"
    echo "Nacos agent proxy registered: $app_name -> $ip:$port"
  else
    echo "curl is not available; skip Nacos agent proxy registration for $app_name"
  fi
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

container_status() {
  docker inspect --format '{{ "{{" }}.State.Status{{ "}}" }}' "$1" 2>/dev/null || true
}

container_health() {
  docker inspect --format '{{ "{{" }}if .State.Health{{ "}}" }}{{ "{{" }}.State.Health.Status{{ "}}" }}{{ "{{" }}end{{ "}}" }}' "$1" 2>/dev/null || true
}

start_pod() {
  service="$1"
  replica="$2"
  service_env="$ENV_DIR/$service.env"
  [ -f "$service_env" ] || return 0
  image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$REVISION")"
  container="$(pod_name "$service" "$REVISION" "$replica")"
  port="$(service_port "$service")"
  health_interval="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_INTERVAL 15s)"
  health_timeout="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_TIMEOUT 5s)"
  health_retries="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_RETRIES 3)"
  health_start_period="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_START_PERIOD 30s)"
  restart_policy="$(read_env_value "$ENV_DIR/compose.env" APP_RESTART_POLICY unless-stopped)"
  health_cmd="$(health_cmd_for_service "$service")"
  resource_args=""
  [ -z "$APP_CPUS" ] || resource_args="$resource_args --cpus $APP_CPUS"
  [ -z "$APP_MEMORY_LIMIT" ] || resource_args="$resource_args --memory $APP_MEMORY_LIMIT"
  docker rm -f "$container" >/dev/null 2>&1 || true
  env_args="--env-file $service_env"
  if [ "$service" != "web-vue3" ]; then
    env_args="--env-file $ENV_DIR/java-common.env --env-file $ENV_DIR/java-secrets.env --env-file $service_env"
  fi
  # shellcheck disable=SC2086
  docker run -d \
    --name "$container" \
    --restart "$restart_policy" \
    --label aifar.app=aifar \
    --label "aifar.install-root=$INSTALL_ROOT" \
    --label "aifar.component=pod" \
    --label "aifar.service=$service" \
    --label "aifar.revision=$REVISION" \
    --label "aifar.pod=$service-$REVISION-r$replica" \
    --label "aifar.replica=$replica" \
    --network "$INGRESS_NETWORK" \
    $resource_args \
    --health-cmd "$health_cmd" \
    --health-interval "$health_interval" \
    --health-timeout "$health_timeout" \
    --health-retries "$health_retries" \
    --health-start-period "$health_start_period" \
    -e "TZ=$TIMEZONE" \
    $env_args \
    "$image" >/dev/null
  echo "AIFAR Pod started: $service revision=$REVISION replica=$replica container=$container"
}

wait_pod_ready() {
  container="$1"
  timeout="$(read_env_value "$ENV_DIR/compose.env" APP_STARTUP_TIMEOUT 300)"
  case "$timeout" in ""|*[!0-9]*) timeout=300 ;; esac
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    status="$(container_status "$container")"
    health="$(container_health "$container")"
    if [ "$status" = "running" ] && { [ "$health" = "healthy" ] || [ -z "$health" ]; }; then
      echo "AIFAR Pod ready: $container"
      return 0
    fi
    echo "$container status=${status:-missing} health=${health:-none}"
    sleep 5
  done
  docker logs --tail 120 "$container" >&2 || true
  return 1
}

wait_initial_pods_ready() {
  for service in $SERVICE_ORDER; do
    [ -f "$ENV_DIR/$service.env" ] || continue
    container="$(pod_name "$service" "$REVISION" 1)"
    wait_pod_ready "$container" || fail "AIFAR Pod did not become ready: $container"
  done
}

write_runtime_spec() {
  spec="$INGRESS_DIR/runtime-spec.json"
  mkdir -p "$INGRESS_DIR"
  cat > "$spec" <<JSON
{
  "version": "runtime-v1",
  "instanceId": "admin",
  "installRoot": "${INSTALL_ROOT}",
  "network": "${INGRESS_NETWORK}",
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
    printf '    {"name":"%s","appName":"%s","port":%s}' "$service" "$app_name" "$port" >> "$spec"
  done
  cat >> "$spec" <<JSON
  ],
  "ingress": {
    "gatewayService": "gateway",
    "webService": "web-vue3",
    "gatewayPort": ${GATEWAY_PORT},
    "webPort": ${WEB_VUE3_PORT}
  }
}
JSON
  printf "%s" "$spec"
}

reconcile_ingress() {
  command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required; install or upgrade Docker runtime first"
  spec="$(write_runtime_spec)"
  echo "reconciling AIFAR runtime through aifar-agent: $spec"
  aifar-agent reconcile-ingress --spec "$spec"
}

write_model_manifest() {
  mkdir -p "$AIFAR_DIR"
  cat > "$AIFAR_DIR/model.json" <<JSON
{
  "model": "${ORCHESTRATION_MODEL}",
  "revision": "${REVISION}",
  "version": "${VERSION}",
  "createdAt": "${CREATED_AT}",
  "configHash": "${CONFIG_HASH}",
  "network": "${INGRESS_NETWORK}",
  "nacosRegistrationMode": "${NACOS_REGISTRATION_MODE}"
}
JSON
}

cleanup_failed_install() {
  pods="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$pods" ] || docker rm -f $pods >/dev/null 2>&1 || true
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
command -v tar >/dev/null 2>&1 || fail "tar command is required"
[ -f "$ARCHIVE" ] || fail "bundle archive not found: $ARCHIVE"

mkdir -p "$INSTALL_ROOT" "$WORK_DIR" "$RUNTIME_DIR" "$ENV_DIR" "$INGRESS_DIR" "$AIFAR_DIR"
rm -rf "$TMP_DIR" "$APP_DIR" "$IMAGE_DIR"
mkdir -p "$TMP_DIR"
tar -xzf "$ARCHIVE" -C "$TMP_DIR"
[ -d "$TMP_DIR/docker-apps" ] || fail "docker-apps directory is missing in bundle"
mv "$TMP_DIR/docker-apps" "$APP_DIR"
if [ -d "$TMP_DIR/docker-images" ]; then
  mv "$TMP_DIR/docker-images" "$IMAGE_DIR"
fi
rm -rf "$TMP_DIR"

trap 'cleanup_failed_install' INT TERM
check_nacos_dependency
resolve_system_timezone
write_compose_env
write_java_env
write_service_envs
strip_web_nginx_runtime_routes
load_docker_images
require_local_image "bellsoft/liberica-openjre-rocky:21"
require_local_image "nginx:stable-alpine"
ensure_network
build_images

for service in $SERVICE_ORDER; do
  start_pod "$service" 1
done

if ! wait_initial_pods_ready; then
  cleanup_failed_install
  fail "AIFAR initial Pods did not become stable"
fi

reconcile_ingress

for service in $SERVICE_ORDER; do
  [ "$service" = "web-vue3" ] && continue
  [ -f "$ENV_DIR/$service.env" ] || continue
  register_nacos_proxy "$service"
done

open_firewall_ports "$GATEWAY_PORT" "$WEB_VUE3_PORT"
allow_selinux_ports http_port_t "$GATEWAY_PORT" "$WEB_VUE3_PORT"
write_model_manifest
trap - INT TERM
echo "AIFAR k8s-like orchestration installed, revision $REVISION"
