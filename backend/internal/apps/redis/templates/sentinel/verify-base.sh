#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
REDIS_PASSWORD={{shq .Password}}
SERVICE_NAME="aifar-redis"
CONFIG_FILE="$INSTALL_ROOT/conf/redis.conf"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "verifying Redis base service for Sentinel"
if [ ! -x "$INSTALL_ROOT/bin/redis-server" ]; then
  echo "Redis base service is missing redis-server at $INSTALL_ROOT/bin/redis-server; install Redis base service first"
  exit 1
fi
if [ ! -x "$INSTALL_ROOT/bin/redis-cli" ]; then
  echo "Redis base service is missing redis-cli at $INSTALL_ROOT/bin/redis-cli; install Redis base service first"
  exit 1
fi
if [ ! -f "$CONFIG_FILE" ]; then
  echo "Redis base configuration is missing at $CONFIG_FILE; install Redis base service first"
  exit 1
fi
if ! $SUDO systemctl is-active --quiet "$SERVICE_NAME"; then
  echo "Redis base service is not active: $SERVICE_NAME"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" 2>&1 || true
  exit 1
fi
if ! VERIFY_OUTPUT="$("$INSTALL_ROOT/bin/redis-cli" -p "$PORT" -a "$REDIS_PASSWORD" --no-auth-warning ping 2>&1)"; then
  echo "$VERIFY_OUTPUT"
  echo "Redis base service is not reachable on port $PORT"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" 2>&1 || true
  exit 1
fi
echo "$VERIFY_OUTPUT" | grep -q PONG || {
  echo "$VERIFY_OUTPUT"
  echo "Redis base service ping did not return PONG on port $PORT"
  exit 1
}
"$INSTALL_ROOT/bin/redis-server" --version
echo "Redis base service verified for Sentinel: $SERVICE_NAME $VERSION"
