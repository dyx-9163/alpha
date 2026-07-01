#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{shq .InstallRoot}}
API_PORT={{.APIPort}}
LOCAL_ENDPOINT={{shq .LocalEndpoint}}
PEER_ENDPOINT={{shq .PeerEndpoint}}
PEER_REMOTE_PREFIX={{shq .PeerRemotePrefix}}
ROOT_USER={{shq .RootUser}}
ROOT_PASSWORD={{shq .RootPassword}}
REPLICATE_DELETES={{if .ReplicateDeletes}}1{{else}}0{{end}}
MC="$INSTALL_ROOT/bin/mc"
MC_CONFIG_DIR="$INSTALL_ROOT/conf/mc"
LOCAL_ALIAS="aifar-local"
PEER_ALIAS="aifar-peer"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "configuring MinIO bucket replication"
if [ ! -x "$MC" ]; then
  echo "MinIO client not found: $MC"
  exit 1
fi

$SUDO mkdir -p "$MC_CONFIG_DIR"
$SUDO chown "$(id -u):$(id -g)" "$MC_CONFIG_DIR" >/dev/null 2>&1 || true
export MC_CONFIG_DIR

"$MC" alias set "$LOCAL_ALIAS" "$LOCAL_ENDPOINT" "$ROOT_USER" "$ROOT_PASSWORD" --api S3v4 >/dev/null
"$MC" alias set "$PEER_ALIAS" "$PEER_ENDPOINT" "$ROOT_USER" "$ROOT_PASSWORD" --api S3v4 >/dev/null

for BUCKET in {{range .Buckets}}{{shq .}} {{end}}; do
  [ -n "$BUCKET" ] || continue
  echo "ensuring replicated bucket: $BUCKET"
  "$MC" mb --ignore-existing "$LOCAL_ALIAS/$BUCKET" >/dev/null
  "$MC" mb --ignore-existing "$PEER_ALIAS/$BUCKET" >/dev/null
  "$MC" version enable "$LOCAL_ALIAS/$BUCKET" >/dev/null
  "$MC" version enable "$PEER_ALIAS/$BUCKET" >/dev/null

  "$MC" replicate rm "$LOCAL_ALIAS/$BUCKET" --all --force >/dev/null 2>&1 || true
  REPLICATE_FLAGS="existing-objects"
  if [ "$REPLICATE_DELETES" = "1" ]; then
    REPLICATE_FLAGS="delete,delete-marker,existing-objects"
  fi
  if ! "$MC" replicate add "$LOCAL_ALIAS/$BUCKET" --remote-bucket "$PEER_REMOTE_PREFIX/$BUCKET" --replicate "$REPLICATE_FLAGS"; then
    echo "failed to configure replication for bucket: $BUCKET"
    "$MC" replicate ls "$LOCAL_ALIAS/$BUCKET" || true
    exit 1
  fi
done

echo "MinIO bucket replication configured from $LOCAL_ENDPOINT to $PEER_ENDPOINT"
