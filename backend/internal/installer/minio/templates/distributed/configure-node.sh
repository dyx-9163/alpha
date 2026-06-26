#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
API_PORT={{.APIPort}}
CONSOLE_PORT={{.ConsolePort}}
ROOT_USER={{shq .RootUser}}
ROOT_PASSWORD={{shq .RootPassword}}
SERVICE_NAME="aifar-minio-$API_PORT"
CONFIG_DIR="$INSTALL_ROOT/conf"
ENV_FILE="$CONFIG_DIR/minio.env"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

MINIO_VOLUMES="{{range .Volumes}}http://{{.Host}}:{{.Port}}{{.Path}} {{end}}"

echo "configuring MinIO distributed service"
cat > "$INSTALL_ROOT/conf/minio.env.tmp" <<CONF
MINIO_ROOT_USER="$ROOT_USER"
MINIO_ROOT_PASSWORD="$ROOT_PASSWORD"
MINIO_VOLUMES="$MINIO_VOLUMES"
MINIO_OPTS="--address :$API_PORT --console-address :$CONSOLE_PORT"
CONF
$SUDO install -m 0600 "$INSTALL_ROOT/conf/minio.env.tmp" "$ENV_FILE"

$SUDO systemctl daemon-reload
if ! $SUDO systemctl restart "$SERVICE_NAME"; then
  echo "MinIO distributed service failed to restart"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  exit 1
fi

echo "verifying MinIO distributed service"
MINIO_READY=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if command -v curl >/dev/null 2>&1; then
    if curl -fsS "http://127.0.0.1:$API_PORT/minio/health/live" >/dev/null 2>&1; then
      MINIO_READY=1
      break
    fi
  elif command -v wget >/dev/null 2>&1; then
    if wget -qO- "http://127.0.0.1:$API_PORT/minio/health/live" >/dev/null 2>&1; then
      MINIO_READY=1
      break
    fi
  elif $SUDO systemctl is-active --quiet "$SERVICE_NAME"; then
    MINIO_READY=1
    break
  fi
  sleep 1
done
if [ "$MINIO_READY" != "1" ]; then
  echo "MinIO distributed service is not reachable"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  exit 1
fi
echo "MinIO distributed node configured"
