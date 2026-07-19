#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
CONFIG_FILE="$ROOT/config.env"

fail() {
  echo "[aifar-https-ingress] $*" >&2
  exit 1
}

[ -f "$CONFIG_FILE" ] || fail "missing config: $CONFIG_FILE"
# This is a trusted local module configuration file, not user request input.
# shellcheck disable=SC1090
. "$CONFIG_FILE"

CONTAINER_NAME="${AIFAR_HTTPS_CONTAINER_NAME:-aifar-https-ingress}"
IMAGE="${AIFAR_HTTPS_IMAGE:-nginx:stable-alpine}"
WEB_PORT="${AIFAR_HTTPS_WEB_PORT:-8080}"
GATEWAY_PORT="${AIFAR_HTTPS_GATEWAY_PORT:-38000}"

command -v docker >/dev/null 2>&1 || fail "docker command is not installed"
[ -f "$ROOT/conf.d/aifar.conf" ] || fail "missing Nginx configuration"
[ -s "$ROOT/tls/fullchain.pem" ] || fail "missing TLS certificate"
[ -s "$ROOT/tls/privkey.pem" ] || fail "missing TLS private key"
chmod 0600 "$ROOT/tls/privkey.pem"

if docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
  if [ "$(docker inspect -f '{{.State.Running}}' "$CONTAINER_NAME")" = "true" ]; then
    docker exec "$CONTAINER_NAME" nginx -t
    echo "[aifar-https-ingress] already running: $CONTAINER_NAME"
    exit 0
  fi
  docker rm "$CONTAINER_NAME" >/dev/null
fi

docker image inspect "$IMAGE" >/dev/null 2>&1 || fail "Docker image is unavailable: $IMAGE"

docker run --rm --network host --entrypoint /bin/sh "$IMAGE" -ec \
  "nc -z 127.0.0.1 $WEB_PORT && nc -z 127.0.0.1 $GATEWAY_PORT" \
  || fail "AIFAR upstream listeners are unavailable on ports $WEB_PORT and/or $GATEWAY_PORT"

if command -v ss >/dev/null 2>&1; then
  for port in 80 443; do
    if ss -ltn 2>/dev/null | grep -E "[:.]${port}[[:space:]]" >/dev/null 2>&1; then
      fail "host port $port is already in use"
    fi
  done
fi

docker run --rm --network host \
  -v "$ROOT/conf.d:/etc/nginx/conf.d:ro,Z" \
  -v "$ROOT/tls:/etc/nginx/tls:ro,Z" \
  "$IMAGE" nginx -t

docker run -d \
  --name "$CONTAINER_NAME" \
  --restart unless-stopped \
  --network host \
  -v "$ROOT/conf.d:/etc/nginx/conf.d:ro,Z" \
  -v "$ROOT/tls:/etc/nginx/tls:ro,Z" \
  "$IMAGE" >/dev/null

docker exec "$CONTAINER_NAME" nginx -t >/dev/null
echo "[aifar-https-ingress] started: https://aifar.local/"
