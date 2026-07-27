set -eu

umask 077

for required in readlink awk rm sleep; do
  command -v "$required" >/dev/null 2>&1 || exit 40
done

INSTALL_ROOT={{.InstallRoot}}
EXPORT_ID={{.ExportID}}
PROC_ROOT={{.ProcRoot}}
KILL_COMMAND={{.KillCommand}}

case "$INSTALL_ROOT" in
  /*) [ "$INSTALL_ROOT" != "/" ] || exit 40 ;;
  *) exit 40 ;;
esac
case "$EXPORT_ID" in
  ''|*[!A-Za-z0-9._-]*) exit 40 ;;
esac
case "$PROC_ROOT:$KILL_COMMAND" in
  /*:/*) ;;
  *) exit 40 ;;
esac
[ -x "$KILL_COMMAND" ] || exit 40

RUNTIME_ROOT="$INSTALL_ROOT/runtime"
DIAGNOSTICS_ROOT="$RUNTIME_ROOT/diagnostics"
PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"

[ -d "$INSTALL_ROOT" ] && [ ! -L "$INSTALL_ROOT" ] || exit 40
install_canonical=$(readlink -f -- "$INSTALL_ROOT") || exit 40
[ "$install_canonical" = "$INSTALL_ROOT" ] || exit 40
[ -d "$RUNTIME_ROOT" ] && [ ! -L "$RUNTIME_ROOT" ] || exit 40
runtime_canonical=$(readlink -f -- "$RUNTIME_ROOT") || exit 40
[ "$runtime_canonical" = "$RUNTIME_ROOT" ] || exit 40
if [ ! -e "$DIAGNOSTICS_ROOT" ] && [ ! -L "$DIAGNOSTICS_ROOT" ]; then
  exit 0
fi
[ -d "$DIAGNOSTICS_ROOT" ] && [ ! -L "$DIAGNOSTICS_ROOT" ] || exit 40
diagnostics_canonical=$(readlink -f -- "$DIAGNOSTICS_ROOT") || exit 40
[ "$diagnostics_canonical" = "$RUNTIME_ROOT/diagnostics" ] || exit 40
DIAGNOSTICS_ROOT=$diagnostics_canonical
PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"

for controlled_root in "$PARTIAL_ROOT"; do
  [ ! -L "$controlled_root" ] || exit 40
  if [ -e "$controlled_root" ]; then
    [ -d "$controlled_root" ] || exit 40
    controlled_canonical=$(readlink -f -- "$controlled_root") || exit 40
    [ "$controlled_canonical" = "$controlled_root" ] || exit 40
  fi
done

pid_file="$PARTIAL_ROOT/.collector.pid"
if [ -e "$pid_file" ] || [ -L "$pid_file" ]; then
  [ -f "$pid_file" ] && [ ! -L "$pid_file" ] || exit 41
  tab=$(printf '\t')
  IFS="$tab" read -r pid recorded_start recorded_pgid extra < "$pid_file" || exit 41
  case "$pid:$recorded_start:$recorded_pgid" in
    *[!0-9:]*) exit 41 ;;
  esac
  [ -z "${extra:-}" ] && [ "$recorded_pgid" = "$pid" ] || exit 41
  stat_file="$PROC_ROOT/$pid/stat"
  if [ -f "$stat_file" ]; then
    current_start=$(awk '{print $22}' "$stat_file") || exit 41
    current_pgid=$(awk '{print $5}' "$stat_file") || exit 41
    case "$current_start:$current_pgid" in
      *[!0-9:]*) exit 41 ;;
    esac
    [ "$current_start" = "$recorded_start" ] && [ "$current_pgid" = "$recorded_pgid" ] || exit 41
    "$KILL_COMMAND" -TERM -- "-$recorded_pgid" 2>/dev/null || true
    attempts=0
    while "$KILL_COMMAND" -0 -- "-$recorded_pgid" 2>/dev/null && [ "$attempts" -lt 10 ]; do
      attempts=$((attempts + 1))
      sleep 1
    done
    if "$KILL_COMMAND" -0 -- "-$recorded_pgid" 2>/dev/null; then
      "$KILL_COMMAND" -KILL -- "-$recorded_pgid" 2>/dev/null || true
      attempts=0
      while "$KILL_COMMAND" -0 -- "-$recorded_pgid" 2>/dev/null && [ "$attempts" -lt 5 ]; do
        attempts=$((attempts + 1))
        sleep 1
      done
    fi
    if "$KILL_COMMAND" -0 -- "-$recorded_pgid" 2>/dev/null; then
      exit 42
    fi
  elif "$KILL_COMMAND" -0 -- "-$recorded_pgid" 2>/dev/null; then
    # The leader disappeared while children in its recorded group remain. Without
    # the leader starttime there is no safe identity proof for signalling the group.
    exit 42
  fi
fi

rm -rf -- "$PARTIAL_ROOT"
[ ! -e "$PARTIAL_ROOT" ] && [ ! -L "$PARTIAL_ROOT" ]
