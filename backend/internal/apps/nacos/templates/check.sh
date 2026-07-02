#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
SERVICE_NAME="aifar-nacos"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

if command -v curl >/dev/null 2>&1; then
  if curl -fsS --max-time 3 "http://127.0.0.1:$PORT/nacos/v1/console/health/readiness" >/dev/null 2>&1; then
    echo "Nacos readiness endpoint is available"
    exit 0
  fi
fi

if command -v ss >/dev/null 2>&1 && ss -lnt | awk '{print $4}' | grep -Eq "(:|\\.)$PORT$"; then
  echo "Nacos port is listening: $PORT"
  exit 0
fi

if ! $SUDO systemctl is-active --quiet "$SERVICE_NAME"; then
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  echo "Nacos readiness and port checks failed, and systemd unit is not active"
  exit 1
fi

echo "Nacos service is active but port $PORT is not reachable"
exit 1
