# Keepalived Single-Node VIP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Configure Keepalived 2.4.2 on `192.168.74.132` to manage VIP `192.168.74.130/24` on `ens160`, then enable and verify the service.

**Architecture:** Keepalived runs as a single `MASTER` VRRP instance and tracks only `ens160`. A transactional remote shell script performs conflict checks, validates a staged configuration, installs it, starts the service, verifies the VIP, and restores the previous state on failure.

**Tech Stack:** openEuler 24.03 LTS SP3, Keepalived 2.4.2, systemd, iproute2, Bash, SSH

## Global Constraints

- Install root remains `/aifar/apps/keepalived`.
- Target server is `192.168.74.132`; target interface is `ens160`.
- VIP is exactly `192.168.74.130/24`.
- This phase tracks interface state only and does not add HTTP or TCP health checks.
- No second `BACKUP` node exists; this configuration cannot provide server-level failover.
- Do not start Keepalived unless configuration validation succeeds.
- On any failed activation or VIP verification, stop Keepalived and restore the previous configuration and enablement state.

---

### Task 1: Apply and verify the single-node VIP configuration

**Files:**
- Create or modify remotely: `/aifar/apps/keepalived/etc/keepalived/keepalived.conf`
- Preserve remotely when present: `/aifar/apps/keepalived/etc/keepalived/keepalived.conf.aifar-backup-*`
- Test remotely: `/aifar/apps/keepalived/sbin/keepalived`, `keepalived.service`, and `ens160`

**Interfaces:**
- Consumes: installed Keepalived binary, linked systemd unit, interface `ens160`, VIP `192.168.74.130/24`
- Produces: an enabled and active `keepalived.service` with VIP `192.168.74.130/24` assigned to `ens160`

- [ ] **Step 1: Run read-only preflight checks**

Run over SSH:

```bash
set -Eeuo pipefail
binary=/aifar/apps/keepalived/sbin/keepalived
config=/aifar/apps/keepalived/etc/keepalived/keepalived.conf
test -x "$binary"
ip link show dev ens160
ip -4 address show dev ens160
ip route get 192.168.74.130
if ip -4 address show | grep -Fq '192.168.74.130/'; then
  echo 'VIP is already configured on this server' >&2
  exit 1
fi
if command -v arping >/dev/null 2>&1; then
  arping -D -I ens160 -c 3 -w 5 192.168.74.130
else
  ping -c 2 -W 1 192.168.74.130 >/dev/null 2>&1 && {
    echo 'VIP responded to ping; refusing to continue' >&2
    exit 1
  }
fi
systemctl show keepalived.service -p LoadState -p ActiveState -p UnitFileState -p FragmentPath --no-pager
```

Expected: the binary and `ens160` exist; the route resolves through `ens160`; VIP is not locally assigned; ARP duplicate detection reports no owner when `arping` is available; the unit is loaded but inactive.

- [ ] **Step 2: Stage the exact Keepalived configuration**

Create a root-only temporary file containing:

```conf
global_defs {
    router_id AIFAR_132
}

vrrp_instance VI_AIFAR_130 {
    state MASTER
    interface ens160
    virtual_router_id 130
    priority 150
    advert_int 1

    track_interface {
        ens160
    }

    virtual_ipaddress {
        192.168.74.130/24 dev ens160 label ens160:vip
    }
}
```

Expected: the staged file is mode `0600` and is not yet the active configuration.

- [ ] **Step 3: Validate the staged configuration before changing service state**

Run:

```bash
/aifar/apps/keepalived/sbin/keepalived \
  -t \
  -f /tmp/aifar-keepalived-vip/keepalived.conf
```

Expected: exit code `0` with no configuration errors. A non-zero result stops the task without modifying the active configuration.

- [ ] **Step 4: Back up the active configuration and install the staged file**

Run:

```bash
set -Eeuo pipefail
config=/aifar/apps/keepalived/etc/keepalived/keepalived.conf
backup="${config}.aifar-backup-$(date +%Y%m%d%H%M%S)"
if test -e "$config"; then
  cp -a -- "$config" "$backup"
  printf '%s\n' "$backup" >/tmp/aifar-keepalived-vip/backup-path
else
  : >/tmp/aifar-keepalived-vip/created-new-config
fi
install -o root -g root -m 600 \
  /tmp/aifar-keepalived-vip/keepalived.conf \
  "$config"
```

Expected: the active configuration matches the validated staged file; an existing file is retained with a timestamped backup name.

- [ ] **Step 5: Enable and start Keepalived, with rollback on failure**

Run:

```bash
set -Eeuo pipefail
systemctl enable keepalived.service
systemctl restart keepalived.service
for attempt in $(seq 1 15); do
  systemctl is-active --quiet keepalived.service && \
    ip -4 address show dev ens160 | grep -Fq '192.168.74.130/24' && exit 0
  sleep 1
done
systemctl status keepalived.service --no-pager -l || true
journalctl -u keepalived.service -n 80 --no-pager || true
exit 1
```

Expected: service becomes active and the VIP appears within 15 seconds.

If the command fails, run immediately:

```bash
set -Eeuo pipefail
config=/aifar/apps/keepalived/etc/keepalived/keepalived.conf
systemctl disable --now keepalived.service || true
if test -s /tmp/aifar-keepalived-vip/backup-path; then
  backup="$(cat /tmp/aifar-keepalived-vip/backup-path)"
  cp -a -- "$backup" "$config"
elif test -e /tmp/aifar-keepalived-vip/created-new-config; then
  rm -f -- "$config"
fi
if ! systemctl show keepalived.service -p LoadState --value 2>/dev/null | grep -Fxq loaded; then
  systemctl link /aifar/apps/keepalived/systemd/keepalived.service
fi
systemctl daemon-reload
```

Expected: Keepalived is inactive and the original configuration state is restored.

- [ ] **Step 6: Run fresh post-install verification**

Run:

```bash
set -Eeuo pipefail
binary=/aifar/apps/keepalived/sbin/keepalived
config=/aifar/apps/keepalived/etc/keepalived/keepalived.conf
"$binary" -t -f "$config"
"$binary" --version 2>&1 | head -1
systemctl is-enabled keepalived.service
systemctl is-active keepalived.service
systemctl show keepalived.service -p ActiveState -p SubState -p UnitFileState -p FragmentPath --no-pager
ip -4 address show dev ens160 | grep -F '192.168.74.130/24'
ping -I 192.168.74.130 -c 2 -W 1 192.168.74.130
journalctl -u keepalived.service -n 30 --no-pager
```

Expected: configuration parsing succeeds; version begins with `Keepalived v2.4.2`; service is `enabled` and `active`; VIP is present on `ens160`; the local VIP ping succeeds; recent logs contain no startup failure.

- [ ] **Step 7: Remove only the temporary staging directory**

Run after validating the absolute path:

```bash
test "$(readlink -f /tmp/aifar-keepalived-vip)" = /tmp/aifar-keepalived-vip
rm -rf -- /tmp/aifar-keepalived-vip
test ! -e /tmp/aifar-keepalived-vip
```

Expected: temporary files are absent; the active configuration and any timestamped backup remain in `/aifar/apps/keepalived/etc/keepalived`.
