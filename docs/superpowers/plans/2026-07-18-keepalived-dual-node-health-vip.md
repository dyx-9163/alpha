# Keepalived Dual-Node Health VIP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy Keepalived 2.4.2 on `192.168.74.132` and `192.168.74.133` so VIP `192.168.74.130/24` follows each node's aggregate health, prefers 132 when both are healthy, fails over to 133, and automatically returns to 132 after recovery.

**Architecture:** Each node checks only its own `/health/aggregate` endpoint and participates in unicast VRRP. Both nodes start as `BACKUP`; priorities 150 and 100 select 132, while the default preemption behavior provides automatic failback. Activation uses a temporary hold marker on 133 so it can start in FAULT without briefly claiming the VIP before 132 has switched to unicast configuration.

**Tech Stack:** openEuler 24.03 LTS SP3, Keepalived 2.4.2, Bash, curl, systemd, iproute2, unicast VRRP

## Global Constraints

- Install root is `/aifar/apps/keepalived` on both nodes.
- Nodes are `192.168.74.132` and `192.168.74.133`; VIP is exactly `192.168.74.130/24`.
- Health requires HTTP 2xx and JSON boolean `"up": true` within 2 seconds.
- Check interval is 2 seconds, `fall 3`, `rise 2`, and weight 0.
- Priorities are 150 on 132 and 100 on 133; do not configure `nopreempt`.
- Both nodes use `virtual_router_id 130` and unicast VRRP to the other node.
- Never write SSH passwords, tokens, private keys, or full credentials into repository files or logs.
- Do not replace the working 132 configuration unless both staged configurations pass syntax validation and the 133 local health endpoint passes.
- Any persistent double-MASTER state or duplicate VIP is a failure requiring immediate rollback.

## Approved Execution Exception

The user explicitly approved continuing while the 133 health endpoint is unavailable. In this execution, 133 must remain FAULT without the VIP, 132 must remain healthy with the VIP, and failover/failback testing is deferred until `http://192.168.74.133:38000/health/aggregate` returns HTTP 2xx with JSON `up=true`. The health script and VRRP configuration are still installed now so 133 becomes eligible automatically after its business runtime recovers.

---

### Task 1: Authenticate and run two-node read-only preflight

**Files:**
- Read remotely on 132: `/aifar/apps/keepalived/etc/keepalived/keepalived.conf`
- Read remotely on 133: `/etc/os-release`, `/etc/openEuler-release`
- Create locally only for the session: a temporary SSH runner that pins each server host key and reads passwords from process environment

**Interfaces:**
- Consumes: root SSH access to 132 and 133
- Produces: verified host keys, platform/interface facts, health results, firewall state, and Keepalived state for both nodes

- [ ] **Step 1: Confirm 133 credentials without persisting them**

Use the user-provided 133 root credential only through a process environment variable. If no 133 credential has been provided, stop and request it; do not assume the 132 password is valid on 133.

Expected: authenticated SSH sessions to both addresses with separately pinned ED25519/SHA256 host keys.

- [ ] **Step 2: Verify platform, interface, VIP, service, and local health on 132**

Run on 132:

```bash
set -Eeuo pipefail
grep -qi 'openEuler' /etc/os-release
grep -qi 'SP3' /etc/openEuler-release
test "$(uname -m)" = x86_64
ip -4 -o address show | grep -F '192.168.74.132/24'
ip -4 -o address show | grep -F '192.168.74.130/24'
/aifar/apps/keepalived/sbin/keepalived --version 2>&1 | sed -n '1p'
systemctl show keepalived.service -p LoadState -p ActiveState -p UnitFileState -p FragmentPath --no-pager
body="$(curl --silent --show-error --fail --connect-timeout 1 --max-time 2 http://192.168.74.132:38000/health/aggregate)"
grep -Eq '"up"[[:space:]]*:[[:space:]]*true([,}])' <<<"$body"
printf '%s\n' "$body"
systemctl is-active firewalld.service 2>/dev/null || true
```

Expected: 132 is openEuler SP3 x86_64, the primary IP and VIP are on the same non-loopback interface, Keepalived 2.4.2 is active, and local health returns 2xx with `up=true`.

- [ ] **Step 3: Verify platform, interface, VIP, service, and local health on 133**

