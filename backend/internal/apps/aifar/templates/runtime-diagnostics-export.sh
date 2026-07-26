set -eu

umask 077

for required in docker find xargs readlink stat sed awk grep tr sha256sum tar gzip du df free uptime systemctl setsid; do
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

DIAGNOSTICS_ROOT="$INSTALL_ROOT/runtime/diagnostics"
LOG_ROOT="$INSTALL_ROOT/runtime/logs"
PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"
FINAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID"
BUNDLE_ROOT="$PARTIAL_ROOT/$ARCHIVE_BASE"
ARCHIVE_NAME="$ARCHIVE_BASE.tar.gz"
ARCHIVE_PARTIAL="$PARTIAL_ROOT/$ARCHIVE_NAME.partial"
TOTAL_FILE="$PARTIAL_ROOT/.uncompressed-bytes"
MANIFEST_RECORDS="$PARTIAL_ROOT/.manifest-records"
ERROR_RECORDS="$PARTIAL_ROOT/.collection-errors"
CANDIDATE_LIST="$PARTIAL_ROOT/.file-log-candidates"
FILE_HELPER="$PARTIAL_ROOT/.stage-file-log.sh"
MAX_UNCOMPRESSED=3221225472
MAX_ARCHIVE=1073741824

mkdir -p "$DIAGNOSTICS_ROOT"
chmod 700 "$DIAGNOSTICS_ROOT"
[ ! -e "$PARTIAL_ROOT" ] || exit 22
[ ! -e "$FINAL_ROOT" ] || exit 22
mkdir -p "$BUNDLE_ROOT/services" "$BUNDLE_ROOT/diagnostics"
printf '%s\n' "$$" > "$PARTIAL_ROOT/.collector.pid"
trap 'touch "$PARTIAL_ROOT/.cancelled" 2>/dev/null || true' INT TERM
printf '0\n' > "$TOTAL_FILE"
: > "$MANIFEST_RECORDS"
: > "$ERROR_RECORDS"

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
  item=$3
  printf '%s\t%s\t%s\n' "$code" "$service" "$item" >> "$ERROR_RECORDS"
}

redact_file() {
  source_file=$1
  target_file=$2
  phase_one="$target_file.redact-1"
  phase_two="$target_file.redact-2"
  phase_three="$target_file.redact-3"
  sed -E \
    -e 's/(Authorization:[[:space:]]*(Basic|Bearer))[[:space:]]+[^[:space:]]+/\1 [REDACTED]/Ig' \
    -e 's/("?(password|passwd|secret|token|api[_-]?key|access[_-]?key)"?[[:space:]]*[:=][[:space:]]*)("[^"]*"|[^,;[:space:]]+)/\1"[REDACTED]"/Ig' \
    -- "$source_file" > "$phase_one"
  sed -E \
    -e 's#(https?://)[^/@[:space:]]+:[^/@[:space:]]+@#\1[REDACTED]@#Ig' \
    -e 's/([?&](password|passwd|secret|token|api[_-]?key|access[_-]?key)=)[^&[:space:]]+/\1[REDACTED]/Ig' \
    -- "$phase_one" > "$phase_two"
  awk '
    BEGIN { in_private_key = 0 }
    /-----BEGIN .*PRIVATE KEY-----/ { print "[REDACTED PRIVATE KEY]"; in_private_key = 1; next }
    in_private_key && /-----END .*PRIVATE KEY-----/ { in_private_key = 0; next }
    in_private_key { next }
    { print }
  ' "$phase_two" > "$phase_three"
  sed -E -e 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' -- "$phase_three" > "$target_file"
  rm -f -- "$phase_one" "$phase_two" "$phase_three"
}

remaining_bytes() {
  used=$(cat "$TOTAL_FILE")
  case "$used" in
    ''|*[!0-9]*) exit 23 ;;
  esac
  [ "$used" -le "$MAX_UNCOMPRESSED" ] || exit 23
  printf '%s\n' $((MAX_UNCOMPRESSED - used))
}

commit_staged_file() {
  staged_bytes=$1
  used=$(cat "$TOTAL_FILE")
  case "$staged_bytes:$used" in
    *[!0-9:]*) exit 23 ;;
  esac
  next=$((used + staged_bytes))
  [ "$next" -le "$MAX_UNCOMPRESSED" ] || return 1
  printf '%s\n' "$next" > "$TOTAL_FILE"
}

