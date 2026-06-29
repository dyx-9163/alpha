#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
REDIS_PORT={{.RedisPort}}
SENTINEL_PORT={{.SentinelPort}}
REDIS_PASSWORD={{shq .Password}}
MASTER_NAME={{shq .MasterName}}
MASTER_HOST={{shq .MasterHost}}
MASTER_PORT={{.MasterPort}}
QUORUM={{.Quorum}}
ROLE={{shq .Role}}
SERVICE_NAME="aifar-redis-$REDIS_PORT"
SENTINEL_SERVICE="aifar-redis-sentinel-$SENTINEL_PORT"
CONFIG_FILE="$INSTALL_ROOT/conf/redis.conf"
SENTINEL_CONFIG="$INSTALL_ROOT/conf/sentinel.conf"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "configuring Redis Sentinel node: $ROLE"
if [ "$ROLE" = "replica" ]; then
  if ! grep -q "^replicaof " "$CONFIG_FILE"; then
    cat >> "$CONFIG_FILE" <<CONF
replicaof $MASTER_HOST $MASTER_PORT
CONF
  fi
fi

cat > "$INSTALL_ROOT/conf/sentinel.conf.tmp" <<CONF
port $SENTINEL_PORT
bind 0.0.0.0 -::1
protected-mode yes
daemonize no
supervised no
dir $INSTALL_ROOT/data
logfile $INSTALL_ROOT/logs/sentinel.log
sentinel monitor $MASTER_NAME $MASTER_HOST $MASTER_PORT $QUORUM
sentinel auth-pass $MASTER_NAME $REDIS_PASSWORD
sentinel down-after-milliseconds $MASTER_NAME 5000
sentinel failover-timeout $MASTER_NAME 60000
sentinel parallel-syncs $MASTER_NAME 1
CONF
$SUDO install -m 0644 "$INSTALL_ROOT/conf/sentinel.conf.tmp" "$SENTINEL_CONFIG"

cat > "$INSTALL_ROOT/conf/$SENTINEL_SERVICE.service" <<SERVICE
[Unit]
Description=AIFAR Redis Sentinel service on port $SENTINEL_PORT
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=$INSTALL_ROOT/bin/redis-server $SENTINEL_CONFIG --sentinel
Restart=always
RestartSec=2
LimitNOFILE=10032

[Install]
WantedBy=multi-user.target
SERVICE
$SUDO install -m 0644 "$INSTALL_ROOT/conf/$SENTINEL_SERVICE.service" "/etc/systemd/system/$SENTINEL_SERVICE.service"

$SUDO systemctl daemon-reload
if [ "$ROLE" != "sentinel" ]; then
  echo "restarting Redis data service"
  $SUDO systemctl restart "$SERVICE_NAME"
fi
echo "starting Redis Sentinel service"
if ! $SUDO systemctl enable --now "$SENTINEL_SERVICE"; then
  echo "Redis Sentinel service failed to start"
  $SUDO systemctl --no-pager --full status "$SENTINEL_SERVICE" || true
  $SUDO journalctl -u "$SENTINEL_SERVICE" -n 80 --no-pager || true
  exit 1
fi

echo "verifying Redis Sentinel"
if ! "$INSTALL_ROOT/bin/redis-cli" -p "$SENTINEL_PORT" sentinel masters >/dev/null 2>&1; then
  echo "Redis Sentinel is not responding"
  $SUDO systemctl --no-pager --full status "$SENTINEL_SERVICE" || true
  exit 1
fi
echo "Redis Sentinel node configured: $ROLE"
