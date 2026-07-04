#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
NETWORK_NAME={{ quote .NetworkName }}
SERVICE_ORDER={{ quote .ServiceOrder }}
CURRENT_LINK="$INSTALL_ROOT/current"
APP_DIR="$INSTALL_ROOT/docker-apps"
RELEASES_DIR="$INSTALL_ROOT/releases"
INGRESS_CONTAINER="aifar-admin-ingress"

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
  if command -v aifar-agent >/dev/null 2>&1; then
    aifar-agent remove-instance --instance admin >/dev/null 2>&1 || true
  fi
  managed="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$managed" ] || docker rm -f $managed >/dev/null 2>&1 || true
  docker rm -f "$INGRESS_CONTAINER" >/dev/null 2>&1 || true
  if [ -L "$CURRENT_LINK" ] || [ -d "$CURRENT_LINK" ]; then
    current_release="$(readlink -f "$CURRENT_LINK" 2>/dev/null || printf "%s" "$CURRENT_LINK")"
    down_release "$current_release"
  fi
  if [ -d "$RELEASES_DIR" ]; then
    find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d | while read -r release_dir; do
      down_release "$release_dir"
    done
  fi
  down_legacy
fi

rm -rf "$INSTALL_ROOT"

if command -v docker >/dev/null 2>&1 && [ -n "$NETWORK_NAME" ]; then
  docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
fi

echo "AIFAR service removed from $INSTALL_ROOT"
