#!/usr/bin/env sh
set -eu
ROOT="$(CDPATH= cd "$(dirname "$0")" && pwd)"
if [ -f "$ROOT/config/defaults.env" ]; then
  seen_config_keys='|'
  line_number=0
  while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    trimmed=$(printf '%s' "$line" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    case "$trimmed" in
      ''|'#'*) continue ;;
    esac
    case "$trimmed" in
      *=*) ;;
      *)
        echo "Malformed defaults.env line $line_number" >&2
        exit 1
        ;;
    esac
    key=${trimmed%%=*}
    value=${trimmed#*=}
    case "$key" in
      AIFAR_?*) ;;
      *)
        echo "Malformed defaults.env line $line_number" >&2
        exit 1
        ;;
    esac
    case "$key" in
      *[!A-Z0-9_]*)
        echo "Malformed defaults.env line $line_number" >&2
        exit 1
        ;;
    esac
    case "$seen_config_keys" in
      *"|$key|"*)
        echo "Duplicate configuration key in defaults.env: $key" >&2
        exit 1
        ;;
    esac
    seen_config_keys="${seen_config_keys}${key}|"
    eval "is_set=\${$key+x}"
    if [ "$is_set" != "x" ]; then
      value=$(printf '%s' "$value" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
      case "$value" in
        \"*\") value=${value#\"}; value=${value%\"} ;;
        \'*\') value=${value#\'}; value=${value%\'} ;;
      esac
      export "$key=$value"
    fi
  done < "$ROOT/config/defaults.env"
fi
if [ "${AIFAR_DEFAULT_DEPLOY_DIR+x}" != "x" ]; then
  export AIFAR_DEFAULT_DEPLOY_DIR="/aifar/apps"
fi
if [ "${AIFAR_STATIC_DIR+x}" != "x" ]; then
  export AIFAR_STATIC_DIR="$ROOT/web/dist"
fi
if [ "${AIFAR_RESOURCE_DIR+x}" != "x" ]; then
  export AIFAR_RESOURCE_DIR="$ROOT/resources"
fi
if [ "${AIFAR_ADDR+x}" != "x" ]; then
  export AIFAR_ADDR="0.0.0.0:8080"
fi
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
