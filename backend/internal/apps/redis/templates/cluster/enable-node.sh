#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
REDIS_PASSWORD={{shq .Password}}
SERVICE_NAME="aifar-redis"
LEGACY_SERVICE_NAME="aifar-redis-$PORT"
CONFIG_FILE="$INSTALL_ROOT/conf/redis.conf"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "enabling Redis Cluster mode on port $PORT"
if ! grep -q "^cluster-enabled yes" "$CONFIG_FILE"; then
  cat >> "$CONFIG_FILE" <<CONF
cluster-enabled yes
cluster-config-file nodes-$PORT.conf
cluster-node-timeout 5000
cluster-announce-port $PORT
cluster-announce-bus-port $((PORT + 10000))
CONF
fi

if ! $SUDO systemctl restart "$SERVICE_NAME"; then
  $SUDO systemctl restart "$LEGACY_SERVICE_NAME"
fi
echo "verifying Redis Cluster node"
if ! "$INSTALL_ROOT/bin/redis-cli" -p "$PORT" -a "$REDIS_PASSWORD" --no-auth-warning ping 2>/dev/null | grep -q PONG; then
  echo "Redis Cluster node is not responding"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO systemctl --no-pager --full status "$LEGACY_SERVICE_NAME" || true
  exit 1
fi
echo "Redis Cluster node enabled: $PORT"
