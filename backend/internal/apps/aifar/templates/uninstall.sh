#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
NETWORK_NAME={{ quote .NetworkName }}
SERVICE_ORDER={{ quote .ServiceOrder }}
CURRENT_LINK="$INSTALL_ROOT/current"
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

down_release() {
  release_dir="$1"
  [ -f "$release_dir/compose.yaml" ] || return 0
  (
    cd "$release_dir"
    compose --env-file env/compose.env -f compose.yaml down --remove-orphans || true
  )
}

down_legacy() {
  [ -d "$APP_DIR" ] || return 0
  for service in $SERVICE_ORDER; do
    [ -f "$APP_DIR/$service/docker-compose.yaml" ] || continue
    (
      cd "$APP_DIR/$service"
      compose --env-file ../.env --env-file .env -f docker-compose.yaml down --remove-orphans || true
    )
  done
}

if command -v docker >/dev/null 2>&1 && [ -d "$INSTALL_ROOT" ]; then
  if [ -L "$CURRENT_LINK" ] || [ -d "$CURRENT_LINK" ]; then
    current_release="$(readlink -f "$CURRENT_LINK" 2>/dev/null || printf "%s" "$CURRENT_LINK")"
    down_release "$current_release"
  fi
  down_legacy
fi

rm -rf "$INSTALL_ROOT"

if command -v docker >/dev/null 2>&1 && [ -n "$NETWORK_NAME" ]; then
  docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
fi

echo "AIFAR service removed from $INSTALL_ROOT"
