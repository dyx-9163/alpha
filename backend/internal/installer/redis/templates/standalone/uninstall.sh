#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
SERVICE_NAME="aifar-redis-$PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping Redis service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true

echo "removing Redis systemd unit"
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

echo "removing Redis install root: $INSTALL_ROOT"
if [ -n "$INSTALL_ROOT" ] && [ "$INSTALL_ROOT" != "/" ] && [ -d "$INSTALL_ROOT" ]; then
  $SUDO rm -rf "$INSTALL_ROOT"
else
  echo "Redis install root does not exist, skip"
fi

REDIS_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$REDIS_ROOT" >/dev/null 2>&1 || true

echo "Redis deployed service removed: $VERSION on port $PORT"
