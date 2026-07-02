#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
WORK_DIR={{ quote .WorkDir }}
ARCHIVE={{ quote .ArchiveRemote }}
SERVICE_ORDER={{ quote .ServiceOrder }}
VERSION={{ quote .Version }}
RELEASE_ID={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
RELEASE_KEEP_COUNT={{ .ReleaseKeepCount }}

RELEASES_DIR="$INSTALL_ROOT/releases"
CURRENT_LINK="$INSTALL_ROOT/current"
RELEASE_DIR="$RELEASES_DIR/$RELEASE_ID"
APP_DIR="$RELEASE_DIR/docker-apps"
SQL_DIR="$RELEASE_DIR/docker-sql"
IMAGE_DIR="$RELEASE_DIR/docker-images"
ENV_DIR="$RELEASE_DIR/env"
AIFAR_DIR="$RELEASE_DIR/.aifar"
TMP_DIR="$INSTALL_ROOT/.extract-$RELEASE_ID-$$"

TIMEZONE={{ quote .Options.Timezone }}
NETWORK_NAME={{ quote .Options.NetworkName }}
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
DB_HOST={{ quote .Options.DBHost }}
DB_PORT={{ quote .Options.DBPort }}
DB_NAME_NACOS={{ quote .Options.DBNameNacos }}
DB_USER={{ quote .Options.DBUser }}
DB_PASSWORD={{ quote .Options.DBPassword }}
REDIS_MODE={{ quote .Options.RedisMode }}
REDIS_HOST={{ quote .Options.RedisHost }}
REDIS_PORT={{ quote .Options.RedisPort }}
REDIS_PASSWORD={{ quote .Options.RedisPassword }}
REDIS_DATABASE={{ quote .Options.RedisDatabase }}
REDIS_SENTINEL_MASTER={{ quote .Options.RedisSentinelMasterName }}
REDIS_SENTINEL_NODES={{ quote .Options.RedisSentinelNodesCSV }}
REDIS_CLUSTER_NODES={{ quote .Options.RedisClusterNodesCSV }}
MINIO_ENABLE_STORAGE={{ quote .Options.MinioEnableStorage }}
MINIO_PLATFORM={{ quote .Options.MinioPlatform }}
MINIO_ENDPOINT={{ quote .Options.MinioEndpoint }}
MINIO_ACCESS_KEY={{ quote .Options.MinioAccessKey }}
MINIO_SECRET_KEY={{ quote .Options.MinioSecretKey }}
MINIO_BUCKET_NAME={{ quote .Options.MinioBucketName }}
MINIO_DOMAIN={{ quote .Options.MinioDomain }}
MINIO_BASE_PATH={{ quote .Options.MinioBasePath }}
INIT_SQL={{ quote .Options.InitSQL }}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose "$@"
    return
  fi
  if command -v docker-compose >/dev/null 2>&1; then
    docker-compose "$@"
    return
  fi
  fail "docker compose or docker-compose is required"
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

retag_image() {
  image="$1"
  case "$image" in
    *@*) printf "%s" "$image" ;;
    *:*) printf "%s:%s" "${image%:*}" "$RELEASE_ID" ;;
    *) printf "%s:%s" "$image" "$RELEASE_ID" ;;
  esac
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

parse_host_port() {
  value="$1"
  fallback_port="$2"
  text="$value"
  case "$text" in
    *://*) text="${text#*://}" ;;
  esac
  text="${text%%/*}"
  PARSED_HOST=""
  PARSED_PORT="$fallback_port"
  case "$text" in
    *:*)
      PARSED_HOST="${text%:*}"
      PARSED_PORT="${text##*:}"
      ;;
    *)
      PARSED_HOST="$text"
      ;;
  esac
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

check_tcp_dependency() {
  name="$1"
  host="$2"
  port="$3"
  [ -n "$host" ] || fail "$name dependency host is empty"
  if tcp_probe "$host" "$port"; then
    echo "$name dependency is reachable at $host:$port"
    return 0
  fi
  fail "$name dependency is not reachable at $host:$port"
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
  rc=$?
  if [ "$rc" = "2" ]; then
    check_tcp_dependency "Nacos" "$NACOS_CONNECT_HOST" "$NACOS_PORT_WEB"
    return 0
  fi
  fail "Nacos dependency readiness check failed at $url"
}

