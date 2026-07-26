#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SPEC_PATH={{ quote .SpecPath }}
ENV_DIR="$INSTALL_ROOT/runtime/env"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is not installed"
[ -f "$SPEC_PATH" ] || fail "AIFAR runtime spec is missing"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"

aifar-agent restart-runtime --spec "$SPEC_PATH"