record_manifest_source() {
  source_name=$1
  relative_name=$2
  original_bytes=$3
  printf '%s\t%s\t%s\n' "$source_name" "$relative_name" "$original_bytes" >> "$MANIFEST_RECORDS"
}

stage_generated_text() {
  relative_name=$1
  source_name=$2
  content=$3
  destination="$BUNDLE_ROOT/$relative_name"
  temporary="$destination.partial"
  mkdir -p "$(dirname "$destination")"
  printf '%s\n' "$content" > "$temporary"
  staged_bytes=$(stat -c '%s' -- "$temporary")
  if ! commit_staged_file "$staged_bytes"; then
    rm -f -- "$temporary"
    exit 24
  fi
  mv "$temporary" "$destination"
  record_manifest_source "$source_name" "$relative_name" "$staged_bytes"
}

stage_generated_file() {
  relative_name=$1
  source_name=$2
  source_file=$3
  destination="$BUNDLE_ROOT/$relative_name"
  mkdir -p "$(dirname "$destination")"
  staged_bytes=$(stat -c '%s' -- "$source_file")
  if ! commit_staged_file "$staged_bytes"; then
    exit 24
  fi
  mv "$source_file" "$destination"
  record_manifest_source "$source_name" "$relative_name" "$staged_bytes"
}

stage_generated_redacted_file() {
  relative_name=$1
  source_name=$2
  source_file=$3
  destination="$BUNDLE_ROOT/$relative_name"
  temporary="$destination.partial"
  mkdir -p "$(dirname "$destination")"
  original_bytes=$(stat -c '%s' -- "$source_file")
  remaining=$(remaining_bytes)
  [ "$remaining" -gt 0 ] || exit 24
  limit_blocks=$(((remaining + 511) / 512))
  if ! (ulimit -f "$limit_blocks"; redact_file "$source_file" "$temporary"); then
    rm -f -- "$source_file" "$temporary" "$temporary.redact-1" "$temporary.redact-2" "$temporary.redact-3"
    exit 24
  fi
  rm -f -- "$source_file"
  staged_bytes=$(stat -c '%s' -- "$temporary")
  if ! commit_staged_file "$staged_bytes"; then
    rm -f -- "$temporary"
    exit 24
  fi
  mv "$temporary" "$destination"
  record_manifest_source "$source_name" "$relative_name" "$original_bytes"
}

