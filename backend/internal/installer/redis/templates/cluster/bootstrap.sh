#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
PORT={{.Port}}
REDIS_PASSWORD={{shq .Password}}
REPLICAS={{.Replicas}}

echo "bootstrapping Redis Cluster"
NODES="{{range .Nodes}}{{.Host}}:{{.Port}} {{end}}"
if "$INSTALL_ROOT/bin/redis-cli" -p "$PORT" -a "$REDIS_PASSWORD" --no-auth-warning cluster info 2>/dev/null | grep -q "cluster_state:ok"; then
  echo "Redis Cluster already reports cluster_state:ok"
  exit 0
fi

printf "yes\n" | "$INSTALL_ROOT/bin/redis-cli" --cluster create $NODES --cluster-replicas "$REPLICAS" -a "$REDIS_PASSWORD" --no-auth-warning
"$INSTALL_ROOT/bin/redis-cli" -p "$PORT" -a "$REDIS_PASSWORD" --no-auth-warning cluster info
echo "Redis Cluster bootstrap completed"
