#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
LEGACY_INSTALL_ROOT={{shq .LegacyInstallRoot}}
API_PORT={{.APIPort}}
SERVICE_NAME="aifar-minio"
LEGACY_SERVICE_NAME="aifar-minio-$API_PORT"
REMOVE_MOUNTED_DISKS={{if .RemoveMountedDisks}}1{{else}}0{{end}}
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping MinIO service"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO systemctl disable --now "$LEGACY_SERVICE_NAME" >/dev/null 2>&1 || true

echo "removing MinIO systemd unit"
$SUDO rm -f "/etc/systemd/system/$SERVICE_NAME.service"
$SUDO rm -f "/etc/systemd/system/$LEGACY_SERVICE_NAME.service"
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

if [ "$REMOVE_MOUNTED_DISKS" = "1" ]; then
  echo "unmounting MinIO mounted data disks"
  {{range .MountRoots}}
  MOUNT_ROOT={{shq .}}
  if [ -n "$MOUNT_ROOT" ] && [ "$MOUNT_ROOT" != "/" ]; then
    if command -v findmnt >/dev/null 2>&1 && findmnt "$MOUNT_ROOT" >/dev/null 2>&1; then
      $SUDO umount "$MOUNT_ROOT"
    elif mountpoint -q "$MOUNT_ROOT" >/dev/null 2>&1; then
      $SUDO umount "$MOUNT_ROOT"
    fi
    if [ -f /etc/fstab ]; then
      FSTAB_TMP="$(mktemp)"
      awk -v mount_root="$MOUNT_ROOT" '$2 != mount_root {print}' /etc/fstab > "$FSTAB_TMP"
      $SUDO install -m 0644 "$FSTAB_TMP" /etc/fstab
      rm -f "$FSTAB_TMP"
    fi
    rmdir "$MOUNT_ROOT" >/dev/null 2>&1 || true
  fi
  {{end}}
fi

for ROOT in "$INSTALL_ROOT" "$LEGACY_INSTALL_ROOT"; do
  echo "removing MinIO install root: $ROOT"
  if [ -n "$ROOT" ] && [ "$ROOT" != "/" ] && [ -d "$ROOT" ]; then
    $SUDO rm -rf "$ROOT"
  else
    echo "MinIO install root does not exist, skip"
  fi
done

MINIO_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$MINIO_ROOT" >/dev/null 2>&1 || true

echo "MinIO deployed service removed: $VERSION on port $API_PORT"
