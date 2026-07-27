set -eu

umask 077

for required in find readlink stat timedatectl; do
  command -v "$required" >/dev/null 2>&1 || exit 20
done

INSTALL_ROOT={{.InstallRoot}}
SERVICES={{.Services}}
LOG_ROOT="$INSTALL_ROOT/runtime/logs"
MAX_FILE_SCAN=1073741824
MAX_TOTAL_SCAN=2147483648

case "$INSTALL_ROOT" in
  /*) [ "$INSTALL_ROOT" != "/" ] || exit 21 ;;
  *) exit 21 ;;
esac
[ -d "$INSTALL_ROOT" ] && [ ! -L "$INSTALL_ROOT" ] || exit 21
install_canonical=$(readlink -f -- "$INSTALL_ROOT") || exit 21
[ "$install_canonical" = "$INSTALL_ROOT" ] || exit 21

server_timezone=$(timedatectl show -p Timezone --value 2>/dev/null || true)
if [ -z "$server_timezone" ] && [ -L /etc/localtime ]; then
  localtime_target=$(readlink -f /etc/localtime 2>/dev/null || true)
  case "$localtime_target" in
    /usr/share/zoneinfo/*) server_timezone=${localtime_target#/usr/share/zoneinfo/} ;;
  esac
fi
case "$server_timezone" in
  ''|/*|*..*|*[!A-Za-z0-9_+./-]*) exit 21 ;;
esac

total_files=0
total_bytes=0
block_code=-
for service in $SERVICES; do
  case "$service" in ''|*[!a-z0-9-]*) exit 21 ;; esac
  service_root="$LOG_ROOT/$service"
  service_files=0
  service_bytes=0
  if [ -e "$service_root" ] || [ -L "$service_root" ]; then
    [ -d "$service_root" ] && [ ! -L "$service_root" ] || exit 21
    service_canonical=$(readlink -f -- "$service_root") || exit 21
    [ "$service_canonical" = "$service_root" ] || exit 21
    sizes=$(find "$service_root" -xdev -type f \
      \( -name '*.log' -o -name '*.log.*' \) \
      ! -name '.*' \
      ! -ipath '*/.*' \
      ! -ipath '*config*' ! -ipath '*database*' ! -ipath '*credential*' \
      ! -ipath '*secret*' ! -ipath '*password*' ! -ipath '*token*' \
      -printf '%s\n') || exit 21
    for file_size in $sizes; do
      case "$file_size" in ''|*[!0-9]*) exit 21 ;; esac
      service_files=$((service_files + 1))
      service_bytes=$((service_bytes + file_size))
      if [ "$file_size" -gt "$MAX_FILE_SCAN" ]; then
        block_code=file-scan-limit-exceeded
      fi
    done
  fi
  total_files=$((total_files + service_files))
  total_bytes=$((total_bytes + service_bytes))
  printf 'AIFAR_DIAG_SERVICE_V2\t%s\t%s\t%s\n' "$service" "$service_files" "$service_bytes"
done

if [ "$total_bytes" -gt "$MAX_TOTAL_SCAN" ]; then
  block_code=total-scan-limit-exceeded
fi
printf 'AIFAR_DIAG_TOTAL_V2\t%s\t%s\t%s\t%s\n' "$total_files" "$total_bytes" "$server_timezone" "$block_code"
