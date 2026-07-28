set -eu

umask 077

for required in awk bash cp date dirname docker find head mkdir mv readlink rm sed sha256sum stat systemctl tar tee timedatectl tr xargs df free uptime; do
  command -v "$required" >/dev/null 2>&1 || exit 20
done

INSTALL_ROOT={{.InstallRoot}}
EXPORT_ID={{.ExportID}}
INSTANCE_ID={{.InstanceID}}
SERVICES={{.Services}}
LOCAL_DATE={{.LocalDate}}
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
FILE_HELPER="$WORK_ROOT/snapshot-one-log.sh"
TOTAL_SCAN_FILE="$WORK_ROOT/total-scan-bytes"
MANIFEST_RECORDS="$WORK_ROOT/manifest-records.tsv"
ERROR_RECORDS="$WORK_ROOT/collection-errors.tsv"
ARCHIVE_NAME="$ARCHIVE_BASE.tar.gz"
MAX_FILE_SCAN=1073741824
MAX_TOTAL_SCAN=2147483648
MAX_SNAPSHOT=524288000
MAX_ARCHIVE=268435456

case "$INSTALL_ROOT" in /*) [ "$INSTALL_ROOT" != "/" ] || exit 21 ;; *) exit 21 ;; esac
case "$EXPORT_ID" in ''|*[!A-Za-z0-9._-]*) exit 21 ;; esac
case "$INSTANCE_ID" in ''|*[!A-Za-z0-9._-]*) exit 21 ;; esac
case "$LOCAL_DATE" in [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]) ;; *) exit 21 ;; esac
case "$ARCHIVE_BASE" in aifar-diagnostics-[A-Za-z0-9._-]*) ;; *) exit 21 ;; esac
[ -d "$INSTALL_ROOT" ] && [ ! -L "$INSTALL_ROOT" ] || exit 22
install_canonical=$(readlink -f -- "$INSTALL_ROOT") || exit 22
[ "$install_canonical" = "$INSTALL_ROOT" ] || exit 22
[ -d "$INSTALL_ROOT/runtime" ] && [ ! -L "$INSTALL_ROOT/runtime" ] || exit 22

server_timezone=$(timedatectl show -p Timezone --value 2>/dev/null || true)
if [ -z "$server_timezone" ] && [ -L /etc/localtime ]; then
  localtime_target=$(readlink -f /etc/localtime 2>/dev/null || true)
  case "$localtime_target" in /usr/share/zoneinfo/*) server_timezone=${localtime_target#/usr/share/zoneinfo/} ;; esac
fi
case "$server_timezone" in ''|/*|*..*|*[!A-Za-z0-9_+./-]*) exit 23 ;; esac
server_today=$(TZ="$server_timezone" date +%F) || exit 23
[ "$LOCAL_DATE" \> "$server_today" ] && exit 23
is_current=0
[ "$LOCAL_DATE" = "$server_today" ] && is_current=1

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

cleanup_partial() { rm -rf -- "$PARTIAL_ROOT"; }
trap cleanup_partial EXIT
trap 'exit 130' INT TERM

pid_stat="$PROC_ROOT/$$/stat"
if [ -f "$pid_stat" ]; then
  pid_start=$(awk '{print $22}' "$pid_stat") || exit 22
  pid_pgid=$(awk '{print $5}' "$pid_stat") || exit 22
  case "$pid_start:$pid_pgid" in *[!0-9:]*) exit 22 ;; esac
  printf '%s\t%s\t%s\n' "$$" "$pid_start" "$pid_pgid" > "$PARTIAL_ROOT/.collector.pid"
fi

printf '0\n' > "$TOTAL_SCAN_FILE"
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
  [[ -n "$path_part" && "$path_part" != "." && "$path_part" != ".." ]] || exit 31
  [[ "$path_part" != .* ]] || exit 0
  lower_part=${path_part,,}
  case "$lower_part" in *config*|*database*|*credential*|*secret*|*password*|*token*|*private*|*keystore*|*truststore*) exit 0 ;; esac
done
base_name=${path_parts[${#path_parts[@]}-1]}
case "$base_name" in *.lck|*.idx) exit 0 ;; *.log|*.log.[A-Za-z0-9]*) ;; *) exit 31 ;; esac

selected=0
case "$relative" in *"$LOCAL_DATE"*) selected=1 ;; esac
if [ "$IS_CURRENT_DATE" -eq 1 ]; then case "$relative" in */*) ;; *) selected=1 ;; esac; fi
[ "$selected" -eq 1 ] || exit 0

