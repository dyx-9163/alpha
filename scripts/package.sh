#!/usr/bin/env sh
set -eu
ROOT="$(CDPATH= cd "$(dirname "$0")/.." && pwd)"
TOOL_ROOT="${AIFAR_TOOL_ROOT:-/opt/aifar-tools}"
if [ -d "/d/tools/node" ]; then
  TOOL_ROOT="/d/tools"
fi
if [ -d "/mnt/d/tools/node" ]; then
  TOOL_ROOT="/mnt/d/tools"
fi
export PATH="$TOOL_ROOT/node:$TOOL_ROOT/node-global:$TOOL_ROOT/go/bin:$TOOL_ROOT/gopath/bin:$PATH"
export GOROOT="$TOOL_ROOT/go"
export GOPATH="$TOOL_ROOT/gopath"
export GOCACHE="$TOOL_ROOT/gocache"
mkdir -p "$GOCACHE"
cd "$ROOT"
pnpm install
pnpm build
echo "Package artifacts generated under $ROOT"
