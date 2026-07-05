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

RUNTIME_DIR="$INSTALL_ROOT/runtime"
APP_DIR="$RUNTIME_DIR/docker-apps"
ENV_DIR="$RUNTIME_DIR/env"
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

write_service_env() {
  service="$1"
  source_env="$APP_DIR/$service/.env"
  service_env="$ENV_DIR/$service.env"
  image="$(retag_image "$(read_env_value "$source_env" APP_IMAGE "aifar-$service:latest")")"
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

start_pod() {
  service="$1"
  replica=1
  service_env="$ENV_DIR/$service.env"
  image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$REVISION")"
  container="$(pod_name "$service" "$REVISION" "$replica")"
  port="$(service_port "$service")"
  health_interval="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_INTERVAL 15s)"
  health_timeout="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_TIMEOUT 5s)"
  health_retries="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_RETRIES 3)"
  health_start_period="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_START_PERIOD 30s)"
  restart_policy="$(read_env_value "$ENV_DIR/compose.env" APP_RESTART_POLICY unless-stopped)"
  tz_value="$(read_env_value "$ENV_DIR/compose.env" TZ system)"
  health_cmd="$(health_cmd_for_service "$service")"
  app_cpus="$(resource_value "$service" APP_CPUS "$(read_env_value "$ENV_DIR/compose.env" APP_CPUS "")")"
  app_memory_limit="$(resource_value "$service" APP_MEMORY_LIMIT "$(read_env_value "$ENV_DIR/compose.env" APP_MEMORY_LIMIT "")")"
  resource_args=""
  [ -z "$app_cpus" ] || resource_args="$resource_args --cpus $app_cpus"
  [ -z "$app_memory_limit" ] || resource_args="$resource_args --memory $app_memory_limit --memory-swap $app_memory_limit"
  docker rm -f "$container" >/dev/null 2>&1 || true
  if [ "$service" != "web-vue3" ]; then
    env_args="--env-file $ENV_DIR/java-common.env --env-file $ENV_DIR/java-secrets.env --env-file $service_env"
    java_cmd="$(java_start_command)"
    # shellcheck disable=SC2086
    docker run -d \
      --name "$container" \
      --restart no \
      --label aifar.app=aifar \
      --label "aifar.install-root=$INSTALL_ROOT" \
      --label "aifar.component=pod" \
      --label "aifar.service=$service" \
      --label "aifar.revision=$REVISION" \
      --label "aifar.release=$REVISION" \
      --label "aifar.pod=$service-$REVISION-r$replica" \
      --label "aifar.replica=$replica" \
      --label aifar.dynamic-jvm=true \
      --network "$INGRESS_NETWORK" \
      $resource_args \
      --health-cmd "$health_cmd" \
      --health-interval "$health_interval" \
      --health-timeout "$health_timeout" \
      --health-retries "$health_retries" \
      --health-start-period "$health_start_period" \
      -e "APP_CONTAINER_NAME=$container" \
      -e "AIFAR_SERVICE_NAME=$service" \
      -e "TZ=$tz_value" \
      $env_args \
      -v "$ENV_DIR:/opt/aifar/runtime/env:ro" \
      --entrypoint /bin/sh \
      "$image" -c "$java_cmd" >/dev/null
  else
    env_args="--env-file $service_env"
    # shellcheck disable=SC2086
    docker run -d \
      --name "$container" \
      --restart no \
      --label aifar.app=aifar \
      --label "aifar.install-root=$INSTALL_ROOT" \
      --label "aifar.component=pod" \
      --label "aifar.service=$service" \
      --label "aifar.revision=$REVISION" \
      --label "aifar.release=$REVISION" \
      --label "aifar.pod=$service-$REVISION-r$replica" \
      --label "aifar.replica=$replica" \
      --label aifar.dynamic-jvm=false \
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
  fi
  wait_pod_ready "$container" || return 1
  docker update --restart "$restart_policy" "$container" >/dev/null 2>&1 || true
  echo "AIFAR service installed: $service container=$container port=$port"
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

write_runtime_spec() {
  spec="$AGENT_DIR/runtime-spec.json"
  gateway_port="$(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)"
  web_port="$(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)"
  mkdir -p "$AGENT_DIR"
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
    "gatewayPort": ${gateway_port},
    "webPort": ${web_port}
  }
}
JSON
  printf "%s" "$spec"
}

reconcile_runtime() {
  check_agent_dependency
  spec="$(write_runtime_spec)"
  aifar-agent reconcile-runtime --spec "$spec"
}

write_model_manifest() {
  mkdir -p "$AIFAR_DIR"
  cat > "$AIFAR_DIR/last-service-install.json" <<JSON
{
  "model": "k8s-like-v1",
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
[ -f "$INSTALL_ROOT/.aifar/model.json" ] || fail "AIFAR k8s-like model manifest is missing"
grep -q '"model"[[:space:]]*:[[:space:]]*"k8s-like-v1"' "$INSTALL_ROOT/.aifar/model.json" || fail "AIFAR instance is legacy; reinstall with k8s-like orchestration"

trap 'cleanup_failed_service_install' EXIT INT TERM
for service in $NEW_SERVICES; do
  service_known "$service" || fail "unsupported AIFAR service: $service"
  [ -d "$APP_DIR/$service" ] || fail "AIFAR service app directory is missing: $service"
  existing="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$service" --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$existing" ] || fail "AIFAR service already has Pod(s): $service"
  [ "$service" = "web-vue3" ] && strip_web_nginx_runtime_routes
  write_service_env "$service"
  ensure_runtime_config_files "$service"
  image="$(read_env_value "$ENV_DIR/$service.env" APP_IMAGE "")"
  [ -n "$image" ] || fail "service image is empty: $service"
  docker build -t "$image" "$APP_DIR/$service"
  start_pod "$service"
done

if ! reconcile_runtime; then
  cleanup_failed_service_install
  fail "AIFAR runtime reconcile failed after service installation"
fi

open_service_ports $NEW_SERVICES

for service in $NEW_SERVICES; do
  [ "$service" = "web-vue3" ] && continue
  register_nacos_proxy "$service"
done

write_model_manifest
SERVICE_INSTALL_SUCCEEDED=1
trap - EXIT INT TERM
echo "AIFAR services installed: $NEW_SERVICES"
