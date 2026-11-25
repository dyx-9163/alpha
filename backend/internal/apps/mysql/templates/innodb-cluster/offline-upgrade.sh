#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
export LC_ALL=C

phase=$1 app=$2 root=$3 work=$4 backup=$5 task=$6 port=$7
shift 7
args=("$@")
sudo_cmd=()
if (( EUID != 0 )); then sudo_cmd=(sudo -n); fi
as_root() { "${sudo_cmd[@]}" "$@"; }

base="$root/mysql"
shell_base="$root/mysql-shell"
router_base="$root/mysql-router"
config="$root/conf/my.cnf"
router_config="$root/router/mysqlrouter.conf"
unit=aifar-mysql.service
if [[ "$app" == mysql-router ]]; then unit=aifar-mysql-router.service; fi
unit_file="/etc/systemd/system/$unit"
runtime_unit_dir="/run/systemd/system/$unit.d"
candidate="$work/my84.cnf"
secret="$work/credential.cnf"
server_next="$base.next-$task"
shell_next="$shell_base.next-$task"
router_next="$router_base.next-$task"
stop_override=''
restart_unchanged=0
swapped_tree=''
pending_config=''
pending_config_dir=''
swapped_config=0
new_mysql_running=0
uuid_pattern='^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
stop_setting=$'[Service]\nTimeoutStopSec=30min'

trap 'printf "MySQL upgrade phase %s stopped at line %s (status %s)\n" "$phase" "$LINENO" "$?" >&2' ERR
trap 'finish' EXIT
trap 'exit 124' TERM

fail() { printf '%s\n' "$*" >&2; exit 1; }

finish() {
  local status=$? binary="$base/bin/mysqld"
  trap - EXIT
  if [[ "$app" == mysql-router ]]; then binary="$router_base/bin/mysqlrouter"; fi
  if (( status != 0 )); then
    if [[ -n "$swapped_tree" ]] && ! restore_swapped_tree "$swapped_tree"; then
      restart_unchanged=0
      printf 'Could not restore the original binary tree %s.old-%s\n' "$swapped_tree" "$task" >&2
    fi
    if (( swapped_config )); then
      if as_root test -f "$config.old-$task" && ! as_root mv -T -- "$config.old-$task" "$config"; then
        restart_unchanged=0
        printf 'Could not restore the original MySQL configuration %s.old-%s\n' "$config" "$task" >&2
      fi
    fi
    if [[ -n "$pending_config" ]] && ! as_root rm -f -- "$pending_config"; then
      printf 'Could not remove the staged MySQL configuration %s\n' "$pending_config" >&2
    fi
    if [[ -n "$pending_config_dir" ]] && ! as_root rmdir -- "$pending_config_dir"; then
      printf 'Could not remove the staging directory %s\n' "$pending_config_dir" >&2
    fi
  fi
  if (( status != 0 && restart_unchanged )); then
    if restart_original_service "$binary"; then
      printf 'Recovered the original service %s\n' "$unit"
    else
      printf 'Could not recover the original service %s\n' "$unit" >&2
    fi
  fi
  if [[ -n "$stop_override" ]]; then
    if ! as_root rm -f -- "$stop_override" || ! as_root systemctl daemon-reload; then
      printf 'Could not remove the temporary shutdown setting %s or reload systemd\n' "$stop_override" >&2
      if (( new_mysql_running )); then
        printf 'MySQL 8.4.13 process started; the temporary shutdown setting needs reconciliation\n' >&2
      fi
      status=1
    fi
  fi
  exit "$status"
}

check_tree() {
  local tree=$1 mounts
  as_root test -d "$tree" || return 1
  test "$(as_root realpath -e -- "$tree")" = "$tree" || return 1
  mounts=$(findmnt --kernel --raw -rn -o TARGET) || return 1
  if awk -v prefix="$tree/" 'index($0, prefix) == 1 { nested=1 } END { exit !nested }' <<< "$mounts"; then
    fail "Nested mounts require a separately planned backup: $tree"
  fi
}

check_stop_layout() {
  local binary=$1 expected_args=$2 exec principal
  test "$(as_root systemctl show -p FragmentPath --value "$unit")" = "$unit_file"
  test -z "$(as_root systemctl show -p DropInPaths --value "$unit")"
  test -f "$unit_file" && test ! -L "$unit_file"
  exec=$(as_root systemctl show -p ExecStart --value "$unit")
  [[ "$exec" == *"path=$binary ; argv[]=$binary $expected_args ;"* ]]
  principal=$(as_root systemctl show -p User --value "$unit")
  if [[ "$app" == mysql ]]; then
    test "$principal" = aifar-mysql
  else
    test "$principal" = aifar-router
  fi
  check_process "$binary"
}

