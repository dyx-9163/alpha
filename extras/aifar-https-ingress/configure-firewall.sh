#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
CONFIG_FILE="$ROOT/config.env"

fail() {
  echo "[aifar-https-ingress] $*" >&2
  exit 1
}

[ "$(id -u)" = "0" ] || fail "run this script as root"
[ -f "$CONFIG_FILE" ] || fail "missing config: $CONFIG_FILE"
# This is a trusted local module configuration file, not user request input.
# shellcheck disable=SC1090
. "$CONFIG_FILE"

case "${AIFAR_HTTPS_CONFIGURE_FIREWALL:-1}" in
  0|false|FALSE|no|NO)
    echo "[aifar-https-ingress] automatic firewalld configuration is disabled"
    exit 0
    ;;
esac

if ! command -v firewall-cmd >/dev/null 2>&1; then
  echo "[aifar-https-ingress] firewall-cmd is unavailable; skipping firewalld configuration"
  exit 0
fi
if ! firewall-cmd --state >/dev/null 2>&1; then
  echo "[aifar-https-ingress] firewalld is not running; skipping firewalld configuration"
  exit 0
fi

zone="${AIFAR_HTTPS_FIREWALL_ZONE:-}"
if [ -z "$zone" ]; then
  interface=""
  if command -v ip >/dev/null 2>&1; then
    interface="$(ip route show default | awk 'NR == 1 { for (i = 1; i <= NF; i++) if ($i == "dev") { print $(i + 1); exit } }')"
  fi
  if [ -n "$interface" ]; then
    zone="$(firewall-cmd --get-zone-of-interface="$interface" 2>/dev/null || true)"
    [ "$zone" != "no zone" ] || zone=""
  fi
fi
if [ -z "$zone" ]; then
  zone="$(firewall-cmd --get-default-zone)"
fi
[ -n "$zone" ] || fail "cannot determine the firewalld zone"

for service in http https; do
  if ! firewall-cmd --quiet --zone="$zone" --query-service="$service"; then
    firewall-cmd --quiet --zone="$zone" --add-service="$service"
  fi
  if ! firewall-cmd --quiet --permanent --zone="$zone" --query-service="$service"; then
    firewall-cmd --quiet --permanent --zone="$zone" --add-service="$service"
  fi
done

echo "[aifar-https-ingress] firewalld permits HTTP/HTTPS in zone: $zone"
