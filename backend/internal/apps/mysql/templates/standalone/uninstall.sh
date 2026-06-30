#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
LEGACY_INSTALL_ROOT={{shq .LegacyInstallRoot}}
PORT={{.Port}}
SERVICE_NAME="aifar-mysql"
LEGACY_SERVICE_NAME="aifar-mysql-$PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping MySQL service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO systemctl disable --now "$LEGACY_SERVICE_NAME" >/dev/null 2>&1 || true

echo "removing MySQL systemd unit"
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO rm -f "/etc/systemd/system/$LEGACY_SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

for ROOT in "$INSTALL_ROOT" "$LEGACY_INSTALL_ROOT"; do
  echo "removing MySQL install root: $ROOT"
  if [ -n "$ROOT" ] && [ "$ROOT" != "/" ] && [ -d "$ROOT" ]; then
    $SUDO rm -rf "$ROOT"
  else
    echo "MySQL install root does not exist, skip"
  fi
done

MYSQL_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$MYSQL_ROOT" >/dev/null 2>&1 || true

echo "MySQL deployed service removed: $VERSION on port $PORT"
