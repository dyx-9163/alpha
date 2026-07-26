set -eu

umask 077

for required in sed awk ps kill sleep rm; do
  command -v "$required" >/dev/null 2>&1 || exit 40
done

INSTALL_ROOT={{.InstallRoot}}
EXPORT_ID={{.ExportID}}

case "$INSTALL_ROOT" in
  /*) [ "$INSTALL_ROOT" != "/" ] || exit 40 ;;
  *) exit 40 ;;
esac
case "$EXPORT_ID" in
  ''|*[!A-Za-z0-9._-]*) exit 40 ;;
esac

DIAGNOSTICS_ROOT="$INSTALL_ROOT/runtime/diagnostics"
PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"
FINAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID"
pid_file="$PARTIAL_ROOT/.collector.pid"

if [ -f "$pid_file" ]; then
  pid=$(sed -n '1p' "$pid_file")
  case "$pid" in
    ''|*[!0-9]*) pid="" ;;
  esac
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    pgid=$(ps -o pgid= -p "$pid" 2>/dev/null | awk '{print $1}')
    if [ "$pgid" = "$pid" ]; then
      kill -TERM -- "-$pid" 2>/dev/null || true
      attempts=0
      while kill -0 "$pid" 2>/dev/null && [ "$attempts" -lt 10 ]; do
        attempts=$((attempts + 1))
        sleep 1
      done
      if kill -0 "$pid" 2>/dev/null; then
        kill -KILL -- "-$pid" 2>/dev/null || true
      fi
    fi
  fi
fi

rm -rf -- "$PARTIAL_ROOT" "$FINAL_ROOT"
[ ! -e "$PARTIAL_ROOT" ]
[ ! -e "$FINAL_ROOT" ]