Run on 133:

```bash
set -Eeuo pipefail
grep -qi 'openEuler' /etc/os-release
grep -qi 'SP3' /etc/openEuler-release
test "$(uname -m)" = x86_64
ip -4 -o address show | grep -F '192.168.74.133/24'
if ip -4 -o address show | grep -Fq '192.168.74.130/24'; then
  echo 'VIP is unexpectedly present on 133' >&2
  exit 1
fi
body="$(curl --silent --show-error --fail --connect-timeout 1 --max-time 2 http://192.168.74.133:38000/health/aggregate)"
grep -Eq '"up"[[:space:]]*:[[:space:]]*true([,}])' <<<"$body"
printf '%s\n' "$body"
if test -x /aifar/apps/keepalived/sbin/keepalived; then
  /aifar/apps/keepalived/sbin/keepalived --version 2>&1 | sed -n '1p'
fi
systemctl show keepalived.service -p LoadState -p ActiveState -p UnitFileState -p FragmentPath --no-pager 2>/dev/null || true
systemctl is-active firewalld.service 2>/dev/null || true
```

Expected: 133 is openEuler SP3 x86_64, its local health returns 2xx with `up=true`, and it does not own the VIP. If local health fails, stop before modifying either node because 133 cannot take over safely.

### Task 2: Install and verify Keepalived on 133 when absent

**Files:**
- Upload temporarily to 133: `install-keepalived-offline.sh`
- Upload temporarily to 133: `keepalived-2.4.2.tar.gz`
- Create remotely: `/aifar/apps/keepalived/**`
- Create remotely: `/etc/systemd/system/keepalived.service` as a link to the custom install tree

**Interfaces:**
- Consumes: successful Task 1 preflight and the already verified local source archive
- Produces: Keepalived 2.4.2 installed on 133, linked but not yet enabled or started

- [ ] **Step 1: Skip installation only when the exact required version is present**

Run on 133:

```bash
if test -x /aifar/apps/keepalived/sbin/keepalived && \
   /aifar/apps/keepalived/sbin/keepalived --version 2>&1 | grep -Fq 'Keepalived v2.4.2'; then
  echo 'Keepalived 2.4.2 already installed'
  exit 0
fi
exit 10
```

Expected: exit 0 only for the exact custom installation; exit 10 means installation is required.

- [ ] **Step 2: Install from source when required**

Create the staging path with `stage="$(mktemp -d /tmp/aifar-keepalived-install.XXXXXX)"`, validate it against `/tmp/aifar-keepalived-install.[A-Za-z0-9]+`, upload the installer and archive to `$stage`, and verify byte size and SHA256. Then run:

```bash
cd "$stage"
bash ./install-keepalived-offline.sh
```

Expected: the installer exits 0, the binary reports 2.4.2, dynamic libraries contain no `not found`, and the service remains inactive without an active `keepalived.conf`.

- [ ] **Step 3: Remove the validated temporary install directory**

Resolve the exact created directory, require it to match `/tmp/aifar-keepalived-install.[A-Za-z0-9]+`, remove only that directory, and confirm it is absent.

Expected: source/upload staging is removed; `/aifar/apps/keepalived` remains.

### Task 3: Stage health scripts and unicast configurations on both nodes

**Files:**
- Create on both nodes: `/aifar/apps/keepalived/scripts/check-aggregate-health.sh`
- Stage on both nodes: `/tmp/aifar-keepalived-dual/keepalived.conf`
- Preserve on both nodes when present: `/aifar/apps/keepalived/etc/keepalived/keepalived.conf.aifar-backup-*`

**Interfaces:**
- Consumes: installed Keepalived and passing local health on both nodes
- Produces: root-owned health scripts and two syntax-validated staged configurations

- [ ] **Step 1: Create the exact 132 health script**

```bash
#!/usr/bin/env bash
set -Eeuo pipefail
test ! -e /run/aifar-keepalived-hold
body="$(curl --silent --show-error --fail --connect-timeout 1 --max-time 2 http://192.168.74.132:38000/health/aggregate)"
grep -Eq '"up"[[:space:]]*:[[:space:]]*true([,}])' <<<"$body"
```

Install as root:root mode 0750, then execute it directly and require exit 0.

