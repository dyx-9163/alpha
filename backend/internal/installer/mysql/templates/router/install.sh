#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
WORK_DIR={{shq .WorkDir}}
ARCHIVE={{shq .ArchivePath}}
INSTALL_ROOT={{shq .InstallRoot}}
BASE_PORT={{.BasePort}}
BOOTSTRAP_HOST={{shq .BootstrapHost}}
BOOTSTRAP_PORT={{.BootstrapPort}}
BOOTSTRAP_USER={{shq .BootstrapUser}}
BOOTSTRAP_PASSWORD={{shq .BootstrapPassword}}
BIND_ADDRESS={{shq .BindAddress}}
ROUTER_USER="aifar-router"
SERVICE_NAME="aifar-mysql-router-$BASE_PORT"
ROUTER_BASE="$INSTALL_ROOT/mysql-router"
ROUTER_DIR="$INSTALL_ROOT/routers/$BASE_PORT"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "installing local RPM dependencies when available"
if [ -d "$WORK_DIR/rpms" ] && ls "$WORK_DIR"/rpms/*.rpm >/dev/null 2>&1; then
  if command -v rpm >/dev/null 2>&1; then
    RPM_LOG="$WORK_DIR/rpm-install.log"
    if $SUDO rpm -Uvh --replacepkgs "$WORK_DIR"/rpms/*.rpm >"$RPM_LOG" 2>&1; then
      cat "$RPM_LOG"
    else
      echo "warning: local RPM dependency installation reported issues; retrying with --nodeps"
      if $SUDO rpm -Uvh --replacepkgs --nodeps "$WORK_DIR"/rpms/*.rpm >"$RPM_LOG" 2>&1; then
        cat "$RPM_LOG"
      else
        echo "warning: local RPM dependency installation failed; MySQL Router may fail if required runtime libraries are missing"
        cat "$RPM_LOG"
      fi
    fi
  else
    echo "rpm command not found, skip RPM dependency installation"
  fi
fi

echo "checking MySQL Router install commands"
command -v tar >/dev/null 2>&1 || { echo "tar is required"; exit 1; }

echo "preparing MySQL Router install directories"
$SUDO mkdir -p "$ROUTER_BASE" "$INSTALL_ROOT/routers" /etc/systemd/system
rm -rf "$WORK_DIR/bundle" "$WORK_DIR/unpacked-router"
mkdir -p "$WORK_DIR/bundle" "$WORK_DIR/unpacked-router"

echo "extracting MySQL official bundle"
tar -xf "$ARCHIVE" -C "$WORK_DIR/bundle"
ROUTER_ARCHIVE="$(find "$WORK_DIR/bundle" -maxdepth 1 -type f \( -name 'mysql-router-*-linux*.tar.xz' -o -name 'mysql-router-*-linux*.tar.gz' \) | sort | head -n 1)"
if [ -z "$ROUTER_ARCHIVE" ] || [ ! -f "$ROUTER_ARCHIVE" ]; then
  echo "MySQL Router binary archive not found in bundle"
  exit 1
fi

echo "extracting MySQL Router archive: $ROUTER_ARCHIVE"
tar -xf "$ROUTER_ARCHIVE" -C "$WORK_DIR/unpacked-router"
ROUTER_SRC="$(find "$WORK_DIR/unpacked-router" -maxdepth 1 -type d -name 'mysql-router-*' | sort | head -n 1)"
if [ -z "$ROUTER_SRC" ] || [ ! -x "$ROUTER_SRC/bin/mysqlrouter" ]; then
  echo "mysqlrouter not found after extraction"
  exit 1
fi

echo "installing MySQL Router binary files"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO rm -rf "$ROUTER_BASE" "$ROUTER_DIR"
$SUDO mkdir -p "$ROUTER_BASE" "$ROUTER_DIR"
$SUDO cp -a "$ROUTER_SRC"/. "$ROUTER_BASE"/

echo "ensuring MySQL Router runtime user"
NOLOGIN="/sbin/nologin"
if [ -x "/usr/sbin/nologin" ]; then
  NOLOGIN="/usr/sbin/nologin"
fi
if ! id "$ROUTER_USER" >/dev/null 2>&1; then
  $SUDO useradd --system --home-dir "$INSTALL_ROOT" --shell "$NOLOGIN" "$ROUTER_USER"
fi

echo "bootstrapping MySQL Router from InnoDB Cluster"
BOOTSTRAP_URI="$BOOTSTRAP_USER@$BOOTSTRAP_HOST:$BOOTSTRAP_PORT"
if ! printf '%s\n' "$BOOTSTRAP_PASSWORD" | $SUDO "$ROUTER_BASE/bin/mysqlrouter" \
  --bootstrap "$BOOTSTRAP_URI" \
  --directory "$ROUTER_DIR" \
  --conf-base-port "$BASE_PORT" \
  --conf-bind-address "$BIND_ADDRESS" \
  --force \
  --user "$ROUTER_USER"; then
  echo "MySQL Router bootstrap failed"
  exit 1
fi

if [ ! -f "$ROUTER_DIR/mysqlrouter.conf" ]; then
  echo "MySQL Router configuration was not generated"
  exit 1
fi

$SUDO chown -R "$ROUTER_USER:$ROUTER_USER" "$INSTALL_ROOT"

echo "writing MySQL Router systemd unit"
cat > "$WORK_DIR/$SERVICE_NAME.service" <<SERVICE
[Unit]
Description=AIFAR MySQL Router on base port $BASE_PORT
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$ROUTER_USER
Group=$ROUTER_USER
WorkingDirectory=$ROUTER_DIR
Environment=LD_LIBRARY_PATH=$ROUTER_BASE/lib:$ROUTER_BASE/lib/private:$ROUTER_BASE/lib/mysqlrouter/private
ExecStart=$ROUTER_BASE/bin/mysqlrouter -c $ROUTER_DIR/mysqlrouter.conf
Restart=always
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE
$SUDO install -m 0644 "$WORK_DIR/$SERVICE_NAME.service" "/etc/systemd/system/$SERVICE_NAME.service"

echo "enabling and starting MySQL Router"
$SUDO systemctl daemon-reload
if ! $SUDO systemctl enable --now "$SERVICE_NAME"; then
  echo "MySQL Router service failed to start"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  exit 1
fi

if ! $SUDO systemctl is-active --quiet "$SERVICE_NAME"; then
  echo "MySQL Router service is not active"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  exit 1
fi

"$ROUTER_BASE/bin/mysqlrouter" --version
echo "MySQL Router service installed: $SERVICE_NAME"
echo "MySQL Router classic read-write endpoint: 0.0.0.0:$BASE_PORT"