check_mysql_dependency() {
  if command -v mysql >/dev/null 2>&1; then
    if mysql --protocol=tcp -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "-p$DB_PASSWORD" -N -s -e "select 1" >/dev/null 2>&1; then
      echo "MySQL dependency is ready at $DB_HOST:$DB_PORT"
      return 0
    fi
    fail "MySQL dependency authentication or readiness check failed at $DB_HOST:$DB_PORT"
  fi
  check_tcp_dependency "MySQL" "$DB_HOST" "$DB_PORT"
}

check_redis_endpoint() {
  host="$1"
  port="$2"
  label="$3"
  if command -v redis-cli >/dev/null 2>&1; then
    if [ -n "$REDIS_PASSWORD" ]; then
      if redis-cli -h "$host" -p "$port" -a "$REDIS_PASSWORD" --no-auth-warning ping >/dev/null 2>&1; then
        echo "$label dependency is ready at $host:$port"
        return 0
      fi
    elif redis-cli -h "$host" -p "$port" ping >/dev/null 2>&1; then
      echo "$label dependency is ready at $host:$port"
      return 0
    fi
    fail "$label dependency ping failed at $host:$port"
  fi
  check_tcp_dependency "$label" "$host" "$port"
}

check_endpoint_list() {
  label="$1"
  endpoints="$2"
  fallback_port="$3"
  [ -n "$endpoints" ] || fail "$label dependency endpoints are empty"
  old_ifs="$IFS"
  IFS=","
  for endpoint in $endpoints; do
    IFS="$old_ifs"
    parse_host_port "$endpoint" "$fallback_port"
    check_redis_endpoint "$PARSED_HOST" "$PARSED_PORT" "$label"
    IFS=","
  done
  IFS="$old_ifs"
}

check_tcp_endpoint_list() {
  label="$1"
  endpoints="$2"
  fallback_port="$3"
  [ -n "$endpoints" ] || fail "$label dependency endpoints are empty"
  old_ifs="$IFS"
  IFS=","
  for endpoint in $endpoints; do
    IFS="$old_ifs"
    parse_host_port "$endpoint" "$fallback_port"
    check_tcp_dependency "$label" "$PARSED_HOST" "$PARSED_PORT"
    IFS=","
  done
  IFS="$old_ifs"
}

check_redis_dependency() {
  case "$REDIS_MODE" in
    sentinel)
      check_tcp_endpoint_list "Redis Sentinel" "$REDIS_SENTINEL_NODES" 26379
      ;;
    cluster)
      check_endpoint_list "Redis Cluster" "$REDIS_CLUSTER_NODES" "$REDIS_PORT"
      ;;
    *)
      check_redis_endpoint "$REDIS_HOST" "$REDIS_PORT" "Redis"
      ;;
  esac
}

check_minio_dependency() {
  [ "$MINIO_ENABLE_STORAGE" = "true" ] || return 0
  endpoint="${MINIO_ENDPOINT%/}"
  [ -n "$endpoint" ] || fail "MinIO endpoint is empty"
  case "$endpoint" in
    http://*|https://*) health_url="$endpoint/minio/health/live" ;;
    *) health_url="http://$endpoint/minio/health/live" ;;
  esac
  if http_probe "$health_url"; then
    echo "MinIO dependency is ready at $endpoint"
    return 0
  fi
  rc=$?
  parse_host_port "$endpoint" 9000
  if [ "$rc" = "2" ]; then
    check_tcp_dependency "MinIO" "$PARSED_HOST" "$PARSED_PORT"
    return 0
  fi
  fail "MinIO dependency readiness check failed at $health_url"
}

check_dependencies() {
  echo "checking AIFAR external dependencies"
  check_nacos_dependency
  check_mysql_dependency
  check_redis_dependency
  check_minio_dependency
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
      if [ -z "$detected" ] && [ -L /etc/localtime ]; then
        localtime_target="$(readlink /etc/localtime 2>/dev/null || true)"
        case "$localtime_target" in
          */zoneinfo/*) detected="${localtime_target#*/zoneinfo/}" ;;
        esac
      fi
      TIMEZONE="${detected:-UTC}"
      ;;
  esac
}

sql_literal_escape() {
  printf "%s" "$1" | sed "s/'/''/g"
}