check_process() {
  local binary=$1 pid
  as_root systemctl is-active --quiet "$unit" || return 1
  pid=$(as_root systemctl show -p MainPID --value "$unit") || return 1
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 1
  test "$(as_root readlink -f -- "/proc/$pid/exe")" = "$binary"
}

start_and_check_process() {
  as_root systemctl start "$unit" || return 1
  wait_for_process "$1"
}

restart_original_service() {
  if [[ "$phase" == server ]] && as_root systemctl is-active --quiet "$unit"; then
    # A failed SQL command might already have stopped group replication.
    as_root systemctl restart "$unit" || return 1
    wait_for_process "$1"
  else
    start_and_check_process "$1"
  fi
}

wait_for_process() {
  local binary=$1 attempt pid
  # Type=simple can report a successful start before the service calls exec.
  for attempt in {1..30}; do
    if check_process "$binary"; then
      return 0
    fi
    if (( attempt < 30 )); then sleep 1; fi
  done
  as_root systemctl show -p ActiveState -p SubState -p MainPID -p Result -p ExecMainStatus "$unit" >&2 || true
  pid=$(as_root systemctl show -p MainPID --value "$unit" || true)
  if [[ "$pid" =~ ^[1-9][0-9]*$ ]]; then
    as_root readlink -f -- "/proc/$pid/exe" >&2 || true
  fi
  printf 'Service %s did not start the expected process %s\n' "$unit" "$binary" >&2
  return 1
}

stop_cleanly() {
  if [[ "$app" == mysql && -z "$stop_override" ]]; then allow_mysql_shutdown; fi
  as_root systemctl stop "$unit"
  test "$(as_root systemctl show -p MainPID --value "$unit")" = 0
  test "$(as_root systemctl show -p ActiveState --value "$unit")" = inactive
  test "$(as_root systemctl show -p Result --value "$unit")" = success
  if [[ "$app" == mysql ]]; then
    test "$(as_root systemctl show -p ExecMainCode --value "$unit")" = 1
    test "$(as_root systemctl show -p ExecMainStatus --value "$unit")" = 0
  fi
}

allow_mysql_shutdown() {
  local dir="$runtime_unit_dir" target
  test -z "$(as_root systemctl show -p DropInPaths --value "$unit")"
  as_root install -d -m 0755 -- "$dir"
  test "$(as_root realpath -e -- "$dir")" = "$dir"
  target="$dir/92-aifar-upgrade-$task.conf"
  if as_root test -e "$target" || as_root test -L "$target"; then
    fail "Existing MySQL shutdown override requires reconciliation: $target"
  fi
  stop_override="$target"
  printf '%s\n' "$stop_setting" | as_root tee "$stop_override" >/dev/null
  as_root systemctl daemon-reload
  test "$(as_root systemctl show -p DropInPaths --value "$unit")" = "$stop_override"
}

selinux_mode() {
  if command -v getenforce >/dev/null 2>&1; then
    getenforce
  elif [[ -e /sys/fs/selinux/enforce ]]; then
    fail 'SELinux is active but getenforce is unavailable'
  else
    printf 'Disabled\n'
  fi
}

check_labels() {
  local binary=$1 mode context
  mode=$(selinux_mode)
  case "$mode" in
    Disabled) printf 'SELinux disabled on target host\n' ;;
    Enforcing|Permissive)
      command -v restorecon >/dev/null && command -v chcon >/dev/null
      context=$(as_root stat -c '%C' -- "$binary")
      [[ -n "$context" && "$context" != '?' ]]
      ;;
    *) fail "Unexpected SELinux mode: $mode" ;;
  esac
}

restore_labels() {
  local previous=$1 active=$2 mode entry original
  mode=$(selinux_mode)
  if [[ "$mode" == Disabled ]]; then return; fi
  as_root restorecon -R "$active"
  # Files in the original tree can have local context overrides. Carry their
  # labels forward for matching entries, while new files use system policy.
  while IFS= read -r -d '' entry; do
    original="$previous${entry#"$active"}"
    if [[ -f "$original" && -f "$entry" && ! -L "$original" && ! -L "$entry" ]] ||
      [[ -d "$original" && -d "$entry" && ! -L "$original" && ! -L "$entry" ]]; then
      as_root chcon --reference="$original" -- "$entry"
    fi
  done < <(as_root find "$active" -xdev ! -type l -print0)
}

