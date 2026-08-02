#!/usr/bin/env sh
set -eu

ACTION="${1:-}"
TASK_ID={{shq .TaskID}}
INSTALL_ROOT={{shq .InstallRoot}}
WORK_DIR={{shq .WorkDir}}
DATA_DIR={{shq .DataDir}}
QUARANTINE_DIR={{shq .QuarantineDir}}
PORT={{.Port}}
MYSQL_USER="aifar-mysql"
SERVICE_NAME="aifar-mysql"
SUDO=""
if [ "$(id -u)" != "0" ]; then SUDO="sudo -n"; fi

validate_paths() {
  test "$DATA_DIR" = "$INSTALL_ROOT/data"
  test "$QUARANTINE_DIR" = "$DATA_DIR.quarantine-$TASK_ID"
  test -d "$INSTALL_ROOT"
  test -d "$DATA_DIR"
  test ! -L "$INSTALL_ROOT"
  test ! -L "$DATA_DIR"
  test "$(readlink -f "$INSTALL_ROOT")" = "$INSTALL_ROOT"
  test "$(readlink -f "$DATA_DIR")" = "$DATA_DIR"
  test "$(dirname "$QUARANTINE_DIR")" = "$INSTALL_ROOT"
  test ! -L "$QUARANTINE_DIR"
  data_mount="$(findmnt -rn -o TARGET -T "$DATA_DIR")"
  parent_mount="$(findmnt -rn -o TARGET -T "$INSTALL_ROOT")"
  test -n "$data_mount"
  test "$data_mount" = "$parent_mount"
  test "$(stat -c %d "$DATA_DIR")" = "$(stat -c %d "$INSTALL_ROOT")"
  required_kb="$(du -sk "$DATA_DIR" | awk '{print $1}')"
  available_kb="$(df -Pk "$INSTALL_ROOT" | awk 'NR==2 {print $4}')"
  test "$available_kb" -gt "$required_kb"
}

validate_secret_file() {
  secret_file="$1"
  test -f "$secret_file"
  test ! -L "$secret_file"
  test "$(stat -c '%u' "$secret_file")" = "$(id -u)"
  test "$(stat -c '%a' "$secret_file")" = "600"
}

