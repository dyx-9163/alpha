#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
# shellcheck disable=SC1091
. "$ROOT/config.env"
CONTAINER_NAME="${AIFAR_HTTPS_CONTAINER_NAME:-aifar-https-ingress}"

if ! docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
  echo "[aifar-https-ingress] not installed"
  exit 3
fi

docker ps --filter "name=^/${CONTAINER_NAME}$" --format 'table {{.Names}}\t{{.Status}}\t{{.Image}}'
if [ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER_NAME")" != "true" ]; then
  echo "[aifar-https-ingress] stopped"
  exit 3
fi

docker exec "$CONTAINER_NAME" nginx -t
echo "[aifar-https-ingress] running"
