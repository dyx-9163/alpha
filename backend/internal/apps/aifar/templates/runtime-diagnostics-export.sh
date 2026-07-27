set -eu

umask 077

for required in awk bash cp date dirname docker find gawk grep head mkdir mv od readlink rm sed sha256sum stat systemctl tail tar timedatectl tr xargs df free uptime; do
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

LOG_ROOT="$INSTALL_ROOT/runtime/logs"
DIAGNOSTICS_ROOT="$INSTALL_ROOT/runtime/diagnostics"
PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"
BUNDLE_PARENT="$PARTIAL_ROOT/bundle"
BUNDLE_ROOT="$BUNDLE_PARENT/$ARCHIVE_BASE"
WORK_ROOT="$PARTIAL_ROOT/work"
FILTER_PROGRAM_FILE="$WORK_ROOT/runtime-diagnostics-filter.awk"
FILE_HELPER="$WORK_ROOT/filter-one-log.sh"
TOTAL_SCAN_FILE="$WORK_ROOT/total-scan-bytes"
TOTAL_FILTERED_FILE="$WORK_ROOT/total-filtered-bytes"
MANIFEST_RECORDS="$WORK_ROOT/manifest-records.tsv"
ERROR_RECORDS="$WORK_ROOT/collection-errors.tsv"
ARCHIVE_NAME="$ARCHIVE_BASE.tar.gz"
MAX_FILE_SCAN=1073741824
MAX_TOTAL_SCAN=2147483648
MAX_FILTERED=524288000
MAX_ARCHIVE=268435456