case "$ACTION" in
  stop-gr)
    validate_secret_file "$WORK_DIR/secret-context.cnf"
    load_state="$($SUDO systemctl show "$SERVICE_NAME" --property=LoadState --value)"
    case "$load_state" in
      not-found)
        printf '__AIFAR_STOP_GR__\tmysqld-offline\n'
        exit 0
        ;;
      loaded) ;;
      *) exit 65 ;;
    esac
    active_state="$($SUDO systemctl show "$SERVICE_NAME" --property=ActiveState --value)"
    case "$active_state" in
      inactive)
        printf '__AIFAR_STOP_GR__\tmysqld-offline\n'
        exit 0
        ;;
      active) ;;
      *) exit 65 ;;
    esac
    "$INSTALL_ROOT/mysql/bin/mysqladmin" --defaults-file="$WORK_DIR/secret-context.cnf" --protocol=tcp --host=127.0.0.1 --port="$PORT" ping >/dev/null 2>&1
    member_count="$("$INSTALL_ROOT/mysql-shell/bin/mysqlsh" --defaults-file="$WORK_DIR/secret-context.cnf" --sql --result-format=tabbed --host=127.0.0.1 --port="$PORT" --execute "SELECT '__AIFAR_MEMBER_COUNT__' AS marker, COUNT(*) AS member_count FROM performance_schema.replication_group_members" | awk -F '\t' '$1 == "__AIFAR_MEMBER_COUNT__" {print $2; found=1} END {if (!found) exit 1}')"
    case "$member_count" in
      0) printf '__AIFAR_STOP_GR__\talready-stopped\n' ;;
      *[!0-9]*|'') exit 65 ;;
      *)
        "$INSTALL_ROOT/mysql-shell/bin/mysqlsh" --defaults-file="$WORK_DIR/secret-context.cnf" --sql --host=127.0.0.1 --port="$PORT" --execute "STOP GROUP_REPLICATION"
        printf '__AIFAR_STOP_GR__\tstopped\n'
        ;;
    esac
    ;;
  quarantine)
    validate_paths
    test ! -e "$QUARANTINE_DIR"
    $SUDO systemctl stop "$SERVICE_NAME"
    mv -- "$DATA_DIR" "$QUARANTINE_DIR"
    $SUDO mkdir -p "$DATA_DIR"
    $SUDO chown "$MYSQL_USER:$MYSQL_USER" "$DATA_DIR"
    ;;
  inspect-quarantine)
    validate_paths
    if [ -e "$QUARANTINE_DIR" ]; then
      test -d "$QUARANTINE_DIR"
      test ! -L "$QUARANTINE_DIR"
      test "$(readlink -f "$QUARANTINE_DIR")" = "$QUARANTINE_DIR"
      test "$(stat -c %d "$QUARANTINE_DIR")" = "$(stat -c %d "$INSTALL_ROOT")"
      printf '__AIFAR_QUARANTINE__\tpresent\n'
    else
      test ! -L "$QUARANTINE_DIR"
      printf '__AIFAR_QUARANTINE__\tabsent\n'
    fi
    ;;
  initialize)
    test -d "$QUARANTINE_DIR"
    test ! -L "$DATA_DIR"
    test -z "$(find "$DATA_DIR" -mindepth 1 -maxdepth 1 -print -quit)"
    $SUDO "$INSTALL_ROOT/mysql/bin/mysqld" --defaults-file="$INSTALL_ROOT/conf/my.cnf" --initialize-insecure --user="$MYSQL_USER"
    $SUDO systemctl start "$SERVICE_NAME"
    ready=0
    i=1
    while [ "$i" -le 300 ]; do
      if "$INSTALL_ROOT/mysql/bin/mysqladmin" --protocol=socket --socket="$INSTALL_ROOT/run/mysql.sock" -uroot ping >/dev/null 2>&1; then
        ready=1
        break
      fi
      i=$((i + 1))
      sleep 1
    done
    test "$ready" = "1"
    validate_secret_file "$WORK_DIR/admin-init.sql"
    "$INSTALL_ROOT/mysql/bin/mysql" --protocol=socket --socket="$INSTALL_ROOT/run/mysql.sock" -uroot < "$WORK_DIR/admin-init.sql"
    rm -f -- "$WORK_DIR/admin-init.sql"
    ;;
  inspect-initialized)
    validate_secret_file "$WORK_DIR/secret-context.cnf"
    test -d "$QUARANTINE_DIR"
    test ! -L "$QUARANTINE_DIR"
    test -d "$DATA_DIR"
    test ! -L "$DATA_DIR"
    if [ -z "$(find "$DATA_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]; then
      printf '__AIFAR_INITIALIZED__\tabsent\n'
      exit 0
    fi
    $SUDO systemctl is-active --quiet "$SERVICE_NAME"
    "$INSTALL_ROOT/mysql/bin/mysql" --defaults-file="$WORK_DIR/secret-context.cnf" --protocol=tcp --host=127.0.0.1 --port="$PORT" --batch --skip-column-names --execute "SELECT 1" >/dev/null
    printf '__AIFAR_INITIALIZED__\tpresent\n'
    ;;
  verify-quarantine)
    validate_paths
    test -d "$QUARANTINE_DIR"
    test ! -L "$QUARANTINE_DIR"
    test "$(readlink -f "$QUARANTINE_DIR")" = "$QUARANTINE_DIR"
    test "$(stat -c %d "$QUARANTINE_DIR")" = "$(stat -c %d "$INSTALL_ROOT")"
    ;;
  verify)
    test -d "$QUARANTINE_DIR"
    test ! -L "$QUARANTINE_DIR"
    $SUDO systemctl is-active --quiet "$SERVICE_NAME"
    ;;
  *) exit 64 ;;
esac
