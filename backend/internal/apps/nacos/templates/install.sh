#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
MODE={{shq .Mode}}
WORK_DIR={{shq .WorkDir}}
ARCHIVE={{shq .ArchivePath}}
JDK_ARCHIVE={{shq .JDKPath}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
GRPC_PORT={{.GRPCPort}}
GRPC_RAFT_PORT={{.GRPCRaftPort}}
RAFT_PORT={{.RaftPort}}
JVM_XMS={{shq .JVMXMS}}
JVM_XMX={{shq .JVMXMX}}
JVM_XMN={{shq .JVMXMN}}
NACOS_USER={{shq .AdminUser}}
NACOS_PASSWORD={{shq .AdminPassword}}
DB_ENABLED={{if .Database.Enabled}}1{{else}}0{{end}}
DB_HOST={{shq .Database.Host}}
DB_PORT={{.Database.Port}}
DB_NAME={{shq .Database.Name}}
DB_USER={{shq .Database.User}}
DB_PASSWORD={{shq .Database.Password}}
INIT_DATABASE={{if .Database.Init}}1{{else}}0{{end}}
SERVICE_USER="aifar-nacos"
SERVICE_NAME="aifar-nacos"
NACOS_HOME="$INSTALL_ROOT/nacos"
JDK_HOME="$INSTALL_ROOT/jdk"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

open_firewall_port() {
  PORT_VALUE="$1"
  if command -v firewall-cmd >/dev/null 2>&1 && $SUDO firewall-cmd --state >/dev/null 2>&1; then
    $SUDO firewall-cmd --add-port="${PORT_VALUE}/tcp" --permanent >/dev/null 2>&1 || true
    $SUDO firewall-cmd --reload >/dev/null 2>&1 || true
  fi
}

dump_nacos_diagnostics() {
  echo "Nacos systemd status"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  for LOG_FILE in "$NACOS_HOME/logs/start.out" "$NACOS_HOME/logs/nacos.log" "$NACOS_HOME/logs/config.log" "$NACOS_HOME/logs/naming-server.log"; do
    if [ -f "$LOG_FILE" ]; then
      echo "----- $LOG_FILE -----"
      $SUDO tail -n 120 "$LOG_FILE" || true
    fi
  done
}

nacos_access_token() {
  USERNAME="$1"
  PASSWORD="$2"
  RESPONSE="$(curl -fsS --max-time 5 -X POST "http://127.0.0.1:$PORT/nacos/v1/auth/users/login" \
    --data-urlencode "username=$USERNAME" \
    --data-urlencode "password=$PASSWORD" 2>/dev/null || true)"
  printf "%s" "$RESPONSE" | sed -n 's/.*"accessToken"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

nacos_user_url() {
  TOKEN="$1"
  URL="http://127.0.0.1:$PORT/nacos/v1/auth/users"
  if [ -n "$TOKEN" ]; then
    URL="$URL?accessToken=$TOKEN"
  fi
  printf "%s" "$URL"
}

nacos_create_user() {
  TOKEN="$1"
  curl -fsS --max-time 5 -X POST "$(nacos_user_url "$TOKEN")" \
    --data-urlencode "username=$NACOS_USER" \
    --data-urlencode "password=$NACOS_PASSWORD" >/dev/null 2>&1
}

nacos_update_user() {
  TOKEN="$1"
  curl -fsS --max-time 5 -X PUT "$(nacos_user_url "$TOKEN")" \
    --data-urlencode "username=$NACOS_USER" \
    --data-urlencode "newPassword=$NACOS_PASSWORD" >/dev/null 2>&1
}

configure_nacos_admin_user() {
  [ -n "$NACOS_USER" ] || { echo "Nacos admin user is required"; exit 1; }
  [ -n "$NACOS_PASSWORD" ] || { echo "Nacos admin password is required"; exit 1; }
  command -v curl >/dev/null 2>&1 || { echo "curl is required to configure Nacos credentials"; exit 1; }

  TOKEN="$(nacos_access_token "$NACOS_USER" "$NACOS_PASSWORD")"
  [ -n "$TOKEN" ] && return 0

  TOKEN="$(nacos_access_token nacos nacos)"
  if [ -z "$TOKEN" ]; then
    if ! nacos_create_user ""; then
      echo "unable to authenticate to Nacos for credential configuration"
      exit 1
    fi
  elif [ "$NACOS_USER" = "nacos" ]; then
    nacos_update_user "$TOKEN" || true
  else
    nacos_create_user "$TOKEN" || nacos_update_user "$TOKEN" || true
  fi

  TOKEN="$(nacos_access_token "$NACOS_USER" "$NACOS_PASSWORD")"
  [ -n "$TOKEN" ] || { echo "Nacos credential verification failed"; exit 1; }
}

echo "checking Nacos install commands"
command -v tar >/dev/null 2>&1 || { echo "tar is required"; exit 1; }
command -v find >/dev/null 2>&1 || { echo "find is required"; exit 1; }

echo "preparing Nacos install directories"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO mkdir -p "$INSTALL_ROOT" "$WORK_DIR/unpacked-nacos" "$WORK_DIR/unpacked-jdk" /etc/systemd/system

echo "extracting JDK archive"
rm -rf "$WORK_DIR/unpacked-jdk"/*
tar -xzf "$JDK_ARCHIVE" -C "$WORK_DIR/unpacked-jdk"
JDK_SRC="$(find "$WORK_DIR/unpacked-jdk" -mindepth 1 -maxdepth 1 -type d | sort | head -n 1)"
if [ -z "$JDK_SRC" ] || [ ! -x "$JDK_SRC/bin/java" ]; then
  echo "JDK java binary not found after extraction"
  exit 1
fi

echo "extracting Nacos archive"
rm -rf "$WORK_DIR/unpacked-nacos"/*
tar -xzf "$ARCHIVE" -C "$WORK_DIR/unpacked-nacos"
NACOS_SRC="$(find "$WORK_DIR/unpacked-nacos" -type d -name nacos | sort | head -n 1)"
if [ -z "$NACOS_SRC" ] || [ ! -x "$NACOS_SRC/bin/startup.sh" ]; then
  echo "Nacos startup script not found after extraction"
  exit 1
fi

echo "installing Nacos files"
$SUDO rm -rf "$NACOS_HOME" "$JDK_HOME"
$SUDO mkdir -p "$NACOS_HOME" "$JDK_HOME"
$SUDO cp -a "$NACOS_SRC"/. "$NACOS_HOME"/
$SUDO cp -a "$JDK_SRC"/. "$JDK_HOME"/

NOLOGIN="/sbin/nologin"
if [ -x "/usr/sbin/nologin" ]; then
  NOLOGIN="/usr/sbin/nologin"
fi
if ! id "$SERVICE_USER" >/dev/null 2>&1; then
  $SUDO useradd --system --home-dir "$INSTALL_ROOT" --shell "$NOLOGIN" "$SERVICE_USER"
fi

echo "writing Nacos application properties"
APP_PROPS="$NACOS_HOME/conf/application.properties"
TMP_PROPS="$WORK_DIR/application.properties"
if [ -f "$APP_PROPS" ]; then
  sed '/^# AIFAR BEGIN$/,/^# AIFAR END$/d' "$APP_PROPS" > "$TMP_PROPS"
else
  : > "$TMP_PROPS"
fi
cat >> "$TMP_PROPS" <<CONF
# AIFAR BEGIN
server.port=$PORT
nacos.core.auth.enabled=true
nacos.core.auth.server.identity.key=aifar
nacos.core.auth.server.identity.value=aifar
nacos.core.auth.plugin.nacos.token.secret.key=Rm9yQWlmYXJPZmZsaW5lRGVwbG95bWVudE9ubHkzMkJ5dGVz
CONF
if [ "$DB_ENABLED" = "1" ]; then
  cat >> "$TMP_PROPS" <<CONF
spring.sql.init.platform=mysql
db.num=1
db.url.0=jdbc:mysql://$DB_HOST:$DB_PORT/$DB_NAME?characterEncoding=utf8&connectTimeout=1000&socketTimeout=3000&autoReconnect=true&useSSL=false&allowPublicKeyRetrieval=true&serverTimezone=Asia/Shanghai
db.user.0=$DB_USER
db.password.0=$DB_PASSWORD
CONF
fi
cat >> "$TMP_PROPS" <<CONF
# AIFAR END
CONF
$SUDO install -m 0644 "$TMP_PROPS" "$APP_PROPS"

if [ "$MODE" = "cluster" ]; then
  echo "writing Nacos cluster.conf"
  cat > "$WORK_DIR/cluster.conf" <<CONF
{{range .ClusterNodes}}{{.Host}}:{{.Port}}
{{end}}CONF
  $SUDO install -m 0644 "$WORK_DIR/cluster.conf" "$NACOS_HOME/conf/cluster.conf"
fi

if [ "$DB_ENABLED" = "1" ] && [ "$INIT_DATABASE" = "1" ]; then
  echo "initializing Nacos MySQL schema"
  command -v mysql >/dev/null 2>&1 || { echo "mysql client is required when init database is enabled"; exit 1; }
  MYSQL_PWD="$DB_PASSWORD" mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" -e "CREATE DATABASE IF NOT EXISTS \`$DB_NAME\` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;"
  MYSQL_PWD="$DB_PASSWORD" mysql -h "$DB_HOST" -P "$DB_PORT" -u "$DB_USER" "$DB_NAME" < "$NACOS_HOME/conf/mysql-schema.sql"
fi

echo "writing Nacos systemd unit"
cat > "$WORK_DIR/$SERVICE_NAME.service" <<SERVICE
[Unit]
Description=AIFAR Nacos service
After=network-online.target
Wants=network-online.target

[Service]
Type=forking
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$NACOS_HOME
Environment=JAVA_HOME=$JDK_HOME
Environment=JVM_XMS=$JVM_XMS
Environment=JVM_XMX=$JVM_XMX
Environment=JVM_XMN=$JVM_XMN
Environment="CUSTOM_NACOS_MEMORY=-Xms$JVM_XMS -Xmx$JVM_XMX -Xmn$JVM_XMN"
ExecStart=$NACOS_HOME/bin/startup.sh -m $MODE
ExecStop=$NACOS_HOME/bin/shutdown.sh
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE
$SUDO chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_ROOT"
$SUDO install -m 0644 "$WORK_DIR/$SERVICE_NAME.service" "/etc/systemd/system/$SERVICE_NAME.service"

echo "enabling and starting Nacos"
$SUDO systemctl daemon-reload
if ! $SUDO systemctl enable "$SERVICE_NAME"; then
  echo "Nacos service failed to enable"
  dump_nacos_diagnostics
  exit 1
fi
START_FAILED=0
if ! $SUDO systemctl restart "$SERVICE_NAME"; then
  START_FAILED=1
  echo "Nacos service start command returned non-zero; waiting for readiness before failing"
fi

echo "waiting for Nacos readiness"
READY=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
  if command -v curl >/dev/null 2>&1 && curl -fsS --max-time 3 "http://127.0.0.1:$PORT/nacos/v1/console/health/readiness" >/dev/null 2>&1; then
    READY=1
    break
  fi
  if command -v ss >/dev/null 2>&1 && ss -lnt | awk '{print $4}' | grep -Eq "(:|\\.)$PORT$"; then
    READY=1
    break
  fi
  sleep 2
done
if [ "$READY" != "1" ]; then
  echo "Nacos is not reachable after installation"
  if [ "$START_FAILED" = "1" ]; then
    echo "Nacos service start command also returned non-zero"
  fi
  dump_nacos_diagnostics
  exit 1
fi

configure_nacos_admin_user

open_firewall_port "$PORT"
open_firewall_port "$GRPC_PORT"
open_firewall_port "$GRPC_RAFT_PORT"
open_firewall_port "$RAFT_PORT"
"$JDK_HOME/bin/java" -version 2>&1 | head -n 1
echo "Nacos $VERSION installed in $MODE mode on port $PORT"
