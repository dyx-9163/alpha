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
SERVICE_NAME="aifar-redis"
LEGACY_SERVICE_NAME="aifar-redis-$REDIS_PORT"
SENTINEL_SERVICE="aifar-redis-sentinel"
LEGACY_SENTINEL_SERVICE="aifar-redis-sentinel-$SENTINEL_PORT"
CONFIG_FILE="$INSTALL_ROOT/conf/redis.conf"
SENTINEL_CONFIG="$INSTALL_ROOT/conf/sentinel.conf"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

{{ serviceAccessHelpers }}

ensure_service_access() {
  if [ "$ROLE" = "sentinel" ]; then
    open_firewall_ports "$SENTINEL_PORT"
    allow_selinux_ports redis_port_t "$SENTINEL_PORT"
  else
    open_firewall_ports "$REDIS_PORT" "$SENTINEL_PORT"
    allow_selinux_ports redis_port_t "$REDIS_PORT" "$SENTINEL_PORT"
  fi
}

echo "configuring Redis Sentinel node: $ROLE"
ensure_service_access

CONFIG_TMP="$(mktemp)"
if [ "$ROLE" = "replica" ]; then
  awk '!/^replicaof / { print }' "$CONFIG_FILE" > "$CONFIG_TMP"
  cat >> "$CONFIG_TMP" <<CONF
replicaof $MASTER_HOST $MASTER_PORT
CONF
  $SUDO install -m 0644 "$CONFIG_TMP" "$CONFIG_FILE"
else
  if grep -q "^replicaof " "$CONFIG_FILE"; then
    awk '!/^replicaof / { print }' "$CONFIG_FILE" > "$CONFIG_TMP"
    $SUDO install -m 0644 "$CONFIG_TMP" "$CONFIG_FILE"
  fi
fi
rm -f "$CONFIG_TMP"

cat > "$INSTALL_ROOT/conf/sentinel.conf.tmp" <<CONF
port $SENTINEL_PORT
bind 0.0.0.0 -::1
protected-mode yes
user default on >$REDIS_PASSWORD allcommands allkeys allchannels
daemonize no
supervised no
dir $INSTALL_ROOT/data
logfile $INSTALL_ROOT/logs/sentinel.log
sentinel monitor $MASTER_NAME $MASTER_HOST $MASTER_PORT $QUORUM
sentinel auth-pass $MASTER_NAME $REDIS_PASSWORD
sentinel sentinel-pass $REDIS_PASSWORD
sentinel down-after-milliseconds $MASTER_NAME 5000
sentinel failover-timeout $MASTER_NAME 60000
sentinel parallel-syncs $MASTER_NAME 1
CONF
$SUDO install -m 0644 "$INSTALL_ROOT/conf/sentinel.conf.tmp" "$SENTINEL_CONFIG"

$SUDO systemctl disable --now "$LEGACY_SENTINEL_SERVICE" >/dev/null 2>&1 || true
$SUDO rm -f "/etc/systemd/system/$LEGACY_SENTINEL_SERVICE.service"

cat > "$INSTALL_ROOT/conf/$SENTINEL_SERVICE.service" <<SERVICE
[Unit]
Description=AIFAR Redis Sentinel service
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
  if ! $SUDO systemctl restart "$SERVICE_NAME"; then
    $SUDO systemctl restart "$LEGACY_SERVICE_NAME"
  fi
fi
echo "starting Redis Sentinel service"
if ! START_OUTPUT="$($SUDO systemctl enable --now "$SENTINEL_SERVICE" 2>&1)"; then
  echo "$START_OUTPUT"
  echo "Redis Sentinel service failed to start"
  $SUDO systemctl --no-pager --full status "$SENTINEL_SERVICE" 2>&1 || true
  $SUDO journalctl -u "$SENTINEL_SERVICE" -n 120 --no-pager 2>&1 || true
  echo "Redis Sentinel config:"
  $SUDO sed -n '1,160p' "$SENTINEL_CONFIG" 2>&1 || true
  exit 1
fi
echo "$START_OUTPUT"

echo "verifying Redis Sentinel"
if ! VERIFY_OUTPUT="$("$INSTALL_ROOT/bin/redis-cli" -p "$SENTINEL_PORT" -a "$REDIS_PASSWORD" --no-auth-warning sentinel masters 2>&1)"; then
  echo "$VERIFY_OUTPUT"
  echo "Redis Sentinel is not responding"
  $SUDO systemctl --no-pager --full status "$SENTINEL_SERVICE" 2>&1 || true
  $SUDO journalctl -u "$SENTINEL_SERVICE" -n 120 --no-pager 2>&1 || true
  exit 1
fi
ensure_service_access
echo "Redis Sentinel node configured: $ROLE"
