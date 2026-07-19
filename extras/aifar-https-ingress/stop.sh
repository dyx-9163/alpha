#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
# shellcheck disable=SC1091
. "$ROOT/config.env"
CONTAINER_NAME="${AIFAR_HTTPS_CONTAINER_NAME:-aifar-https-ingress}"

if ! command -v docker >/dev/null 2>&1; then
  echo "[aifar-https-ingress] docker command is not installed" >&2
  exit 1
fi

if ! docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
  echo "[aifar-https-ingress] already stopped"
  exit 0
fi

docker stop "$CONTAINER_NAME" >/dev/null
docker rm "$CONTAINER_NAME" >/dev/null
echo "[aifar-https-ingress] stopped"
