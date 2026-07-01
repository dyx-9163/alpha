#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
WORK_DIR={{shq .WorkDir}}
ARCHIVE={{shq .ArchivePath}}
GO_ARCHIVE={{shq .GoArchivePath}}
GOMODCACHE_ARCHIVE={{shq .GoModCachePath}}
MC_BINARY={{shq .MCRemotePath}}
INSTALL_ROOT={{shq .InstallRoot}}
DATA_DIR={{shq .DataDir}}
MINIO_VOLUME_LIST={{shq .VolumeList}}
API_PORT={{.APIPort}}
CONSOLE_PORT={{.ConsolePort}}
ROOT_USER={{shq .RootUser}}
ROOT_PASSWORD={{shq .RootPassword}}
SERVICE_NAME="aifar-minio"
LEGACY_SERVICE_NAME="aifar-minio-$API_PORT"
CONFIG_DIR="$INSTALL_ROOT/conf"
LOG_DIR="$INSTALL_ROOT/logs"
RUN_DIR="$INSTALL_ROOT/run"
ENV_FILE="$CONFIG_DIR/minio.env"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

{{ serviceAccessHelpers }}

echo "checking MinIO build commands"
command -v tar >/dev/null 2>&1 || { echo "tar is required"; exit 1; }

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
        echo "warning: local RPM dependency installation failed; continuing if Go build tools are already present"
        cat "$RPM_LOG"
      fi
    fi
  else
    echo "rpm command not found, skip RPM dependency installation"
  fi
fi

echo "preparing MinIO install directories"
$SUDO systemctl disable --now "$SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO systemctl disable --now "$LEGACY_SERVICE_NAME" >/dev/null 2>&1 || true
$SUDO rm -f "/etc/systemd/system/$LEGACY_SERVICE_NAME.service"
$SUDO mkdir -p "$INSTALL_ROOT/bin" "$CONFIG_DIR" "$LOG_DIR" "$RUN_DIR" /etc/systemd/system
{{range .DataDirs}}
$SUDO mkdir -p {{shq .}}
{{end}}
rm -rf "$WORK_DIR/toolchain" "$WORK_DIR/gomodcache" "$WORK_DIR/unpacked" "$WORK_DIR/gopath"
mkdir -p "$WORK_DIR/toolchain" "$WORK_DIR/gomodcache" "$WORK_DIR/unpacked" "$WORK_DIR/gopath"

echo "extracting Go toolchain"
tar -xzf "$GO_ARCHIVE" -C "$WORK_DIR/toolchain"
if [ ! -x "$WORK_DIR/toolchain/go/bin/go" ]; then
  echo "Go toolchain not found after extraction"
  exit 1
fi

echo "extracting Go module cache"
tar -xzf "$GOMODCACHE_ARCHIVE" -C "$WORK_DIR/gomodcache"

echo "extracting MinIO source"
tar -xzf "$ARCHIVE" -C "$WORK_DIR/unpacked"
SRC_DIR="$(find "$WORK_DIR/unpacked" -maxdepth 1 -type d -name 'minio-*' | head -n 1)"
if [ -z "$SRC_DIR" ] || [ ! -d "$SRC_DIR" ]; then
  echo "MinIO source directory not found"
  exit 1
fi

export GOROOT="$WORK_DIR/toolchain/go"
export GOPATH="$WORK_DIR/gopath"
export GOMODCACHE="$WORK_DIR/gomodcache"
export PATH="$GOROOT/bin:$GOPATH/bin:$PATH"
export GOPROXY=off
export GOSUMDB=off
export CGO_ENABLED=0
export GOOS=linux
export GOARCH=amd64

echo "building MinIO from source"
cd "$SRC_DIR"
LDFLAGS="$(go run buildscripts/gen-ldflags.go 2>/dev/null || true)"
if command -v make >/dev/null 2>&1 && make build; then
  echo "MinIO built with make"
else
  echo "make build unavailable or failed, using direct go build"
  go build -tags kqueue -trimpath --ldflags "$LDFLAGS" -o "$SRC_DIR/minio"
fi
if [ ! -x "$SRC_DIR/minio" ]; then
  echo "MinIO binary was not produced"
  exit 1
fi
$SUDO install -m 0755 "$SRC_DIR/minio" "$INSTALL_ROOT/bin/minio"

if [ -n "$MC_BINARY" ] && [ -f "$MC_BINARY" ]; then
  echo "installing MinIO client"
  $SUDO install -m 0755 "$MC_BINARY" "$INSTALL_ROOT/bin/mc"
else
  echo "MinIO client package not found, skip mc installation"
fi

echo "writing MinIO environment"
cat > "$WORK_DIR/minio.env" <<CONF
MINIO_ROOT_USER="$ROOT_USER"
MINIO_ROOT_PASSWORD="$ROOT_PASSWORD"
MINIO_VOLUMES="$MINIO_VOLUME_LIST"
MINIO_OPTS="--address :$API_PORT --console-address :$CONSOLE_PORT"
{{if .ReplicationPriority}}MINIO_API_REPLICATION_PRIORITY={{shq .ReplicationPriority}}{{end}}
{{if gt .ReplicationMaxWorkers 0}}MINIO_API_REPLICATION_MAX_WORKERS={{.ReplicationMaxWorkers}}{{end}}
{{if gt .ReplicationMaxLargeWorkers 0}}MINIO_API_REPLICATION_MAX_LRG_WORKERS={{.ReplicationMaxLargeWorkers}}{{end}}
CONF
$SUDO install -m 0600 "$WORK_DIR/minio.env" "$ENV_FILE"

echo "writing MinIO systemd unit"
cat > "$WORK_DIR/$SERVICE_NAME.service" <<SERVICE
[Unit]
Description=AIFAR MinIO service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$INSTALL_ROOT
EnvironmentFile=$ENV_FILE
ExecStart=$INSTALL_ROOT/bin/minio server \$MINIO_OPTS \$MINIO_VOLUMES
Restart=always
RestartSec=2
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE
$SUDO install -m 0644 "$WORK_DIR/$SERVICE_NAME.service" "/etc/systemd/system/$SERVICE_NAME.service"

echo "enabling and starting MinIO"
$SUDO systemctl daemon-reload
if ! $SUDO systemctl enable --now "$SERVICE_NAME"; then
  echo "MinIO service failed to start"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  exit 1
fi

echo "verifying MinIO service"
MINIO_READY=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  if command -v curl >/dev/null 2>&1; then
    if curl -fsS "http://127.0.0.1:$API_PORT/minio/health/live" >/dev/null 2>&1; then
      MINIO_READY=1
      break
    fi
  elif command -v wget >/dev/null 2>&1; then
    if wget -qO- "http://127.0.0.1:$API_PORT/minio/health/live" >/dev/null 2>&1; then
      MINIO_READY=1
      break
    fi
  elif $SUDO systemctl is-active --quiet "$SERVICE_NAME"; then
    MINIO_READY=1
    break
  fi
  sleep 1
done
if [ "$MINIO_READY" != "1" ]; then
  echo "MinIO is not reachable after installation"
  $SUDO systemctl --no-pager --full status "$SERVICE_NAME" || true
  $SUDO journalctl -u "$SERVICE_NAME" -n 120 --no-pager || true
  exit 1
fi

"$INSTALL_ROOT/bin/minio" --version
open_firewall_ports "$API_PORT" "$CONSOLE_PORT"
allow_selinux_ports http_port_t "$API_PORT" "$CONSOLE_PORT"
echo "MinIO standalone service installed: $SERVICE_NAME"
echo "MinIO API endpoint: http://127.0.0.1:$API_PORT"