cat > "$FILE_HELPER" <<'AIFAR_DIAGNOSTIC_FILE_HELPER'
set -eu
redact_file() {
  source_file=$1
  target_file=$2
  phase_one="$target_file.redact-1"
  phase_two="$target_file.redact-2"
  phase_three="$target_file.redact-3"
  sed -E \
    -e 's/(Authorization:[[:space:]]*(Basic|Bearer))[[:space:]]+[^[:space:]]+/\1 [REDACTED]/Ig' \
    -e 's/("?(password|passwd|secret|token|api[_-]?key|access[_-]?key)"?[[:space:]]*[:=][[:space:]]*)("[^"]*"|[^,;[:space:]]+)/\1"[REDACTED]"/Ig' \
    -- "$source_file" > "$phase_one"
  sed -E \
    -e 's#(https?://)[^/@[:space:]]+:[^/@[:space:]]+@#\1[REDACTED]@#Ig' \
    -e 's/([?&](password|passwd|secret|token|api[_-]?key|access[_-]?key)=)[^&[:space:]]+/\1[REDACTED]/Ig' \
    -- "$phase_one" > "$phase_two"
  awk '
    BEGIN { in_private_key = 0 }
    /-----BEGIN .*PRIVATE KEY-----/ { print "[REDACTED PRIVATE KEY]"; in_private_key = 1; next }
    in_private_key && /-----END .*PRIVATE KEY-----/ { in_private_key = 0; next }
    in_private_key { next }
    { print }
  ' "$phase_two" > "$phase_three"
  sed -E -e 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' -- "$phase_three" > "$target_file"
  rm -f -- "$phase_one" "$phase_two" "$phase_three"
}
service=$1
service_root=$2
shift 2
for candidate do
  if ! resolved=$(readlink -f -- "$candidate"); then
    printf 'file-resolve-failed\t%s\t%s\n' "$service" "$candidate" >> "$ERROR_RECORDS"
    continue
  fi
  case "$resolved" in
    "$service_root"/*) ;;
    *)
      printf 'file-prefix-rejected\t%s\t%s\n' "$service" "$candidate" >> "$ERROR_RECORDS"
      continue
      ;;
  esac
  relative_name=${resolved#"$service_root"/}
  if ! printf '%s' "$relative_name" | LC_ALL=C grep -Eq '^[A-Za-z0-9._/@+-]+$'; then
    printf 'file-name-rejected\t%s\t-\n' "$service" >> "$ERROR_RECORDS"
    continue
  fi
  relative_lower=$(printf '%s' "$relative_name" | tr '[:upper:]' '[:lower:]')
  case "$relative_lower" in
    .env|*/.env|.env.*|*/.env.*|*.env|*.env.*|config|*/config|application|*/application|*.conf|*.cnf|*.toml|*.xml|*.jks|*.p12|*.pfx|*.keystore|*.ini|*.properties|*.yaml|*.yml|*.json|*.db|*.db-*|*.sqlite|*.sqlite3|*.sql|*.mdb|*.accdb|*.pem|*.key|*.crt|*.gz|*.zip|*.xz|*.bz2|*.zst)
      printf 'sensitive-file-skipped\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
      continue
      ;;
  esac
  if ! stat_resolved=$(readlink -f -- "$resolved"); then
    printf 'file-resolve-failed\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
    continue
  fi
  case "$stat_resolved" in
    "$service_root"/*) ;;
    *)
      printf 'file-prefix-rejected\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
      continue
      ;;
  esac
  if [ "$stat_resolved" != "$resolved" ]; then
    printf 'file-path-changed\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
    continue
  fi
  if ! original_bytes=$(stat -c '%s' -- "$resolved"); then
    printf 'file-stat-failed\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
    continue
  fi
  used=$(cat "$TOTAL_FILE")
  case "$original_bytes:$used" in
    *[!0-9:]*) exit 31 ;;
  esac
  remaining=$((MAX_UNCOMPRESSED - used))
  if [ "$original_bytes" -gt "$remaining" ]; then
    printf 'uncompressed-limit\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
    continue
  fi
  destination_relative="services/$service/file-logs/$relative_name"
  destination="$BUNDLE_ROOT/$destination_relative"
  temporary="$destination.partial"
  mkdir -p "$(dirname "$destination")"
  limit_blocks=$(((remaining + 511) / 512))
  if ! read_resolved=$(readlink -f -- "$resolved"); then
    printf 'file-resolve-failed\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
    continue
  fi
  case "$read_resolved" in
    "$service_root"/*) ;;
    *)
      printf 'file-prefix-rejected\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
      continue
      ;;
  esac
  if [ "$read_resolved" != "$resolved" ]; then
    printf 'file-path-changed\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
    continue
  fi
  if ! (ulimit -f "$limit_blocks"; redact_file "$resolved" "$temporary"); then
    rm -f -- "$temporary" "$temporary.redact-1" "$temporary.redact-2" "$temporary.redact-3"
    printf 'file-redaction-failed\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
    continue
  fi
  staged_bytes=$(stat -c '%s' -- "$temporary")
  if [ "$staged_bytes" -gt "$remaining" ]; then
    rm -f -- "$temporary"
    printf 'uncompressed-limit\t%s\t%s\n' "$service" "$relative_name" >> "$ERROR_RECORDS"
    continue
  fi
  next=$((used + staged_bytes))
  printf '%s\n' "$next" > "$TOTAL_FILE"
  mv "$temporary" "$destination"
  printf 'file:%s/%s\t%s\t%s\n' "$service" "$relative_name" "$destination_relative" "$original_bytes" >> "$MANIFEST_RECORDS"
done
AIFAR_DIAGNOSTIC_FILE_HELPER

# The helper is fixed here, not uploaded or supplied by the client.
export BUNDLE_ROOT TOTAL_FILE MANIFEST_RECORDS ERROR_RECORDS MAX_UNCOMPRESSED

for service in $SERVICES; do
  case "$service" in
    ''|*[!a-z0-9-]*) exit 25 ;;
  esac
  mkdir -p "$BUNDLE_ROOT/services/$service/file-logs" "$BUNDLE_ROOT/services/$service/container-logs"
  service_root="$LOG_ROOT/$service"
  if [ -d "$service_root" ]; then
    if find "$LOG_ROOT/$service" -xdev -type f -newermt "$SINCE" ! -newermt "$UNTIL" -print0 > "$CANDIDATE_LIST"; then
      export service_root service
      if ! xargs -0 -r sh "$FILE_HELPER" "$service" "$service_root" < "$CANDIDATE_LIST"; then
        add_error file-collection-failed "$service" -
      fi
    else
      add_error file-discovery-failed "$service" -
    fi
  fi

  if ! containers=$(docker ps -a --filter "label=aifar.instance=$INSTANCE_ID" --filter "label=aifar.service=$service" --format '{{"{{.Names}}"}}'); then
    add_error container-discovery-failed "$service" -
    containers=""
  fi
  for container in $containers; do
    case "$container" in
      ''|*[!A-Za-z0-9._-]*)
        add_error container-name-rejected "$service" -
        continue
        ;;
    esac
    remaining=$(remaining_bytes)
    [ "$remaining" -gt 0 ] || {
      add_error uncompressed-limit "$service" "$container"
      continue
    }
    raw="$PARTIAL_ROOT/.container-$service-$container.raw"
    limit_blocks=$(((remaining + 511) / 512))
    if ! (ulimit -f "$limit_blocks"; docker logs --since "$SINCE" --until "$UNTIL" --timestamps "$container") > "$raw" 2>&1; then
      rm -f -- "$raw"
      add_error container-log-failed "$service" "$container"
      continue
    fi
    original_bytes=$(stat -c '%s' -- "$raw")
    if [ "$original_bytes" -gt "$remaining" ]; then
      rm -f -- "$raw"
      add_error uncompressed-limit "$service" "$container"
      continue
    fi
    destination_relative="services/$service/container-logs/$container.log"
    destination="$BUNDLE_ROOT/$destination_relative"
    temporary="$destination.partial"
    mkdir -p "$(dirname "$destination")"
    if ! (ulimit -f "$limit_blocks"; redact_file "$raw" "$temporary"); then
      rm -f -- "$raw" "$temporary" "$temporary.redact-1" "$temporary.redact-2" "$temporary.redact-3"
      add_error container-redaction-failed "$service" "$container"
      continue
    fi
    rm -f -- "$raw"
    staged_bytes=$(stat -c '%s' -- "$temporary")
    if ! commit_staged_file "$staged_bytes"; then
      rm -f -- "$temporary"
      add_error uncompressed-limit "$service" "$container"
      continue
    fi
    mv "$temporary" "$destination"
    record_manifest_source "docker:$container" "$destination_relative" "$original_bytes"
  done
done

stage_generated_text "diagnostics/runtime-summary.json" "store:runtime-summary" "$RUNTIME_SUMMARY_JSON"
stage_generated_text "diagnostics/deployments.json" "store:deployments" "$DEPLOYMENTS_JSON"
stage_generated_text "diagnostics/pods.json" "store:pods" "$PODS_JSON"
stage_generated_text "diagnostics/release-summary.json" "store:release-summary" "$RELEASE_SUMMARY_JSON"
stage_generated_text "README.txt" "generated:readme" "$README_TEXT"

containers_file="$PARTIAL_ROOT/.containers.txt"
if ! docker ps -a --filter "label=aifar.instance=$INSTANCE_ID" --format 'table {{"{{.Names}}"}}\t{{"{{.Image}}"}}\t{{"{{.Status}}"}}' > "$containers_file" 2>&1; then
  add_error runtime-container-list-failed - -
fi
stage_generated_redacted_file "diagnostics/containers.txt" "remote:docker-ps" "$containers_file"

health_file="$PARTIAL_ROOT/.health-checks.txt"
: > "$health_file"
for service in $SERVICES; do
  if ! containers=$(docker ps -a --filter "label=aifar.instance=$INSTANCE_ID" --filter "label=aifar.service=$service" --format '{{"{{.Names}}"}}'); then
    add_error health-container-discovery-failed "$service" -
    continue
  fi
  for container in $containers; do
    printf '%s\t' "$container" >> "$health_file"
    if ! docker inspect --format '{{"{{json .State.Health}}"}}' "$container" >> "$health_file" 2>&1; then
      add_error health-inspection-failed "$service" "$container"
      printf '%s\n' unavailable >> "$health_file"
    fi
  done
done
stage_generated_redacted_file "diagnostics/health-checks.txt" "remote:docker-health" "$health_file"

agent_file="$PARTIAL_ROOT/.agent-status.txt"
if ! systemctl status aifar-agent --no-pager > "$agent_file" 2>&1; then
  add_error agent-status-failed - -
fi
stage_generated_redacted_file "diagnostics/agent-status.txt" "remote:systemctl-aifar-agent" "$agent_file"

host_file="$PARTIAL_ROOT/.host-resources.txt"
{
  printf '%s\n' '== uptime =='
  uptime
  printf '%s\n' '== memory =='
  free -m
  printf '%s\n' '== filesystem =='
  df -h "$INSTALL_ROOT"
} > "$host_file" 2>&1 || add_error host-resources-failed - -
stage_generated_redacted_file "diagnostics/host-resources.txt" "remote:host-resources" "$host_file"

errors_file="$PARTIAL_ROOT/.collection-errors.txt"
cp "$ERROR_RECORDS" "$errors_file"
stage_generated_file "collection-errors.txt" "generated:collection-errors" "$errors_file"

manifest_tmp="$PARTIAL_ROOT/.manifest.json"
sorted_records="$PARTIAL_ROOT/.manifest-records.sorted"
LC_ALL=C sort -t "$(printf '\t')" -k2,2 "$MANIFEST_RECORDS" > "$sorted_records"
printf '{"files":[' > "$manifest_tmp"
first=1
tab=$(printf '\t')
while IFS="$tab" read -r source_name relative_name original_bytes; do
  [ -n "$relative_name" ] || continue
  staged_path="$BUNDLE_ROOT/$relative_name"
  [ -f "$staged_path" ] || exit 26
  staged_bytes=$(stat -c '%s' -- "$staged_path")
  staged_sha=$(sha256sum "$staged_path" | awk '{print $1}')
  source_json=$(printf '%s' "$source_name" | json_escape)
  relative_json=$(printf '%s' "$relative_name" | json_escape)
  if [ "$first" -eq 0 ]; then
    printf ',' >> "$manifest_tmp"
  fi
  first=0
  printf '{"source":"%s","relativePath":"%s","originalBytes":%s,"stagedBytes":%s,"sha256":"%s"}' \
    "$source_json" "$relative_json" "$original_bytes" "$staged_bytes" "$staged_sha" >> "$manifest_tmp"
done < "$sorted_records"
printf ']}\n' >> "$manifest_tmp"
manifest_bytes=$(stat -c '%s' -- "$manifest_tmp")
if ! commit_staged_file "$manifest_bytes"; then
  exit 24
fi
mv "$manifest_tmp" "$BUNDLE_ROOT/manifest.json"

uncompressed_bytes=$(du -sb "$BUNDLE_ROOT" | awk '{print $1}')
case "$uncompressed_bytes" in
  ''|*[!0-9]*) exit 27 ;;
esac
[ "$uncompressed_bytes" -le "$MAX_UNCOMPRESSED" ] || exit 27

if ! (ulimit -f 2097152; tar -czf "$ARCHIVE_PARTIAL" -C "$PARTIAL_ROOT" "$ARCHIVE_BASE"); then
  exit 28
fi
archive_bytes=$(stat -c '%s' -- "$ARCHIVE_PARTIAL")
case "$archive_bytes" in
  ''|*[!0-9]*) exit 28 ;;
esac
[ "$archive_bytes" -le "$MAX_ARCHIVE" ] || exit 28
tar -tzf "$ARCHIVE_PARTIAL" >/dev/null
archive_sha=$(sha256sum "$ARCHIVE_PARTIAL" | awk '{print $1}')
case "$archive_sha" in
  *[!a-f0-9]*|'') exit 28 ;;
esac
[ "${#archive_sha}" -eq 64 ] || exit 28

warning_count=$(wc -l < "$ERROR_RECORDS" | awk '{print $1}')
case "$warning_count" in
  ''|*[!0-9]*) exit 29 ;;
esac

mv "$ARCHIVE_PARTIAL" "$PARTIAL_ROOT/$ARCHIVE_NAME"
rm -rf -- "$BUNDLE_ROOT"
rm -f -- "$PARTIAL_ROOT/.collector.pid" "$TOTAL_FILE" "$MANIFEST_RECORDS" "$ERROR_RECORDS" \
  "$CANDIDATE_LIST" "$FILE_HELPER" "$sorted_records"
trap - INT TERM
mv "$PARTIAL_ROOT" "$FINAL_ROOT"

relative_path="$EXPORT_ID/$ARCHIVE_NAME"
printf 'AIFAR_DIAG_RESULT\t%s\t%s\t%s\t%s\t%s\t%s\n' \
  "$relative_path" "$ARCHIVE_NAME" "$archive_bytes" "$uncompressed_bytes" "$archive_sha" "$warning_count"
