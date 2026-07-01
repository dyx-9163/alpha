#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
WORK_DIR={{ quote .WorkDir }}
ARCHIVE={{ quote .ArchiveRemote }}
SERVICE_ORDER={{ quote .ServiceOrder }}
APP_DIR="$INSTALL_ROOT/docker-apps"
SQL_DIR="$INSTALL_ROOT/docker-sql"
TMP_DIR="$INSTALL_ROOT/.extract-$$"

TIMEZONE={{ quote .Options.Timezone }}
NETWORK_NAME={{ quote .Options.NetworkName }}
APP_CPUS={{ quote .Options.AppCPUs }}
APP_MEMORY_LIMIT={{ quote .Options.AppMemoryLimit }}
GATEWAY_PORT={{ quote .Options.GatewayPort }}
WEB_VUE3_PORT={{ quote .Options.WebPort }}
NACOS_PORT_WEB={{ quote .Options.NacosWebPort }}
NACOS_PORT_API={{ quote .Options.NacosAPIPort }}
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

{{ serviceAccessHelpers }}

down_existing() {
  [ -d "$APP_DIR" ] || return 0
  for service in web-vue3 gateway meeting contacts im message file system permission oauth nacos; do
    [ -f "$APP_DIR/$service/docker-compose.yaml" ] || continue
    (
      cd "$APP_DIR/$service"
      compose --env-file ../.env --env-file .env -f docker-compose.yaml down --remove-orphans || true
    )
  done
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
command -v tar >/dev/null 2>&1 || fail "tar command is required"
[ -f "$ARCHIVE" ] || fail "bundle archive not found: $ARCHIVE"

mkdir -p "$INSTALL_ROOT" "$WORK_DIR"
down_existing
rm -rf "$TMP_DIR"
mkdir -p "$TMP_DIR"
tar -xzf "$ARCHIVE" -C "$TMP_DIR"
[ -d "$TMP_DIR/docker-apps" ] || fail "docker-apps directory is missing in bundle"

rm -rf "$APP_DIR" "$SQL_DIR"
mv "$TMP_DIR/docker-apps" "$APP_DIR"
if [ -d "$TMP_DIR/docker-sql" ]; then
  mv "$TMP_DIR/docker-sql" "$SQL_DIR"
fi
rm -rf "$TMP_DIR"

ROOT_ENV="$APP_DIR/.env"
set_env TZ "$TIMEZONE" "$ROOT_ENV"
set_env APP_CPUS "$APP_CPUS" "$ROOT_ENV"
set_env APP_MEMORY_LIMIT "$APP_MEMORY_LIMIT" "$ROOT_ENV"
set_env APP_NETWORK_NAME "$NETWORK_NAME" "$ROOT_ENV"
set_env APP_NETWORK_DRIVER "bridge" "$ROOT_ENV"
set_env GATEWAY_PORT "$GATEWAY_PORT" "$ROOT_ENV"
set_env WEB_VUE3_PORT "$WEB_VUE3_PORT" "$ROOT_ENV"
set_env NACOS_PORT_WEB "$NACOS_PORT_WEB" "$ROOT_ENV"
set_env NACOS_PORT_API "$NACOS_PORT_API" "$ROOT_ENV"
set_env NACOS_USER "$NACOS_USER" "$ROOT_ENV"
set_env NACOS_PASSWORD "$NACOS_PASSWORD" "$ROOT_ENV"
set_env NACOS_HOST "aifar-nacos:${NACOS_PORT_WEB}" "$ROOT_ENV"
set_env NACOS_NS "$NACOS_NS" "$ROOT_ENV"
set_env AIFAR_DB_HOST "$DB_HOST" "$ROOT_ENV"
set_env AIFAR_DB_PORT "$DB_PORT" "$ROOT_ENV"
set_env AIFAR_DB_NAME_NACOS "$DB_NAME_NACOS" "$ROOT_ENV"
set_env AIFAR_DB_USER "$DB_USER" "$ROOT_ENV"
set_env SPRING_DATASOURCE_HOST "$DB_HOST" "$ROOT_ENV"
set_env SPRING_DATASOURCE_PORT "$DB_PORT" "$ROOT_ENV"
set_env SPRING_DATASOURCE_USERNAME "$DB_USER" "$ROOT_ENV"
set_env SPRING_DATASOURCE_PASSWORD "$DB_PASSWORD" "$ROOT_ENV"
set_env AIFAR_REDIS_MODE "$REDIS_MODE" "$ROOT_ENV"
set_env AIFAR_REDIS_HOST "$REDIS_HOST" "$ROOT_ENV"
set_env AIFAR_REDIS_PORT "$REDIS_PORT" "$ROOT_ENV"
set_env AIFAR_REDIS_DATABASE "$REDIS_DATABASE" "$ROOT_ENV"
set_env AIFAR_REDIS_SENTINEL_MASTER "$REDIS_SENTINEL_MASTER" "$ROOT_ENV"
set_env AIFAR_REDIS_SENTINEL_NODES "$REDIS_SENTINEL_NODES" "$ROOT_ENV"
set_env AIFAR_REDIS_CLUSTER_NODES "$REDIS_CLUSTER_NODES" "$ROOT_ENV"
set_env SPRING_DATA_REDIS_HOST "$REDIS_HOST" "$ROOT_ENV"
set_env SPRING_DATA_REDIS_PORT "$REDIS_PORT" "$ROOT_ENV"
set_env SPRING_DATA_REDIS_PASSWORD "$REDIS_PASSWORD" "$ROOT_ENV"
set_env SPRING_DATA_REDIS_DATABASE "$REDIS_DATABASE" "$ROOT_ENV"
set_env SPRING_DATA_REDIS_SENTINEL_MASTER "$REDIS_SENTINEL_MASTER" "$ROOT_ENV"
set_env SPRING_DATA_REDIS_SENTINEL_NODES "$REDIS_SENTINEL_NODES" "$ROOT_ENV"
set_env SPRING_DATA_REDIS_CLUSTER_NODES "$REDIS_CLUSTER_NODES" "$ROOT_ENV"

NACOS_ENV="$APP_DIR/nacos/.env"
set_env DB_HOST "$DB_HOST" "$NACOS_ENV"
set_env DB_PORT "$DB_PORT" "$NACOS_ENV"
set_env DB_NAME_NACOS "$DB_NAME_NACOS" "$NACOS_ENV"
set_env DB_USER "$DB_USER" "$NACOS_ENV"
set_env DB_PASSWORD "$DB_PASSWORD" "$NACOS_ENV"
set_env REDIS_MODE "$REDIS_MODE" "$NACOS_ENV"
set_env REDIS_HOST "$REDIS_HOST" "$NACOS_ENV"
set_env REDIS_PORT "$REDIS_PORT" "$NACOS_ENV"
set_env REDIS_DATABASE "$REDIS_DATABASE" "$NACOS_ENV"
set_env REDIS_SENTINEL_MASTER "$REDIS_SENTINEL_MASTER" "$NACOS_ENV"
set_env REDIS_SENTINEL_NODES "$REDIS_SENTINEL_NODES" "$NACOS_ENV"
set_env REDIS_CLUSTER_NODES "$REDIS_CLUSTER_NODES" "$NACOS_ENV"
if [ -n "$REDIS_PASSWORD" ]; then
  set_env REDIS_PASSWORD "$REDIS_PASSWORD" "$NACOS_ENV"
fi

if [ "$INIT_SQL" = "true" ]; then
  command -v mysql >/dev/null 2>&1 || fail "mysql client is required when SQL initialization is enabled"
  [ -f "$SQL_DIR/aifar_cloud_nacos.sql" ] || fail "nacos SQL file is missing"
  mysql --protocol=tcp -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "-p$DB_PASSWORD" < "$SQL_DIR/aifar_cloud_nacos.sql"
  if [ -f "$SQL_DIR/aifar_init.sql" ]; then
    mysql --protocol=tcp -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "-p$DB_PASSWORD" < "$SQL_DIR/aifar_init.sql"
  fi
fi

docker network inspect "$NETWORK_NAME" >/dev/null 2>&1 || docker network create --driver bridge "$NETWORK_NAME" >/dev/null

for service in $SERVICE_ORDER; do
  [ -f "$APP_DIR/$service/docker-compose.yaml" ] || continue
  (
    cd "$APP_DIR/$service"
    compose --env-file ../.env --env-file .env -f docker-compose.yaml up -d --build
  )
done

open_firewall_ports "$GATEWAY_PORT" "$WEB_VUE3_PORT" "$NACOS_PORT_WEB" "$NACOS_PORT_API"
allow_selinux_ports http_port_t "$GATEWAY_PORT" "$WEB_VUE3_PORT" "$NACOS_PORT_WEB" "$NACOS_PORT_API"
echo "AIFAR service deployed under $INSTALL_ROOT"
docker ps --filter "network=$NETWORK_NAME"
