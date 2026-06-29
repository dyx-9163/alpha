#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
WORK_DIR={{shq .WorkDir}}
ARCHIVE={{shq .ArchivePath}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
REDIS_PASSWORD={{shq .Password}}
SERVICE_NAME="aifar-redis-$PORT"
CONFIG_DIR="$INSTALL_ROOT/conf"
DATA_DIR="$INSTALL_ROOT/data"
LOG_DIR="$INSTALL_ROOT/logs"
RUN_DIR="$INSTALL_ROOT/run"
CONFIG_FILE="$CONFIG_DIR/redis.conf"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "checking Redis build commands"
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
        echo "warning: local RPM dependency installation failed; Redis build may fail if gcc/make are missing"
        cat "$RPM_LOG"
      fi
    fi
  else
    echo "rpm command not found, skip RPM dependency installation"
  fi
fi

command -v make >/dev/null 2>&1 || { echo "make is required after RPM preparation"; exit 1; }
command -v gcc >/dev/null 2>&1 || { echo "gcc is required after RPM preparation"; exit 1; }

echo "extracting Redis archive"
rm -rf "$WORK_DIR/unpacked"
mkdir -p "$WORK_DIR/unpacked"
tar -xzf "$ARCHIVE" -C "$WORK_DIR/unpacked"
SRC_DIR="$WORK_DIR/unpacked/redis-$VERSION"
if [ ! -d "$SRC_DIR" ]; then
  echo "Redis source directory not found: $SRC_DIR"
  exit 1
fi

echo "building Redis from source"
NPROC="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)"
make -C "$SRC_DIR" distclean >/dev/null 2>&1 || true
make -C "$SRC_DIR" BUILD_TLS=no MALLOC=libc -j "$NPROC"

echo "installing Redis files"
$SUDO mkdir -p "$INSTALL_ROOT/bin" "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR" "$RUN_DIR" /etc/systemd/system
for bin in redis-server redis-cli redis-benchmark redis-check-aof redis-check-rdb; do
  $SUDO install -m 0755 "$SRC_DIR/src/$bin" "$INSTALL_ROOT/bin/$bin"
done

{{if .StartService}}
echo "writing Redis configuration"
cat > "$WORK_DIR/redis.conf" <<CONF
bind 0.0.0.0 -::1
protected-mode yes
requirepass $REDIS_PASSWORD
masterauth $REDIS_PASSWORD
port $PORT
tcp-backlog 511
timeout 0
tcp-keepalive 300
daemonize no
supervised no
pidfile $RUN_DIR/redis.pid
loglevel notice
logfile $LOG_DIR/redis.log
databases 16
always-show-logo no
dir $DATA_DIR
appendonly yes
appendfilename "appendonly.aof"
save 900 1
save 300 10
save 60 10000
CONF
$SUDO install -m 0644 "$WORK_DIR/redis.conf" "$CONFIG_FILE"
echo "Redis config: $CONFIG_FILE"

echo "writing Redis systemd unit"
cat > "$WORK_DIR/$SERVICE_NAME.service" <<SERVICE
[Unit]
Description=AIFAR Redis standalone service on port $PORT
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_ROOT/bin/redis-server $CONFIG_FILE
ExecStop=$INSTALL_ROOT/bin/redis-cli -p $PORT shutdown
Restart=always
RestartSec=2
LimitNOFILE=10032

[Install]
WantedBy=multi-user.target
SERVICE
$SUDO install -m 0644 "$WORK_DIR/$SERVICE_NAME.service" "/etc/systemd/system/$SERVICE_NAME.service"

echo "enabling and starting Redis"
$SUDO systemctl daemon-reload
if ! $SUDO systemctl enable --now "$SERVICE_NAME"; then
  echo "Redis service failed to start"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 80 --no-pager || true
  exit 1
fi

echo "verifying Redis service"
REDIS_READY=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
  if "$INSTALL_ROOT/bin/redis-cli" -p "$PORT" -a "$REDIS_PASSWORD" --no-auth-warning ping 2>/dev/null | grep -q PONG; then
    REDIS_READY=1
    break
  fi
  sleep 1
done
if [ "$REDIS_READY" != "1" ]; then
  echo "Redis is not reachable after installation"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 80 --no-pager || true
  exit 1
fi
"$INSTALL_ROOT/bin/redis-server" --version
"$INSTALL_ROOT/bin/redis-cli" -p "$PORT" -a "$REDIS_PASSWORD" --no-auth-warning ping
echo "Redis standalone service installed: $SERVICE_NAME"
{{else}}
"$INSTALL_ROOT/bin/redis-server" --version
echo "Redis binaries installed for Sentinel"
{{end}}