check_version() {
  local binary=$1 libraries=$2 version=$3 principal=${4:-} pattern report
  local -a command=(env "LD_LIBRARY_PATH=$libraries")
  if [[ -n "$principal" ]]; then
    command=(as_root runuser -u "$principal" -- "${command[@]}")
  fi
  pattern=${version//./\\.}
  report=$("${command[@]}" ldd "$binary") || return 1
  if grep -Fq 'not found' <<< "$report"; then
    printf '%s\n' "$report" >&2
    printf 'Missing runtime library for %s\n' "$binary" >&2
    return 1
  fi
  report=$("${command[@]}" "$binary" --version) || return 1
  grep -Eq "(^|[^0-9.])$pattern([^0-9.]|$)" <<< "$report"
}

router_libs() {
  printf '%s/lib:%s/lib/private:%s/lib/mysqlrouter/private' "$1" "$1" "$1"
}

mysqlsh() {
  local binary=$1
  shift
  test -f "$secret" && test ! -L "$secret"
  test "$(stat -c '%a:%u' -- "$secret")" = "600:$(id -u)"
  MYSQLSH_USER_CONFIG_HOME="$work" "$binary" --defaults-file="$secret" --mysql \
    --no-wizard --credential-store-helper='<disabled>' --save-passwords=never "$@"
}

direct_sql() {
  mysqlsh "$shell_base/bin/mysqlsh" --sql --result-format=tabbed --host=127.0.0.1 --port="$port" --execute "$1"
}

probe() {
  local gtid=$1
  [[ "$gtid" =~ ^[0-9a-fA-F:,-]*$ ]]
  direct_sql "
SELECT '__AIFAR_UPGRADE_LOCAL__' AS marker, @@server_uuid AS local_uuid,
  @@global.group_replication_group_name AS group_uuid, @@version AS local_version,
  REPLACE(REPLACE(@@global.gtid_executed, CHAR(10), ''), CHAR(13), '') AS executed_gtid,
  CASE
  WHEN NOT (@@global.group_replication_single_primary_mode AND NOT @@global.group_replication_bootstrap_group
    AND @@global.group_replication_start_on_boot AND @@global.gtid_mode = 'ON') THEN 'group-configuration'
  WHEN NOT (@@global.port = $port AND TRIM(TRAILING '/' FROM @@global.basedir) = '$base'
    AND @@global.datadir = '$root/data/' AND @@global.log_bin_basename = '$root/logs/mysql-bin'
    AND @@global.relay_log_basename = '$root/logs/mysql-relay-bin'
    AND COALESCE(@@global.innodb_undo_directory, '') IN ('', '.', './', '$root/data/')
    AND COALESCE(@@global.innodb_data_home_dir, '') IN ('', '.', './', '$root/data/')
    AND COALESCE(@@global.innodb_directories, '') = '')
    OR EXISTS (SELECT 1 FROM information_schema.INNODB_DATAFILES
      WHERE (LEFT(PATH, 1) = '/' AND LEFT(PATH, CHAR_LENGTH('$root/data/')) <> '$root/data/')
        OR CONCAT('/', PATH, '/') LIKE '%/../%') THEN 'storage-layout'
  WHEN NOT GTID_SUBSET('$gtid', @@global.gtid_executed) THEN 'baseline-gtid'
  WHEN EXISTS (SELECT 1 FROM mysql.user WHERE plugin = 'mysql_native_password' AND account_locked = 'N') THEN 'native-password-accounts'
  WHEN @@global.event_scheduler = 'ON' AND EXISTS (SELECT 1 FROM information_schema.EVENTS WHERE STATUS = 'ENABLED') THEN 'enabled-sql-events'
  WHEN EXISTS (SELECT 1 FROM performance_schema.replication_group_member_stats
      WHERE COUNT_TRANSACTIONS_IN_QUEUE <> 0 OR COUNT_TRANSACTIONS_REMOTE_IN_APPLIER_QUEUE <> 0) THEN 'replication-queues'
  ELSE '1' END AS readiness;
SELECT '__AIFAR_UPGRADE_MEMBER__' AS marker, MEMBER_ID, MEMBER_HOST, MEMBER_PORT, MEMBER_VERSION, MEMBER_ROLE, MEMBER_STATE
  FROM performance_schema.replication_group_members ORDER BY MEMBER_ID;
"
}

extract_tree() {
  local archive=$1 parent=$2 pattern=$3 binary=$4 target=$5
  local -a dirs
  mkdir -- "$parent"
  tar --no-same-owner -xf "$work/bundles/$archive" -C "$parent"
  shopt -s nullglob
  dirs=("$parent"/$pattern)
  shopt -u nullglob
  test "${#dirs[@]}" = 1 && test -d "${dirs[0]}" && test -x "${dirs[0]}/$binary"
  test ! -e "$target" && test ! -L "$target"
  as_root cp -a -- "${dirs[0]}" "$target"
  # Keep the archive modes, including private directories created under umask 077.
  case "$binary" in
    bin/mysqld) as_root chown -hR -- aifar-mysql:aifar-mysql "$target" ;;
    bin/mysqlrouter) as_root chown -hR -- aifar-router:aifar-router "$target" ;;
  esac
}