patch_nacos_sql_namespace() {
  sql_file="$SQL_DIR/aifar_cloud_nacos.sql"
  [ -f "$sql_file" ] || return 0
  escaped_ns="$(sql_literal_escape "$NACOS_NS")"
  tmp="${sql_file}.tmp"
  awk -v replacement="'$escaped_ns'" "{ gsub(/'dyx'/, replacement); print }" "$sql_file" > "$tmp"
  mv "$tmp" "$sql_file"
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
example alpha-example
extend alpha-extend
email alpha-email
datareport alpha-datareport
visualdev alpha-visualdev
workflow alpha-workflow
tenant alpha-tenant
scheduletask alpha-scheduletask
visualdata alpha-visualdata
app alpha-app
flowForm alpha-flowForm
schedule alpha-schedule
EOF
}

alpha_service_name() {
  service="$1"
  alpha_service_pairs | awk -v s="$service" '$1==s {print $2; exit}'
}

patch_nacos_sql_service_names() {
  sql_file="$SQL_DIR/aifar_cloud_nacos.sql"
  [ -f "$sql_file" ] || return 0
  alpha_service_pairs | while read -r service app_name; do
    tmp="${sql_file}.tmp"
    sed "s/aifar-${service}/${app_name}/g" "$sql_file" > "$tmp"
    mv "$tmp" "$sql_file"
  done
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

write_compose_env() {
  source_env="$APP_DIR/.env"
  compose_env="$ENV_DIR/compose.env"
  : > "$compose_env"
  set_env COMPOSE_PROJECT_NAME "aifar-admin" "$compose_env"
  set_env TZ "$TIMEZONE" "$compose_env"
  set_env APP_CPUS "$APP_CPUS" "$compose_env"
  set_env APP_MEMORY_LIMIT "$APP_MEMORY_LIMIT" "$compose_env"
  set_env APP_RESTART_POLICY "$(read_env_value "$source_env" APP_RESTART_POLICY unless-stopped)" "$compose_env"
  set_env APP_NETWORK_NAME "$NETWORK_NAME" "$compose_env"
  set_env APP_NETWORK_DRIVER "bridge" "$compose_env"
  set_env APP_HEALTH_PROTOCOL "$(read_env_value "$source_env" APP_HEALTH_PROTOCOL http)" "$compose_env"
  set_env APP_HEALTH_HOST "$(read_env_value "$source_env" APP_HEALTH_HOST 127.0.0.1)" "$compose_env"
  set_env APP_HEALTH_PATH "$(read_env_value "$source_env" APP_HEALTH_PATH '')" "$compose_env"
  set_env APP_HEALTH_CONNECT_TIMEOUT "$(read_env_value "$source_env" APP_HEALTH_CONNECT_TIMEOUT 3)" "$compose_env"
  set_env APP_HEALTH_INTERVAL "$(read_env_value "$source_env" APP_HEALTH_INTERVAL 15s)" "$compose_env"
  set_env APP_HEALTH_TIMEOUT "$(read_env_value "$source_env" APP_HEALTH_TIMEOUT 5s)" "$compose_env"
  set_env APP_HEALTH_RETRIES "$(read_env_value "$source_env" APP_HEALTH_RETRIES 3)" "$compose_env"
  set_env APP_HEALTH_START_PERIOD "$(read_env_value "$source_env" APP_HEALTH_START_PERIOD 30s)" "$compose_env"
  set_env APP_STARTUP_TIMEOUT "$(read_env_value "$source_env" APP_STARTUP_TIMEOUT 180)" "$compose_env"
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
  set_env NACOS_PASSWORD "$NACOS_PASSWORD" "$secrets_env"
  chmod 0644 "$common_env"
  chmod 0600 "$secrets_env"
}

write_service_envs() {
  for service in $SERVICE_ORDER; do
    [ -d "$APP_DIR/$service" ] || continue
    source_env="$APP_DIR/$service/.env"
    service_env="$ENV_DIR/$service.env"
    image="$(retag_image "$(read_env_value "$source_env" APP_IMAGE "aifar-$service:latest")")"
    container="$(read_env_value "$source_env" APP_CONTAINER_NAME "aifar-$service")"
    : > "$service_env"
    set_env APP_IMAGE "$image" "$service_env"
    set_env APP_CONTAINER_NAME "$container" "$service_env"
    app_name="$(alpha_service_name "$service")"
    if [ -n "$app_name" ]; then
      set_env SPRING_APPLICATION_NAME "$app_name" "$service_env"
    fi
    chmod 0644 "$service_env"
  done
}

append_compose_service() {
  service="$1"
  port_var="$(service_port_var "$service")"
  service_env="$ENV_DIR/$service.env"
  [ -f "$service_env" ] || return 0
  image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$RELEASE_ID")"
  container="$(read_env_value "$service_env" APP_CONTAINER_NAME "aifar-$service")"
  cat >> "$RELEASE_DIR/compose.yaml" <<YAML
  $service:
    build:
      context: ./docker-apps/$service
      dockerfile: Dockerfile
    image: $image
    container_name: $container
    restart: \${APP_RESTART_POLICY}
YAML
  if [ "$service" = "web-vue3" ]; then
    cat >> "$RELEASE_DIR/compose.yaml" <<YAML
    env_file:
      - ./env/$service.env
YAML
  else
    cat >> "$RELEASE_DIR/compose.yaml" <<YAML
    env_file:
      - ./env/java-common.env
      - ./env/java-secrets.env
      - ./env/$service.env
YAML
  fi
  if [ -n "$port_var" ]; then
    printf '    ports:\n' >> "$RELEASE_DIR/compose.yaml"
    printf '      - "${%s}:${%s}"\n' "$port_var" "$port_var" >> "$RELEASE_DIR/compose.yaml"
    cat >> "$RELEASE_DIR/compose.yaml" <<YAML
    healthcheck:
YAML
    if [ "$service" = "web-vue3" ]; then
      printf '      test: ["CMD-SHELL", "wget -q -T ${APP_HEALTH_CONNECT_TIMEOUT} -O /dev/null ${APP_HEALTH_PROTOCOL}://${APP_HEALTH_HOST}:${%s}${APP_HEALTH_PATH} || exit 1"]\n' "$port_var" >> "$RELEASE_DIR/compose.yaml"
    else
      printf '      test: ["CMD-SHELL", "curl -fsS --connect-timeout ${APP_HEALTH_CONNECT_TIMEOUT} ${APP_HEALTH_PROTOCOL}://${APP_HEALTH_HOST}:${%s}${APP_HEALTH_PATH} >/dev/null || exit 1"]\n' "$port_var" >> "$RELEASE_DIR/compose.yaml"
    fi
    cat >> "$RELEASE_DIR/compose.yaml" <<YAML
      interval: \${APP_HEALTH_INTERVAL}
      timeout: \${APP_HEALTH_TIMEOUT}
      retries: \${APP_HEALTH_RETRIES}
      start_period: \${APP_HEALTH_START_PERIOD}
YAML
  fi
  cat >> "$RELEASE_DIR/compose.yaml" <<YAML
    environment:
      TZ: \${TZ}
    cpus: \${APP_CPUS}
    mem_limit: \${APP_MEMORY_LIMIT}
    networks:
      - app-network

YAML
}

write_root_compose() {
  cat > "$RELEASE_DIR/compose.yaml" <<'YAML'
services:
YAML
  for service in $SERVICE_ORDER; do
    [ -d "$APP_DIR/$service" ] || continue
    append_compose_service "$service"
  done
  cat >> "$RELEASE_DIR/compose.yaml" <<'YAML'
networks:
  app-network:
    external: true
    name: ${APP_NETWORK_NAME}
YAML
}

write_manifest() {
  status="$1"
  mkdir -p "$AIFAR_DIR"
  cat > "$AIFAR_DIR/manifest.json" <<MANIFEST
{
  "app": "aifar",
  "version": "$VERSION",
  "releaseId": "$RELEASE_ID",
  "layout": "release-v1",
  "status": "$status",
  "configHash": "$CONFIG_HASH",
  "createdAt": "$CREATED_AT",
  "releaseRetention": $RELEASE_KEEP_COUNT
}
MANIFEST
}

ensure_network() {
  docker network inspect "$NETWORK_NAME" >/dev/null 2>&1 || docker network create --driver bridge "$NETWORK_NAME" >/dev/null
}

current_release() {
  if [ -L "$CURRENT_LINK" ] || [ -d "$CURRENT_LINK" ]; then
    readlink -f "$CURRENT_LINK" 2>/dev/null || printf "%s" "$CURRENT_LINK"
  fi
}

down_release() {
  release_dir="$1"
  [ -n "$release_dir" ] || return 0
  [ -f "$release_dir/compose.yaml" ] || return 0
  (
    cd "$release_dir"
    compose --env-file env/compose.env -f compose.yaml down --remove-orphans || true
  )
}

down_legacy() {
  legacy_app_dir="$INSTALL_ROOT/docker-apps"
  [ -d "$legacy_app_dir" ] || return 0
  for service in web-vue3 gateway meeting contacts im message file system permission oauth; do
    [ -f "$legacy_app_dir/$service/docker-compose.yaml" ] || continue
    (
      cd "$legacy_app_dir/$service"
      compose --env-file ../.env --env-file .env -f docker-compose.yaml down --remove-orphans || true
    )
  done
}

start_release() {
  (
    cd "$RELEASE_DIR"
    APP_RESTART_POLICY=no
    export APP_RESTART_POLICY
    compose --env-file env/compose.env -f compose.yaml up -d --build
  )
}

container_for_service() {
  service="$1"
  read_env_value "$ENV_DIR/$service.env" APP_CONTAINER_NAME ""
}

container_status() {
  docker inspect --format '{{ "{{" }}.State.Status{{ "}}" }}' "$1" 2>/dev/null || true
}

container_health() {
  docker inspect --format '{{ "{{" }}if .State.Health{{ "}}" }}{{ "{{" }}.State.Health.Status{{ "}}" }}{{ "{{" }}end{{ "}}" }}' "$1" 2>/dev/null || true
}

container_restart_count() {
  docker inspect --format '{{ "{{" }}.RestartCount{{ "}}" }}' "$1" 2>/dev/null || printf "0"
}

service_runtime_ready() {
  service="$1"
  container="$(container_for_service "$service")"
  [ -n "$container" ] || return 0
  status="$(container_status "$container")"
  if [ "$status" != "running" ]; then
    echo "$service container $container is not running: ${status:-missing}" >&2
    return 1
  fi
  health="$(container_health "$container")"
  if [ -n "$health" ] && [ "$health" != "healthy" ]; then
    echo "$service container $container health is $health" >&2
    return 1
  fi
  if [ "$service" != "web-vue3" ]; then
    restarts="$(container_restart_count "$container")"
    case "$restarts" in
      ""|*[!0-9]*) restarts=0 ;;
    esac
    if [ "$restarts" -gt 0 ]; then
      echo "$service container $container restarted $restarts time(s) during startup" >&2
      return 1
    fi
  fi
  return 0
}

