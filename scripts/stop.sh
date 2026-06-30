#!/usr/bin/env sh
set -eu
ROOT="$(CDPATH= cd "$(dirname "$0")" && pwd)"
PID_FILE="$ROOT/run/aifar.pid"
if [ ! -f "$PID_FILE" ]; then
  echo "AIFAR is not running: pid file not found"
  exit 0
fi
PID="$(cat "$PID_FILE" 2>/dev/null || true)"
if [ -z "$PID" ]; then
  rm -f "$PID_FILE"
  echo "AIFAR is not running: empty pid file removed"
  exit 0
fi
if kill -0 "$PID" 2>/dev/null; then
  kill "$PID"
  echo "AIFAR stopped: pid=$PID"
else
  echo "AIFAR is not running: stale pid=$PID"
fi
rm -f "$PID_FILE"