derive_mysql_config() {
  as_root test -f "$1" && as_root test ! -L "$1" || return 1
  as_root sed -E '/^[[:space:]]*binlog[-_]transaction[-_]dependency[-_]tracking[[:space:]]*=/Id' "$1"
}

check_derived_mysql_config() {
  as_root test -f "$2" && as_root test ! -L "$2" || return 1
  derive_mysql_config "$1" | as_root cmp -s -- "$2" -
}

check_config() {
  as_root test -f "$config" && as_root test ! -L "$config"
  if as_root grep -Eqi '^[[:space:]]*(!include|.*(keyring|init[-_]file|innodb[-_]log[-_]group[-_]home[-_]dir))' "$config"; then
    fail 'Includes, external keyrings, or external startup/recovery files require a separately planned upgrade'
  fi
  derive_mysql_config "$config" > "$candidate"
  as_root sha256sum -- "$config" > "$work/source-config.sha256"
  as_root env LD_LIBRARY_PATH="$server_next/lib" "$server_next/bin/mysqld" --defaults-file="$candidate" \
    --user=aifar-mysql --basedir="$server_next" --plugin-dir="$server_next/lib/plugin" --validate-config
}

stage() {
  local hash=$1
  test "$(uname -m)" = x86_64
  [[ "$hash" =~ ^[[:xdigit:]]{64}$ ]]
  printf '%s  %s\n' "$hash" "$work/bundle.tar" | sha256sum --check --status
  test "$(realpath -e -- "$root")" = "$root"
  mkdir -- "$work/bundles"
  if [[ "$app" == mysql ]]; then
    check_stop_layout "$base/bin/mysqld" "--defaults-file=$config"
    check_version "$base/bin/mysqld" "$base/lib" 8.0.36 aifar-mysql
    check_version "$shell_base/bin/mysqlsh" "$shell_base/lib" 8.0.36
    check_labels "$base/bin/mysqld"
    check_labels "$shell_base/bin/mysqlsh"
    for tree in "$base" "$shell_base" "$root/conf" "$root/data" "$root/logs"; do check_tree "$tree"; done
    if mountpoint -q "$base" || mountpoint -q "$shell_base"; then fail 'Installed binaries are mountpoints'; fi
    local server_archive=mysql-8.4.13-linux-glibc2.28-x86_64-minimal.tar.xz
    local shell_archive=mysql-shell-26.10.0-linux-glibc2.28-x86-64bit.tar.gz
    tar --no-same-owner -xf "$work/bundle.tar" -C "$work/bundles" "$server_archive" "$shell_archive"
    printf '%s  %s\n' \
      5dfbb764f731df40ef9e4f5d69aed32bdac8ba1af20bd96b7d40a942d83d4ed2 "$work/bundles/$server_archive" \
      0e6f5e69d2bf1e5c1dcdfb31dff3b43c4731ef94d391d7d1a87fa28649e75106 "$work/bundles/$shell_archive" | sha256sum --check --status
    extract_tree "$server_archive" "$work/server" 'mysql-8.4.13-*' bin/mysqld "$server_next"
    extract_tree "$shell_archive" "$work/shell" 'mysql-shell-26.10.0-*' bin/mysqlsh "$shell_next"
    check_version "$server_next/bin/mysqld" "$server_next/lib" 8.4.13 aifar-mysql
    check_version "$shell_next/bin/mysqlsh" "$shell_next/lib" 26.10.0
    check_config
  else
    check_stop_layout "$router_base/bin/mysqlrouter" "-c $router_config"
    check_version "$router_base/bin/mysqlrouter" "$(router_libs "$router_base")" 8.0.36 aifar-router
    check_labels "$router_base/bin/mysqlrouter"
    check_tree "$router_base"
    check_tree "$root/router"
    if mountpoint -q "$router_base"; then fail 'Router binaries are a mountpoint'; fi
    local router_archive=mysql-router-26.10.0-linux-glibc2.28-x86_64.tar.xz
    tar --no-same-owner -xf "$work/bundle.tar" -C "$work/bundles" "$router_archive"
    printf '%s  %s\n' 5074914158bcf6da8c034b6202bf86d566d7c0f175d438279d8d218c6580b025 \
      "$work/bundles/$router_archive" | sha256sum --check --status
    extract_tree "$router_archive" "$work/router" 'mysql-router-26.10.0-*' bin/mysqlrouter "$router_next"
    check_version "$router_next/bin/mysqlrouter" "$(router_libs "$router_next")" 26.10.0 aifar-router
  fi
  rm -rf -- "$work/bundles" "$work/server" "$work/shell" "$work/router"
  rm -f -- "$work/bundle.tar"
}