wait_release_ready() {
  startup_timeout="$(read_env_value "$ENV_DIR/compose.env" APP_STARTUP_TIMEOUT 180)"
  stable_window="$(read_env_value "$ENV_DIR/compose.env" APP_STABLE_WINDOW 10)"
  case "$startup_timeout" in
    ""|*[!0-9]*) startup_timeout=180 ;;
  esac
  deadline=$(( $(date +%s) + startup_timeout ))
  while :; do
    pending=""
    for service in $SERVICE_ORDER; do
      [ -f "$ENV_DIR/$service.env" ] || continue
      if ! service_runtime_ready "$service"; then
        pending="$pending $service"
      fi
    done
    if [ -z "$pending" ]; then
      break
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "AIFAR release $RELEASE_ID did not become ready:$pending" >&2
      return 1
    fi
    sleep 3
  done
  case "$stable_window" in
    ""|*[!0-9]*) stable_window=10 ;;
  esac
  if [ "$stable_window" -gt 0 ]; then
    sleep "$stable_window"
  fi
  for service in $SERVICE_ORDER; do
    [ -f "$ENV_DIR/$service.env" ] || continue
    service_runtime_ready "$service" || return 1
  done
}

apply_restart_policy() {
  policy="$(read_env_value "$ENV_DIR/compose.env" APP_RESTART_POLICY unless-stopped)"
  [ -n "$policy" ] || policy="unless-stopped"
  for service in $SERVICE_ORDER; do
    [ -f "$ENV_DIR/$service.env" ] || continue
    container="$(container_for_service "$service")"
    [ -n "$container" ] || continue
    docker update --restart "$policy" "$container" >/dev/null 2>&1 || true
  done
}

