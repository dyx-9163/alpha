#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
BASE_PORT={{.BasePort}}
SERVICE_NAME="aifar-mysql-router-$BASE_PORT"
ROUTER_DIR="$INSTALL_ROOT/routers/$BASE_PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping MySQL Router service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true

echo "removing MySQL Router systemd unit"
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

echo "removing MySQL Router instance directory: $ROUTER_DIR"
if [ -n "$ROUTER_DIR" ] && [ "$ROUTER_DIR" != "/" ] && [ -d "$ROUTER_DIR" ]; then
  $SUDO rm -rf "$ROUTER_DIR"
else
  echo "MySQL Router instance directory does not exist, skip"
fi

if [ -d "$INSTALL_ROOT/routers" ] && [ -z "$(find "$INSTALL_ROOT/routers" -mindepth 1 -maxdepth 1 -type d -print -quit)" ]; then
  echo "removing MySQL Router install root: $INSTALL_ROOT"
  $SUDO rm -rf "$INSTALL_ROOT"
fi

MYSQL_ROUTER_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$MYSQL_ROUTER_ROOT" >/dev/null 2>&1 || true

echo "MySQL Router deployed service removed: $VERSION on base port $BASE_PORT"
