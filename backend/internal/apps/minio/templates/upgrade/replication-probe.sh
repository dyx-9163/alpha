#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
export LC_ALL=C

phase=$1 root=$2 endpoint=$3 bucket=$4 key=$5 value=$6
config="$root/conf/mc"
client="$root/bin/mcli"
bucket_pattern='^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$'
key_pattern='^aifar-offline-upgrade/[A-Za-z0-9_-]{1,96}/[a-f0-9]{32}\.probe$'
version_pattern='^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'

[[ "$endpoint" =~ ^http://127\.0\.0\.1:[0-9]{1,5}$ && "$bucket" =~ $bucket_pattern ]]
case "$phase" in
  inventory-rule|retention)
    client="$root/bin/mc"
    ;;
  list|put|stat|hash|remove)
    [[ "$key" =~ $key_pattern ]]
    ;;
  *) exit 2 ;;
esac

# Hold the same host lock as the migration phases so restart recovery cannot
# take over while a detached replication probe is still using the service.
phase_lock=/run/lock/aifar-minio-offline-upgrade.lock
if [[ ! -e "$phase_lock" && ! -L "$phase_lock" ]]; then
  (set -o noclobber; : > "$phase_lock") 2>/dev/null || true
fi
[[ -f "$phase_lock" && ! -L "$phase_lock" && "$(stat -c '%u:%a' -- "$phase_lock")" = "$EUID:600" ]]
exec 9< "$phase_lock"
flock -w 5 9

actual=$(timeout 8 "$client" --config-dir "$config" --no-color alias list aifar-local |
  awk '$1 == "URL" && $2 == ":" {print $3}')
[[ "$actual" == "$endpoint" ]]

object="aifar-local/$bucket/$key"
run_cli() { timeout 15 "$client" --config-dir "$config" --no-color "$@"; }

case "$phase" in
  inventory-rule|retention)
    # The caller distinguishes the known "not configured" JSON from permission,
    # transport, and other errors. Pass JSON through without exposing it in logs.
    if [[ "$phase" == inventory-rule ]]; then
      run_cli --json replicate ls "aifar-local/$bucket" 2>&1 || true
    else
      run_cli --json retention info --default "aifar-local/$bucket" 2>&1 || true
    fi
    ;;
  list)
    # ListObjectVersions reads only the locally stored version index. A HEAD or
    # GET alone can be proxied to the other site by the replication service.
    run_cli --json ls --versions --recursive "$object"
    ;;
  put)
    [[ "$value" =~ ^[a-f0-9]{64}$ ]]
    payload_file=$(mktemp /tmp/aifar-silo-canary.XXXXXXXX)
    trap 'rm -f -- "$payload_file"' EXIT
    printf '%s' "$value" > "$payload_file"
    run_cli --json cp --disable-multipart --custom-header 'If-None-Match:*' "$payload_file" "$object" >/dev/null
    ;;
  stat)
    [[ "$value" =~ $version_pattern ]]
    run_cli --json stat --version-id "$value" "$object"
    ;;
  hash)
    [[ "$value" =~ $version_pattern ]]
    checksum=$(run_cli cat --version-id "$value" "$object" | sha256sum)
    printf '%s\n' "${checksum%% *}"
    ;;
  remove)
    [[ "$value" =~ $version_pattern ]]
    run_cli --json rm --force --version-id "$value" "$object" >/dev/null
    ;;
esac
