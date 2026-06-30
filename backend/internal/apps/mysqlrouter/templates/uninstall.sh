#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
LEGACY_INSTALL_ROOT={{shq .LegacyInstallRoot}}
BASE_PORT={{.BasePort}}
SERVICE_NAME="aifar-mysql-router"
LEGACY_SERVICE_NAME="aifar-mysql-router-$BASE_PORT"
ROUTER_DIR="$INSTALL_ROOT/router"
LEGACY_ROUTER_DIR="$INSTALL_ROOT/routers/$BASE_PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping MySQL Router service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO systemctl disable --now "$LEGACY_SERVICE_NAME" >/dev/null 2>&1 || true

echo "removing MySQL Router systemd unit"
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO rm -f "/etc/systemd/system/$LEGACY_SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

for DIR in "$ROUTER_DIR" "$LEGACY_ROUTER_DIR"; do
  echo "removing MySQL Router instance directory: $DIR"
  if [ -n "$DIR" ] && [ "$DIR" != "/" ] && [ -d "$DIR" ]; then
    $SUDO rm -rf "$DIR"
  else
    echo "MySQL Router instance directory does not exist, skip"
  fi
done

for ROOT in "$INSTALL_ROOT" "$LEGACY_INSTALL_ROOT"; do
  echo "removing MySQL Router install root: $ROOT"
  if [ -n "$ROOT" ] && [ "$ROOT" != "/" ] && [ -d "$ROOT" ]; then
    $SUDO rm -rf "$ROOT"
  fi
done

MYSQL_ROUTER_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$MYSQL_ROUTER_ROOT" >/dev/null 2>&1 || true

echo "MySQL Router deployed service removed: $VERSION on base port $BASE_PORT"