activate_release() {
  if [ -L "$CURRENT_LINK" ] || [ -f "$CURRENT_LINK" ]; then
    rm -f "$CURRENT_LINK"
  elif [ -d "$CURRENT_LINK" ]; then
    rm -rf "$CURRENT_LINK"
  fi
  ln -s "$RELEASE_DIR" "$CURRENT_LINK"
}

rollback_release() {
  previous="$1"
  [ -n "$previous" ] || return 0
  [ -f "$previous/compose.yaml" ] || return 0
  echo "rolling back to previous AIFAR release: $previous"
  (
    cd "$previous"
    compose --env-file env/compose.env -f compose.yaml up -d || true
  )
}

cleanup_release_images() {
  release_dir="$1"
  [ -d "$release_dir/env" ] || return 0
  for env_file in "$release_dir"/env/*.env; do
    [ -f "$env_file" ] || continue
    image="$(read_env_value "$env_file" APP_IMAGE '')"
    [ -n "$image" ] || continue
    docker image rm -f "$image" >/dev/null 2>&1 || true
  done
}

cleanup_old_releases() {
  [ -d "$RELEASES_DIR" ] || return 0
  current="$(current_release || true)"
  count=0
  find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d | sort -r | while read -r release_dir; do
    [ -f "$release_dir/.aifar/manifest.json" ] || continue
    grep -q '"status"[[:space:]]*:[[:space:]]*"success"' "$release_dir/.aifar/manifest.json" || continue
    count=$((count + 1))
    if [ "$count" -le "$RELEASE_KEEP_COUNT" ]; then
      continue
    fi
    if [ -n "$current" ] && [ "$(readlink -f "$release_dir" 2>/dev/null || printf "%s" "$release_dir")" = "$current" ]; then
      continue
    fi
    cleanup_release_images "$release_dir"
    rm -rf "$release_dir"
  done
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
command -v tar >/dev/null 2>&1 || fail "tar command is required"
[ -f "$ARCHIVE" ] || fail "bundle archive not found: $ARCHIVE"

mkdir -p "$INSTALL_ROOT" "$WORK_DIR" "$RELEASES_DIR"
rm -rf "$TMP_DIR" "$RELEASE_DIR"
mkdir -p "$TMP_DIR"
tar -xzf "$ARCHIVE" -C "$TMP_DIR"
[ -d "$TMP_DIR/docker-apps" ] || fail "docker-apps directory is missing in bundle"

mkdir -p "$RELEASE_DIR"
mv "$TMP_DIR/docker-apps" "$APP_DIR"
if [ -d "$TMP_DIR/docker-sql" ]; then
  mv "$TMP_DIR/docker-sql" "$SQL_DIR"
fi
if [ -d "$TMP_DIR/docker-images" ]; then
  mv "$TMP_DIR/docker-images" "$IMAGE_DIR"
fi
rm -rf "$TMP_DIR"
mkdir -p "$ENV_DIR" "$AIFAR_DIR"

resolve_system_timezone
patch_nacos_sql_namespace
patch_nacos_sql_service_names
write_compose_env
write_java_env
write_service_envs
write_root_compose
write_manifest "pending"
load_docker_images
require_local_image "bellsoft/liberica-openjre-rocky:21"
require_local_image "nginx:stable-alpine"
check_dependencies

if [ "$INIT_SQL" = "true" ]; then
  command -v mysql >/dev/null 2>&1 || fail "mysql client is required when SQL initialization is enabled"
  [ -f "$SQL_DIR/aifar_cloud_nacos.sql" ] || fail "nacos SQL file is missing"
  mysql --protocol=tcp -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "-p$DB_PASSWORD" < "$SQL_DIR/aifar_cloud_nacos.sql"
  if [ -f "$SQL_DIR/aifar_init.sql" ]; then
    mysql --protocol=tcp -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "-p$DB_PASSWORD" < "$SQL_DIR/aifar_init.sql"
  fi
fi

ensure_network
previous_release="$(current_release || true)"
down_release "$previous_release"
down_legacy

if ! start_release; then
  write_manifest "failed"
  down_release "$RELEASE_DIR"
  rollback_release "$previous_release"
  fail "AIFAR release $RELEASE_ID failed to start"
fi

if ! wait_release_ready; then
  write_manifest "failed"
  down_release "$RELEASE_DIR"
  rollback_release "$previous_release"
  fail "AIFAR release $RELEASE_ID did not become stable"
fi

apply_restart_policy
write_manifest "success"
activate_release
cleanup_old_releases

open_firewall_ports "$GATEWAY_PORT" "$WEB_VUE3_PORT"
allow_selinux_ports http_port_t "$GATEWAY_PORT" "$WEB_VUE3_PORT"
echo "AIFAR service deployed under $INSTALL_ROOT release $RELEASE_ID"
docker ps --filter "network=$NETWORK_NAME"
