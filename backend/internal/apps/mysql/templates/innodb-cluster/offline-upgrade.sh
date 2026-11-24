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
candidate="$work/my84.cnf"
secret="$work/credential.cnf"
server_next="$base.next-$task"
shell_next="$shell_base.next-$task"
router_next="$router_base.next-$task"
stop_override=''
restart_unchanged=0

trap 'printf "MySQL upgrade phase %s stopped at line %s (status %s)\n" "$phase" "$LINENO" "$?" >&2' ERR
trap 'finish' EXIT

fail() { printf '%s\n' "$*" >&2; exit 1; }

finish() {
  local status=$?
  trap - EXIT
  if (( status != 0 && restart_unchanged )); then
    if [[ "$phase" == server ]] && as_root systemctl is-active --quiet "$unit"; then
      # The preflight requires group_replication_start_on_boot and prohibits
      # bootstrap mode, so normal startup returns the unchanged member to its group.
      as_root systemctl restart "$unit" || printf 'Could not restart the unchanged MySQL member\n' >&2
    else
      as_root systemctl start "$unit" || printf 'Could not start the unchanged service %s\n' "$unit" >&2
    fi
  fi
  if [[ -n "$stop_override" ]]; then
    if ! as_root rm -f -- "$stop_override" || ! as_root systemctl daemon-reload; then
      printf 'Could not remove the temporary shutdown setting %s or reload systemd\n' "$stop_override" >&2
      status=1
    fi
  fi
  exit "$status"
}

