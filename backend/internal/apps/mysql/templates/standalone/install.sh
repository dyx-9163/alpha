#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
WORK_DIR={{shq .WorkDir}}
ARCHIVE={{shq .ArchivePath}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
CREDENTIAL_CONTEXT="${1:-}"
MYSQL_USER="aifar-mysql"
SERVICE_NAME="aifar-mysql"
LEGACY_SERVICE_NAME="aifar-mysql-$PORT"
MYSQL_BASE="$INSTALL_ROOT/mysql"
MYSQL_SHELL_BASE="$INSTALL_ROOT/mysql-shell"
CONFIG_DIR="$INSTALL_ROOT/conf"
DATA_DIR="$INSTALL_ROOT/data"
LOG_DIR="$INSTALL_ROOT/logs"
RUN_DIR="$INSTALL_ROOT/run"
IMPORT_DIR="$INSTALL_ROOT/import"
CONFIG_FILE="$CONFIG_DIR/my.cnf"
SOCKET_FILE="$RUN_DIR/mysql.sock"
SECURE_ROOT_SQL="$WORK_DIR/secure-root.sql"
SECURE_CLIENT_FILE="$WORK_DIR/secure-client.cnf"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

umask 077
cleanup_secret_artifacts() {
  cleanup_status=0
  rm -f -- "$SECURE_ROOT_SQL" || cleanup_status=1
  rm -f -- "$SECURE_CLIENT_FILE" || cleanup_status=1
  rm -f -- "$CREDENTIAL_CONTEXT" || cleanup_status=1
  return "$cleanup_status"
}
finish() {
  status=$?
  trap - EXIT HUP INT TERM
  if ! cleanup_secret_artifacts; then
    status=1
  fi
  exit "$status"
}
trap finish EXIT
trap 'exit 1' HUP INT TERM

[ "$CREDENTIAL_CONTEXT" = "$WORK_DIR/mysql-credential.context" ] || { echo "invalid MySQL credential context path"; exit 1; }
[ -f "$CREDENTIAL_CONTEXT" ] && [ ! -L "$CREDENTIAL_CONTEXT" ] || { echo "invalid MySQL credential context"; exit 1; }
[ "$(stat -c '%u' "$CREDENTIAL_CONTEXT")" = "$(id -u)" ] || { echo "invalid MySQL credential context owner"; exit 1; }
[ "$(stat -c '%a' "$CREDENTIAL_CONTEXT")" = "600" ] || { echo "invalid MySQL credential context mode"; exit 1; }
exec 3< "$CREDENTIAL_CONTEXT"
IFS= read -r CREDENTIAL_MAGIC <&3 || { echo "invalid MySQL credential context"; exit 1; }
IFS= read -r ROOT_USER <&3 || { echo "invalid MySQL credential context"; exit 1; }
IFS= read -r ROOT_PASSWORD <&3 || { echo "invalid MySQL credential context"; exit 1; }
if IFS= read -r CREDENTIAL_EXTRA <&3; then
  echo "invalid MySQL credential context"
  exit 1
fi
exec 3<&-
[ "$CREDENTIAL_MAGIC" = "AIFAR_MYSQL_CREDENTIAL_CONTEXT_V1" ] || { echo "invalid MySQL credential context version"; exit 1; }
[ -n "$ROOT_USER" ] && [ -n "$ROOT_PASSWORD" ] || { echo "incomplete MySQL credential context"; exit 1; }
OPTION_USER="$(printf '%s' "$ROOT_USER" | sed 's/\\/\\\\/g; s/"/\\"/g')"
OPTION_PASSWORD="$(printf '%s' "$ROOT_PASSWORD" | sed 's/\\/\\\\/g; s/"/\\"/g')"
rm -f -- "$SECURE_CLIENT_FILE" "$SECURE_ROOT_SQL"
(umask 077; cat > "$SECURE_CLIENT_FILE" <<CLIENT
[client]
user="$OPTION_USER"
password="$OPTION_PASSWORD"
CLIENT
)
chmod 0600 "$SECURE_CLIENT_FILE"

dump_mysql_diagnostics() {
  echo "MySQL systemd status"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  if [ -f "$LOG_DIR/mysql-error.log" ]; then
    echo "----- $LOG_DIR/mysql-error.log -----"
    $SUDO tail -n 160 "$LOG_DIR/mysql-error.log" || true
  fi
  if command -v ss >/dev/null 2>&1; then
    echo "----- listening TCP ports for MySQL -----"
    ss -lnt | awk '{print $4}' | grep -E "(:|\\.)$PORT$" || true
  fi
  if [ -S "$SOCKET_FILE" ]; then
    echo "MySQL socket exists: $SOCKET_FILE"
  else
    echo "MySQL socket missing: $SOCKET_FILE"
  fi
}

{{ serviceAccessHelpers }}

echo "checking MySQL binary install commands"
command -v tar >/dev/null 2>&1 || { echo "tar is required"; exit 1; }

echo "installing local RPM dependencies when available"
if [ -d "$WORK_DIR/rpms" ] && ls "$WORK_DIR"/rpms/*.rpm >/dev/null 2>&1; then
  if command -v rpm >/dev/null 2>&1; then
    RPM_LOG="$WORK_DIR/rpm-install.log"
    if $SUDO rpm -Uvh --replacepkgs "$WORK_DIR"/rpms/*.rpm >"$RPM_LOG" 2>&1; then
      cat "$RPM_LOG"
    else
      echo "warning: local RPM dependency installation reported issues; retrying with --nodeps"
      if $SUDO rpm -Uvh --replacepkgs --nodeps "$WORK_DIR"/rpms/*.rpm >"$RPM_LOG" 2>&1; then
        cat "$RPM_LOG"
      else
        echo "warning: local RPM dependency installation failed; MySQL may fail if required runtime libraries are missing"
        cat "$RPM_LOG"
      fi
    fi
  else
    echo "rpm command not found, skip RPM dependency installation"
  fi
fi

echo "preparing MySQL install directories"
$SUDO mkdir -p "$MYSQL_BASE" "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR" "$RUN_DIR" "$IMPORT_DIR" /etc/systemd/system
rm -rf "$WORK_DIR/bundle" "$WORK_DIR/unpacked"
mkdir -p "$WORK_DIR/bundle" "$WORK_DIR/unpacked"

echo "extracting MySQL official bundle"
tar -xf "$ARCHIVE" -C "$WORK_DIR/bundle"
MYSQL_ARCHIVE="$(find "$WORK_DIR/bundle" -maxdepth 1 -type f \( -name 'mysql-*-minimal.tar.xz' -o -name 'mysql-*-linux*.tar.xz' -o -name 'mysql-*-linux*.tar.gz' \) | sort | head -n 1)"
if [ -z "$MYSQL_ARCHIVE" ] || [ ! -f "$MYSQL_ARCHIVE" ]; then
  echo "MySQL server binary archive not found in bundle"
  exit 1
fi

echo "extracting MySQL server binary archive: $MYSQL_ARCHIVE"
tar -xf "$MYSQL_ARCHIVE" -C "$WORK_DIR/unpacked"
MYSQL_SRC="$(find "$WORK_DIR/unpacked" -maxdepth 1 -type d -name 'mysql-*' | sort | head -n 1)"
if [ -z "$MYSQL_SRC" ] || [ ! -x "$MYSQL_SRC/bin/mysqld" ]; then
  echo "mysqld not found after extraction"
  exit 1
fi

echo "installing MySQL binary files"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO systemctl disable --now "$LEGACY_SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO rm -f "/etc/systemd/system/$LEGACY_SERVICE_NAME.service"
$SUDO rm -rf "$MYSQL_BASE"
$SUDO mkdir -p "$MYSQL_BASE"
$SUDO cp -a "$MYSQL_SRC"/. "$MYSQL_BASE"/

MYSQL_SHELL_ARCHIVE="$(find "$WORK_DIR/bundle" -maxdepth 1 -type f \( -name 'mysql-shell-*-linux*.tar.xz' -o -name 'mysql-shell-*-linux*.tar.gz' \) | sort | head -n 1)"
if [ -n "$MYSQL_SHELL_ARCHIVE" ] && [ -f "$MYSQL_SHELL_ARCHIVE" ]; then
  echo "extracting MySQL Shell archive: $MYSQL_SHELL_ARCHIVE"
  rm -rf "$WORK_DIR/unpacked-shell"
  mkdir -p "$WORK_DIR/unpacked-shell"
  tar -xf "$MYSQL_SHELL_ARCHIVE" -C "$WORK_DIR/unpacked-shell"
  MYSQL_SHELL_SRC="$(find "$WORK_DIR/unpacked-shell" -maxdepth 1 -type d -name 'mysql-shell-*' | sort | head -n 1)"
  if [ -n "$MYSQL_SHELL_SRC" ] && [ -x "$MYSQL_SHELL_SRC/bin/mysqlsh" ]; then
    echo "installing MySQL Shell"
    $SUDO rm -rf "$MYSQL_SHELL_BASE"
    $SUDO mkdir -p "$MYSQL_SHELL_BASE"
    $SUDO cp -a "$MYSQL_SHELL_SRC"/. "$MYSQL_SHELL_BASE"/
  else
    echo "warning: mysqlsh not found after extracting MySQL Shell archive"
  fi
else
  echo "warning: MySQL Shell archive not found in bundle; InnoDB Cluster bootstrap requires mysqlsh"
fi

echo "ensuring MySQL runtime user"
NOLOGIN="/sbin/nologin"
if [ -x "/usr/sbin/nologin" ]; then
  NOLOGIN="/usr/sbin/nologin"
fi
if ! id "$MYSQL_USER" >/dev/null 2>&1; then
  $SUDO useradd --system --home-dir "$INSTALL_ROOT" --shell "$NOLOGIN" "$MYSQL_USER"
fi

echo "writing MySQL configuration"
cat > "$WORK_DIR/my.cnf" <<CONF
[mysqld]
basedir=$MYSQL_BASE
datadir=$DATA_DIR
port=$PORT
socket=$SOCKET_FILE
pid-file=$RUN_DIR/mysqld.pid
log-error=$LOG_DIR/mysql-error.log
bind-address=0.0.0.0
{{if .ReportHost}}report_host={{.ReportHost}}
{{end}}server-id={{.ServerID}}
log-bin=$LOG_DIR/mysql-bin
relay-log=$LOG_DIR/mysql-relay-bin
binlog_format=ROW
log_replica_updates=ON
gtid_mode=ON
enforce_gtid_consistency=ON
binlog_transaction_dependency_tracking=WRITESET
mysqlx=0
skip_name_resolve=ON
character-set-server=utf8mb4
collation-server=utf8mb4_0900_ai_ci
max_connections=500
secure-file-priv=$IMPORT_DIR

[client]
socket=$SOCKET_FILE
port=$PORT
default-character-set=utf8mb4
CONF
$SUDO install -m 0644 "$WORK_DIR/my.cnf" "$CONFIG_FILE"
$SUDO chown -R "$MYSQL_USER:$MYSQL_USER" "$INSTALL_ROOT"

NEED_SECURE=0
if [ ! -d "$DATA_DIR/mysql" ]; then
  echo "initializing MySQL data directory"
  NEED_SECURE=1
  $SUDO rm -rf "$DATA_DIR"/*
  $SUDO "$MYSQL_BASE/bin/mysqld" --defaults-file="$CONFIG_FILE" --initialize-insecure --user="$MYSQL_USER"
  $SUDO chown -R "$MYSQL_USER:$MYSQL_USER" "$DATA_DIR" "$LOG_DIR" "$RUN_DIR"
else
  echo "existing MySQL data directory detected, skip initialization"
fi

echo "writing MySQL systemd unit"
cat > "$WORK_DIR/$SERVICE_NAME.service" <<SERVICE
[Unit]
Description=AIFAR MySQL service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$MYSQL_USER
Group=$MYSQL_USER
WorkingDirectory=$INSTALL_ROOT
Environment=LD_LIBRARY_PATH=$MYSQL_BASE/lib
ExecStart=$MYSQL_BASE/bin/mysqld --defaults-file=$CONFIG_FILE
Restart=always
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE
$SUDO install -m 0644 "$WORK_DIR/$SERVICE_NAME.service" "/etc/systemd/system/$SERVICE_NAME.service"

echo "enabling and starting MySQL"
$SUDO systemctl daemon-reload
if ! $SUDO systemctl enable --now "$SERVICE_NAME"; then
  echo "MySQL service failed to start"
  dump_mysql_diagnostics
  exit 1
fi

echo "waiting for MySQL service readiness"
MYSQL_BOOTSTRAP_READY=0
MYSQL_BOOTSTRAP_PROTOCOL=""
i=1
while [ "$i" -le 300 ]; do
  if [ "$NEED_SECURE" = "1" ]; then
    if "$MYSQL_BASE/bin/mysqladmin" --protocol=socket --socket="$SOCKET_FILE" -uroot ping >/dev/null 2>&1; then
      MYSQL_BOOTSTRAP_READY=1
      MYSQL_BOOTSTRAP_PROTOCOL="socket"
      break
    fi
    if "$MYSQL_BASE/bin/mysqladmin" --protocol=tcp -h 127.0.0.1 -P "$PORT" -uroot ping >/dev/null 2>&1; then
      MYSQL_BOOTSTRAP_READY=1
      MYSQL_BOOTSTRAP_PROTOCOL="tcp"
      break
    fi
  else
    if "$MYSQL_BASE/bin/mysqladmin" --defaults-extra-file="$SECURE_CLIENT_FILE" --protocol=tcp -h 127.0.0.1 -P "$PORT" ping >/dev/null 2>&1; then
      MYSQL_BOOTSTRAP_READY=1
      MYSQL_BOOTSTRAP_PROTOCOL="tcp"
      break
    fi
  fi
  if [ $((i % 30)) -eq 0 ]; then
    echo "still waiting for MySQL readiness ($i/300)"
  fi
  i=$((i + 1))
  sleep 1
done
if [ "$MYSQL_BOOTSTRAP_READY" != "1" ]; then
  if [ "$NEED_SECURE" = "1" ]; then
    echo "MySQL bootstrap connection is not ready after installation"
  else
    echo "Existing MySQL data directory is present, but configured administrator credentials could not connect"
    echo "Use the original administrator password, or uninstall/remove the existing MySQL data directory before reinstalling"
  fi
  dump_mysql_diagnostics
  exit 1
fi

if [ "$NEED_SECURE" = "1" ]; then
  echo "setting MySQL administrator password and remote account"
  SQL_USER="$(printf "%s" "$ROOT_USER" | sed "s/\\\\/\\\\\\\\/g; s/'/''/g")"
  SQL_PASSWORD="$(printf "%s" "$ROOT_PASSWORD" | sed "s/\\\\/\\\\\\\\/g; s/'/''/g")"
  (umask 077; cat > "$SECURE_ROOT_SQL" <<SQL
ALTER USER 'root'@'localhost' IDENTIFIED BY '$SQL_PASSWORD';
CREATE USER IF NOT EXISTS '$SQL_USER'@'%' IDENTIFIED BY '$SQL_PASSWORD';
ALTER USER '$SQL_USER'@'%' IDENTIFIED BY '$SQL_PASSWORD';
CREATE USER IF NOT EXISTS '$SQL_USER'@'127.0.0.1' IDENTIFIED BY '$SQL_PASSWORD';
ALTER USER '$SQL_USER'@'127.0.0.1' IDENTIFIED BY '$SQL_PASSWORD';
GRANT ALL PRIVILEGES ON *.* TO '$SQL_USER'@'%' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON *.* TO '$SQL_USER'@'127.0.0.1' WITH GRANT OPTION;
FLUSH PRIVILEGES;
SQL
  )
  chmod 0600 "$SECURE_ROOT_SQL"
  if [ "$MYSQL_BOOTSTRAP_PROTOCOL" = "tcp" ]; then
    "$MYSQL_BASE/bin/mysql" --protocol=tcp -h 127.0.0.1 -P "$PORT" -uroot < "$SECURE_ROOT_SQL"
  else
    "$MYSQL_BASE/bin/mysql" --protocol=socket --socket="$SOCKET_FILE" -uroot < "$SECURE_ROOT_SQL"
  fi
  rm -f -- "$SECURE_ROOT_SQL"
fi

echo "verifying MySQL service"
MYSQL_READY=0
i=1
while [ "$i" -le 120 ]; do
  if "$MYSQL_BASE/bin/mysqladmin" --defaults-extra-file="$SECURE_CLIENT_FILE" --protocol=tcp -h 127.0.0.1 -P "$PORT" ping >/dev/null 2>&1; then
    MYSQL_READY=1
    break
  fi
  i=$((i + 1))
  sleep 1
done
if [ "$MYSQL_READY" != "1" ]; then
  echo "MySQL is not reachable with configured administrator credentials"
  dump_mysql_diagnostics
  exit 1
fi

"$MYSQL_BASE/bin/mysqld" --version
"$MYSQL_BASE/bin/mysqladmin" --defaults-extra-file="$SECURE_CLIENT_FILE" --protocol=tcp -h 127.0.0.1 -P "$PORT" ping
open_firewall_ports "$PORT"
allow_selinux_ports mysqld_port_t "$PORT"
if ! cleanup_secret_artifacts; then
  exit 1
fi
trap - EXIT HUP INT TERM
echo "MySQL service installed: $SERVICE_NAME"
echo "MySQL endpoint: 127.0.0.1:$PORT"
