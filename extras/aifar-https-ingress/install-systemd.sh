#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)"
UNIT_NAME="aifar-https-ingress.service"
UNIT_TARGET="/etc/systemd/system/$UNIT_NAME"

[ "$(id -u)" = "0" ] || { echo "run this script as root" >&2; exit 1; }
case "$ROOT" in
  *[[:space:]\|]* ) echo "module path must not contain whitespace or |: $ROOT" >&2; exit 1 ;;
esac

chmod 0755 "$ROOT"/*.sh
"$ROOT/configure-firewall.sh"
escaped_root="$(printf '%s' "$ROOT" | sed 's/[&|]/\\&/g')"
sed "s|@@MODULE_DIR@@|$escaped_root|g" "$ROOT/$UNIT_NAME" > "$UNIT_TARGET"
chmod 0644 "$UNIT_TARGET"

systemctl daemon-reload
systemctl enable --now "$UNIT_NAME"
systemctl --no-pager --full status "$UNIT_NAME"
