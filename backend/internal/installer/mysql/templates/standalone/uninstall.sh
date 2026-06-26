#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
SERVICE_NAME="aifar-mysql-$PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping MySQL service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true

echo "removing MySQL systemd unit"
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

echo "removing MySQL install root: $INSTALL_ROOT"
if [ -n "$INSTALL_ROOT" ] && [ "$INSTALL_ROOT" != "/" ] && [ -d "$INSTALL_ROOT" ]; then
  $SUDO rm -rf "$INSTALL_ROOT"
else
  echo "MySQL install root does not exist, skip"
fi

MYSQL_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$MYSQL_ROOT" >/dev/null 2>&1 || true

echo "MySQL deployed service removed: $VERSION on port $PORT"