check_backup_source() {
  local binary=$1
  check_process "$binary" || return 1
  if [[ "$app" == mysql ]]; then
    check_version "$binary" "$base/lib" 8.0.36 aifar-mysql || return 1
    check_tool_version "$shell_base" 8.0.36
  else
    check_tool_version "$router_base" 8.0.36
  fi
}

write_backup() {
  local prior_binary tree archive bytes available source parent
  local -a trees
  if [[ "$app" == mysql ]]; then
    prior_binary="$base/bin/mysqld"
    trees=(mysql mysql-shell conf data logs)
  else
    prior_binary="$router_base/bin/mysqlrouter"
    trees=(mysql-router router)
  fi
  parent=$(dirname -- "$backup")
  as_root install -d -m 0700 -- "$parent"
  test "$(as_root realpath -e -- "$parent")" = "$parent"
  as_root mkdir -m 700 -- "$backup"
  for tree in "${trees[@]}"; do check_tree "$root/$tree"; done

  bytes=$(for tree in "${trees[@]}"; do as_root du -s -x -B1 -- "$root/$tree"; done | awk '{ n += $1 } END { printf "%.0f\n", n }')
  available=$(as_root df -B1 --output=avail "$backup" | tail -1)
  (( available > bytes + bytes / 10 + 67108864 )) || fail 'Insufficient cold-backup space on target host'
  check_backup_source "$prior_binary" || fail 'Cold backup requires the expected running 8.0.36 service and tools'

  # If any backup command fails before mutation, restart the unchanged source daemon.
  restart_unchanged=1
  stop_cleanly
  for tree in "${trees[@]}"; do
    source="$root/$tree"
    archive="$backup/$tree.tar"
    findmnt --kernel -n -T "$source" -o TARGET,SOURCE,FSTYPE,UUID | as_root tee "$backup/$tree.mount" >/dev/null
    as_root tar --acls --xattrs --xattrs-include='*' --selinux --sparse --numeric-owner --one-file-system -cpf "$archive" -C "$source" .
    as_root tar --acls --xattrs --xattrs-include='*' --selinux --numeric-owner --compare -f "$archive" -C "$source"
    as_root sha256sum -- "$archive" | as_root tee "$archive.sha256" >/dev/null
  done
  as_root cp --archive -- "$unit_file" "$backup/$unit"
  as_root sync -f "$backup"
  start_and_check_process "$prior_binary"
  as_root touch -- "$backup/complete"
  restart_unchanged=0
}

verify_backup() {
  local tree archive file current original recorded digest
  test -n "$backup"
  test "$(as_root realpath -e -- "$backup")" = "$backup"
  as_root test -f "$backup/complete"
  as_root test ! -L "$backup/complete"
  for tree in "$@"; do
    archive="$backup/$tree.tar"
    for file in "$archive" "$archive.sha256" "$backup/$tree.mount"; do
      as_root test -f "$file"
      as_root test ! -L "$file"
    done
    recorded=$(as_root cat -- "$archive.sha256")
    digest=$(as_root sha256sum -- "$archive")
    test "$recorded" = "$digest" || fail "Cold backup checksum does not match $archive"
    current=$(findmnt --kernel -n -T "$root/$tree" -o TARGET,SOURCE,FSTYPE,UUID)
    original=$(as_root cat -- "$backup/$tree.mount")
    [[ -n "$current" && "$current" == "$original" ]]
  done
}

activate_tree() {
  local target=$1 next=$2 old="$1.old-$task"
  check_tree "$target"
  check_tree "$next"
  if [[ -e "$old" || -L "$old" ]]; then fail "Original binary tree already exists: $old"; fi
  swapped_tree="$target"
  as_root mv -T -- "$target" "$old"
  as_root mv -T -- "$next" "$target"
  restore_labels "$old" "$target"
}

restore_swapped_tree() {
  local target=$1 old="$1.old-$task" next="$1.next-$task" state pid
  local moved_target=0
  if [[ ! -e "$old" && ! -L "$old" ]]; then
    [[ -d "$target" && ! -L "$target" && -d "$next" && ! -L "$next" ]]
    return
  fi
  [[ -d "$old" && ! -L "$old" ]] || return 1
  if [[ "$app" == mysql-router ]]; then
    as_root systemctl stop "$unit" || return 1
    pid=$(as_root systemctl show -p MainPID --value "$unit") || return 1
    state=$(as_root systemctl show -p ActiveState --value "$unit") || return 1
    [[ "$pid" == 0 && ( "$state" == inactive || "$state" == failed ) ]] || return 1
    as_root systemctl reset-failed "$unit" || true
  fi
  if [[ -e "$target" || -L "$target" ]]; then
    [[ -d "$target" && ! -L "$target" && ! -e "$next" && ! -L "$next" ]] || return 1
    as_root mv -T -- "$target" "$next" || return 1
    moved_target=1
  fi
  if as_root mv -T -- "$old" "$target"; then return 0; fi
  if (( moved_target )) && ! as_root mv -T -- "$next" "$target"; then
    printf 'Could not restore the staged path %s\n' "$target" >&2
  fi
  return 1
}

