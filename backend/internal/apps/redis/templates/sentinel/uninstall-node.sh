#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
LEGACY_INSTALL_ROOT={{shq .LegacyInstallRoot}}
REDIS_PORT={{.RedisPort}}
SENTINEL_PORT={{.SentinelPort}}
SERVICE_NAME="aifar-redis"
LEGACY_SERVICE_NAME="aifar-redis-$REDIS_PORT"
SENTINEL_SERVICE="aifar-redis-sentinel"
LEGACY_SENTINEL_SERVICE="aifar-redis-sentinel-$SENTINEL_PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping Redis Sentinel service"
$SUDO systemctl disable --now "$SENTINEL_SERVICE" >/dev/null 2>&1 || true
$SUDO systemctl disable --now "$LEGACY_SENTINEL_SERVICE" >/dev/null 2>&1 || true
$SUDO rm -f "/etc/systemd/system/$SENTINEL_SERVICE.service"
$SUDO rm -f "/etc/systemd/system/$LEGACY_SENTINEL_SERVICE.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

echo "stopping Redis service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO systemctl disable --now "$LEGACY_SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO rm -f "/etc/systemd/system/$LEGACY_SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

for ROOT in "$INSTALL_ROOT" "$LEGACY_INSTALL_ROOT"; do
  echo "removing Redis install root: $ROOT"
  if [ -n "$ROOT" ] && [ "$ROOT" != "/" ] && [ -d "$ROOT" ]; then
    $SUDO rm -rf "$ROOT"
  fi
done

REDIS_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$REDIS_ROOT" >/dev/null 2>&1 || true
echo "Redis Sentinel deployed service removed: $VERSION"