read -r initial_device initial_inode initial_size < <(stat -Lc '%d %i %s' -- "$source_descriptor") || exit 31
case "$initial_device:$initial_inode:$initial_size" in *[!0-9:]*) exit 31 ;; esac
[ "$initial_size" -le "$MAX_FILE_SCAN" ] || exit 41
total_scan=$(cat "$TOTAL_SCAN_FILE")
case "$total_scan" in ''|*[!0-9]*) exit 31 ;; esac
next_scan=$((total_scan + initial_size))
[ "$next_scan" -le "$MAX_TOTAL_SCAN" ] || exit 42
[ "$next_scan" -le "$MAX_SNAPSHOT" ] || exit 43
printf '%s\n' "$next_scan" > "$TOTAL_SCAN_FILE"

destination_relative="services/$service/$relative"
destination="$BUNDLE_ROOT/$destination_relative"
staged="$WORK_ROOT/staged/$destination_relative"
mkdir -p -- "$(dirname "$staged")"
snapshot_hash_line=$(head -c "$initial_size" -- "$source_descriptor" | tee "$staged" | sha256sum) || exit 31
source_snapshot_sha=${snapshot_hash_line%% *}
case "$source_snapshot_sha" in ''|*[!a-f0-9]*) exit 31 ;; esac
staged_size=$(stat -Lc '%s' -- "$staged") || exit 31
[ "$staged_size" -eq "$initial_size" ] || exit 32
archive_entry_hash_line=$(sha256sum -- "$staged") || exit 31
archive_entry_sha=${archive_entry_hash_line%% *}
[ "$archive_entry_sha" = "$source_snapshot_sha" ] || exit 32
verify_hash_line=$(head -c "$initial_size" -- "$source_descriptor" | sha256sum) || exit 31
verify_sha=${verify_hash_line%% *}
[ "$verify_sha" = "$source_snapshot_sha" ] || exit 32

read -r final_device final_inode final_size < <(stat -Lc '%d %i %s' -- "$source_descriptor") || exit 31
[[ -f "$source_file" && ! -L "$source_file" ]] || exit 32
read -r path_device path_inode < <(stat -Lc '%d %i' -- "$source_file") || exit 32
[ "$final_device" = "$initial_device" ] && [ "$final_inode" = "$initial_inode" ] || exit 32
[ "$path_device" = "$initial_device" ] && [ "$path_inode" = "$initial_inode" ] || exit 32
if [ "$IS_CURRENT_DATE" -eq 1 ]; then [ "$final_size" -ge "$initial_size" ] || exit 32; else [ "$final_size" -eq "$initial_size" ] || exit 32; fi

exec 9<&-
mkdir -p -- "$(dirname "$destination")"
mv -T -- "$staged" "$destination"
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
  "$service" "$relative" "$initial_device" "$initial_inode" "$initial_size" "$source_snapshot_sha" "$archive_entry_sha" "$IS_CURRENT_DATE" >> "$MANIFEST_RECORDS"
AIFAR_RUNTIME_DIAGNOSTIC_FILE_HELPER
chmod 700 "$FILE_HELPER"