lock_phase() {
  test "$(as_root realpath -e -- "$root")" = "$root"
  exec {phase_lock}< "$root"
  flock -n "$phase_lock" || fail "Another MySQL upgrade phase still owns $root"
}

check_tool_version() {
  local tree=$1 expected=$2
  if [[ "$app" == mysql ]]; then
    check_version "$tree/bin/mysqlsh" "$tree/lib" "$expected"
  else
    check_version "$tree/bin/mysqlrouter" "$(router_libs "$tree")" "$expected" aifar-router
  fi
}

tool_version() {
  local tree=$1
  check_tree "$tree" || return 1
  if [[ "$app" == mysql-router ]]; then check_process "$tree/bin/mysqlrouter" || return 1; fi
  if check_tool_version "$tree" 26.10.0; then
    check_tree "$tree.old-$task" || return 1
    check_tool_version "$tree.old-$task" 8.0.36 || return 1
    [[ ! -e "$tree.next-$task" && ! -L "$tree.next-$task" ]] || return 1
    printf '26.10.0\n'
  else
    check_tool_version "$tree" 8.0.36 || return 1
    [[ ! -e "$tree.old-$task" && ! -L "$tree.old-$task" ]] || return 1
    check_tree "$tree.next-$task" || return 1
    check_tool_version "$tree.next-$task" 26.10.0 || return 1
    printf '8.0.36\n'
  fi
}

verify_tool_backup() {
  if [[ "$app" == mysql ]]; then verify_backup mysql-shell; else verify_backup mysql-router router; fi
}

verify_server_members() {
  local member=$1 primary=$2 csv=$3 uuid
  local -a members
  IFS=, read -r -a members <<< "$csv" || return 1
  [[ "$member" =~ $uuid_pattern && "$primary" =~ $uuid_pattern && "${#members[@]}" == 3 ]] || return 1
  [[ ",$csv," == *",$member,"* && ",$csv," == *",$primary,"* ]] || return 1
  for uuid in "${members[@]}"; do [[ "$uuid" =~ $uuid_pattern ]] || return 1; done
  direct_sql "
SELECT '__AIFAR_UPGRADE_CUTOVER__',
  @@server_uuid = '$member' AND @@version = '8.0.36'
  AND COUNT(DISTINCT MEMBER_ID) = 3 AND COUNT(*) = 3
  AND SUM(MEMBER_ID IN ('${members[0]}', '${members[1]}', '${members[2]}')) = 3
  AND SUM(MEMBER_STATE = 'ONLINE') = 3 AND SUM(MEMBER_ROLE = 'PRIMARY') = 1
  AND SUM(MEMBER_ID = '$primary' AND MEMBER_ROLE = 'PRIMARY') = 1
FROM performance_schema.replication_group_members" |
    grep -Fx $'__AIFAR_UPGRADE_CUTOVER__\t1'
}

resume_server_check() {
  local override="$runtime_unit_dir/92-aifar-upgrade-$task.conf" dropins actual expected recorded
  check_tree "$base"
  check_process "$base/bin/mysqld"
  as_root test -f "$config"
  as_root test ! -L "$config"
  [[ ! -e "$config.next-$task" && ! -L "$config.next-$task" ]]
  if check_version "$base/bin/mysqld" "$base/lib" 8.4.13 aifar-mysql; then
    check_tree "$base.old-$task"
    check_version "$base.old-$task/bin/mysqld" "$base.old-$task/lib" 8.0.36 aifar-mysql
    [[ ! -e "$server_next" && ! -L "$server_next" ]]
    as_root test -f "$config.old-$task"
    as_root test ! -L "$config.old-$task"
    read -r expected recorded < "$work/source-config.sha256"
    [[ "$expected" =~ ^[[:xdigit:]]{64}$ && "$recorded" == "$config" ]]
    actual=$(as_root sha256sum -- "$config.old-$task")
    [[ "${actual%% *}" == "$expected" ]]
    check_derived_mysql_config "$config.old-$task" "$config"
  else
    check_version "$base/bin/mysqld" "$base/lib" 8.0.36 aifar-mysql
    [[ ! -e "$base.old-$task" && ! -L "$base.old-$task" && ! -e "$config.old-$task" && ! -L "$config.old-$task" ]]
    check_tree "$server_next"
    check_version "$server_next/bin/mysqld" "$server_next/lib" 8.4.13 aifar-mysql
    as_root sha256sum --check --status "$work/source-config.sha256"
    check_derived_mysql_config "$config" "$candidate"
  fi
  dropins=$(as_root systemctl show -p DropInPaths --value "$unit")
  if [[ -n "$dropins" && "$dropins" != "$override" ]]; then
    fail 'Unexpected MySQL systemd overrides; preserve the cold backup and verify the service configuration'
  fi
  if as_root test -e "$override" || as_root test -L "$override"; then
    as_root test -f "$override"
    as_root test ! -L "$override"
    test "$(as_root realpath -e -- "$override")" = "$override"
    printf '%s\n' "$stop_setting" | as_root cmp -s -- "$override" - ||
      fail "Unexpected contents of MySQL shutdown override: $override"
    as_root rm -- "$override"
    as_root systemctl daemon-reload
  elif [[ "$dropins" == "$override" ]]; then
    # The prior command may have removed the file but failed to reload the unit.
    as_root systemctl daemon-reload
  fi
  test -z "$(as_root systemctl show -p DropInPaths --value "$unit")"
}