case "$INSTALL_ROOT" in /*) [ "$INSTALL_ROOT" != "/" ] || exit 21 ;; *) exit 21 ;; esac
case "$EXPORT_ID" in ''|*[!A-Za-z0-9._-]*) exit 21 ;; esac
case "$INSTANCE_ID" in ''|*[!A-Za-z0-9._-]*) exit 21 ;; esac
case "$ARCHIVE_BASE" in aifar-diagnostics-[A-Za-z0-9._-]*) ;; *) exit 21 ;; esac
[ -d "$INSTALL_ROOT" ] && [ ! -L "$INSTALL_ROOT" ] || exit 22
install_canonical=$(readlink -f -- "$INSTALL_ROOT") || exit 22
[ "$install_canonical" = "$INSTALL_ROOT" ] || exit 22
[ -d "$INSTALL_ROOT/runtime" ] && [ ! -L "$INSTALL_ROOT/runtime" ] || exit 22

if [ -e "$DIAGNOSTICS_ROOT" ] || [ -L "$DIAGNOSTICS_ROOT" ]; then
  [ -d "$DIAGNOSTICS_ROOT" ] && [ ! -L "$DIAGNOSTICS_ROOT" ] || exit 22
else
  mkdir -- "$DIAGNOSTICS_ROOT" || exit 22
fi
diagnostics_canonical=$(readlink -f -- "$DIAGNOSTICS_ROOT") || exit 22
[ "$diagnostics_canonical" = "$INSTALL_ROOT/runtime/diagnostics" ] || exit 22
[ ! -e "$PARTIAL_ROOT" ] && [ ! -L "$PARTIAL_ROOT" ] || exit 22
mkdir -- "$PARTIAL_ROOT" || exit 22
mkdir -p "$BUNDLE_ROOT/services" "$BUNDLE_ROOT/diagnostics" "$WORK_ROOT"
chmod 700 "$DIAGNOSTICS_ROOT" "$PARTIAL_ROOT" "$BUNDLE_PARENT" "$BUNDLE_ROOT" "$WORK_ROOT"

cleanup_partial() {
  rm -rf -- "$PARTIAL_ROOT"
}
trap cleanup_partial EXIT
trap 'exit 130' INT TERM

pid_stat="$PROC_ROOT/$$/stat"
if [ -f "$pid_stat" ]; then
  pid_start=$(awk '{print $22}' "$pid_stat") || exit 22
  pid_pgid=$(awk '{print $5}' "$pid_stat") || exit 22
  case "$pid_start:$pid_pgid" in *[!0-9:]*) exit 22 ;; esac
  printf '%s\t%s\t%s\n' "$$" "$pid_start" "$pid_pgid" > "$PARTIAL_ROOT/.collector.pid"
fi

since_epoch=$(date -d "$SINCE" +%s) || exit 23
until_epoch=$(date -d "$UNTIL" +%s) || exit 23
case "$since_epoch:$until_epoch" in *[!0-9:]*) exit 23 ;; esac
[ "$since_epoch" -lt "$until_epoch" ] || exit 23

server_timezone=$(timedatectl show -p Timezone --value 2>/dev/null || true)
if [ -z "$server_timezone" ] && [ -L /etc/localtime ]; then
  localtime_target=$(readlink -f /etc/localtime 2>/dev/null || true)
  case "$localtime_target" in /usr/share/zoneinfo/*) server_timezone=${localtime_target#/usr/share/zoneinfo/} ;; esac
fi
case "$server_timezone" in ''|/*|*..*|*[!A-Za-z0-9_+./-]*) exit 23 ;; esac

cat > "$FILTER_PROGRAM_FILE" <<'AIFAR_RUNTIME_DIAGNOSTIC_FILTER'
{{.FilterProgram}}
AIFAR_RUNTIME_DIAGNOSTIC_FILTER
chmod 600 "$FILTER_PROGRAM_FILE"

printf '0\n' > "$TOTAL_SCAN_FILE"
printf '0\n' > "$TOTAL_FILTERED_FILE"
: > "$MANIFEST_RECORDS"
: > "$ERROR_RECORDS"

cat > "$FILE_HELPER" <<'AIFAR_RUNTIME_DIAGNOSTIC_FILE_HELPER'
#!/usr/bin/env bash
set -euo pipefail

service=$1
service_root=$2
source_file=$3

case "$service" in ''|*[!a-z0-9-]*) exit 31 ;; esac
[[ -f "$source_file" && ! -L "$source_file" ]] || exit 31
exec 9< "$source_file" || exit 31
source_descriptor=/proc/self/fd/9
[ -e "$source_descriptor" ] || source_descriptor=/dev/fd/9
[ -e "$source_descriptor" ] || exit 31
source_canonical=$(readlink -f -- "$source_descriptor") || exit 31
case "$source_canonical" in "$service_root"/*) ;; *) exit 31 ;; esac
relative=${source_canonical#"$service_root"/}
[[ "$relative" =~ ^[A-Za-z0-9._/-]+$ ]] || exit 31
IFS='/' read -r -a path_parts <<< "$relative"
for path_part in "${path_parts[@]}"; do
  [[ -n "$path_part" && "$path_part" != "." && "$path_part" != ".." && "$path_part" != .* ]] || exit 31
  lower_part=${path_part,,}
  case "$lower_part" in *config*|*database*|*credential*|*secret*|*password*|*token*|*private*|*keystore*|*truststore*) exit 31 ;; esac
done
base_name=${path_parts[${#path_parts[@]}-1]}
case "$base_name" in *.log|*.log.[A-Za-z0-9]*) ;; *) exit 31 ;; esac

read -r initial_device initial_inode initial_size < <(stat -Lc '%d %i %s' -- "$source_descriptor") || exit 31
case "$initial_device:$initial_inode:$initial_size" in *[!0-9:]*) exit 31 ;; esac
[ "$initial_size" -le "$MAX_FILE_SCAN" ] || exit 41
total_scan=$(cat "$TOTAL_SCAN_FILE")
case "$total_scan" in ''|*[!0-9]*) exit 31 ;; esac
next_scan=$((total_scan + initial_size))
[ "$next_scan" -le "$MAX_TOTAL_SCAN" ] || exit 42
printf '%s\n' "$next_scan" > "$TOTAL_SCAN_FILE"

initial_ended_newline=1
if [ "$initial_size" -gt 0 ]; then
  last_hex=$(tail -c 1 -- "$source_descriptor" | od -An -t x1 | tr -d '[:space:]')
  [ "$last_hex" = "0a" ] || initial_ended_newline=0
fi

destination_relative="services/$service/file-logs/$relative"
destination="$BUNDLE_ROOT/$destination_relative"
staged="$WORK_ROOT/staged/$destination_relative"
summary="$WORK_ROOT/summaries/$service-${initial_inode}.tsv"
warnings="$WORK_ROOT/warnings/$service-${initial_inode}.tsv"
mkdir -p -- "$(dirname "$destination")" "$(dirname "$staged")" "$(dirname "$summary")" "$(dirname "$warnings")"
: > "$warnings"

redact_stream() {
  sed -E \
    -e 's/(Authorization:[[:space:]]*(Basic|Bearer))[[:space:]]+[^[:space:]]+/\1 [REDACTED]/Ig' \
    -e 's/("?(password|passwd|secret|token|api[_-]?key|access[_-]?key)"?[[:space:]]*[:=][[:space:]]*)("[^"]*"|[^,;[:space:]]+)/\1"[REDACTED]"/Ig' \
    -e 's#(https?://)[^/@[:space:]]+:[^/@[:space:]]+@#\1[REDACTED]@#Ig' \
  | awk '
      BEGIN { in_key = 0 }
      /-----BEGIN .*PRIVATE KEY-----/ { print "[REDACTED PRIVATE KEY]"; in_key = 1; next }
      in_key && /-----END .*PRIVATE KEY-----/ { in_key = 0; next }
      in_key { next }
      { print }
    ' \
  | sed -E 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g'
}

head -c "$initial_size" -- "$source_descriptor" \
  | TZ="$server_timezone" gawk -v since_epoch="$since_epoch" -v until_epoch="$until_epoch" -v server_tz="$server_timezone" \
      -v initial_ended_newline="$initial_ended_newline" -v summary_path="$summary" -v warning_path="$warnings" \
      -f "$FILTER_PROGRAM_FILE" \
  | redact_stream > "$staged"

tab=$'\t'
IFS="$tab" read -r protocol parser scanned_bytes filtered_bytes filtered_records warning_count extra < "$summary" || exit 31
[ "$protocol" = "AIFAR_DIAG_FILTER_V1" ] && [ -z "${extra:-}" ] || exit 31
case "$parser:$scanned_bytes:$filtered_bytes:$filtered_records:$warning_count" in *[!A-Za-z0-9._:-]*) exit 31 ;; esac
[ "$scanned_bytes" -le "$initial_size" ] || exit 31
total_filtered=$(cat "$TOTAL_FILTERED_FILE")
case "$total_filtered" in ''|*[!0-9]*) exit 31 ;; esac
next_filtered=$((total_filtered + filtered_bytes))
[ "$next_filtered" -le "$MAX_FILTERED" ] || exit 43
printf '%s\n' "$next_filtered" > "$TOTAL_FILTERED_FILE"

warning_codes=
while IFS="$tab" read -r warning_code warning_occurrences extra; do
  [ -n "$warning_code" ] || continue
  case "$warning_code:$warning_occurrences" in *[!a-z0-9:-]*) exit 31 ;; esac
  [ -z "${extra:-}" ] || exit 31
  printf '%s\t%s\t%s\t%s\n' "$warning_code" "$service" "$relative" "$warning_occurrences" >> "$ERROR_RECORDS"
  if [ -z "$warning_codes" ]; then warning_codes=$warning_code; else warning_codes="$warning_codes,$warning_code"; fi
done < "$warnings"

exec 9<&-

if [ "$filtered_bytes" -gt 0 ]; then
  staged_sha=$(sha256sum -- "$staged") || exit 31
  staged_sha=${staged_sha%% *}
  case "$staged_sha" in ''|*[!a-f0-9]*) exit 31 ;; esac
  mv -T -- "$staged" "$destination"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$service" "$relative" "$initial_device" "$initial_inode" "$initial_size" "$scanned_bytes" "$filtered_bytes" "$staged_sha" "${parser}:${warning_codes:--}" >> "$MANIFEST_RECORDS"
else
  rm -f -- "$staged"
fi
AIFAR_RUNTIME_DIAGNOSTIC_FILE_HELPER
chmod 700 "$FILE_HELPER"

export BUNDLE_ROOT WORK_ROOT FILTER_PROGRAM_FILE TOTAL_SCAN_FILE TOTAL_FILTERED_FILE MANIFEST_RECORDS ERROR_RECORDS
export MAX_FILE_SCAN MAX_TOTAL_SCAN MAX_FILTERED since_epoch until_epoch server_timezone

for service in $SERVICES; do
  case "$service" in ''|*[!a-z0-9-]*) exit 30 ;; esac
  service_root="$LOG_ROOT/$service"
  if [ -e "$service_root" ] || [ -L "$service_root" ]; then
    [ -d "$service_root" ] && [ ! -L "$service_root" ] || exit 30
    service_canonical=$(readlink -f -- "$service_root") || exit 30
    [ "$service_canonical" = "$service_root" ] || exit 30
    candidates="$WORK_ROOT/candidates-$service"
    find "$service_root" -xdev -type f \
      \( -name '*.log' -o -name '*.log.*' \) \
      ! -name '.*' ! -ipath '*/.*' \
      ! -ipath '*config*' ! -ipath '*database*' ! -ipath '*credential*' \
      ! -ipath '*secret*' ! -ipath '*password*' ! -ipath '*token*' \
      -print0 > "$candidates" || exit 30
    xargs -0 -r -n 1 bash "$FILE_HELPER" "$service" "$service_root" < "$candidates" || exit $?
  fi
