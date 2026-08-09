#!/usr/bin/env sh
set -eu

MANIFEST_PATH={{ quote .ManifestPath }}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is not installed"
[ -f "$MANIFEST_PATH" ] || fail "AIFAR service Manifest is missing"

aifar-agent apply-deployment --manifest "$MANIFEST_PATH"
