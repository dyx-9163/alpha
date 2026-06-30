#!/usr/bin/env sh
set -eu
ROOT="$(CDPATH= cd "$(dirname "$0")" && pwd)"
if [ -f "$ROOT/config/defaults.env" ]; then
  set -a
  . "$ROOT/config/defaults.env"
  set +a
fi
if [ -z "${AIFAR_DEFAULT_PASSWORD:-}" ]; then
  export AIFAR_DEFAULT_PASSWORD="Oversea.123"
fi
if [ -z "${AIFAR_BOOTSTRAP_PASSWORD:-}" ]; then
  export AIFAR_BOOTSTRAP_PASSWORD="$AIFAR_DEFAULT_PASSWORD"
fi
export AIFAR_DEFAULT_DEPLOY_DIR="${AIFAR_DEFAULT_DEPLOY_DIR:-/aifar/apps}"
export AIFAR_STATIC_DIR="${AIFAR_STATIC_DIR:-$ROOT/web/dist}"
export AIFAR_RESOURCE_DIR="${AIFAR_RESOURCE_DIR:-$ROOT/resources}"
export AIFAR_ADDR="${AIFAR_ADDR:-0.0.0.0:8080}"
BIN="$ROOT/bin/aifar-server-linux-amd64"
RUN_DIR="$ROOT/run"
LOG_DIR="$ROOT/logs"
PID_FILE="$RUN_DIR/aifar.pid"
LOG_FILE="$LOG_DIR/aifar.log"
mkdir -p "$RUN_DIR" "$LOG_DIR"
if [ ! -f "$BIN" ]; then
  echo "Missing backend binary: $BIN" >&2
  echo "Build it with scripts/package.sh or extract bin from aifar-deployment.zip." >&2
  exit 1
fi
chmod +x "$BIN" 2>/dev/null || true
cd "$ROOT"
if [ "${1:-}" = "foreground" ]; then
  exec "$BIN"
fi
if [ -f "$PID_FILE" ]; then
  OLD_PID="$(cat "$PID_FILE" 2>/dev/null || true)"
  if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null; then
    echo "AIFAR is already running: pid=$OLD_PID"
    echo "Log: $LOG_FILE"
    exit 0
  fi
fi
nohup "$BIN" >> "$LOG_FILE" 2>&1 &
PID="$!"
echo "$PID" > "$PID_FILE"
echo "AIFAR started in background: pid=$PID"
echo "Address: $AIFAR_ADDR"
echo "Log: $LOG_FILE"
