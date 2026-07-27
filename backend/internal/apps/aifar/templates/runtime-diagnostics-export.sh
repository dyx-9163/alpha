set -eu

umask 077

for required in docker find xargs readlink stat sed awk grep tr sha256sum tar gzip du df free uptime systemctl cat cp mv mkdir chmod rm sort wc; do
  command -v "$required" >/dev/null 2>&1 || exit 20
done

INSTALL_ROOT={{.InstallRoot}}
EXPORT_ID={{.ExportID}}
INSTANCE_ID={{.InstanceID}}
SERVICES={{.Services}}
SINCE={{.Since}}
UNTIL={{.Until}}
ARCHIVE_BASE={{.ArchiveBase}}
RUNTIME_SUMMARY_JSON={{.RuntimeSummary}}
DEPLOYMENTS_JSON={{.Deployments}}
PODS_JSON={{.Pods}}
RELEASE_SUMMARY_JSON={{.ReleaseSummary}}
README_TEXT={{.Readme}}
PROC_ROOT={{.ProcRoot}}
FILE_LIMIT_BLOCKS={{.FileLimitBlocks}}

case "$INSTALL_ROOT" in
  /*) [ "$INSTALL_ROOT" != "/" ] || exit 21 ;;
  *) exit 21 ;;
esac
case "$EXPORT_ID" in
  ''|*[!A-Za-z0-9._-]*) exit 21 ;;
esac
case "$ARCHIVE_BASE" in
  aifar-diagnostics-*[!A-Za-z0-9._-]*|'') exit 21 ;;
  aifar-diagnostics-*) ;;
  *) exit 21 ;;
esac
case "$FILE_LIMIT_BLOCKS" in
  unlimited|*[!0-9]*|'') [ "$FILE_LIMIT_BLOCKS" = "unlimited" ] || exit 21 ;;
esac

RUNTIME_ROOT="$INSTALL_ROOT/runtime"
DIAGNOSTICS_ROOT="$RUNTIME_ROOT/diagnostics"
LOG_ROOT="$RUNTIME_ROOT/logs"
PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"
FINAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID"
BUNDLE_PARENT="$PARTIAL_ROOT/bundle"
BUNDLE_ROOT="$BUNDLE_PARENT/$ARCHIVE_BASE"
WORK_ROOT="$PARTIAL_ROOT/work"
RAW_ROOT="$WORK_ROOT/raw"
STAGE_ROOT="$WORK_ROOT/staged"
SCRATCH_ROOT="$WORK_ROOT/redact"
LIMIT_MARKER_ROOT="$WORK_ROOT/limit-markers"
ARCHIVE_NAME="$ARCHIVE_BASE.tar.gz"
ARCHIVE_PARTIAL="$WORK_ROOT/$ARCHIVE_NAME.partial"
TOTAL_FILE="$WORK_ROOT/uncompressed-bytes"
MANIFEST_RECORDS="$WORK_ROOT/manifest-records"
ERROR_RECORDS="$WORK_ROOT/collection-errors"
CANDIDATE_LIST="$WORK_ROOT/file-log-candidates"
FILE_HELPER="$WORK_ROOT/stage-file-log.sh"
MAX_UNCOMPRESSED=3221225472
MAX_ARCHIVE=1073741824

[ -d "$INSTALL_ROOT" ] && [ ! -L "$INSTALL_ROOT" ] || exit 22
install_canonical=$(readlink -f -- "$INSTALL_ROOT") || exit 22
[ "$install_canonical" = "$INSTALL_ROOT" ] || exit 22
[ -d "$RUNTIME_ROOT" ] && [ ! -L "$RUNTIME_ROOT" ] || exit 22
runtime_canonical=$(readlink -f -- "$RUNTIME_ROOT") || exit 22
[ "$runtime_canonical" = "$RUNTIME_ROOT" ] || exit 22
if [ -e "$DIAGNOSTICS_ROOT" ] || [ -L "$DIAGNOSTICS_ROOT" ]; then
  [ -d "$DIAGNOSTICS_ROOT" ] && [ ! -L "$DIAGNOSTICS_ROOT" ] || exit 22
else
  mkdir -- "$DIAGNOSTICS_ROOT" || exit 22
fi
diagnostics_canonical=$(readlink -f -- "$DIAGNOSTICS_ROOT") || exit 22
[ "$diagnostics_canonical" = "$RUNTIME_ROOT/diagnostics" ] || exit 22
DIAGNOSTICS_ROOT=$diagnostics_canonical
chmod 700 "$DIAGNOSTICS_ROOT"
if [ -e "$LOG_ROOT" ] || [ -L "$LOG_ROOT" ]; then
  [ -d "$LOG_ROOT" ] && [ ! -L "$LOG_ROOT" ] || exit 22
  logs_canonical=$(readlink -f -- "$LOG_ROOT") || exit 22
  [ "$logs_canonical" = "$RUNTIME_ROOT/logs" ] || exit 22
  LOG_ROOT=$logs_canonical
fi
[ ! -e "$PARTIAL_ROOT" ] && [ ! -L "$PARTIAL_ROOT" ] || exit 22
[ ! -e "$FINAL_ROOT" ] && [ ! -L "$FINAL_ROOT" ] || exit 22
mkdir -- "$PARTIAL_ROOT" || exit 22
[ -d "$PARTIAL_ROOT" ] && [ ! -L "$PARTIAL_ROOT" ] || exit 22
partial_canonical=$(readlink -f -- "$PARTIAL_ROOT") || exit 22
[ "$partial_canonical" = "$DIAGNOSTICS_ROOT/$EXPORT_ID.partial" ] || exit 22
PARTIAL_ROOT=$partial_canonical
mkdir -p "$BUNDLE_ROOT/services" "$BUNDLE_ROOT/diagnostics" "$RAW_ROOT" "$STAGE_ROOT" "$SCRATCH_ROOT" "$LIMIT_MARKER_ROOT"
cd "$PARTIAL_ROOT"

pid_stat="$PROC_ROOT/$$/stat"
[ -f "$pid_stat" ] || exit 22
pid_start=$(awk '{print $22}' "$pid_stat") || exit 22
pid_pgid=$(awk '{print $5}' "$pid_stat") || exit 22
case "$pid_start:$pid_pgid" in
  *[!0-9:]*) exit 22 ;;
esac
[ "$pid_pgid" = "$$" ] || exit 22
printf '%s\t%s\t%s\n' "$$" "$pid_start" "$pid_pgid" > "$PARTIAL_ROOT/.collector.pid"
trap 'touch "$PARTIAL_ROOT/.cancelled" 2>/dev/null || true' INT TERM
printf '0\n' > "$TOTAL_FILE"
: > "$MANIFEST_RECORDS"
: > "$ERROR_RECORDS"

run_limited() {
  LIMIT_SETUP_FAILED=0
  limit_blocks=$1
  shift
  case "$limit_blocks" in
    unlimited|*[!0-9]*|'')
      if [ "$limit_blocks" != "unlimited" ]; then LIMIT_SETUP_FAILED=1; return 1; fi
      ;;
  esac
  limit_marker="$LIMIT_MARKER_ROOT/limit-setup-$$"
  rm -f -- "$limit_marker" || { LIMIT_SETUP_FAILED=1; return 1; }
  : > "$limit_marker" || { LIMIT_SETUP_FAILED=1; return 1; }
  if (
    ulimit -f "$limit_blocks" &&
    rm -f -- "$limit_marker" &&
    "$@"
  ); then
    command_status=0
  else
    command_status=$?
  fi
  if [ -e "$limit_marker" ]; then
    LIMIT_SETUP_FAILED=1
    rm -f -- "$limit_marker" || return 1
    return 1
  fi
  return "$command_status"
}

json_escape() {
  LC_ALL=C awk '
    BEGIN { ORS = ""; escape = sprintf("%c", 92) }
    {
      if (NR > 1) printf "%s%s", escape, "n"
      for (i = 1; i <= length($0); i++) {
        c = substr($0, i, 1)
        if (c == escape) printf "%s%s", escape, escape
        else if (c == "\"") printf "%s%s", escape, c
        else if (c == "\t") printf "%s%s", escape, "t"
        else if (c == "\r") printf "%s%s", escape, "r"
        else if (c == sprintf("%c", 8)) printf "%s%s", escape, "b"
        else if (c == sprintf("%c", 12)) printf "%s%s", escape, "f"
        else printf "%s", c
      }
    }
  '
}

add_error() {
  code=$1
  service=$2
  case "$code" in ''|*[!a-z0-9-]*) exit 23 ;; esac
  case "$service" in -|*[!a-z0-9-]*) [ "$service" = "-" ] || exit 23 ;; esac
  printf '%s\t%s\t-\n' "$code" "$service" >> "$ERROR_RECORDS"
}

redact_file() {
  source_file=$1
  target_file=$2
  scratch_prefix=$3
  phase_one="$scratch_prefix-1"
  phase_two="$scratch_prefix-2"
  phase_three="$scratch_prefix-3"
  rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"
  if ! sed -E \
    -e 's/(Authorization:[[:space:]]*(Basic|Bearer))[[:space:]]+[^[:space:]]+/\1 [REDACTED]/Ig' \
    -e 's/("?(password|passwd|secret|token|api[_-]?key|access[_-]?key)"?[[:space:]]*[:=][[:space:]]*)("[^"]*"|[^,;[:space:]]+)/\1"[REDACTED]"/Ig' \
    -- "$source_file" > "$phase_one"; then
    rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"
    return 1
  fi
  if ! sed -E \
    -e 's#(https?://)[^/@[:space:]]+:[^/@[:space:]]+@#\1[REDACTED]@#Ig' \
    -e 's/([?&](password|passwd|secret|token|api[_-]?key|access[_-]?key)=)[^&[:space:]]+/\1[REDACTED]/Ig' \
    -- "$phase_one" > "$phase_two"; then
    rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"
    return 1
  fi
  if ! awk '
    BEGIN { in_private_key = 0 }
    /-----BEGIN .*PRIVATE KEY-----/ { print "[REDACTED PRIVATE KEY]"; in_private_key = 1; next }
    in_private_key && /-----END .*PRIVATE KEY-----/ { in_private_key = 0; next }
    in_private_key { next }
    { print }
  ' "$phase_two" > "$phase_three"; then
    rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"
    return 1
  fi
  if ! sed -E -e 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' -- "$phase_three" > "$target_file"; then
    rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"
    return 1
  fi
  rm -f -- "$phase_one" "$phase_two" "$phase_three" || return 1
}

remaining_bytes() {
  used=$(cat "$TOTAL_FILE") || exit 23
  case "$used" in ''|*[!0-9]*) exit 23 ;; esac
  [ "$used" -le "$MAX_UNCOMPRESSED" ] || exit 23
  printf '%s\n' $((MAX_UNCOMPRESSED - used))
}

commit_staged_file() {
  staged_bytes=$1
  used=$(cat "$TOTAL_FILE") || exit 23
  case "$staged_bytes:$used" in *[!0-9:]*) exit 23 ;; esac
  next=$((used + staged_bytes))
  [ "$next" -le "$MAX_UNCOMPRESSED" ] || return 1
  printf '%s\n' "$next" > "$TOTAL_FILE"
}

record_manifest_source() {
  printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$MANIFEST_RECORDS"
}

stage_generated_text() {
  relative_name=$1
  source_name=$2
  content=$3
  destination="$BUNDLE_ROOT/$relative_name"
  staged="$STAGE_ROOT/$relative_name"
  mkdir -p "$(dirname "$destination")" "$(dirname "$staged")"
  printf '%s\n' "$content" > "$staged"
  staged_bytes=$(stat -c '%s' -- "$staged") || exit 24
  commit_staged_file "$staged_bytes" || exit 24
  mv -T -- "$staged" "$destination" || exit 24
  record_manifest_source "$source_name" "$relative_name" "$staged_bytes"
}

stage_generated_file() {
  relative_name=$1
  source_name=$2
  source_file=$3
  destination="$BUNDLE_ROOT/$relative_name"
  mkdir -p "$(dirname "$destination")"
  staged_bytes=$(stat -c '%s' -- "$source_file") || exit 24
  commit_staged_file "$staged_bytes" || exit 24
  mv -T -- "$source_file" "$destination" || exit 24
  record_manifest_source "$source_name" "$relative_name" "$staged_bytes"
}

stage_generated_redacted_file() {
  relative_name=$1
  source_name=$2
  source_file=$3
  destination="$BUNDLE_ROOT/$relative_name"
  staged="$STAGE_ROOT/$relative_name"
  scratch="$SCRATCH_ROOT/$relative_name"
  mkdir -p "$(dirname "$destination")" "$(dirname "$staged")" "$(dirname "$scratch")"
  original_bytes=$(stat -c '%s' -- "$source_file") || exit 24
  remaining=$(remaining_bytes)
  [ "$remaining" -gt 0 ] || exit 24
  run_limited "$FILE_LIMIT_BLOCKS" redact_file "$source_file" "$staged" "$scratch" || exit 24
  rm -f -- "$source_file" || exit 24
  [ -f "$staged" ] && [ ! -L "$staged" ] || exit 24
  staged_bytes=$(stat -c '%s' -- "$staged") || exit 24
  [ "$staged_bytes" -le "$remaining" ] || exit 24
  commit_staged_file "$staged_bytes" || exit 24
  mv -T -- "$staged" "$destination" || exit 24
  record_manifest_source "$source_name" "$relative_name" "$original_bytes"
}

cat > "$FILE_HELPER" <<'AIFAR_DIAGNOSTIC_FILE_HELPER'
set -eu
run_limited() {
  LIMIT_SETUP_FAILED=0
  limit_blocks=$1
  shift
  case "$limit_blocks" in
    unlimited|*[!0-9]*|'')
      if [ "$limit_blocks" != "unlimited" ]; then LIMIT_SETUP_FAILED=1; return 1; fi
      ;;
  esac
  limit_marker="$LIMIT_MARKER_ROOT/limit-setup-$$"
  rm -f -- "$limit_marker" || { LIMIT_SETUP_FAILED=1; return 1; }
  : > "$limit_marker" || { LIMIT_SETUP_FAILED=1; return 1; }
  if (
    ulimit -f "$limit_blocks" &&
    rm -f -- "$limit_marker" &&
    "$@"
  ); then
    command_status=0
  else
    command_status=$?
  fi
  if [ -e "$limit_marker" ]; then
    LIMIT_SETUP_FAILED=1
    rm -f -- "$limit_marker" || return 1
    return 1
  fi
  return "$command_status"
}
redact_file() {
  source_file=$1
  target_file=$2
  scratch_prefix=$3
  phase_one="$scratch_prefix-1"
  phase_two="$scratch_prefix-2"
  phase_three="$scratch_prefix-3"
  rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"
  if ! sed -E \
    -e 's/(Authorization:[[:space:]]*(Basic|Bearer))[[:space:]]+[^[:space:]]+/\1 [REDACTED]/Ig' \
    -e 's/("?(password|passwd|secret|token|api[_-]?key|access[_-]?key)"?[[:space:]]*[:=][[:space:]]*)("[^"]*"|[^,;[:space:]]+)/\1"[REDACTED]"/Ig' \
    -- "$source_file" > "$phase_one"; then rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"; return 1; fi
  if ! sed -E \
    -e 's#(https?://)[^/@[:space:]]+:[^/@[:space:]]+@#\1[REDACTED]@#Ig' \
    -e 's/([?&](password|passwd|secret|token|api[_-]?key|access[_-]?key)=)[^&[:space:]]+/\1[REDACTED]/Ig' \
    -- "$phase_one" > "$phase_two"; then rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"; return 1; fi
  if ! awk '
    BEGIN { in_private_key = 0 }
    /-----BEGIN .*PRIVATE KEY-----/ { print "[REDACTED PRIVATE KEY]"; in_private_key = 1; next }
    in_private_key && /-----END .*PRIVATE KEY-----/ { in_private_key = 0; next }
    in_private_key { next }
    { print }
  ' "$phase_two" > "$phase_three"; then rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"; return 1; fi
  if ! sed -E -e 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' -- "$phase_three" > "$target_file"; then
    rm -f -- "$target_file" "$phase_one" "$phase_two" "$phase_three"
    return 1
  fi
  rm -f -- "$phase_one" "$phase_two" "$phase_three" || return 1
}
add_error() {
  code=$1
  service=$2
  case "$code" in ''|*[!a-z0-9-]*) exit 31 ;; esac
  case "$service" in -|*[!a-z0-9-]*) [ "$service" = "-" ] || exit 31 ;; esac
  printf '%s\t%s\t-\n' "$code" "$service" >> "$ERROR_RECORDS"
}
is_safe_log_relative() {
  relative_name=$1
  case "$relative_name" in
    ''|/*|*//*|*[!A-Za-z0-9._/@+-]*) return 1 ;;
  esac
  previous_ifs=$IFS
  IFS=/
  set -- $relative_name
  IFS=$previous_ifs
  [ "$#" -gt 0 ] || return 1
  last_component=
  for component do
    case "$component" in ''|.*) return 1 ;; esac
    component_lower=$(printf '%s' "$component" | tr '[:upper:]' '[:lower:]') || return 1
    case "$component_lower" in
      config|configs|configuration|data|database|db|credential|credentials|secret|secrets|password|passwords|token|tokens|key|keys|cert|certs|certificate|certificates|private|.ssh|config.*|data.*|database.*|db.*|credential.*|credentials.*|secret.*|secrets.*|password.*|passwords.*|token.*|tokens.*|private.*|id_*) return 1 ;;
    esac
    last_component=$component_lower
  done
  case "$last_component" in
    *.log) return 0 ;;
    *.log.*)
      rotation=${last_component##*.log.}
      case "$rotation" in ''|*[!0-9]*) return 1 ;; esac
      return 0
      ;;
    *) return 1 ;;
  esac
}
service=$1
service_root=$2
shift 2
counter=0
for candidate do
  counter=$((counter + 1))
  resolved=$(readlink -f -- "$candidate") || { add_error file-resolve-failed "$service"; continue; }
  case "$resolved" in "$service_root"/*) ;; *) add_error file-prefix-rejected "$service"; continue ;; esac
  relative_name=${resolved#"$service_root"/}
  if ! is_safe_log_relative "$relative_name"; then
    add_error file-name-rejected "$service"
    continue
  fi
  stat_resolved=$(readlink -f -- "$candidate") || { add_error file-resolve-failed "$service"; continue; }
  case "$stat_resolved" in "$service_root"/*) ;; *) add_error file-prefix-rejected "$service"; continue ;; esac
  [ "$stat_resolved" = "$resolved" ] || { add_error file-path-changed "$service"; continue; }
  copy_source=$(readlink -f -- "$candidate") || { add_error file-resolve-failed "$service"; continue; }
  case "$copy_source" in "$service_root"/*) ;; *) add_error file-prefix-rejected "$service"; continue ;; esac
  [ "$copy_source" = "$resolved" ] || { add_error file-path-changed "$service"; continue; }
  source_identity=$(stat -Lc '%d:%i:%s' -- "$candidate") || { add_error file-stat-failed "$service"; continue; }
  source_device=${source_identity%%:*}
  source_remainder=${source_identity#*:}
  source_inode=${source_remainder%%:*}
  source_bytes=${source_remainder#*:}
  case "$source_device:$source_inode:$source_bytes" in *[!0-9:]*) add_error file-stat-failed "$service"; continue ;; esac
  used=$(cat "$TOTAL_FILE") || exit 31
  case "$used" in ''|*[!0-9]*) exit 31 ;; esac
  [ "$used" -le "$MAX_UNCOMPRESSED" ] || exit 31
  remaining=$((MAX_UNCOMPRESSED - used))
  if [ "$source_bytes" -gt "$remaining" ]; then add_error uncompressed-limit "$service"; continue; fi
  remaining_blocks=$((remaining / 1024))
  [ "$remaining_blocks" -gt 0 ] || { add_error uncompressed-limit "$service"; continue; }
  case "$FILE_LIMIT_BLOCKS" in
    unlimited) snapshot_blocks=$remaining_blocks ;;
    *)
      snapshot_blocks=$FILE_LIMIT_BLOCKS
      [ "$snapshot_blocks" -le "$remaining_blocks" ] || snapshot_blocks=$remaining_blocks
      ;;
  esac
  raw="$RAW_ROOT/files/$service/$counter.raw"
  mkdir -p "$(dirname "$raw")"
  if run_limited "$snapshot_blocks" cp -P -- "$candidate" "$raw"; then
    :
  else
    command_status=$?
    rm -f -- "$raw"
    [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 31
    add_error file-copy-limit "$service"
    continue
  fi
  if [ -L "$raw" ] || [ ! -f "$raw" ]; then rm -f -- "$raw"; add_error file-path-changed "$service"; continue; fi
  raw_resolved=$(readlink -f -- "$raw") || exit 31
  case "$raw_resolved" in "$RAW_ROOT"/*) ;; *) exit 31 ;; esac
  original_bytes=$(stat -c '%s' -- "$raw") || exit 31
  case "$original_bytes" in ''|*[!0-9]*) exit 31 ;; esac
  if [ "$original_bytes" -ne "$source_bytes" ] || [ "$original_bytes" -gt "$remaining" ]; then
    rm -f -- "$raw"
    add_error file-source-changed "$service"
    continue
  fi
  copied_source_identity=$(stat -Lc '%d:%i:%s' -- "$candidate") || { rm -f -- "$raw"; add_error file-source-changed "$service"; continue; }
  if [ "$copied_source_identity" != "$source_identity" ]; then
    rm -f -- "$raw"
    add_error file-source-changed "$service"
    continue
  fi
  destination_relative="services/$service/file-logs/$relative_name"
  destination="$BUNDLE_ROOT/$destination_relative"
  staged="$STAGE_ROOT/$destination_relative"
  scratch="$SCRATCH_ROOT/$destination_relative"
  mkdir -p "$(dirname "$destination")" "$(dirname "$staged")" "$(dirname "$scratch")"
  run_limited "$FILE_LIMIT_BLOCKS" redact_file "$raw" "$staged" "$scratch" || exit 31
  rm -f -- "$raw" || exit 31
  [ -f "$staged" ] && [ ! -L "$staged" ] || exit 31
  staged_bytes=$(stat -c '%s' -- "$staged") || exit 31
  [ "$staged_bytes" -le "$remaining" ] || exit 31
  printf '%s\n' $((used + staged_bytes)) > "$TOTAL_FILE"
  mv -T -- "$staged" "$destination" || exit 31
  printf 'file:%s/%s\t%s\t%s\n' "$service" "$relative_name" "$destination_relative" "$original_bytes" >> "$MANIFEST_RECORDS"
done
AIFAR_DIAGNOSTIC_FILE_HELPER

revalidate_container_identity() {
  expected_service=$1
  container_id=$2
  identity_file=$3
  case "$container_id" in ''|*[!A-Fa-f0-9]*) add_error container-identity-changed "$expected_service"; return 1 ;; esac
  if run_limited "$FILE_LIMIT_BLOCKS" docker inspect --format '{{"{{index .Config.Labels \"aifar.instance\"}}\t{{index .Config.Labels \"aifar.service\"}}\t{{.Name}}"}}' "$container_id" > "$identity_file" 2>&1; then
    identity=$(cat "$identity_file") || exit 32
    rm -f -- "$identity_file" || exit 32
  else
    command_status=$?
    rm -f -- "$identity_file"
    [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 32
    add_error container-identity-changed "$expected_service"
    return 1
  fi
  case "$identity" in *$'\t'*$'\t'*) ;; *) add_error container-identity-changed "$expected_service"; return 1 ;; esac
  actual_instance=${identity%%$'\t'*}
  identity_rest=${identity#*$'\t'}
  actual_service=${identity_rest%%$'\t'*}
  actual_name=${identity_rest#*$'\t'}
  actual_name=${actual_name#/}
  if [ "$actual_instance" != "$INSTANCE_ID" ] || [ "$actual_service" != "$expected_service" ]; then
    add_error container-identity-changed "$expected_service"
    return 1
  fi
  case "$actual_name" in ''|*[!A-Za-z0-9._-]*) add_error container-identity-changed "$expected_service"; return 1 ;; esac
  REVALIDATED_CONTAINER_NAME=$actual_name
  return 0
}

export BUNDLE_ROOT RAW_ROOT STAGE_ROOT SCRATCH_ROOT LIMIT_MARKER_ROOT TOTAL_FILE MANIFEST_RECORDS ERROR_RECORDS MAX_UNCOMPRESSED FILE_LIMIT_BLOCKS

for service in $SERVICES; do
  case "$service" in ''|*[!a-z0-9-]*) exit 25 ;; esac
  mkdir -p "$BUNDLE_ROOT/services/$service/file-logs" "$BUNDLE_ROOT/services/$service/container-logs"
  service_root="$LOG_ROOT/$service"
  if [ -e "$service_root" ] || [ -L "$service_root" ]; then
    if [ ! -d "$service_root" ] || [ -L "$service_root" ]; then
      add_error file-root-rejected "$service"
    else
      service_root=$(readlink -f -- "$service_root") || exit 31
      case "$service_root" in "$LOG_ROOT"/*) ;; *) exit 31 ;; esac
      if find "$service_root" -xdev -type f -newermt "$SINCE" ! -newermt "$UNTIL" -print0 > "$CANDIDATE_LIST"; then
        if ! xargs -0 -r sh "$FILE_HELPER" "$service" "$service_root" < "$CANDIDATE_LIST"; then exit 31; fi
      else
        exit 31
      fi
    fi
  fi
  container_ids_file="$RAW_ROOT/container-ids-$service"
  if run_limited "$FILE_LIMIT_BLOCKS" docker ps -a --no-trunc --filter "label=aifar.instance=$INSTANCE_ID" --filter "label=aifar.service=$service" --format '{{"{{.ID}}"}}' > "$container_ids_file" 2>&1; then
    container_ids=$(cat "$container_ids_file") || exit 32
    rm -f -- "$container_ids_file" || exit 32
  else
    command_status=$?
    rm -f -- "$container_ids_file"
    [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 32
    add_error container-list-failed "$service"
    container_ids=
  fi
  container_counter=0
  for container_id in $container_ids; do
    container_counter=$((container_counter + 1))
    identity_file="$RAW_ROOT/container-identity-$service-$container_counter"
    if ! revalidate_container_identity "$service" "$container_id" "$identity_file"; then
      continue
    fi
    container=$REVALIDATED_CONTAINER_NAME
    remaining=$(remaining_bytes)
    [ "$remaining" -gt 0 ] || exit 32
    raw="$RAW_ROOT/containers/$service/$container.raw"
    staged="$STAGE_ROOT/services/$service/container-logs/$container.log"
    scratch="$SCRATCH_ROOT/services/$service/container-logs/$container.log"
    destination_relative="services/$service/container-logs/$container.log"
    destination="$BUNDLE_ROOT/$destination_relative"
    mkdir -p "$(dirname "$raw")" "$(dirname "$staged")" "$(dirname "$scratch")" "$(dirname "$destination")"
    if run_limited "$FILE_LIMIT_BLOCKS" docker logs --since "$SINCE" --until "$UNTIL" --timestamps "$container_id" > "$raw" 2>&1; then
      :
    else
      command_status=$?
      rm -f -- "$raw"
      [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 32
      add_error container-log-failed "$service"
      continue
    fi
    [ -f "$raw" ] && [ ! -L "$raw" ] || exit 32
    original_bytes=$(stat -c '%s' -- "$raw") || exit 32
    [ "$original_bytes" -le "$remaining" ] || exit 32
    run_limited "$FILE_LIMIT_BLOCKS" redact_file "$raw" "$staged" "$scratch" || exit 32
    rm -f -- "$raw" || exit 32
    staged_bytes=$(stat -c '%s' -- "$staged") || exit 32
    [ "$staged_bytes" -le "$remaining" ] || exit 32
    commit_staged_file "$staged_bytes" || exit 32
    mv -T -- "$staged" "$destination" || exit 32
    record_manifest_source "docker:$container_id" "$destination_relative" "$original_bytes"
  done
done

stage_generated_text "diagnostics/runtime-summary.json" "store:runtime-summary" "$RUNTIME_SUMMARY_JSON"
stage_generated_text "diagnostics/deployments.json" "store:deployments" "$DEPLOYMENTS_JSON"
stage_generated_text "diagnostics/pods.json" "store:pods" "$PODS_JSON"
stage_generated_text "diagnostics/release-summary.json" "store:release-summary" "$RELEASE_SUMMARY_JSON"
stage_generated_text "README.txt" "generated:readme" "$README_TEXT"

containers_file="$RAW_ROOT/containers.txt"
if run_limited "$FILE_LIMIT_BLOCKS" docker ps -a --filter "label=aifar.instance=$INSTANCE_ID" --format 'table {{"{{.Names}}"}}\t{{"{{.Image}}"}}\t{{"{{.Status}}"}}' > "$containers_file" 2>&1; then
  stage_generated_redacted_file "diagnostics/containers.txt" "remote:docker-ps" "$containers_file"
else
  command_status=$?
  rm -f -- "$containers_file"
  [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 33
  add_error container-summary-failed -
fi

health_file="$RAW_ROOT/health-checks.txt"
: > "$health_file"
for service in $SERVICES; do
  container_ids_file="$RAW_ROOT/health-container-ids-$service"
  if run_limited "$FILE_LIMIT_BLOCKS" docker ps -a --no-trunc --filter "label=aifar.instance=$INSTANCE_ID" --filter "label=aifar.service=$service" --format '{{"{{.ID}}"}}' > "$container_ids_file" 2>&1; then
    container_ids=$(cat "$container_ids_file") || exit 33
    rm -f -- "$container_ids_file" || exit 33
  else
    command_status=$?
    rm -f -- "$container_ids_file"
    [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 33
    add_error health-container-list-failed "$service"
    container_ids=
  fi
  health_counter=0
  for container_id in $container_ids; do
    health_counter=$((health_counter + 1))
    identity_file="$RAW_ROOT/health-container-identity-$service-$health_counter"
    if ! revalidate_container_identity "$service" "$container_id" "$identity_file"; then
      continue
    fi
    container=$REVALIDATED_CONTAINER_NAME
    health_item="$RAW_ROOT/health-$service-$health_counter.raw"
    if run_limited "$FILE_LIMIT_BLOCKS" docker inspect --format '{{"{{json .State.Health}}"}}' "$container_id" > "$health_item" 2>&1; then
      printf '%s\t' "$container" >> "$health_file"
      cat "$health_item" >> "$health_file" || exit 33
      rm -f -- "$health_item" || exit 33
    else
      command_status=$?
      rm -f -- "$health_item"
      [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 33
      add_error container-health-failed "$service"
    fi
  done
done
stage_generated_redacted_file "diagnostics/health-checks.txt" "remote:docker-health" "$health_file"

agent_file="$RAW_ROOT/agent-status.txt"
if run_limited "$FILE_LIMIT_BLOCKS" systemctl status aifar-agent --no-pager > "$agent_file" 2>&1; then
  stage_generated_redacted_file "diagnostics/agent-status.txt" "remote:systemctl-aifar-agent" "$agent_file"
else
  command_status=$?
  rm -f -- "$agent_file"
  [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 33
  add_error agent-status-failed -
fi

host_file="$RAW_ROOT/host-resources.txt"
: > "$host_file"
append_host_diagnostic() {
  label=$1
  error_code=$2
  shift 2
  host_item="$RAW_ROOT/host-$label.raw"
  if run_limited "$FILE_LIMIT_BLOCKS" "$@" > "$host_item" 2>&1; then
    printf '== %s ==\n' "$label" >> "$host_file"
    cat "$host_item" >> "$host_file" || exit 33
    rm -f -- "$host_item" || exit 33
  else
    command_status=$?
    rm -f -- "$host_item"
    [ "${LIMIT_SETUP_FAILED:-0}" -eq 0 ] || exit 33
    add_error "$error_code" -
  fi
}
append_host_diagnostic uptime host-uptime-failed uptime
append_host_diagnostic memory host-memory-failed free -m
append_host_diagnostic filesystem host-filesystem-failed df -h "$INSTALL_ROOT"
if [ -s "$host_file" ]; then
  stage_generated_redacted_file "diagnostics/host-resources.txt" "remote:host-resources" "$host_file"
else
  rm -f -- "$host_file"
fi

warning_count=$(awk 'END { print NR + 0 }' "$ERROR_RECORDS") || exit 29
case "$warning_count" in ''|*[!0-9]*) exit 29 ;; esac
stage_generated_file "collection-errors.txt" "generated:collection-errors" "$ERROR_RECORDS"

sha256_file() {
  sha_line=$(sha256sum -- "$1") || return 1
  set -- $sha_line
  sha_value=${1:-}
  case "$sha_value" in ''|*[!a-f0-9]*) return 1 ;; esac
  [ "${#sha_value}" -eq 64 ] || return 1
  printf '%s\n' "$sha_value"
}

manifest_tmp="$STAGE_ROOT/manifest.json"
sorted_records="$WORK_ROOT/manifest-records.sorted"
LC_ALL=C sort -t "$(printf '\t')" -k2,2 "$MANIFEST_RECORDS" > "$sorted_records" || exit 26
printf '{"files":[' > "$manifest_tmp"
first=1
tab=$(printf '\t')
while IFS="$tab" read -r source_name relative_name original_bytes; do
  [ -n "$relative_name" ] || continue
  staged_path="$BUNDLE_ROOT/$relative_name"
  [ -f "$staged_path" ] && [ ! -L "$staged_path" ] || exit 26
  staged_bytes=$(stat -c '%s' -- "$staged_path") || exit 26
  staged_sha=$(sha256_file "$staged_path") || exit 26
  source_json=$(printf '%s' "$source_name" | json_escape)
  relative_json=$(printf '%s' "$relative_name" | json_escape)
  if [ "$first" -eq 0 ]; then printf ',' >> "$manifest_tmp"; fi
  first=0
  printf '{"source":"%s","relativePath":"%s","originalBytes":%s,"stagedBytes":%s,"sha256":"%s"}' \
    "$source_json" "$relative_json" "$original_bytes" "$staged_bytes" "$staged_sha" >> "$manifest_tmp"
done < "$sorted_records"
printf ']}\n' >> "$manifest_tmp"
manifest_bytes=$(stat -c '%s' -- "$manifest_tmp") || exit 26
commit_staged_file "$manifest_bytes" || exit 26
mv -T -- "$manifest_tmp" "$BUNDLE_ROOT/manifest.json" || exit 26

du_line=$(du -sb "$BUNDLE_ROOT") || exit 27
set -- $du_line
uncompressed_bytes=${1:-}
case "$uncompressed_bytes" in ''|*[!0-9]*) exit 27 ;; esac
[ "$uncompressed_bytes" -le "$MAX_UNCOMPRESSED" ] || exit 27

run_limited "$FILE_LIMIT_BLOCKS" tar -czf "$ARCHIVE_PARTIAL" -C "$BUNDLE_PARENT" "$ARCHIVE_BASE" || exit 28
archive_bytes=$(stat -c '%s' -- "$ARCHIVE_PARTIAL") || exit 28
case "$archive_bytes" in ''|*[!0-9]*) exit 28 ;; esac
[ "$archive_bytes" -le "$MAX_ARCHIVE" ] || exit 28
tar -tzf "$ARCHIVE_PARTIAL" >/dev/null || exit 28
archive_sha=$(sha256_file "$ARCHIVE_PARTIAL") || exit 28

mv -T -- "$ARCHIVE_PARTIAL" "$PARTIAL_ROOT/$ARCHIVE_NAME" || exit 28
rm -rf -- "$BUNDLE_PARENT" "$WORK_ROOT"
rm -f -- "$PARTIAL_ROOT/.collector.pid"
trap - INT TERM
[ ! -e "$FINAL_ROOT" ] && [ ! -L "$FINAL_ROOT" ] || exit 22
mv -Tn -- "$PARTIAL_ROOT" "$FINAL_ROOT" || exit 22
[ ! -e "$PARTIAL_ROOT" ] && [ ! -L "$PARTIAL_ROOT" ] || exit 22

relative_path="$EXPORT_ID/$ARCHIVE_NAME"
printf 'AIFAR_DIAG_RESULT\t%s\t%s\t%s\t%s\t%s\t%s\n' \
  "$relative_path" "$ARCHIVE_NAME" "$archive_bytes" "$uncompressed_bytes" "$archive_sha" "$warning_count"
