set -eu

umask 077

for required in bash date find mktemp readlink rm stat timedatectl tr; do
  command -v "$required" >/dev/null 2>&1 || exit 20
done

INSTALL_ROOT={{.InstallRoot}}
SERVICES={{.Services}}
LOCAL_DATE={{.LocalDate}}
LOG_ROOT="$INSTALL_ROOT/runtime/logs"
MAX_FILE_SCAN=1073741824
MAX_TOTAL_SCAN=2147483648
MAX_SNAPSHOT=524288000

case "$INSTALL_ROOT" in /*) [ "$INSTALL_ROOT" != "/" ] || exit 21 ;; *) exit 21 ;; esac
case "$LOCAL_DATE" in [0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]) ;; *) exit 23 ;; esac
[ -d "$INSTALL_ROOT" ] && [ ! -L "$INSTALL_ROOT" ] || exit 21
install_canonical=$(readlink -f -- "$INSTALL_ROOT") || exit 21
[ "$install_canonical" = "$INSTALL_ROOT" ] || exit 21

server_timezone=$(timedatectl show -p Timezone --value 2>/dev/null || true)
if [ -z "$server_timezone" ] && [ -L /etc/localtime ]; then
  localtime_target=$(readlink -f /etc/localtime 2>/dev/null || true)
  case "$localtime_target" in /usr/share/zoneinfo/*) server_timezone=${localtime_target#/usr/share/zoneinfo/} ;; esac
fi
case "$server_timezone" in ''|/*|*..*|*[!A-Za-z0-9_+./-]*) exit 21 ;; esac

server_today=$(TZ="$server_timezone" date +%F) || exit 23
day_start=$(TZ="$server_timezone" date -d "$LOCAL_DATE 00:00:00" +%s) || exit 23
day_end=$(TZ="$server_timezone" date -d "$LOCAL_DATE +1 day 00:00:00" +%s) || exit 23
case "$server_today:$day_start:$day_end" in *[!0-9:-]*) exit 23 ;; esac
is_current=0
[ "$LOCAL_DATE" = "$server_today" ] && is_current=1

total_files=0
total_bytes=0
block_code=-
if [ "$LOCAL_DATE" \> "$server_today" ]; then
  block_code=future-date
fi

for service in $SERVICES; do
  case "$service" in ''|*[!a-z0-9-]*) exit 21 ;; esac
  service_root="$LOG_ROOT/$service"
  service_files=0
  service_bytes=0
  if [ "$block_code" = "-" ] && { [ -e "$service_root" ] || [ -L "$service_root" ]; }; then
    [ -d "$service_root" ] && [ ! -L "$service_root" ] || exit 21
    service_canonical=$(readlink -f -- "$service_root") || exit 21
    [ "$service_canonical" = "$service_root" ] || exit 21
    candidate_file=$(mktemp) || exit 21
    find "$service_root" -xdev -type f \( -name '*.log' -o -name '*.log.*' \) ! -name '.*' -printf '%P\t%s\n' > "$candidate_file" || { rm -f -- "$candidate_file"; exit 21; }
    while IFS=$'\t' read -r relative file_size; do
      [ -n "$relative" ] || continue
      case "$relative" in *[!A-Za-z0-9._/-]*|.*|*/.*) continue ;; esac
      lower_relative=$(printf '%s' "$relative" | tr '[:upper:]' '[:lower:]')
      case "$lower_relative" in *config*|*database*|*credential*|*secret*|*password*|*token*|*private*|*keystore*|*truststore*) continue ;; esac
      case "${relative##*/}" in *.lck|*.idx) continue ;; *.log|*.log.[A-Za-z0-9]*) ;; *) continue ;; esac
      selected=0
      case "$relative" in *"$LOCAL_DATE"*) selected=1 ;; esac
      if [ "$is_current" -eq 1 ]; then
        case "$relative" in
          *[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]*) ;;
          *.log) selected=1 ;;
        esac
      fi
      [ "$selected" -eq 1 ] || continue
      case "$file_size" in ''|*[!0-9]*) exit 21 ;; esac
      service_files=$((service_files + 1))
      service_bytes=$((service_bytes + file_size))
      [ "$file_size" -le "$MAX_FILE_SCAN" ] || block_code=file-scan-limit-exceeded
    done < "$candidate_file"
    rm -f -- "$candidate_file"
  fi
  total_files=$((total_files + service_files))
  total_bytes=$((total_bytes + service_bytes))
  printf 'AIFAR_DIAG_SERVICE_V3\t%s\t%s\t%s\n' "$service" "$service_files" "$service_bytes"
done

[ "$total_bytes" -le "$MAX_TOTAL_SCAN" ] || block_code=total-scan-limit-exceeded
[ "$total_bytes" -le "$MAX_SNAPSHOT" ] || block_code=snapshot-limit-exceeded
if [ "$block_code" = "-" ] && [ "$total_files" -eq 0 ]; then block_code=no-candidate-files; fi
printf 'AIFAR_DIAG_TOTAL_V3\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
  "$total_files" "$total_bytes" "$server_timezone" "$LOCAL_DATE" "$day_start" "$day_end" "$is_current" "$block_code"
