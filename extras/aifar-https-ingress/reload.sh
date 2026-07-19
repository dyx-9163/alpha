#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
# shellcheck disable=SC1091
. "$ROOT/config.env"
CONTAINER_NAME="${AIFAR_HTTPS_CONTAINER_NAME:-aifar-https-ingress}"

docker exec "$CONTAINER_NAME" nginx -t
docker exec "$CONTAINER_NAME" nginx -s reload
echo "[aifar-https-ingress] configuration reloaded"
