#!/usr/bin/env sh
set -eu

UNIT_NAME="aifar-https-ingress.service"
UNIT_TARGET="/etc/systemd/system/$UNIT_NAME"

[ "$(id -u)" = "0" ] || { echo "run this script as root" >&2; exit 1; }

systemctl disable --now "$UNIT_NAME" 2>/dev/null || true
rm -f "$UNIT_TARGET"
systemctl daemon-reload
systemctl reset-failed "$UNIT_NAME" 2>/dev/null || true
echo "[aifar-https-ingress] systemd unit removed"
