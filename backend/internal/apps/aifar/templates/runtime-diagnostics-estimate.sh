set -eu

for required in docker find tar gzip sha256sum df stat setsid; do
  command -v "$required" >/dev/null 2>&1 || exit 20
done

INSTALL_ROOT={{.InstallRoot}}
INSTANCE_ID={{.InstanceID}}
SERVICES={{.Services}}
SINCE_UNIX={{.SinceUnix}}
UNTIL_UNIX={{.UntilUnix}}
LOG_ROOT="$INSTALL_ROOT/runtime/logs"

file_total=0
container_total=0
for service in $SERVICES; do
  file_bytes=0
  if [ -d "$LOG_ROOT/$service" ]; then
    if ! file_sizes=$(find "$LOG_ROOT/$service" -xdev -type f -newermt "@$SINCE_UNIX" ! -newermt "@$UNTIL_UNIX" -printf '%s\n'); then
      exit 21
    fi
    for file_size in $file_sizes; do
      case "$file_size" in
        ''|*[!0-9]*) exit 21 ;;
      esac
      file_bytes=$((file_bytes + file_size))
    done
  fi

  container_bytes=0
  conservative=0
  if ! container_ids=$(docker ps -aq --filter "label=aifar.instance=$INSTANCE_ID" --filter "label=aifar.service=$service"); then
    exit 22
  fi
  for container_id in $container_ids; do
    if ! log_path=$(docker inspect --format='{{"{{.LogPath}}"}}' "$container_id"); then
      exit 23
    fi
    if [ -z "$log_path" ] || [ ! -f "$log_path" ]; then
      exit 23
    fi
    if ! log_size=$(stat -c '%s' -- "$log_path"); then
      exit 24
    fi
    case "$log_size" in
      ''|*[!0-9]*) exit 24 ;;
    esac
    container_bytes=$((container_bytes + log_size))
    conservative=1
  done

  file_total=$((file_total + file_bytes))
  container_total=$((container_total + container_bytes))
  printf 'AIFAR_DIAG_SERVICE\t%s\t%s\t%s\n' "$service" "$file_bytes" "$container_bytes"
  if [ "$conservative" -eq 1 ]; then
    printf 'AIFAR_DIAG_WARNING\tdocker-log-conservative\t%s\n' "$service"
  fi
done

available_kib=0
while read -r filesystem blocks used available capacity mounted; do
  case "$available" in
    ''|*[!0-9]*) continue ;;
    *) available_kib=$available ;;
  esac
done <<AIFAR_DIAG_DF
$(df -Pk "$INSTALL_ROOT")
AIFAR_DIAG_DF
available_bytes=$((available_kib * 1024))

total_bytes=$((file_total + container_total))
buffer_bytes=$((total_bytes / 5))
if [ "$buffer_bytes" -lt 536870912 ]; then
  buffer_bytes=536870912
fi
required_bytes=$((total_bytes + 1073741824 + buffer_bytes))
printf 'AIFAR_DIAG_TOTAL\t%s\t%s\t%s\t%s\t%s\n' "$file_total" "$container_total" "$total_bytes" "$available_bytes" "$required_bytes"
