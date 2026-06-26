#!/usr/bin/env sh
set -eu

VERSION={{shq .Version}}
WORK_DIR={{shq .WorkDir}}
ARCHIVE={{shq .ArchivePath}}
INSTALL_ROOT={{shq .InstallRoot}}
DAEMON_DIR="$INSTALL_ROOT/daemon"
DAEMON_CONFIG="$DAEMON_DIR/daemon.json"
DATA_ROOT="$INSTALL_ROOT/data"
EXEC_ROOT="$INSTALL_ROOT/exec"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

echo "checking required commands"
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
        echo "warning: local RPM dependency installation failed; continuing with Docker static bundle"
        echo "warning: Docker daemon startup can still fail if the host misses required networking packages"
        cat "$RPM_LOG"
      fi
    fi
  else
    echo "rpm command not found, skip RPM dependency installation"
  fi
fi

echo "extracting Docker bundle"
rm -rf "$WORK_DIR/unpacked"
mkdir -p "$WORK_DIR/unpacked"
tar -xf "$ARCHIVE" -C "$WORK_DIR/unpacked"
tar -xzf "$WORK_DIR/unpacked/docker-$VERSION.tgz" -C "$WORK_DIR/unpacked"

echo "installing Docker binaries"
$SUDO mkdir -p "$INSTALL_ROOT" "$DAEMON_DIR" "$DATA_ROOT" "$EXEC_ROOT" /usr/local/bin /usr/local/lib/docker/cli-plugins /etc/systemd/system
$SUDO cp -f "$WORK_DIR/unpacked/docker/"* "$INSTALL_ROOT/"
for bin in containerd containerd-shim-runc-v2 ctr docker docker-init docker-proxy dockerd runc; do
  $SUDO install -m 0755 "$WORK_DIR/unpacked/docker/$bin" "/usr/local/bin/$bin"
done
$SUDO install -m 0755 "$WORK_DIR/unpacked/docker-compose-linux-x86_64" /usr/local/lib/docker/cli-plugins/docker-compose
$SUDO ln -sf /usr/local/lib/docker/cli-plugins/docker-compose /usr/local/bin/docker-compose

echo "writing Docker daemon configuration"
cat > "$WORK_DIR/daemon.json" <<JSON
{
  "exec-opts": ["native.cgroupdriver=systemd"],
  "data-root": "$DATA_ROOT",
  "exec-root": "$EXEC_ROOT",
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "100m",
    "max-file": "3"
  },
  "storage-driver": "overlay2"
}
JSON
$SUDO install -m 0644 "$WORK_DIR/daemon.json" "$DAEMON_CONFIG"
echo "Docker daemon config: $DAEMON_CONFIG"

echo "writing systemd units"
cat > "$WORK_DIR/containerd.service" <<'SERVICE'
[Unit]
Description=containerd container runtime
Documentation=https://containerd.io
After=network.target local-fs.target

[Service]
ExecStartPre=-/sbin/modprobe overlay
ExecStart=/usr/local/bin/containerd
Restart=always
RestartSec=5
Delegate=yes
KillMode=process
OOMScoreAdjust=-999
LimitNOFILE=1048576
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity

[Install]
WantedBy=multi-user.target
SERVICE

cat > "$WORK_DIR/docker.service" <<SERVICE
[Unit]
Description=Docker Application Container Engine
Documentation=https://docs.docker.com
After=network-online.target containerd.service
Wants=network-online.target
Requires=containerd.service

[Service]
Type=simple
ExecStart=/usr/local/bin/dockerd --config-file=$DAEMON_CONFIG --containerd=/run/containerd/containerd.sock
ExecReload=/bin/kill -s HUP \$MAINPID
TimeoutStartSec=0
Restart=always
RestartSec=2
StartLimitBurst=3
StartLimitInterval=60s
LimitNOFILE=infinity
LimitNPROC=infinity
LimitCORE=infinity
TasksMax=infinity
Delegate=yes
KillMode=process
OOMScoreAdjust=-500

[Install]
WantedBy=multi-user.target
SERVICE

$SUDO install -m 0644 "$WORK_DIR/containerd.service" /etc/systemd/system/containerd.service
$SUDO install -m 0644 "$WORK_DIR/docker.service" /etc/systemd/system/docker.service

echo "enabling and starting services"
$SUDO systemctl daemon-reload
if ! $SUDO systemctl enable --now containerd; then
  echo "containerd service failed to start"
  $SUDO systemctl --no-pager --full status containerd || true
  $SUDO journalctl -u containerd -n 80 --no-pager || true
  exit 1
fi
if ! $SUDO systemctl enable --now docker; then
  echo "Docker service failed to start"
  $SUDO systemctl --no-pager --full status docker || true
  $SUDO journalctl -u docker -n 80 --no-pager || true
  exit 1
fi

echo "verifying Docker CLI"
/usr/local/bin/docker --version
echo "waiting for Docker daemon"
DOCKER_READY=0
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
  if /usr/local/bin/docker info >/dev/null 2>&1; then
    DOCKER_READY=1
    break
  fi
  sleep 1
done
if [ "$DOCKER_READY" != "1" ]; then
  echo "Docker daemon is not reachable after installation"
  $SUDO systemctl --no-pager --full status docker || true
  $SUDO journalctl -u docker -n 80 --no-pager || true
  exit 1
fi
/usr/local/bin/docker version
echo "verifying Docker Compose"
/usr/local/bin/docker compose version || /usr/local/bin/docker-compose version