export BUNDLE_ROOT WORK_ROOT TOTAL_SCAN_FILE MANIFEST_RECORDS ERROR_RECORDS
export MAX_FILE_SCAN MAX_TOTAL_SCAN MAX_SNAPSHOT LOCAL_DATE IS_CURRENT_DATE="$is_current"

for service in $SERVICES; do
  case "$service" in ''|*[!a-z0-9-]*) exit 30 ;; esac
  service_root="$LOG_ROOT/$service"
  if [ -e "$service_root" ] || [ -L "$service_root" ]; then
    [ -d "$service_root" ] && [ ! -L "$service_root" ] || exit 30
    service_canonical=$(readlink -f -- "$service_root") || exit 30
    [ "$service_canonical" = "$service_root" ] || exit 30
    candidates="$WORK_ROOT/candidates-$service"
    find "$service_root" -xdev -type f \( -name '*.log' -o -name '*.log.*' \) ! -name '.*' -print0 > "$candidates" || exit 30
    xargs -0 -r -n 1 bash "$FILE_HELPER" "$service" "$service_root" < "$candidates" || exit $?
  fi
done

[ -s "$MANIFEST_RECORDS" ] || exit 46

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
systemctl status aifar-agent --no-pager 2>&1 | sed -E 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' > "$BUNDLE_ROOT/diagnostics/agent-status.txt" \
  || printf 'agent-status-failed\t-\t-\t1\n' >> "$ERROR_RECORDS"
{ uptime || true; free -m || true; df -h "$INSTALL_ROOT" || true; } 2>&1 \
  | sed -E 's/[A-Za-z0-9+\/_=-]{64,}/[REDACTED]/g' > "$BUNDLE_ROOT/diagnostics/host-resources.txt"
cp "$ERROR_RECORDS" "$BUNDLE_ROOT/collection-errors.txt"

manifest_tmp="$WORK_ROOT/manifest.json"
snapshot_started_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
printf '{"formatVersion":"AIFAR_DIAGNOSTIC_RAW_SNAPSHOT_V1","localDate":"%s","since":"%s","until":"%s","serverTimezone":"%s","selectedServices":"%s","snapshotStartedAt":"%s","sources":[' \
  "$LOCAL_DATE" "$SINCE" "$UNTIL" "$server_timezone" "$SERVICES" "$snapshot_started_at" > "$manifest_tmp"
first=1
tab=$(printf '\t')
while IFS="$tab" read -r service relative device inode captured source_sha entry_sha active_snapshot; do
  [ -n "$service" ] || continue
  if [ "$first" -eq 0 ]; then printf ',' >> "$manifest_tmp"; fi
  first=0
  printf '{"service":"%s","sourcePath":"%s","device":"%s","inode":"%s","capturedBytes":%s,"sourceSnapshotSha256":"%s","archiveEntrySha256":"%s","activeSnapshot":%s}' \
    "$service" "$relative" "$device" "$inode" "$captured" "$source_sha" "$entry_sha" "$active_snapshot" >> "$manifest_tmp"
done < "$MANIFEST_RECORDS"
printf ']}\n' >> "$manifest_tmp"
mv -T -- "$manifest_tmp" "$BUNDLE_ROOT/manifest.json"

uncompressed_bytes=$(find "$BUNDLE_ROOT" -xdev -type f -printf '%s\n' | awk '{ total += $1 } END { printf "%.0f\n", total }') || exit 45
case "$uncompressed_bytes" in ''|*[!0-9]*) exit 45 ;; esac
[ "$uncompressed_bytes" -le "$MAX_SNAPSHOT" ] || exit 43
warning_count=$(awk -F '\t' '{ total += $4 } END { print total + 0 }' "$ERROR_RECORDS") || exit 35
printf 'AIFAR_DIAG_STREAM_V1\t%s\t%s\t%s\t%s\n' "$ARCHIVE_NAME" "$uncompressed_bytes" "$warning_count" "$server_timezone"
tar -czf - -C "$BUNDLE_PARENT" "$ARCHIVE_BASE"
