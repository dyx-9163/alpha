#!/usr/bin/env bash
set -Eeuo pipefail
trap 'printf "AIFAR_SILO_LINE=%s\n" "$LINENO" >&2' ERR
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

# A cancelled SSH command may outlive its connection. Keep recovery behind
# the previous command on this host, even after the Panel disconnects.
exec 9>/run/lock/aifar-minio-offline-upgrade.lock
flock -w 120 9

verify_checksum() {
  printf '%s  %s\n' "$1" "$2" | sha256sum --check --status
}

verify_version() {
  local first
  first=$("$1" --version)
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
  [[ "$(systemctl show --property=FragmentPath --value "$unit")" = "$unit_path" ]]
  [[ "$(systemctl show --property=WorkingDirectory --value "$unit")" = "$root" ]]
  [[ -z "$(systemctl show --property=DropInPaths --value "$unit")" ]]
  grep -Fx -- "EnvironmentFile=$env_file" "$unit_path" >/dev/null
  grep -Fx -- "ExecStart=$server_bin server "'$MINIO_OPTS $MINIO_VOLUMES' "$unit_path" >/dev/null
}

check_active_process() {
  local release=$1 pid i
  local -a arguments
  systemctl is-active --quiet "$unit"
  pid=$(systemctl show --property=MainPID --value "$unit")
  [[ "$pid" =~ ^[1-9][0-9]*$ ]]
  [[ "$(readlink -e "/proc/$pid/exe")" = "$server_bin" ]]
  verify_version "/proc/$pid/exe" "$release"
  if [[ "$release" = "$silo_release" ]]; then
    cmp --silent -- "/proc/$pid/exe" "$candidate/silo"
  fi
  mapfile -d '' -t arguments < "/proc/$pid/cmdline"
  [[ ${#arguments[@]} -eq $(( 6 + volume_count )) ]]
  [[ ${arguments[0]} = "$server_bin" && ${arguments[1]} = server ]]
  [[ ${arguments[2]} = --address && ${arguments[3]} = ":$api_port" ]]
  [[ ${arguments[4]} = --console-address && ${arguments[5]} = ":$console_port" ]]
  for (( i=0; i<volume_count; i++ )); do
    [[ ${arguments[i+6]} = "${volumes[i]}" ]]
  done
}

check_stopped() {
  [[ "$(systemctl show --property=MainPID --value "$unit")" = 0 ]]
  [[ "$(systemctl show --property=ActiveState --value "$unit")" = inactive ]]
  [[ "$(systemctl show --property=Result --value "$unit")" = success ]]
  local listeners
  listeners=$(ss -H -lnt "( sport = :$api_port or sport = :$console_port )")
  [[ -z "$listeners" ]]
}

check_boot() {
  cmp --silent -- "$backup/boot-id" /proc/sys/kernel/random/boot_id
}

verify_mount() {
  local volume=$1
  [[ "$(realpath -e -- "$volume")" = "$volume" ]]
  [[ -d "$volume/.minio.sys" && ! -L "$volume/.minio.sys" ]]
  findmnt --noheadings --raw --target "$volume" --output TARGET,SOURCE,FSTYPE,OPTIONS,UUID,MAJ:MIN
  stat -c '%d:%i' -- "$volume"
}

check_volume_mounts() {
  local i
  for i in "${!volumes[@]}"; do
    verify_mount "${volumes[i]}" | cmp --silent -- "$backup/volume-$i.mount" -
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
  for bucket in "${buckets[@]}"; do
    timeout 20 "$legacy_mc" --config-dir "$mc_config" --json version info "aifar-local/$bucket"
    timeout 20 "$legacy_mc" --config-dir "$mc_config" --json replicate ls "aifar-local/$bucket"
    timeout 20 "$legacy_mc" --config-dir "$mc_config" --json replicate status "aifar-local/$bucket"
  done
}

inspect_source() {
  local i mount_target mounts
  check_unit
  check_active_process "$old_release"
  readiness "$local_url"
  readiness "$peer_url"
  cp -- /proc/sys/kernel/random/boot_id "$backup/boot-id"
  mounts=$(findmnt --list --raw --noheadings --output TARGET)
  for i in "${!volumes[@]}"; do
    while IFS= read -r mount_target; do
      [[ "$mount_target" != "${volumes[i]}/"* ]]
    done <<< "$mounts"
    verify_mount "${volumes[i]}" > "$backup/volume-$i.mount"
  done
  check_backup_capacity
  sha256sum -- "$server_bin" "$legacy_mc" "$unit_path" "$env_file" \
    "$mc_config/config.json" > "$backup/source.sha256"
  read_replication
}

archive_tree() {
  local source=$1 file=$2
  tar --acls --xattrs --selinux --numeric-owner --one-file-system --sparse \
    -cpf "$file.partial" -C "$source" .
  tar --acls --xattrs --selinux --numeric-owner --compare -f "$file.partial" -C "$source"
  mv -T -- "$file.partial" "$file"
}

cold_backup() {
  local i
  check_boot
  check_stopped
  check_unit
  check_volume_mounts
  check_backup_capacity
  sha256sum --check --status "$backup/source.sha256"
  cp -a -- "$server_bin" "$backup/minio.original"
  cp -a -- "$unit_path" "$backup/$unit"
  printf '%s\n' "${volumes[@]}" > "$backup/minio-volume-paths.txt"
  archive_tree "$root/bin" "$backup/minio-bin.tar"
  archive_tree "$root/conf" "$backup/minio-conf.tar"
  for i in "${!volumes[@]}"; do
    archive_tree "${volumes[i]}" "$backup/volume-$i.tar"
    tar -tf "$backup/volume-$i.tar" --occurrence=1 ./.minio.sys >/dev/null
  done
  (cd "$backup" && sha256sum -- *.tar "$unit" "minio.original" "minio-volume-paths.txt" volume-*.mount > checksums.txt)
  check_stopped
  check_volume_mounts
  sync -f "$backup"
  touch -- "$backup/cold-backup-complete"
  sync -f "$backup"
}

cleanup_partial_backups() {
  local i
  rm -f -- "$backup/minio-bin.tar.partial" "$backup/minio-conf.tar.partial"
  for i in "${!volumes[@]}"; do
    rm -f -- "$backup/volume-$i.tar.partial"
  done
}

install_like_original() {
  local src=$1 dest=$2 temporary
  temporary=$(mktemp "$root/bin/.aifar-silo.XXXXXXXX")
  install -m "$(stat -c %a "$server_bin")" -o "$(stat -c %u "$server_bin")" \
    -g "$(stat -c %g "$server_bin")" -- "$src" "$temporary"
  if command -v selinuxenabled >/dev/null && selinuxenabled; then
    chcon --reference="$server_bin" -- "$temporary"
  fi
  mv -T -- "$temporary" "$dest"
}

case "$phase" in
  stage) prepare_candidates ;;
  before) inspect_source ;;
  stop)
    check_boot
    check_unit
    check_active_process "$old_release"
    sha256sum --check --status "$backup/source.sha256"
    systemctl stop "$unit"
    check_stopped
    ;;
  backup) cold_backup ;;
  verify-backup)
    check_boot
    check_stopped
    check_volume_mounts
    [[ -f "$backup/cold-backup-complete" ]]
    (cd "$backup" && sha256sum --check --status checksums.txt)
    ;;
  switch)
    check_boot
    check_stopped
    check_unit
    check_volume_mounts
    [[ -f "$backup/cold-backup-complete" && -s "$backup/checksums.txt" ]]
    [[ ! -e "$mcli" && ! -L "$mcli" ]]
    sha256sum --check --status "$backup/source.sha256"
    (cd "$candidate" && sha256sum --check --status binaries.sha256)
    touch -- "$backup/switch-attempted"
    sync -f "$backup"
    install_like_original "$candidate/mcli" "$mcli"
    install_like_original "$candidate/silo" "$server_bin"
    sync -f "$root/bin"
    ;;
  start)
    check_boot
    check_stopped
    check_unit
    check_volume_mounts
    cmp --silent -- "$candidate/silo" "$server_bin"
    cmp --silent -- "$candidate/mcli" "$mcli"
    tail -n +2 "$backup/source.sha256" | sha256sum --check --status
    touch -- "$backup/start-attempted"
    sync -f "$backup"
    systemctl start "$unit"
    ;;
  after)
    check_boot
    check_volume_mounts
    wait_live "$local_url"
    wait_live "$peer_url"
    check_active_process "$silo_release"
    read_replication
    ;;
  stop-for-recovery)
    systemctl stop "$unit"
    check_stopped
    ;;
  restore-minio)
    check_boot
    [[ ! -e "$backup/start-attempted" ]]
    cleanup_partial_backups
    if [[ -e "$backup/switch-attempted" ]]; then
      check_stopped
      [[ -f "$backup/cold-backup-complete" ]]
      read -r original_sha _ < "$backup/source.sha256"
      verify_checksum "$original_sha" "$backup/minio.original"
      install_like_original "$backup/minio.original" "$server_bin"
      if [[ -e "$mcli" ]]; then
        cmp --silent -- "$candidate/mcli" "$mcli"
        rm -- "$mcli"
      fi
      sync -f "$root/bin"
    fi
    sha256sum --check --status "$backup/source.sha256"
    verify_version "$server_bin" "$old_release"
    ;;
  start-old)
    check_boot
    [[ ! -e "$backup/start-attempted" ]]
    check_unit
    check_volume_mounts
    sha256sum --check --status "$backup/source.sha256"
    systemctl start "$unit"
    wait_live "$local_url"
    check_active_process "$old_release"
    ;;
  *) exit 2 ;;
esac