done

write_generated() {
  relative=$1
  content=$2
  destination="$BUNDLE_ROOT/$relative"
  mkdir -p -- "$(dirname "$destination")"
  printf '%s\n' "$content" > "$destination"
}

write_generated "diagnostics/runtime-summary.json" "$RUNTIME_SUMMARY_JSON"
write_generated "diagnostics/deployments.json" "$DEPLOYMENTS_JSON"
write_generated "diagnostics/pods.json" "$PODS_JSON"
write_generated "diagnostics/release-summary.json" "$RELEASE_SUMMARY_JSON"
write_generated "README.txt" "$README_TEXT"

docker ps -a --filter "label=aifar.instance=$INSTANCE_ID" --format 'table {{"{{.Names}}"}}\t{{"{{.Image}}"}}\t{{"{{.Status}}"}}' \
  | sed -E 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' > "$BUNDLE_ROOT/diagnostics/containers.txt" 2>/dev/null \
  || printf 'container-summary-failed\t-\t-\t1\n' >> "$ERROR_RECORDS"

: > "$BUNDLE_ROOT/diagnostics/health-checks.txt"
for container_id in $(docker ps -aq --filter "label=aifar.instance=$INSTANCE_ID" 2>/dev/null || true); do
  docker inspect --format '{{"{{.Name}}"}} {{"{{json .State.Health}}"}}' "$container_id" \
    | sed -E 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' >> "$BUNDLE_ROOT/diagnostics/health-checks.txt" 2>/dev/null \
    || printf 'container-health-failed\t-\t-\t1\n' >> "$ERROR_RECORDS"
