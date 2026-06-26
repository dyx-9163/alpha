#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
API_PORT={{.APIPort}}
SERVICE_NAME="aifar-minio-$API_PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping MinIO service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true

echo "removing MinIO systemd unit"
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

echo "removing MinIO install root: $INSTALL_ROOT"
if [ -n "$INSTALL_ROOT" ] && [ "$INSTALL_ROOT" != "/" ] && [ -d "$INSTALL_ROOT" ]; then
  $SUDO rm -rf "$INSTALL_ROOT"
else
  echo "MinIO install root does not exist, skip"
fi

MINIO_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$MINIO_ROOT" >/dev/null 2>&1 || true

echo "MinIO deployed service removed: $VERSION on port $API_PORT"
