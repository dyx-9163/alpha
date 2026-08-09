#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_NAME={{ quote .ServiceName }}
REPLICAS={{ .Replicas }}
SERVICE_ENV="$INSTALL_ROOT/runtime/env/$SERVICE_NAME.env"

[ -f "$SERVICE_ENV" ] || { echo "ERROR: service environment is missing" >&2; exit 1; }
tmp="$SERVICE_ENV.tmp"
grep -v '^AIFAR_DESIRED_REPLICAS=' "$SERVICE_ENV" > "$tmp" || true
printf "AIFAR_DESIRED_REPLICAS=%s\n" "$REPLICAS" >> "$tmp"
mv "$tmp" "$SERVICE_ENV"