done
systemctl status aifar-agent --no-pager 2>&1 \
  | sed -E 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' > "$BUNDLE_ROOT/diagnostics/agent-status.txt" \
  || printf 'agent-status-failed\t-\t-\t1\n' >> "$ERROR_RECORDS"
{
  uptime || true
  free -m || true
  df -h "$INSTALL_ROOT" || true
} 2>&1 | sed -E 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' > "$BUNDLE_ROOT/diagnostics/host-resources.txt"

warning_count=$(awk -F '\t' '{ total += $4 } END { print total + 0 }' "$ERROR_RECORDS") || exit 35
cp "$ERROR_RECORDS" "$BUNDLE_ROOT/collection-errors.txt"

manifest_tmp="$WORK_ROOT/manifest.json"
generated_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
printf '{"formatVersion":"AIFAR_DIAGNOSTIC_V2","since":"%s","until":"%s","serverTimezone":"%s","selectedServices":"%s","limits":{"maxFileScanBytes":%s,"maxTotalScanBytes":%s,"maxFilteredBytes":%s,"maxArchiveBytes":%s},"redactionRuleVersion":"v1","generatedAt":"%s","sources":[' \
  "$SINCE" "$UNTIL" "$server_timezone" "$SERVICES" "$MAX_FILE_SCAN" "$MAX_TOTAL_SCAN" "$MAX_FILTERED" "$MAX_ARCHIVE" "$generated_at" > "$manifest_tmp"
first=1
tab=$(printf '\t')
while IFS="$tab" read -r service relative device inode initial scanned filtered sha parser_warnings; do
  [ -n "$service" ] || continue
  if [ "$first" -eq 0 ]; then printf ',' >> "$manifest_tmp"; fi
  first=0
  printf '{"service":"%s","sourcePath":"%s","device":"%s","inode":"%s","initialBytes":%s,"scannedBytes":%s,"filteredBytes":%s,"sha256":"%s","parserWarnings":"%s"}' \
    "$service" "$relative" "$device" "$inode" "$initial" "$scanned" "$filtered" "$sha" "$parser_warnings" >> "$manifest_tmp"
done < "$MANIFEST_RECORDS"
printf ']}\n' >> "$manifest_tmp"
mv -T -- "$manifest_tmp" "$BUNDLE_ROOT/manifest.json"

if grep -R -I -E -q -- '-----BEGIN .*PRIVATE KEY-----|Authorization:[[:space:]]*(Basic|Bearer)[[:space:]]+[^[]|https?://[^/@[:space:]]+:[^/@[:space:]]+@' "$BUNDLE_ROOT"; then
  exit 44
fi

uncompressed_bytes=$(find "$BUNDLE_ROOT" -xdev -type f -printf '%s\n' | awk '{ total += $1 } END { printf "%.0f\n", total }') || exit 45
case "$uncompressed_bytes" in ''|*[!0-9]*) exit 45 ;; esac
[ "$uncompressed_bytes" -le "$MAX_FILTERED" ] || exit 43

printf 'AIFAR_DIAG_STREAM_V1\t%s\t%s\t%s\t%s\n' "$ARCHIVE_NAME" "$uncompressed_bytes" "$warning_count" "$server_timezone"
tar -czf - -C "$BUNDLE_PARENT" "$ARCHIVE_BASE"