case "$phase" in
  probe) probe "${args[0]}" ;;
  stage) lock_phase; stage "${args[0]}" ;;
  checker)
    member=$(direct_sql "
SELECT CONCAT(MEMBER_ROLE, ':', (@@global.read_only OR @@global.super_read_only)) AS checker_member
FROM performance_schema.replication_group_members
WHERE MEMBER_ID = @@server_uuid AND MEMBER_STATE = 'ONLINE'")
    checker_args=()
    case "${args[0]}:$member" in
      PRIMARY:$'checker_member\nPRIMARY:0'|PRIMARY:$'checker_member\nPRIMARY:1'|SECONDARY:$'checker_member\nSECONDARY:0') ;;
      SECONDARY:$'checker_member\nSECONDARY:1')
        # The primary still runs CHECK TABLE FOR UPGRADE for the replicated tables.
        checker_args+=(--exclude=checkTableCommand)
        ;;
      *) fail 'MySQL upgrade checker could not confirm the expected online member role' ;;
    esac
    if ! mysqlsh "$shell_next/bin/mysqlsh" --host=127.0.0.1 --port="$port" -- util check-for-server-upgrade \
      --target-version=8.4.13 --output-format=JSON --config-path="$candidate" "${checker_args[@]}" > "$work/upgrade-check.json"; then
      cat -- "$work/upgrade-check.json"
      fail 'MySQL Shell Upgrade Checker failed'
    fi
    cat -- "$work/upgrade-check.json"
    ;;
  backup) lock_phase; write_backup ;;
  tools|resume-tools-probe)
    lock_phase
    verify_tool_backup
    tool_tree="$router_base"
    if [[ "$app" == mysql ]]; then tool_tree="$shell_base"; fi
    actual=$(tool_version "$tool_tree") || fail 'Cannot confirm the current and staged MySQL tool versions'
    if [[ "$phase" == resume-tools-probe ]]; then
      printf '__AIFAR_UPGRADE_TOOL__\t%s\n' "$actual"
      exit 0
    fi
    if [[ "$actual" == 26.10.0 ]]; then exit 0; fi
    if [[ "$app" == mysql ]]; then
      activate_tree "$shell_base" "$shell_next"
      check_tool_version "$shell_base" 26.10.0
    else
      restart_unchanged=1
      stop_cleanly
      activate_tree "$router_base" "$router_next"
      start_and_check_process "$router_base/bin/mysqlrouter"
    fi
    ;;
  resume-backup-check)
    lock_phase
    if [[ "$app" == mysql ]]; then verify_backup mysql mysql-shell conf data logs; else verify_backup mysql-router router; fi
    ;;
  resume-server-check)
    lock_phase
    [[ "$app" == mysql ]]
    resume_server_check
    ;;
  revert-router)
    lock_phase
    [[ "$app" == mysql-router ]]
    verify_tool_backup
    # A failed routing check can occur while the new Router is already exiting.
    # Validate both trees before stopping the current unit.
    check_tree "$router_base"
    check_tree "$router_base.old-$task"
    check_tool_version "$router_base" 26.10.0
    check_tool_version "$router_base.old-$task" 8.0.36
    [[ ! -e "$router_next" && ! -L "$router_next" ]]
    restore_swapped_tree "$router_base" || fail 'Could not restore the original Router tree; preserve the cold backup'
    start_and_check_process "$router_base/bin/mysqlrouter" || fail 'Restored the original Router tree but its process did not start'
    ;;
  verify-router)
    check_process "$router_base/bin/mysqlrouter"
    check_version "$router_base/bin/mysqlrouter" "$(router_libs "$router_base")" 26.10.0 aifar-router
    ;;
  route-sql|route-sql-ro)
    IFS=, read -r -a route_members <<< "${args[2]}"
    (( ${#route_members[@]} > 0 ))
    for uuid in "${route_members[@]}"; do [[ "$uuid" =~ $uuid_pattern ]]; done
    read_only=0
    if [[ "$phase" == route-sql-ro ]]; then read_only=1; fi
    for attempt in {1..15}; do
      if output=$(timeout 8 bash "$work/upgrade.sh" route-sql-probe "$app" "$root" "$work" "$backup" "$task" "$port" "${args[0]}" "${args[1]}"); then
        for uuid in "${route_members[@]}"; do
          if grep -Fxq $'__AIFAR_UPGRADE_ROUTER__\t'"$uuid"$'\t'"$read_only" <<< "$output"; then exit 0; fi
        done
      fi
      sleep 2
    done
    if [[ "$phase" == route-sql-ro ]]; then
      fail 'Router read-only endpoint did not connect to an expected read-only secondary'
    fi
    fail 'Router read/write endpoint did not connect to the expected primary'
    ;;
  route-sql-probe)
    mysqlsh "$shell_base/bin/mysqlsh" --sql --result-format=tabbed --host="${args[0]}" --port="${args[1]}" \
      --execute "SELECT '__AIFAR_UPGRADE_ROUTER__', @@server_uuid, @@global.read_only OR @@global.super_read_only"
    ;;
  metadata-dry-run)
    mysqlsh "$shell_base/bin/mysqlsh" --js --host=127.0.0.1 --port="$port" --execute 'dba.upgradeMetadata({dryRun:true})'
    ;;
  metadata-upgrade)
    lock_phase
    mysqlsh "$shell_base/bin/mysqlsh" --js --host=127.0.0.1 --port="$port" --execute \
      'dba.upgradeMetadata(); const cluster = dba.getCluster(); const result = cluster.status(); if (result.defaultReplicaSet.status !== "OK") throw new Error("Cluster metadata status is unhealthy");'
    ;;
  server)
    lock_phase
    [[ "${args[0]}" =~ $uuid_pattern ]]
    direct_sql "SELECT '__AIFAR_UPGRADE_UUID__', @@server_uuid WHERE @@version = '8.0.36'" | grep -Fx $'__AIFAR_UPGRADE_UUID__\t'"${args[0]}"
    verify_backup mysql conf data logs
    as_root sha256sum --check --status "$work/source-config.sha256"
    check_derived_mysql_config "$config" "$candidate" || fail 'Staged MySQL configuration differs from the expected configuration'
    if [[ -e "$config.old-$task" || -L "$config.old-$task" || -e "$config.next-$task" || -L "$config.next-$task" ]]; then
      fail 'Existing MySQL cutover configuration needs reconciliation'
    fi
    allow_mysql_shutdown
    verify_server_members "${args[0]}" "${args[1]}" "${args[2]}" ||
      fail 'MySQL group members or primary changed before shutdown'
    restart_unchanged=1
    direct_sql 'SET GLOBAL innodb_fast_shutdown=0; STOP GROUP_REPLICATION;'
    stop_cleanly
    activate_tree "$base" "$server_next"
    as_root mkdir -m 0700 -- "$config.next-$task"
    pending_config_dir="$config.next-$task"
    pending_config="$pending_config_dir/my.cnf"
    as_root cp --archive -- "$config" "$pending_config"
    as_root test -f "$pending_config"
    as_root test ! -L "$pending_config"
    derive_mysql_config "$config" | as_root tee "$pending_config" >/dev/null
    as_root sha256sum --check --status "$work/source-config.sha256"
    as_root test -f "$candidate"
    as_root test ! -L "$candidate"
    as_root cmp -s -- "$pending_config" "$candidate" ||
      fail 'Staged MySQL configuration changed before activation'
    swapped_config=1
    as_root mv -T -- "$config" "$config.old-$task"
    as_root mv -T -- "$pending_config" "$config"
    as_root rmdir -- "$pending_config_dir"
    pending_config=''
    pending_config_dir=''
    # Once systemd is asked to start 8.4, its data dictionary may be upgraded.
    # Keep the original tree and cold backups for an offline recovery.
    restart_unchanged=0 swapped_tree='' swapped_config=0
    start_and_check_process "$base/bin/mysqld"
    new_mysql_running=1
    printf '__AIFAR_UPGRADE_SERVER_STARTED__\n'
    ;;
  *) fail 'Unknown upgrade phase' ;;
esac
