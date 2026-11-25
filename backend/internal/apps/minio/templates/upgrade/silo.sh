#!/usr/bin/env bash
set -Eeuo pipefail
trap 'printf "AIFAR_SILO_LINE=%s\n" "$LINENO" >&2' ERR
trap 'exit 124' TERM
umask 077
export LC_ALL=C

phase=$1
root=$2
backup=$3
stage=$4
local_url=$5
peer_url=$6
console_port=$7
old_release=$8
silo_release=$9
silo_asset=${10}
silo_sha=${11}
mcli_asset=${12}
mcli_sha=${13}
volume_count=${14}
shift 14
volumes=()
for (( i=0; i<volume_count; i++ )); do
  volumes+=("$1")
  shift
done
buckets=("$@")

unit=aifar-minio.service
unit_path="/etc/systemd/system/$unit"
server_bin="$root/bin/minio"
legacy_mc="$root/bin/mc"
mcli="$root/bin/mcli"
mc_config="$root/conf/mc"
env_file="$root/conf/minio.env"
api_port=${local_url##*:}
candidate="$backup/.candidate"
temporary_binary=''
trap 'if [[ -n "$temporary_binary" ]]; then rm -f -- "$temporary_binary"; fi' EXIT

# A cancelled SSH command may outlive its connection. Keep recovery behind
# the previous command on this host, even after the Panel disconnects.
phase_lock=/run/lock/aifar-minio-offline-upgrade.lock
if [[ ! -e "$phase_lock" && ! -L "$phase_lock" ]]; then
  (set -o noclobber; : > "$phase_lock") 2>/dev/null || true
fi
[[ -f "$phase_lock" && ! -L "$phase_lock" && "$(stat -c '%u:%a' -- "$phase_lock")" = "$EUID:600" ]]
exec 9< "$phase_lock"
if [[ "$phase" = state || "$phase" = idle || "$phase" = verify-old ]]; then
  flock -n 9
else
  flock -w 120 9
fi

fail() { printf '%s\n' "$*" >&2; exit 1; }

check_workspace() {
  local task_name=${backup##*/}
  [[ "$task_name" =~ ^[A-Za-z0-9_-]+$ && "$backup" = "${root%/*}/_backups/minio-silo/$task_name" ]]
  [[ -d "$root" && "$(realpath -e -- "$root")" = "$root" ]]
  [[ -d "$backup" && "$(realpath -e -- "$backup")" = "$backup" ]]
  [[ "$(stat -c '%u:%a' -- "$backup")" = "$EUID:700" ]]
}

verify_checksum() {
  printf '%s  %s\n' "$1" "$2" | sha256sum --check --strict --status
}

verify_version() {
  local first
  first=$("$1" --version) || return 1
  first=${first%%$'\n'*}
  [[ " $first " == *" RELEASE.$2 "* ]]
}

readiness() {
  curl --silent --fail --connect-timeout 3 --max-time 5 --noproxy '*' \
    "$1/minio/health/ready" >/dev/null &&
  curl --silent --fail --connect-timeout 3 --max-time 5 --noproxy '*' \
    "$1/minio/health/cluster" >/dev/null
}

wait_live() {
  local endpoint=$1 attempt
  for (( attempt=0; attempt<40; attempt++ )); do
    if readiness "$endpoint"; then
      return 0
    fi
    sleep 2
  done
  return 1
}

check_unit() {
  local dropins
  [[ "$(systemctl show --property=FragmentPath --value "$unit")" = "$unit_path" ]] || return 1
  [[ "$(systemctl show --property=WorkingDirectory --value "$unit")" = "$root" ]] || return 1
  dropins=$(systemctl show --property=DropInPaths --value "$unit") || return 1
  [[ -z "$dropins" && -f "$unit_path" && ! -L "$unit_path" ]] || return 1
  grep -Fx -- "EnvironmentFile=$env_file" "$unit_path" >/dev/null || return 1
  grep -Fx -- "ExecStart=$server_bin server "'$MINIO_OPTS $MINIO_VOLUMES' "$unit_path" >/dev/null
}

check_active_process() {
  local release=$1 pid i
  local -a arguments
  systemctl is-active --quiet "$unit" || return 1
  pid=$(systemctl show --property=MainPID --value "$unit") || return 1
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
  [[ "$(readlink -e "/proc/$pid/exe")" = "$server_bin" ]] || return 1
  cmp --silent -- "/proc/$pid/exe" "$server_bin" || return 1
  verify_version "/proc/$pid/exe" "$release" || return 1
  mapfile -d '' -t arguments < "/proc/$pid/cmdline" || return 1
  [[ ${#arguments[@]} -eq $(( 6 + volume_count )) ]] || return 1
  [[ ${arguments[0]} = "$server_bin" && ${arguments[1]} = server ]] || return 1
  [[ ${arguments[2]} = --address && ${arguments[3]} = ":$api_port" ]] || return 1
  [[ ${arguments[4]} = --console-address && ${arguments[5]} = ":$console_port" ]] || return 1
  for (( i=0; i<volume_count; i++ )); do
    [[ ${arguments[i+6]} = "${volumes[i]}" ]] || return 1
  done
}

check_quiescent() {
  local listeners state
  [[ "$(systemctl show --property=MainPID --value "$unit")" = 0 ]] || return 1
  state=$(systemctl show --property=ActiveState --value "$unit") || return 1
  [[ "$state" = inactive || "$state" = failed ]] || return 1
  listeners=$(ss -H -lnt "( sport = :$api_port or sport = :$console_port )") || return 1
  [[ -z "$listeners" ]]
}

check_stopped() {
  check_quiescent || return 1
  [[ "$(systemctl show --property=ActiveState --value "$unit")" = inactive ]] || return 1
  [[ "$(systemctl show --property=Result --value "$unit")" = success ]]
}

check_boot() {
  cmp --silent -- "$backup/boot-id" /proc/sys/kernel/random/boot_id
}

verify_mount() {
  local volume=$1
  [[ "$(realpath -e -- "$volume")" = "$volume" ]] || return 1
  [[ -d "$volume/.minio.sys" && ! -L "$volume/.minio.sys" ]] || return 1
  findmnt --noheadings --raw --target "$volume" --output TARGET,SOURCE,FSTYPE,OPTIONS,UUID,MAJ:MIN || return 1
  stat -c '%d:%i' -- "$volume"
}

check_no_nested_mounts() {
  local tree mount_target mounts
  mounts=$(findmnt --kernel --raw --noheadings --output TARGET) || return 1
  for tree in "$@"; do
    while IFS= read -r mount_target; do
      [[ "$mount_target" != "$tree/"* ]] || return 1
    done <<< "$mounts"
  done
}

check_volume_mounts() {
  local i
  check_no_nested_mounts "$root/bin" "$root/conf" "${volumes[@]}" || return 1
  for i in "${!volumes[@]}"; do
    verify_mount "${volumes[i]}" | cmp --silent -- "$backup/volume-$i.mount" - || return 1
  done
}

check_backup_capacity() {
  local volume size total=0 available
  # tar headers for very small objects also consume space. The archive must
  # still complete and compare against the source before cutover.
  for volume in "$root/bin" "$root/conf" "${volumes[@]}"; do
    size=$(du -sx -B1 -- "$volume" | awk '{print $1}')
    total=$(( total + size ))
  done
  size=$(stat -c '%s' -- "$server_bin")
  total=$(( total + size ))
  available=$(df --output=avail -B1 -- "$backup" | tail -n1)
  (( available > total + total/10 + 67108864 ))
}

prepare_candidates() {
  [[ $(uname -m) = x86_64 ]]
  command -v curl >/dev/null
  command -v ss >/dev/null
  [[ -x "$server_bin" && -x "$legacy_mc" && ! -e "$mcli" && ! -L "$mcli" ]]
  [[ "$(realpath -e -- "$root")" = "$root" ]]
  [[ "$(realpath -e -- "$server_bin")" = "$server_bin" ]]
  [[ "$(realpath -e -- "$legacy_mc")" = "$legacy_mc" ]]
  [[ "$(realpath -e -- "$env_file")" = "$env_file" ]]
  [[ "$(realpath -e -- "$mc_config/config.json")" = "$mc_config/config.json" ]]
  [[ "$(realpath -m -- "$backup")" = "$backup" ]]
  mkdir -p -- "${backup%/*}"
  mkdir -- "$backup"
  mkdir -- "$candidate"
  # Verify root's own copies before reading either archive.
  cp -- "$stage/$silo_asset" "$candidate/$silo_asset"
  cp -- "$stage/$mcli_asset" "$candidate/$mcli_asset"
  verify_checksum "$silo_sha" "$candidate/$silo_asset"
  verify_checksum "$mcli_sha" "$candidate/$mcli_asset"
  tar -xOzf "$candidate/$silo_asset" silo > "$candidate/silo"
  tar -xOzf "$candidate/$mcli_asset" mcli > "$candidate/mcli"
  chmod 0700 -- "$candidate/silo" "$candidate/mcli"
  verify_version "$candidate/silo" "$silo_release"
  verify_version "$candidate/mcli" "$silo_release"
  (cd "$candidate" && sha256sum silo mcli > binaries.sha256)
  rm -f -- "$candidate/$silo_asset" "$candidate/$mcli_asset"
}

read_replication() {
  local alias endpoint expected bucket
  for alias in aifar-local aifar-peer; do
    expected=$local_url
    if [[ "$alias" = aifar-peer ]]; then expected=$peer_url; fi
    # The plaintext listing contains credentials on other lines. Keep only its URL here.
    endpoint=$(timeout 10 "$legacy_mc" --config-dir "$mc_config" --no-color alias list "$alias" |
      awk '$1 == "URL" && $2 == ":" {print $3}')
    [[ "$endpoint" = "$expected" ]]
    timeout 20 "$legacy_mc" --config-dir "$mc_config" --json admin info "$alias"
  done
  printf '%s\n' AIFAR_SILO_BUCKET_INVENTORY_BEGIN
  timeout 20 "$legacy_mc" --config-dir "$mc_config" --json ls aifar-local
  printf '%s\n' AIFAR_SILO_BUCKET_INVENTORY_END
  for bucket in "${buckets[@]}"; do
    timeout 20 "$legacy_mc" --config-dir "$mc_config" --json version info "aifar-local/$bucket"
    timeout 20 "$legacy_mc" --config-dir "$mc_config" --json replicate ls "aifar-local/$bucket"
    timeout 20 "$legacy_mc" --config-dir "$mc_config" --json replicate status "aifar-local/$bucket"
  done
}

inspect_source() {
  local i
  check_workspace
  check_unit
  check_active_process "$old_release"
  readiness "$local_url"
  readiness "$peer_url"
  check_no_nested_mounts "$root/bin" "$root/conf" "${volumes[@]}"
  cp -- /proc/sys/kernel/random/boot_id "$backup/boot-id"
  for i in "${!volumes[@]}"; do
    verify_mount "${volumes[i]}" > "$backup/volume-$i.mount"
  done
  check_backup_capacity
  sha256sum -- "$server_bin" "$legacy_mc" "$unit_path" "$env_file" \
    "$mc_config/config.json" > "$backup/source.sha256"
  sync -f "$backup"
  read_replication
}

archive_tree() {
  local source=$1 file=$2
  check_no_nested_mounts "$source"
  tar --acls --xattrs --xattrs-include='*' --selinux --numeric-owner --one-file-system --sparse \
    -cpf "$file.partial" -C "$source" .
  tar --acls --xattrs --xattrs-include='*' --selinux --numeric-owner --compare -f "$file.partial" -C "$source"
  check_no_nested_mounts "$source"
  mv -T -- "$file.partial" "$file"
}

read_attempt() {
  local file="$backup/$1"
  if [[ -e "$file" || -L "$file" ]]; then
    [[ -f "$file" && ! -L "$file" && ! -s "$file" && -O "$file" ]] || return 1
    printf '1'
  else
    printf '0'
  fi
}

record_attempt() {
  local existing
  existing=$(read_attempt "$1")
  if [[ "$existing" = 0 ]]; then (set -o noclobber; : > "$backup/$1"); fi
  sync -f "$backup"
}

read_source_checksums() {
  local i digest
  local -a records=()
  source_paths=("$server_bin" "$legacy_mc" "$unit_path" "$env_file" "$mc_config/config.json")
  source_digests=()
  [[ -f "$backup/source.sha256" && ! -L "$backup/source.sha256" ]]
  mapfile -t records < "$backup/source.sha256"
  [[ ${#records[@]} -eq ${#source_paths[@]} ]]
  for i in "${!source_paths[@]}"; do
    digest=${records[i]%% *}
    [[ "$digest" =~ ^[[:xdigit:]]{64}$ && "${records[i]}" = "$digest  ${source_paths[i]}" ]]
    [[ -f "${source_paths[i]}" && "$(realpath -e -- "${source_paths[i]}")" = "${source_paths[i]}" ]]
    source_digests+=("$digest")
  done
}

check_source_configuration() {
  local i
  for (( i=1; i<${#source_paths[@]}; i++ )); do
    verify_checksum "${source_digests[i]}" "${source_paths[i]}" || return 1
  done
}

check_candidate() {
  [[ -d "$candidate" && ! -L "$candidate" && "$(stat -c '%u:%a' -- "$candidate")" = "$EUID:700" ]] || return 1
  [[ -f "$candidate/binaries.sha256" && ! -L "$candidate/binaries.sha256" ]] || return 1
  [[ -x "$candidate/silo" && ! -L "$candidate/silo" && -x "$candidate/mcli" && ! -L "$candidate/mcli" ]] || return 1
  (cd "$candidate" && sha256sum -- silo mcli) | cmp --silent -- "$candidate/binaries.sha256" -
}

read_backup_files() {
  local i
  backup_files=(minio-bin.tar minio-conf.tar)
  for i in "${!volumes[@]}"; do backup_files+=("volume-$i.tar"); done
  backup_files+=("$unit" minio.original minio-volume-paths.txt)
  for i in "${!volumes[@]}"; do backup_files+=("volume-$i.mount"); done
}

check_backup_index() {
  local i file digest
  local -a records=()
  [[ -f "$backup/checksums.txt" && ! -L "$backup/checksums.txt" ]]
  mapfile -t records < "$backup/checksums.txt"
  read_backup_files
  [[ ${#records[@]} -eq ${#backup_files[@]} ]]
  for i in "${!backup_files[@]}"; do
    file=${backup_files[i]}
    digest=${records[i]%% *}
    [[ "$digest" =~ ^[[:xdigit:]]{64}$ && "${records[i]}" = "$digest  $file" ]]
    [[ -f "$backup/$file" && ! -L "$backup/$file" ]]
  done
  printf '%s\n' "${volumes[@]}" | cmp --silent -- "$backup/minio-volume-paths.txt" -
}

verify_completed_backup() {
  (( backup_complete ))
  check_backup_index
  (cd "$backup" && sha256sum --check --strict --status checksums.txt)
  check_boot
  check_volume_mounts
}

read_node_state() {
  check_workspace
  check_boot
  check_unit
  check_volume_mounts
  read_source_checksums
  stop_attempted=$(read_attempt stop-attempted)
  backup_complete=$(read_attempt cold-backup-complete)
  switch_attempted=$(read_attempt switch-attempted)
  start_attempted=$(read_attempt start-attempted)
  restore_attempted=$(read_attempt restore-attempted)
  (( (!backup_complete || stop_attempted) && (!switch_attempted || backup_complete) &&
    (!start_attempted || switch_attempted) && (!restore_attempted || (stop_attempted && !start_attempted)) ))
  if (( backup_complete )); then check_backup_index; fi

  staged_mcli=0
  if [[ -e "$mcli" || -L "$mcli" ]]; then
    (( switch_attempted ))
    check_candidate
    [[ -f "$mcli" && ! -L "$mcli" ]]
    cmp --silent -- "$candidate/mcli" "$mcli"
    staged_mcli=1
  fi
  if verify_checksum "${source_digests[0]}" "$server_bin"; then
    verify_version "$server_bin" "$old_release"
    (( !start_attempted ))
    if check_active_process "$old_release"; then
      (( !staged_mcli ))
      runtime_state=old-running
    else
      (( stop_attempted ))
      check_quiescent
      runtime_state=old-stopped
    fi
  else
    (( staged_mcli && switch_attempted ))
    cmp --silent -- "$candidate/silo" "$server_bin"
    verify_version "$server_bin" "$silo_release"
    if check_active_process "$silo_release"; then
      (( start_attempted && !restore_attempted ))
      runtime_state=silo-running
    else
      check_quiescent
      runtime_state=silo-stopped
    fi
  fi
  # Reporting a still-running untouched source is harmless even if a config
  # edit made it ineligible for this migration. The stop phase checks it again.
  if [[ "$runtime_state" != old-running ]] || (( stop_attempted )); then
    check_source_configuration
  fi
}

cold_backup() {
  local i
  read_node_state
  if (( backup_complete )); then
    verify_completed_backup
    return
  fi
  [[ "$runtime_state" = old-stopped ]]
  (( !switch_attempted && !restore_attempted ))
  check_stopped
  cleanup_partial_backups
  check_backup_capacity
  check_volume_mounts
  cp -aT -- "$server_bin" "$backup/minio.original"
  cp -aT -- "$unit_path" "$backup/$unit"
  printf '%s\n' "${volumes[@]}" > "$backup/minio-volume-paths.txt"
  archive_tree "$root/bin" "$backup/minio-bin.tar"
  archive_tree "$root/conf" "$backup/minio-conf.tar"
  for i in "${!volumes[@]}"; do
    archive_tree "${volumes[i]}" "$backup/volume-$i.tar"
    tar -tf "$backup/volume-$i.tar" --occurrence=1 ./.minio.sys >/dev/null
  done
  read_backup_files
  (cd "$backup" && sha256sum -- "${backup_files[@]}" > checksums.txt)
  check_stopped
  check_volume_mounts
  sync -f "$backup"
  record_attempt cold-backup-complete
}

cleanup_partial_backups() {
  local file
  read_backup_files
  for file in "${backup_files[@]}"; do
    # Keep the live mount snapshots from the original preflight.
    if [[ "$file" != *.mount ]]; then rm -f -- "$backup/$file" "$backup/$file.partial"; fi
  done
  rm -f -- "$backup/checksums.txt"
}

install_like_original() {
  local src=$1 dest=$2
  temporary_binary=$(mktemp "$root/bin/.aifar-silo.XXXXXXXX")
  install -m "$(stat -c %a "$server_bin")" -o "$(stat -c %u "$server_bin")" \
    -g "$(stat -c %g "$server_bin")" -- "$src" "$temporary_binary"
  if command -v selinuxenabled >/dev/null && selinuxenabled; then
    chcon --reference="$server_bin" -- "$temporary_binary"
  fi
  mv -T -- "$temporary_binary" "$dest"
  temporary_binary=''
}

case "$phase" in
  stage) prepare_candidates ;;
  before) inspect_source ;;
  idle) check_workspace ;;
  state)
    read_node_state
    printf 'AIFAR_SILO_STATE\t%s\t%s\t%s\t%s\t%s\t%s\n' "$runtime_state" "$stop_attempted" "$backup_complete" \
      "$switch_attempted" "$start_attempted" "$restore_attempted"
    ;;
  verify-old)
    read_node_state
    [[ "$runtime_state" = old-running ]]
    wait_live "$local_url"
    check_active_process "$old_release"
    ;;
  stop)
    read_node_state
    (( !switch_attempted && !restore_attempted ))
    if [[ "$runtime_state" = old-stopped ]]; then
      check_stopped
      exit 0
    fi
    [[ "$runtime_state" = old-running ]]
    (( !backup_complete ))
    check_source_configuration
    check_candidate
    record_attempt stop-attempted
    systemctl stop "$unit"
    check_stopped
    ;;
  backup) cold_backup ;;
  verify-backup)
    read_node_state
    verify_completed_backup
    ;;
  switch)
    read_node_state
    (( backup_complete && !restore_attempted ))
    if [[ "$runtime_state" = silo-running || "$runtime_state" = silo-stopped ]]; then exit 0; fi
    [[ "$runtime_state" = old-stopped ]]
    check_stopped
    check_candidate
    verify_checksum "${source_digests[0]}" "$backup/minio.original"
    record_attempt switch-attempted
    if (( !staged_mcli )); then install_like_original "$candidate/mcli" "$mcli"; fi
    install_like_original "$candidate/silo" "$server_bin"
    sync -f "$root/bin"
    ;;
  start)
    read_node_state
    (( switch_attempted && !restore_attempted ))
    if [[ "$runtime_state" = silo-stopped ]]; then
      if systemctl is-failed --quiet "$unit"; then systemctl reset-failed "$unit"; fi
      record_attempt start-attempted
      systemctl start "$unit"
    else
      [[ "$runtime_state" = silo-running ]]
    fi
    wait_live "$local_url"
    check_active_process "$silo_release"
    ;;
  after)
    read_node_state
    [[ "$runtime_state" = silo-running ]]
    wait_live "$local_url"
    wait_live "$peer_url"
    check_active_process "$silo_release"
    read_replication
    ;;
  stop-for-recovery)
    read_node_state
    if [[ "$runtime_state" = silo-running ]]; then
      systemctl stop "$unit"
      check_quiescent
    fi
    ;;
  restore-minio)
    read_node_state
    (( stop_attempted && !start_attempted ))
    record_attempt restore-attempted
    if (( !backup_complete )); then cleanup_partial_backups; fi
    if [[ "$runtime_state" = silo-stopped ]]; then
      verify_checksum "${source_digests[0]}" "$backup/minio.original"
      install_like_original "$backup/minio.original" "$server_bin"
    fi
    if (( staged_mcli )); then rm -- "$mcli"; fi
    sync -f "$root/bin"
    verify_checksum "${source_digests[0]}" "$server_bin"
    verify_version "$server_bin" "$old_release"
    ;;
  start-old)
    read_node_state
    (( restore_attempted && !start_attempted && !staged_mcli ))
    if [[ "$runtime_state" = old-stopped ]]; then
      if systemctl is-failed --quiet "$unit"; then systemctl reset-failed "$unit"; fi
      systemctl start "$unit"
    else
      [[ "$runtime_state" = old-running ]]
    fi
    wait_live "$local_url"
    check_active_process "$old_release"
    ;;
  *) exit 2 ;;
esac
