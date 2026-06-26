#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
REDIS_PORT={{.RedisPort}}
SENTINEL_PORT={{.SentinelPort}}
SERVICE_NAME="aifar-redis-$REDIS_PORT"
SENTINEL_SERVICE="aifar-redis-sentinel-$SENTINEL_PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping Redis Sentinel service"
$SUDO systemctl disable --now "$SENTINEL_SERVICE" >/dev/null 2>&1 || true
$SUDO rm -f "/etc/systemd/system/$SENTINEL_SERVICE.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

echo "stopping Redis service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

echo "removing Redis install root: $INSTALL_ROOT"
if [ -n "$INSTALL_ROOT" ] && [ "$INSTALL_ROOT" != "/" ] && [ -d "$INSTALL_ROOT" ]; then
  $SUDO rm -rf "$INSTALL_ROOT"
fi

REDIS_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$REDIS_ROOT" >/dev/null 2>&1 || true
echo "Redis Sentinel deployed service removed: $VERSION"
