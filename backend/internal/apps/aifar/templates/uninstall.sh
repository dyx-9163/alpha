#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
NETWORK_NAME={{ quote .NetworkName }}

if command -v docker >/dev/null 2>&1 && [ -d "$INSTALL_ROOT" ]; then
  if command -v aifar-agent >/dev/null 2>&1; then
    aifar-agent remove-instance --instance admin >/dev/null 2>&1 || true
  fi
  pods="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$pods" ] || docker rm -f $pods >/dev/null 2>&1 || true
fi

rm -rf "$INSTALL_ROOT"

if command -v docker >/dev/null 2>&1 && [ -n "$NETWORK_NAME" ]; then
  docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
fi

echo "AIFAR agent orchestration removed from $INSTALL_ROOT"