- [ ] **Step 2: Create the exact 133 health script**

```bash
#!/usr/bin/env bash
set -Eeuo pipefail
test ! -e /run/aifar-keepalived-hold
body="$(curl --silent --show-error --fail --connect-timeout 1 --max-time 2 http://192.168.74.133:38000/health/aggregate)"
grep -Eq '"up"[[:space:]]*:[[:space:]]*true([,}])' <<<"$body"
```

Install as root:root mode 0750, then execute it directly and require exit 0.

- [ ] **Step 3: Stage the exact 132 configuration**

Run the following so the interface holding `192.168.74.132/24` is discovered and substituted consistently:

```bash
iface="$(ip -4 -o address show | awk '$4 == "192.168.74.132/24" {print $2; exit}')"
test -n "$iface"
cat >/tmp/aifar-keepalived-dual/keepalived.conf <<EOF
global_defs {
    router_id AIFAR_132
    enable_script_security
    script_user root
}

vrrp_script chk_aggregate {
    script "/aifar/apps/keepalived/scripts/check-aggregate-health.sh"
    interval 2
    timeout 3
    fall 3
    rise 2
    weight 0
    user root
}

vrrp_instance VI_AIFAR_130 {
    state BACKUP
    interface $iface
    virtual_router_id 130
    priority 150
    advert_int 1
    unicast_src_ip 192.168.74.132
    unicast_peer {
        192.168.74.133
    }
    virtual_ipaddress {
        192.168.74.130/24 dev $iface label $iface:vip
    }
    track_script {
        chk_aggregate
    }
}
EOF
chown root:root /tmp/aifar-keepalived-dual/keepalived.conf
chmod 600 /tmp/aifar-keepalived-dual/keepalived.conf
```

Expected: staged file is root:root mode 0600 and references the actual 132 interface.

- [ ] **Step 4: Stage the exact 133 configuration**

Run the following so the interface holding `192.168.74.133/24` is discovered and substituted consistently:

```bash
iface="$(ip -4 -o address show | awk '$4 == "192.168.74.133/24" {print $2; exit}')"
test -n "$iface"
cat >/tmp/aifar-keepalived-dual/keepalived.conf <<EOF
global_defs {
    router_id AIFAR_133
    enable_script_security
    script_user root
}

vrrp_script chk_aggregate {
    script "/aifar/apps/keepalived/scripts/check-aggregate-health.sh"
    interval 2
    timeout 3
    fall 3
    rise 2
    weight 0
    user root
}

vrrp_instance VI_AIFAR_130 {
    state BACKUP
    interface $iface
    virtual_router_id 130
    priority 100
    advert_int 1
    unicast_src_ip 192.168.74.133
    unicast_peer {
        192.168.74.132
    }
    virtual_ipaddress {
        192.168.74.130/24 dev $iface label $iface:vip
    }
    track_script {
        chk_aggregate
    }
}
EOF
chown root:root /tmp/aifar-keepalived-dual/keepalived.conf
chmod 600 /tmp/aifar-keepalived-dual/keepalived.conf
```

Expected: staged file is root:root mode 0600 and references the actual 133 interface.

- [ ] **Step 5: Validate both staged configurations before installing either**

Run on each node:

```bash
/aifar/apps/keepalived/sbin/keepalived -t -f /tmp/aifar-keepalived-dual/keepalived.conf
```

Expected: both commands exit 0 without script-security or configuration warnings. A failure leaves 132's active single-node configuration untouched and 133 inactive.

- [ ] **Step 6: Allow source-scoped unicast VRRP when firewalld is active**

If `systemctl is-active --quiet firewalld.service` succeeds, add only these peer-scoped rules and reload firewalld:

On 132:

```bash
firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.74.133/32" protocol value="vrrp" accept'
firewall-cmd --reload
```

On 133:

```bash
firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.74.132/32" protocol value="vrrp" accept'
firewall-cmd --reload
```

Record whether each rule already existed. Expected: existing rules remain unchanged; newly added rules are removed during rollback if dual-node activation fails.

### Task 4: Activate safely, verify failover, and clean up

**Files:**
- Modify on both nodes: `/aifar/apps/keepalived/etc/keepalived/keepalived.conf`
- Create temporarily on 133: `/run/aifar-keepalived-hold`
- Remove after success on both nodes: `/tmp/aifar-keepalived-dual`

