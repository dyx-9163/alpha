#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
LEGACY_INSTALL_ROOT={{shq .LegacyInstallRoot}}
PORT={{.Port}}
SERVICE_NAME="aifar-nacos"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping Nacos service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true

echo "removing Nacos systemd unit"
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

for ROOT in "$INSTALL_ROOT" "$LEGACY_INSTALL_ROOT"; do
  echo "removing Nacos install root: $ROOT"
  if [ -n "$ROOT" ] && [ "$ROOT" != "/" ] && [ -d "$ROOT" ]; then
    $SUDO rm -rf "$ROOT"
  else
    echo "Nacos install root does not exist, skip"
  fi
done

echo "Nacos deployed service removed: $VERSION on port $PORT"
