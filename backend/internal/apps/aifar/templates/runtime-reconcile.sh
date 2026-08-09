#!/usr/bin/env sh
set -eu

INSTANCE_ID={{ quote .InstanceID }}
SERVICE_NAME={{ quote .ServiceName }}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is not installed"
[ -n "$INSTANCE_ID" ] || fail "AIFAR instance id is required"
[ -n "$SERVICE_NAME" ] || fail "AIFAR service name is required"

aifar-agent reconcile-deployment --instance "$INSTANCE_ID" --service "$SERVICE_NAME"