check_tree() {
  local tree=$1 mounts
  as_root test -d "$tree"
  test "$(as_root realpath -e -- "$tree")" = "$tree"
  mounts=$(findmnt --kernel --raw -rn -o TARGET)
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
  as_root systemctl is-active --quiet "$unit"
  pid=$(as_root systemctl show -p MainPID --value "$unit")
  test "$pid" -gt 0
  test "$(as_root readlink -f -- "/proc/$pid/exe")" = "$binary"
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
  local dir="/run/systemd/system/$unit.d" target
  test -z "$(as_root systemctl show -p DropInPaths --value "$unit")"
  as_root install -d -m 0755 -- "$dir"
  test "$(as_root realpath -e -- "$dir")" = "$dir"
  target="$dir/92-aifar-upgrade-$task.conf"
  as_root test ! -e "$target" && as_root test ! -L "$target"
  stop_override="$target"
  printf '[Service]\nTimeoutStopSec=30min\n' | as_root tee "$stop_override" >/dev/null
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
  local binary=$1 libraries=$2 version=$3 pattern report
  pattern=${version//./\\.}
  report=$(env LD_LIBRARY_PATH="$libraries" ldd "$binary")
  if grep -Fq 'not found' <<< "$report"; then
    printf '%s\n' "$report" >&2
    fail "Missing runtime library for $binary"
  fi
  report=$(env LD_LIBRARY_PATH="$libraries" "$binary" --version)
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
}

check_config() {
  as_root test -f "$config" && as_root test ! -L "$config"
  if as_root grep -Eqi '^[[:space:]]*(!include|.*(keyring|init[-_]file|innodb[-_]log[-_]group[-_]home[-_]dir))' "$config"; then
    fail 'Includes, external keyrings, or external startup/recovery files require a separately planned upgrade'
  fi
  as_root sed -E '/^[[:space:]]*binlog[-_]transaction[-_]dependency[-_]tracking[[:space:]]*=/Id' "$config" > "$candidate"
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
    check_version "$server_next/bin/mysqld" "$server_next/lib" 8.4.13
    check_version "$shell_next/bin/mysqlsh" "$shell_next/lib" 26.10.0
    check_config
  else
    check_stop_layout "$router_base/bin/mysqlrouter" "-c $router_config"
    check_labels "$router_base/bin/mysqlrouter"
    check_tree "$router_base"
    check_tree "$root/router"
    if mountpoint -q "$router_base"; then fail 'Router binaries are a mountpoint'; fi
    local router_archive=mysql-router-26.10.0-linux-glibc2.28-x86_64.tar.xz
    tar --no-same-owner -xf "$work/bundle.tar" -C "$work/bundles" "$router_archive"
    printf '%s  %s\n' 5074914158bcf6da8c034b6202bf86d566d7c0f175d438279d8d218c6580b025 \
      "$work/bundles/$router_archive" | sha256sum --check --status
    extract_tree "$router_archive" "$work/router" 'mysql-router-26.10.0-*' bin/mysqlrouter "$router_next"
    check_version "$router_next/bin/mysqlrouter" "$(router_libs "$router_next")" 26.10.0
  fi
  rm -rf -- "$work/bundles" "$work/server" "$work/shell" "$work/router"
  rm -f -- "$work/bundle.tar"
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
  as_root systemctl start "$unit"
  check_process "$prior_binary"
  as_root touch -- "$backup/complete"
  restart_unchanged=0
}

verify_backup() {
  local tree current original
  test -n "$backup"
  as_root test -f "$backup/complete"
  for tree in "$@"; do
    as_root sha256sum --check --status "$backup/$tree.tar.sha256"
    current=$(findmnt --kernel -n -T "$root/$tree" -o TARGET,SOURCE,FSTYPE,UUID)
    original=$(as_root cat -- "$backup/$tree.mount")
    test -n "$current" && test "$current" = "$original"
  done
}

activate_tree() {
  local target=$1 candidate=$2 old="$1.old-$task"
  check_tree "$target"
  test ! -e "$old" && test ! -L "$old"
  as_root mv -T -- "$target" "$old"
  if ! as_root mv -T -- "$candidate" "$target"; then
    if ! as_root mv -T -- "$old" "$target"; then
      restart_unchanged=0
      printf 'Could not restore the original binary tree %s\n' "$old" >&2
    fi
    return 1
  fi
  restart_unchanged=0
  restore_labels "$old" "$target"
}

case "$phase" in
  probe) probe "${args[0]}" ;;
  stage) stage "${args[0]}" ;;
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
  backup) write_backup ;;
  tools)
    if [[ "$app" == mysql ]]; then
      verify_backup mysql-shell
      activate_tree "$shell_base" "$shell_next"
      check_version "$shell_base/bin/mysqlsh" "$shell_base/lib" 26.10.0
    else
      verify_backup mysql-router router
      restart_unchanged=1
      stop_cleanly
      activate_tree "$router_base" "$router_next"
      as_root systemctl start "$unit"
      check_process "$router_base/bin/mysqlrouter"
    fi
    ;;
  verify-router)
    check_process "$router_base/bin/mysqlrouter"
    check_version "$router_base/bin/mysqlrouter" "$(router_libs "$router_base")" 26.10.0
    ;;
  route-sql)
    expected=$'__AIFAR_UPGRADE_ROUTER__\t'"${args[2]}"$'\t0'
    for attempt in {1..15}; do
      if timeout 8 bash "$work/upgrade.sh" route-sql-probe "$app" "$root" "$work" "$backup" "$task" "$port" "${args[0]}" "${args[1]}" | grep -Fxq "$expected"; then
        exit 0
      fi
      sleep 2
    done
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
    mysqlsh "$shell_base/bin/mysqlsh" --js --host=127.0.0.1 --port="$port" --execute \
      'dba.upgradeMetadata(); const cluster = dba.getCluster(); const result = cluster.status(); if (result.defaultReplicaSet.status !== "OK") throw new Error("Cluster metadata status is unhealthy");'
    ;;
  server)
    [[ "${args[0]}" =~ ^[0-9a-f-]{36}$ ]]
    direct_sql "SELECT '__AIFAR_UPGRADE_UUID__', @@server_uuid WHERE @@version = '8.0.36'" | grep -Fx $'__AIFAR_UPGRADE_UUID__\t'"${args[0]}"
    verify_backup mysql conf data logs
    as_root sha256sum --check --status "$work/source-config.sha256"
    allow_mysql_shutdown
    restart_unchanged=1
    direct_sql 'SET GLOBAL innodb_fast_shutdown=0; STOP GROUP_REPLICATION;'
    stop_cleanly
    # After switching binaries, the original directories and unit remain available
    # for an offline, separately reviewed recovery from the cold backups.
    activate_tree "$base" "$server_next"
    as_root cp --archive -- "$config" "$config.old-$task"
    as_root cp --archive -- "$config" "$config.next-$task"
    as_root tee "$config.next-$task" < "$candidate" >/dev/null
    as_root mv -T -- "$config.next-$task" "$config"
    as_root systemctl start "$unit"
    check_process "$base/bin/mysqld"
    ;;
  *) fail 'Unknown upgrade phase' ;;
esac
