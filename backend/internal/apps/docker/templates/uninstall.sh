#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
INSTALL_ROOT={{shq .InstallRoot}}
LEGACY_INSTALL_ROOT={{shq .LegacyInstallRoot}}
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "stopping Docker services"
$SUDO systemctl disable --now docker >/dev/null 2>&1 || true
$SUDO systemctl disable --now containerd >/dev/null 2>&1 || true

echo "removing Docker systemd units"
$SUDO rm -f /etc/systemd/system/docker.service /etc/systemd/system/containerd.service
$SUDO systemctl daemon-reload >/dev/null 2>&1 || true

echo "removing Docker binaries"
for bin in containerd containerd-shim-runc-v2 ctr docker docker-init docker-proxy dockerd runc docker-compose; do
  $SUDO rm -f "/usr/local/bin/$bin"
done
$SUDO rm -f /usr/local/lib/docker/cli-plugins/docker-compose
hash -r >/dev/null 2>&1 || true
echo "cleared docker command cache for this shell; existing interactive shells can run: hash -r"

for ROOT in "$INSTALL_ROOT" "$LEGACY_INSTALL_ROOT"; do
  echo "removing Docker install root: $ROOT"
  if [ -n "$ROOT" ] && [ "$ROOT" != "/" ] && [ -d "$ROOT" ]; then
    $SUDO rm -rf "$ROOT"
  else
    echo "Docker install root does not exist, skip"
  fi
done

DOCKER_ROOT="$(dirname "$INSTALL_ROOT")"
rmdir "$DOCKER_ROOT" >/dev/null 2>&1 || true

echo "verifying Docker removal"
FAILED=0
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet docker 2>/dev/null; then
  echo "docker.service is still active"
  FAILED=1
fi
for unit in /etc/systemd/system/docker.service /etc/systemd/system/containerd.service; do
  if $SUDO test -e "$unit"; then
    echo "unit remains: $unit"
    FAILED=1
  fi
done
for bin in containerd containerd-shim-runc-v2 ctr docker docker-init docker-proxy dockerd runc docker-compose; do
  if $SUDO test -e "/usr/local/bin/$bin"; then
    echo "binary remains: /usr/local/bin/$bin"
    FAILED=1
  fi
done
if $SUDO test -e /usr/local/lib/docker/cli-plugins/docker-compose; then
  echo "compose plugin remains: /usr/local/lib/docker/cli-plugins/docker-compose"
  FAILED=1
fi
if [ -n "$INSTALL_ROOT" ] && [ "$INSTALL_ROOT" != "/" ] && $SUDO test -d "$INSTALL_ROOT"; then
  echo "install root remains: $INSTALL_ROOT"
  FAILED=1
fi
if [ -n "$LEGACY_INSTALL_ROOT" ] && [ "$LEGACY_INSTALL_ROOT" != "/" ] && $SUDO test -d "$LEGACY_INSTALL_ROOT"; then
  echo "legacy install root remains: $LEGACY_INSTALL_ROOT"
  FAILED=1
fi
if [ "$FAILED" -ne 0 ]; then
  echo "Docker removal verification failed"
  exit 1
fi

echo "Docker deployed service removed: $VERSION"
