#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
NETWORK_NAME={{ quote .NetworkName }}
SERVICE_ORDER={{ quote .ServiceOrder }}
APP_DIR="$INSTALL_ROOT/docker-apps"

compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose "$@"
    return
  fi
  if command -v docker-compose >/dev/null 2>&1; then
    docker-compose "$@"
    return
  fi
  return 1
}

if command -v docker >/dev/null 2>&1 && [ -d "$APP_DIR" ]; then
  for service in $SERVICE_ORDER; do
    [ -f "$APP_DIR/$service/docker-compose.yaml" ] || continue
    (
      cd "$APP_DIR/$service"
      compose --env-file ../.env --env-file .env -f docker-compose.yaml down --remove-orphans || true
    )
  done
fi

rm -rf "$INSTALL_ROOT"

if command -v docker >/dev/null 2>&1 && [ -n "$NETWORK_NAME" ]; then
  docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
fi

echo "AIFAR service removed from $INSTALL_ROOT"
