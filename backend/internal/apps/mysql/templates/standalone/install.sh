#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
WORK_DIR={{shq .WorkDir}}
ARCHIVE={{shq .ArchivePath}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
ROOT_USER={{shq .RootUser}}
ROOT_PASSWORD={{shq .RootPassword}}
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
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

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
ExecStop=/bin/kill -TERM \$MAINPID
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
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  exit 1
fi

echo "waiting for MySQL service readiness"
MYSQL_SOCKET_READY=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
  if [ "$NEED_SECURE" = "1" ]; then
    if "$MYSQL_BASE/bin/mysqladmin" --protocol=socket --socket="$SOCKET_FILE" -uroot ping >/dev/null 2>&1; then
      MYSQL_SOCKET_READY=1
      break
    fi
  else
    if MYSQL_PWD="$ROOT_PASSWORD" "$MYSQL_BASE/bin/mysqladmin" --protocol=tcp -h 127.0.0.1 -P "$PORT" -u "$ROOT_USER" ping >/dev/null 2>&1; then
      MYSQL_SOCKET_READY=1
      break
    fi
  fi
  sleep 1
done
if [ "$MYSQL_SOCKET_READY" != "1" ]; then
  if [ "$NEED_SECURE" = "1" ]; then
    echo "MySQL socket is not ready after installation"
  else
    echo "Existing MySQL data directory is present, but configured administrator credentials could not connect"
    echo "Use the original administrator password, or uninstall/remove the existing MySQL data directory before reinstalling"
  fi
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  exit 1
fi

if [ "$NEED_SECURE" = "1" ]; then
  echo "setting MySQL administrator password and remote account"
  SQL_USER="$(printf "%s" "$ROOT_USER" | sed "s/'/''/g")"
  SQL_PASSWORD="$(printf "%s" "$ROOT_PASSWORD" | sed "s/'/''/g")"
  cat > "$WORK_DIR/secure-root.sql" <<SQL
ALTER USER 'root'@'localhost' IDENTIFIED BY '$SQL_PASSWORD';
CREATE USER IF NOT EXISTS '$SQL_USER'@'%' IDENTIFIED BY '$SQL_PASSWORD';
ALTER USER '$SQL_USER'@'%' IDENTIFIED BY '$SQL_PASSWORD';
CREATE USER IF NOT EXISTS '$SQL_USER'@'127.0.0.1' IDENTIFIED BY '$SQL_PASSWORD';
ALTER USER '$SQL_USER'@'127.0.0.1' IDENTIFIED BY '$SQL_PASSWORD';
GRANT ALL PRIVILEGES ON *.* TO '$SQL_USER'@'%' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON *.* TO '$SQL_USER'@'127.0.0.1' WITH GRANT OPTION;
FLUSH PRIVILEGES;
SQL
  "$MYSQL_BASE/bin/mysql" --protocol=socket --socket="$SOCKET_FILE" -uroot < "$WORK_DIR/secure-root.sql"
  rm -f "$WORK_DIR/secure-root.sql"
fi

echo "verifying MySQL service"
MYSQL_READY=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if MYSQL_PWD="$ROOT_PASSWORD" "$MYSQL_BASE/bin/mysqladmin" --protocol=tcp -h 127.0.0.1 -P "$PORT" -u "$ROOT_USER" ping >/dev/null 2>&1; then
    MYSQL_READY=1
    break
  fi
  sleep 1
done
if [ "$MYSQL_READY" != "1" ]; then
  echo "MySQL is not reachable with configured administrator credentials"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  exit 1
fi

"$MYSQL_BASE/bin/mysqld" --version
MYSQL_PWD="$ROOT_PASSWORD" "$MYSQL_BASE/bin/mysqladmin" --protocol=tcp -h 127.0.0.1 -P "$PORT" -u "$ROOT_USER" ping
echo "MySQL service installed: $SERVICE_NAME"
echo "MySQL endpoint: 127.0.0.1:$PORT"