**Interfaces:**
- Consumes: two validated configurations and passing health scripts
- Produces: active dual-node VRRP, verified failover/failback, and no temporary staging

- [ ] **Step 1: Back up current files and service states on both nodes**

For each node, record `systemctl is-active`, `systemctl is-enabled`, configuration existence, configuration SHA256, and a timestamped backup path. Copy an existing configuration with `cp -a` before replacement.

Expected: rollback data exists independently on each node and contains no credentials.

- [ ] **Step 2: Start 133 in a forced-FAULT hold state**

Run on 133:

```bash
touch /run/aifar-keepalived-hold
install -o root -g root -m 600 /tmp/aifar-keepalived-dual/keepalived.conf /aifar/apps/keepalived/etc/keepalived/keepalived.conf
systemctl enable keepalived.service
systemctl restart keepalived.service
sleep 7
systemctl is-active keepalived.service
if ip -4 -o address show | grep -Fq '192.168.74.130/24'; then
  echo '133 claimed VIP while hold was active' >&2
  exit 1
fi
```

Expected: 133 service is active but its script is down and it does not claim the VIP.

- [ ] **Step 3: Switch 132 to the validated unicast configuration**

Run on 132:

```bash
install -o root -g root -m 600 /tmp/aifar-keepalived-dual/keepalived.conf /aifar/apps/keepalived/etc/keepalived/keepalived.conf
systemctl restart keepalived.service
for attempt in $(seq 1 15); do
  systemctl is-active --quiet keepalived.service && \
    ip -4 -o address show | grep -Fq '192.168.74.130/24' && exit 0
  sleep 1
done
exit 1
```

Expected: 132 remains active and uniquely owns the VIP.

- [ ] **Step 4: Release the 133 hold and verify steady-state election**

Run on 133:

```bash
rm -f /run/aifar-keepalived-hold
for attempt in $(seq 1 15); do
  /aifar/apps/keepalived/scripts/check-aggregate-health.sh && break
  sleep 1
done
systemctl is-active keepalived.service
sleep 5
if ip -4 -o address show | grep -Fq '192.168.74.130/24'; then
  echo '133 owns VIP while healthy 132 has higher priority' >&2
  exit 1
fi
```

Expected: 133 is healthy and remains BACKUP; 132 alone owns the VIP. Logs on both nodes show unicast advertisements without persistent double-MASTER.

- [ ] **Step 5: Test failover by stopping only 132 Keepalived**

Run on 132:

```bash
systemctl stop keepalived.service
```

Poll both nodes for up to 15 seconds. Expected: VIP disappears from 132 and appears exactly once on 133. From the current Windows host, `ping.exe -n 2 -w 2000 192.168.74.130` succeeds.

- [ ] **Step 6: Test automatic failback by starting 132 Keepalived**

Run on 132:

```bash
systemctl start keepalived.service
```

Poll both nodes for up to 20 seconds. Expected: after 132's health script reaches `rise 2`, VIP appears exactly once on 132 and disappears from 133. Both services remain enabled and active, and Windows ping succeeds.

- [ ] **Step 7: Run fresh final verification on both nodes**

On each node run configuration parsing, health script execution, `systemctl is-enabled`, `systemctl is-active`, process listing, exact VIP count, and the last 60 Keepalived journal lines. Require no configuration errors, script-security errors, or persistent MASTER state on 133 while 132 is healthy.

Expected: 132 is MASTER with the VIP; 133 is BACKUP without the VIP; both health scripts exit 0.

- [ ] **Step 8: Roll back immediately on activation failure**

If any activation or election check fails: stop 133, restore 133's original configuration/service state, restore 132's original single-node configuration, start 132, verify 132 alone owns the VIP, and remove `/run/aifar-keepalived-hold`. Do not leave both nodes running configurations that have not passed the election check.

- [ ] **Step 9: Remove temporary files after success**

On each node validate that the resolved staging path is exactly `/tmp/aifar-keepalived-dual`, remove only that path, verify it is absent, and retain timestamped configuration backups. Remove local temporary SSH runners after checking that their paths remain within the workspace.

Expected: no staging or credential material remains; active configurations, scripts, and recoverable backups remain.
