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

case "$ACTION" in
  stop-gr)
    "$INSTALL_ROOT/mysql-shell/bin/mysqlsh" --defaults-file="$WORK_DIR/secret-context.cnf" --sql --host=127.0.0.1 --port="$PORT" --execute "STOP GROUP_REPLICATION"
    ;;
  quarantine)
    validate_paths
    test ! -e "$QUARANTINE_DIR"
    $SUDO systemctl stop "$SERVICE_NAME"
    mv -- "$DATA_DIR" "$QUARANTINE_DIR"
    $SUDO mkdir -p "$DATA_DIR"
    $SUDO chown "$MYSQL_USER:$MYSQL_USER" "$DATA_DIR"
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
    test -f "$WORK_DIR/admin-init.sql"
    test ! -L "$WORK_DIR/admin-init.sql"
    "$INSTALL_ROOT/mysql/bin/mysql" --protocol=socket --socket="$INSTALL_ROOT/run/mysql.sock" -uroot < "$WORK_DIR/admin-init.sql"
    rm -f -- "$WORK_DIR/admin-init.sql"
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
