# Keepalived Managed Configuration and Startup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the existing openEuler Keepalived 2.4.2 offline installer consume a strict node configuration, render and install production VRRP health-failover configuration, manage the peer-only VRRP firewall rule, and leave the service enabled and active with rollback-safe repeat installation.

**Architecture:** Keep the existing zero-argument `install-keepalived-offline.sh` as the sole entry point. Add data-only configuration, a render template, and a separately testable health script; expose guarded Bash functions so Node tests can source them under Git Bash with fake `ip`, `systemctl`, `firewall-cmd`, and `curl` commands. Preserve the current backup-first and ownership-proof safety model while extending it to installation rollback and exact firewall ownership.

**Tech Stack:** Bash 4+, Keepalived 2.4.2, systemd, firewalld rich rules, SELinux tools, curl, Python 3 JSON parser, Node.js `node:test`, AIFAR release packaging scripts.

## Global Constraints

- Support only openEuler 24.03 LTS SP3 x86_64.
- Install only under `/aifar/apps/keepalived`; backups remain below `/aifar/backups`.
- Use only the DNF repositories already configured on the target host; never add a public repository.
- Keep `install-keepalived-offline.sh`, `configure-selinux.sh`, and `uninstall-keepalived.sh` zero-argument commands.
- Parse `keepalived.env` as data with a fixed allowlist; never source or evaluate it.
- Require local IP, peer IP, VIP/CIDR, interface, priority, virtual router ID, and local health URL.
- Both nodes render `state BACKUP`, unicast VRRP, `weight 0`, and normal preemption; do not render `nopreempt`.
- Fix health timing at interval 2, timeout 3, fall 3, and rise 2.
- Treat an unhealthy application as a warning after service startup: `keepalived.service` stays active while the VRRP instance remains `FAULT`.
- When firewalld is active, allow protocol 112 only from the configured peer `/32`; never claim a pre-existing exact rule.
- Preserve SELinux mode and distribution-derived labels; never invoke `setenforce`, edit `/etc/selinux/config`, or generate `audit2allow` policy.
- A repeated install must back up the previous installation and restore files, exact firewall state, and original active/enabled state on failure.
- The Linux release contains the whole module and executable scripts with mode 0755; the Windows release excludes it.
- Do not change the pinned source archive: size `6350291`, SHA256 `76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b`.

## File Map

- Create `extras/keepalived/keepalived.env.example`: documented node-local input contract.
- Create `extras/keepalived/keepalived.conf.tpl`: deterministic production VRRP template containing only validated placeholders.
- Create `extras/keepalived/check-aggregate-health.sh`: root-run, zero-argument runtime health probe with a source guard for tests.
- Modify `extras/keepalived/install-keepalived-offline.sh`: strict parsing, validation, rendering, install transaction, firewall reconciliation, service activation, and rollback.
- Modify `extras/keepalived/configure-selinux.sh`: literal fcontext lookup and managed `libexec` labeling.
- Modify `extras/keepalived/uninstall-keepalived.sh`: literal fcontext lookup, backup of new state, and exact owned-firewall removal.
- Modify `extras/keepalived/README.md`: preparation, two-node example, automatic startup, failover, recovery, operations, and uninstall behavior.
- Modify `scripts/keepalived-extra.test.mjs`: repository/static contract tests and removal of the obsolete “never activates” assertion.
- Create `scripts/keepalived-runtime.test.mjs`: Git Bash function-level tests with fake host commands.
- Modify `scripts/package-release.mjs`: mark the health probe executable in directory copies and normalized tar headers.
- Modify `scripts/release-pipeline.test.mjs`: assert new files, Linux executable mode, checksums, and Windows exclusion.
- Keep `extras/keepalived/SHA256SUMS` unchanged because it intentionally pins only the unchanged upstream source archive; release-level `checksums.txt` covers every new module file.

---

### Task 1: Add the Generic Node Contract and VRRP Template

**Files:**
- Create: `extras/keepalived/keepalived.env.example`
- Create: `extras/keepalived/keepalived.conf.tpl`
- Modify: `scripts/keepalived-extra.test.mjs:20-78`

**Interfaces:**
- Consumes: none.
- Produces: exact environment keys `KEEPALIVED_LOCAL_IP`, `KEEPALIVED_PEER_IP`, `KEEPALIVED_VIP_CIDR`, `KEEPALIVED_INTERFACE`, `KEEPALIVED_PRIORITY`, `KEEPALIVED_VIRTUAL_ROUTER_ID`, and `KEEPALIVED_HEALTH_URL`; template placeholders consumed by Task 3.

- [ ] **Step 1: Extend the static artifact test and add exact template assertions**

Add the two new filenames to both artifact lists in `scripts/keepalived-extra.test.mjs`, then add:

```js
test('generic node example exposes exactly the supported Keepalived keys', () => {
  const example = read('keepalived.env.example')
  const keys = [...example.matchAll(/^([A-Z][A-Z0-9_]*)=/gm)].map((match) => match[1])
  assert.deepEqual(keys, [
    'KEEPALIVED_LOCAL_IP',
    'KEEPALIVED_PEER_IP',
    'KEEPALIVED_VIP_CIDR',
    'KEEPALIVED_INTERFACE',
    'KEEPALIVED_PRIORITY',
    'KEEPALIVED_VIRTUAL_ROUTER_ID',
    'KEEPALIVED_HEALTH_URL'
  ])
})

test('production template uses BACKUP unicast health-fault and preemption defaults', () => {
  const template = read('keepalived.conf.tpl')
  for (const placeholder of [
    '@ROUTER_ID@', '@INTERFACE@', '@VIRTUAL_ROUTER_ID@', '@PRIORITY@',
    '@LOCAL_IP@', '@PEER_IP@', '@VIP_CIDR@'
  ]) assert.match(template, new RegExp(placeholder))
  assert.match(template, /state BACKUP/)
  assert.match(template, /interval 2/)
  assert.match(template, /timeout 3/)
  assert.match(template, /fall 3/)
  assert.match(template, /rise 2/)
  assert.match(template, /weight 0/)
  assert.match(template, /unicast_src_ip @LOCAL_IP@/)
  assert.doesNotMatch(template, /nopreempt/)
})
```

- [ ] **Step 2: Run the focused test and verify the missing files fail it**

Run: `node --test scripts/keepalived-extra.test.mjs`

Expected: FAIL with `ENOENT` for `keepalived.env.example` or `keepalived.conf.tpl`.

- [ ] **Step 3: Add the exact generic node example**

Create `extras/keepalived/keepalived.env.example`:

```dotenv
# Copy this file to keepalived.env beside install-keepalived-offline.sh.
# Configure different LOCAL_IP, PEER_IP, PRIORITY and HEALTH_URL values on each node.
KEEPALIVED_LOCAL_IP=192.168.74.132
KEEPALIVED_PEER_IP=192.168.74.133
KEEPALIVED_VIP_CIDR=192.168.74.130/24
KEEPALIVED_INTERFACE=ens160
KEEPALIVED_PRIORITY=150
KEEPALIVED_VIRTUAL_ROUTER_ID=130
KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate
```

- [ ] **Step 4: Add the exact production template**

Create `extras/keepalived/keepalived.conf.tpl`:

```conf
global_defs {
    router_id @ROUTER_ID@
    script_user root
    enable_script_security
}

vrrp_script check_aifar_health {
    script "/aifar/apps/keepalived/libexec/check-aggregate-health.sh"
    interval 2
    timeout 3
    fall 3
    rise 2
    weight 0
}

vrrp_instance AIFAR_VI {
    state BACKUP
    interface @INTERFACE@
    virtual_router_id @VIRTUAL_ROUTER_ID@
    priority @PRIORITY@
    advert_int 1
    unicast_src_ip @LOCAL_IP@
    unicast_peer {
        @PEER_IP@
    }
    virtual_ipaddress {
        @VIP_CIDR@ dev @INTERFACE@
    }
    track_script {
        check_aifar_health
    }
}
```

- [ ] **Step 5: Run the focused test and verify it passes**

Run: `node --test scripts/keepalived-extra.test.mjs`

Expected: PASS for the new artifact/key/template tests; no failing assertion remains about these two files.

- [ ] **Step 6: Commit the contract and template**

```bash
git add extras/keepalived/keepalived.env.example extras/keepalived/keepalived.conf.tpl scripts/keepalived-extra.test.mjs
git commit -m "feat: add generic keepalived node contract"
```

### Task 2: Add the Strict Aggregate Health Probe

**Files:**
- Create: `extras/keepalived/check-aggregate-health.sh`
- Create: `scripts/keepalived-runtime.test.mjs`

**Interfaces:**
- Consumes: one root-owned URL line at `/aifar/apps/keepalived/etc/keepalived-health-url` in production.
- Produces: `validate_health_url_shape(url) -> 0|1`, `read_health_url(file) -> URL`, and `check_health(file) -> 0|nonzero`; the Keepalived template invokes the script with zero arguments.

- [ ] **Step 1: Create a Git Bash harness and failing health matrix**

Create `scripts/keepalived-runtime.test.mjs` with the same `toMsysPath` and `D:\\tools\\git\\bin\\bash.exe` convention as `scripts/selinux-extra.test.mjs`. Define `healthScriptPath`, create `harnessPath` and `urlPath` inside the temporary fixture, then write this harness through a JavaScript template literal:

```js
writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(healthScriptPath)}'
HEALTH_URL_FILE='${toMsysPath(urlPath)}'
curl() {
    [[ "\${FAKE_CURL_STATUS:-0}" -eq 0 ]] || return "\$FAKE_CURL_STATUS"
    printf '%s' "\${FAKE_CURL_BODY:-}"
}
check_health "\$HEALTH_URL_FILE"
`)
```

Add table-driven Node tests for:

```js
[
  { body: '{"status":{"up":true},"up":true}', status: 0, expected: 0 },
  { body: '{"up":false}', status: 0, expected: 1 },
  { body: '{"up":"true"}', status: 0, expected: 1 },
  { body: '{"up":true', status: 0, expected: 1 },
  { body: '{"up":true}', status: 22, expected: 1 },
  { body: '{"up":true}', status: 28, expected: 1 }
]
```

Also test that two URL lines, a URL containing `user:pass@`, and ports `0` and `65536` fail; retain port `38000` in the success case.

- [ ] **Step 2: Run the runtime test and verify the missing script fails it**

Run: `node --test scripts/keepalived-runtime.test.mjs`

Expected: FAIL because `check-aggregate-health.sh` cannot be sourced.

- [ ] **Step 3: Implement the health probe with strict JSON parsing**

Create `extras/keepalived/check-aggregate-health.sh` with these complete functions and a guarded entry point:

```bash
#!/usr/bin/env bash
set -Eeuo pipefail

readonly DEFAULT_HEALTH_URL_FILE="/aifar/apps/keepalived/etc/keepalived-health-url"

validate_health_url_shape() {
    local url="$1" remainder="" authority="" port=""
    [[ "$url" =~ ^https?://[A-Za-z0-9.-]+(:[0-9]{1,5})?(/[A-Za-z0-9._~/?\&=%-]*)?$ ]] || return 1
    remainder="${url#*://}"
    authority="${remainder%%/*}"
    if [[ "$authority" == *:* ]]; then
        port="${authority##*:}"
        [[ "$port" =~ ^[0-9]+$ ]] && ((10#$port >= 1 && 10#$port <= 65535)) || return 1
    fi
}

read_health_url() {
    local file="$1"
    local -a lines=()
    [[ -r "$file" ]] || return 1
    mapfile -t lines <"$file"
    [[ "${#lines[@]}" -eq 1 && -n "${lines[0]}" ]] || return 1
    validate_health_url_shape "${lines[0]}" || return 1
    printf '%s\n' "${lines[0]}"
}

json_up_is_true() {
    python3 -c 'import json, sys
try:
    value = json.load(sys.stdin)
except (TypeError, ValueError):
    raise SystemExit(1)
raise SystemExit(0 if isinstance(value, dict) and value.get("up") is True else 1)'
}

check_health() {
    local url_file="$1"
    local url=""
    local response=""
    url="$(read_health_url "$url_file")" || return 1
    response="$(curl --fail --silent --show-error --connect-timeout 1 --max-time 2 -- "$url")" || return 1
    printf '%s' "$response" | json_up_is_true
}

main() {
    [[ "$#" -eq 0 ]] || return 2
    check_health "$DEFAULT_HEALTH_URL_FILE"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
```

- [ ] **Step 4: Run health tests and shell syntax checks**

Run:

```bash
node --test scripts/keepalived-runtime.test.mjs
D:\tools\git\bin\bash.exe -n extras/keepalived/check-aggregate-health.sh
```

Expected: all runtime health cases PASS; Bash syntax check exits 0.

- [ ] **Step 5: Commit the health probe**

```bash
git add extras/keepalived/check-aggregate-health.sh scripts/keepalived-runtime.test.mjs
git commit -m "feat: add keepalived aggregate health probe"
```

### Task 3: Parse, Validate, and Render Node Configuration

**Files:**
- Modify: `extras/keepalived/install-keepalived-offline.sh:8-231`
- Modify: `scripts/keepalived-runtime.test.mjs`
- Modify: `scripts/keepalived-extra.test.mjs:38-46`

**Interfaces:**
- Consumes: Task 1 environment keys/template and Task 2 health script.
- Produces: `parse_node_config(file)`, `validate_node_config()`, `render_keepalived_config(template, output)`, and populated `NODE_*` globals for Tasks 4 and 5.

- [ ] **Step 1: Add failing parser, validation, and renderer tests**

Extend the runtime harness to source `install-keepalived-offline.sh`; require the installer to guard `main` with `BASH_SOURCE`. Add one valid configuration test using a fake `ip` function:

```bash
ip() {
    case "$*" in
        'link show dev ens160') return 0 ;;
        '-o -4 addr show dev ens160') printf '2: ens160 inet 192.168.74.132/24 scope global ens160\n' ;;
        '-4 route get 192.168.74.130') printf '192.168.74.130 dev ens160 src 192.168.74.132\n' ;;
        *) return 1 ;;
    esac
}
parse_node_config "$NODE_ENV"
validate_node_config
render_keepalived_config "$CONFIG_TEMPLATE" "$RENDERED_CONFIG"
```

Assert the output has no `@[A-Z_]+@`, has router ID `AIFAR_192_168_74_132`, and contains the exact interface, addresses, priority, VRID, and VIP. Add separate failing cases for missing key, duplicate key, unknown key, `$()`, equal local/peer, VIP equal to a node, priority 0/255, VRID 0/256, missing interface, wrong interface address, wrong route device, remote health host, URL credentials, and malformed CIDR.

Replace the obsolete static test name and negative activation assertion with:

```js
test('installer verifies source and requires managed configuration before build', () => {
  const installer = read('install-keepalived-offline.sh')
  const mainStart = indexOfOrFail(installer, '\nmain() {')
  const mainBody = installer.slice(mainStart, installer.indexOf('\nif [[ "${BASH_SOURCE[0]}" == "$0" ]]'))
  assert.ok(indexOfOrFail(mainBody, 'parse_node_config "$NODE_CONFIG"') < indexOfOrFail(mainBody, 'install_build_dependencies'))
  assert.ok(indexOfOrFail(mainBody, 'validate_node_config') < indexOfOrFail(mainBody, 'install_build_dependencies'))
  assert.doesNotMatch(installer, /cp\s+.*keepalived\.conf\.sample.*keepalived\.conf/)
})
```

- [ ] **Step 2: Run focused tests and verify they fail on missing functions**

Run: `node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs`

Expected: FAIL with `parse_node_config: command not found` and the obsolete activation contract removed.

- [ ] **Step 3: Add installer constants, globals, source guard, and allowlisted parser**

Add constants for `keepalived.env`, the template, health source, formal config, health URL, firewall record, and backup root. Add one empty `NODE_*` global per required key. Implement the parser without `source` or `eval`:

```bash
parse_node_config() {
    local file="$1" line="" key="" value=""
    local line_number=0
    declare -A seen=()
    [[ -r "$file" ]] || die "缺少节点配置：$file；请复制 keepalived.env.example 后修改"
    while IFS= read -r line || [[ -n "$line" ]]; do
        line_number=$((line_number + 1))
        line="${line%$'\r'}"
        [[ -z "$line" || "$line" == \#* ]] && continue
        [[ "$line" =~ ^([A-Z][A-Z0-9_]*)=([^[:space:]]+)$ ]] || die "节点配置第 $line_number 行格式无效"
        key="${BASH_REMATCH[1]}"
        value="${BASH_REMATCH[2]}"
        case "$key" in
            KEEPALIVED_LOCAL_IP|KEEPALIVED_PEER_IP|KEEPALIVED_VIP_CIDR|KEEPALIVED_INTERFACE|KEEPALIVED_PRIORITY|KEEPALIVED_VIRTUAL_ROUTER_ID|KEEPALIVED_HEALTH_URL) ;;
            *) die "节点配置包含未知字段：$key" ;;
        esac
        [[ -z "${seen[$key]+x}" ]] || die "节点配置字段重复：$key"
        seen[$key]=1
        printf -v "NODE_${key#KEEPALIVED_}" '%s' "$value"
    done <"$file"
    local variable=""
    for key in LOCAL_IP PEER_IP VIP_CIDR INTERFACE PRIORITY VIRTUAL_ROUTER_ID HEALTH_URL; do
        variable="NODE_$key"
        [[ -n "${!variable}" ]] || die "节点配置缺少字段：KEEPALIVED_$key"
    done
}
```

After `die()` is defined and the colocated health script has been checked as a regular file, source that trusted module script so the installer reuses its `validate_health_url_shape()` implementation:

```bash
# shellcheck source=check-aggregate-health.sh
source "$HEALTH_SCRIPT_SOURCE"
```

The finished code must contain no `source "$NODE_CONFIG"`, `eval`, or command substitution of config values. Sourcing the fixed module-owned health implementation is allowed; sourcing the node data file is not.

- [ ] **Step 4: Add exact validators and renderer**

Implement:

```bash
valid_ipv4() {
    local address="$1" octet=""
    local -a octets=()
    [[ "$address" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
    IFS=. read -r -a octets <<<"$address"
    for octet in "${octets[@]}"; do
        [[ "$octet" =~ ^(0|[1-9][0-9]{0,2})$ ]] || return 1
        ((10#$octet <= 255)) || return 1
    done
}

validate_node_config() {
    local vip="${NODE_VIP_CIDR%/*}"
    local prefix="${NODE_VIP_CIDR##*/}"
    local authority="${NODE_HEALTH_URL#*://}"
    local host_port="${authority%%/*}"
    local host="${host_port%%:*}"
    valid_ipv4 "$NODE_LOCAL_IP" || die "本机 IP 无效"
    valid_ipv4 "$NODE_PEER_IP" || die "对端 IP 无效"
    [[ "$NODE_LOCAL_IP" != "$NODE_PEER_IP" ]] || die "本机 IP 与对端 IP 不能相同"
    [[ "$NODE_VIP_CIDR" == */* ]] && valid_ipv4 "$vip" || die "VIP/CIDR 无效"
    [[ "$prefix" =~ ^[0-9]+$ ]] && ((prefix >= 1 && prefix <= 32)) || die "VIP 前缀必须为 1-32"
    [[ "$vip" != "$NODE_LOCAL_IP" && "$vip" != "$NODE_PEER_IP" ]] || die "VIP 不能等于节点 IP"
    [[ "$NODE_INTERFACE" =~ ^[A-Za-z0-9_.:-]{1,15}$ ]] || die "接口名称无效"
    [[ "$NODE_PRIORITY" =~ ^[0-9]+$ ]] && ((NODE_PRIORITY >= 1 && NODE_PRIORITY <= 254)) || die "优先级必须为 1-254"
    [[ "$NODE_VIRTUAL_ROUTER_ID" =~ ^[0-9]+$ ]] && ((NODE_VIRTUAL_ROUTER_ID >= 1 && NODE_VIRTUAL_ROUTER_ID <= 255)) || die "virtual router id 必须为 1-255"
    validate_health_url_shape "$NODE_HEALTH_URL" || die "健康 URL 格式无效"
    [[ "$host" == "$NODE_LOCAL_IP" || "$host" == 127.0.0.1 || "$host" == localhost ]] || die "健康 URL 必须指向本机"
    ip link show dev "$NODE_INTERFACE" >/dev/null 2>&1 || die "接口不存在：$NODE_INTERFACE"
    ip -o -4 addr show dev "$NODE_INTERFACE" | awk -v expected="$NODE_LOCAL_IP" '{split($4,a,"/"); if (a[1]==expected) found=1} END{exit !found}' || die "接口未绑定本机 IP"
    ip -4 route get "$vip" | awk -v expected="$NODE_INTERFACE" '{for(i=1;i<NF;i++) if($i=="dev" && $(i+1)==expected) found=1} END{exit !found}' || die "VIP 不通过指定接口路由"
}

render_keepalived_config() {
    local template="$1" output="$2" content="" router_id=""
    router_id="AIFAR_${NODE_LOCAL_IP//./_}"
    content="$(<"$template")"
    content="${content//@ROUTER_ID@/$router_id}"
    content="${content//@INTERFACE@/$NODE_INTERFACE}"
    content="${content//@VIRTUAL_ROUTER_ID@/$NODE_VIRTUAL_ROUTER_ID}"
    content="${content//@PRIORITY@/$NODE_PRIORITY}"
    content="${content//@LOCAL_IP@/$NODE_LOCAL_IP}"
    content="${content//@PEER_IP@/$NODE_PEER_IP}"
    content="${content//@VIP_CIDR@/$NODE_VIP_CIDR}"
    [[ ! "$content" =~ @[A-Z_]+@ ]] || die "Keepalived 模板仍有未替换字段"
    printf '%s\n' "$content" >"$output"
}
```

Add `curl` and `python3` to the DNF package array and require `ip`, `awk`, `curl`, and `python3`. Guard the installer entry point exactly like the health script so the test can source functions.

- [ ] **Step 5: Run parser/render tests and syntax checks**

Run:

```bash
node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
D:\tools\git\bin\bash.exe -n extras/keepalived/install-keepalived-offline.sh
```

Expected: all parser, validation, rendering, and existing archive tests PASS; Bash syntax exits 0.

- [ ] **Step 6: Commit strict configuration handling**

```bash
git add extras/keepalived/install-keepalived-offline.sh scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
git commit -m "feat: validate and render keepalived node config"
```

### Task 4: Add Backup-Safe Installation and Service Activation

**Files:**
- Modify: `extras/keepalived/install-keepalived-offline.sh`
- Modify: `scripts/keepalived-runtime.test.mjs`
- Modify: `scripts/keepalived-extra.test.mjs`

**Interfaces:**
- Consumes: validated `NODE_*` globals and rendered config from Task 3.
- Produces: `capture_service_state()`, `create_install_backup()`, `install_managed_configuration(staged_config)`, `activate_keepalived()`, `rollback_install_transaction()`, and transaction globals consumed by Task 5.

- [ ] **Step 1: Add failing service and rollback-order tests**

Use a fake `systemctl()` function that appends every argument to `CALL_LOG` and returns configured states for `is-active` and `is-enabled`. Test these cases:

- previously active/enabled: success calls `restart`, not `start`;
- previously inactive/disabled: success calls `enable` then `start`;
- health command failure: activation still returns success and logs a warning;
- final `is-active` failure: transaction fails;
- rollback restores active/enabled with `enable` + `restart`;
- rollback restores inactive/disabled with `stop` + `disable`.

Add static ordering assertions:

```js
const mainStart = indexOfOrFail(installer, '\nmain() {')
const mainBody = installer.slice(mainStart, installer.indexOf('\nif [[ "${BASH_SOURCE[0]}" == "$0" ]]'))
assert.ok(indexOfOrFail(mainBody, 'create_install_backup') < indexOfOrFail(mainBody, 'build_and_install_keepalived'))
assert.ok(indexOfOrFail(mainBody, 'install_managed_configuration') < indexOfOrFail(mainBody, 'activate_keepalived'))
assert.match(installer, /systemctl enable keepalived\.service/)
assert.match(installer, /systemctl is-active --quiet keepalived\.service/)
```

- [ ] **Step 2: Run the focused tests and verify missing transaction functions fail**

Run: `node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs`

Expected: FAIL for missing `capture_service_state` or `activate_keepalived`.

- [ ] **Step 3: Implement service-state capture and full-root backup**

Add globals `TRANSACTION_ACTIVE=0`, `APP_ROOT_EXISTED=0`, `SERVICE_WAS_ACTIVE=0`, `SERVICE_WAS_ENABLED=0`, `UNIT_LINK_EXISTED=0`, `UNIT_LINK_TARGET=''`, and `BACKUP_DIR=''`. Implement:

```bash
capture_service_state() {
    systemctl is-active --quiet keepalived.service && SERVICE_WAS_ACTIVE=1 || SERVICE_WAS_ACTIVE=0
    systemctl is-enabled --quiet keepalived.service && SERVICE_WAS_ENABLED=1 || SERVICE_WAS_ENABLED=0
    if [[ -e "$UNIT_LINK" || -L "$UNIT_LINK" ]]; then
        UNIT_LINK_EXISTED=1
        UNIT_LINK_TARGET="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
    fi
}

create_install_backup() {
    umask 077
    install -d -o root -g root -m 700 "$BACKUP_ROOT"
    BACKUP_DIR="$BACKUP_ROOT/keepalived-update-$(date -u +%Y%m%dT%H%M%SZ)-$$"
    install -d -o root -g root -m 700 "$BACKUP_DIR"
    if [[ -d "$APP_ROOT" ]]; then
        APP_ROOT_EXISTED=1
        cp -a -- "$APP_ROOT" "$BACKUP_DIR/installed-root"
    fi
    printf 'app_root_existed=%s\nservice_was_active=%s\nservice_was_enabled=%s\nunit_link_existed=%s\nunit_link_target=%s\n' \
        "$APP_ROOT_EXISTED" "$SERVICE_WAS_ACTIVE" "$SERVICE_WAS_ENABLED" "$UNIT_LINK_EXISTED" "$UNIT_LINK_TARGET" \
        >"$BACKUP_DIR/install-state.txt"
    (cd "$BACKUP_DIR" && find . -type f ! -name BACKUP.sha256 -print0 | sort -z | xargs -0 sha256sum >BACKUP.sha256 && sha256sum --check BACKUP.sha256)
}
```

Validate unit ownership before backup. Set `TRANSACTION_ACTIVE=1` only after the backup verifies.

- [ ] **Step 4: Install managed files, validate syntax, and activate service**

Add:

```bash
install_managed_configuration() {
    local staged_config="$1"
    local config_tmp="$FORMAL_CONFIG.tmp.$$"
    install -d -o root -g root -m 750 "$APP_ROOT/etc/keepalived" "$APP_ROOT/libexec" "$APP_ROOT/var/lib/aifar"
    install -o root -g root -m 750 "$HEALTH_SCRIPT_SOURCE" "$APP_ROOT/libexec/check-aggregate-health.sh"
    printf '%s\n' "$NODE_HEALTH_URL" >"$WORK_DIR/keepalived-health-url"
    install -o root -g root -m 640 "$WORK_DIR/keepalived-health-url" "$HEALTH_URL_FILE"
    "$APP_ROOT/sbin/keepalived" -t -f "$staged_config"
    install -o root -g root -m 640 "$staged_config" "$config_tmp"
    mv -f -- "$config_tmp" "$FORMAL_CONFIG"
    "$APP_ROOT/sbin/keepalived" -t -f "$FORMAL_CONFIG"
}

activate_keepalived() {
    systemctl daemon-reload
    systemctl enable keepalived.service
    if [[ "$SERVICE_WAS_ACTIVE" -eq 1 ]]; then
        systemctl restart keepalived.service
    else
        systemctl start keepalived.service
    fi
    systemctl is-active --quiet keepalived.service || die "keepalived.service 启动失败"
    if ! "$APP_ROOT/libexec/check-aggregate-health.sh"; then
        log "WARNING: 健康接口当前不可用；服务保持 active，VRRP 实例将保持 FAULT"
    fi
}
```

Render into `$WORK_DIR/keepalived.conf`, then call build, unit registration, managed installation, SELinux configuration, activation, and final verification in that order. Do not copy the upstream sample config.

- [ ] **Step 5: Implement trap-driven rollback**

Replace `cleanup` so a nonzero exit with `TRANSACTION_ACTIVE=1` calls rollback with `set +e`, retains the original exit code, then removes only the validated `/tmp/keepalived-offline.*` work directory. `rollback_install_transaction` must stop the failed service, restore the entire prior root from `BACKUP_DIR/installed-root` when `APP_ROOT_EXISTED=1`, otherwise delete only the exact validated `$APP_ROOT`; restore/remove the owned unit link; reload systemd; then restore active/enabled state exactly.

Use these exact state actions:

```bash
if [[ "$SERVICE_WAS_ENABLED" -eq 1 ]]; then
    systemctl enable keepalived.service
else
    systemctl disable keepalived.service
fi
if [[ "$SERVICE_WAS_ACTIVE" -eq 1 ]]; then
    systemctl restart keepalived.service
else
    systemctl stop keepalived.service
fi
```

Before every recursive removal, require `readlink -f -- "$APP_ROOT"` to equal `/aifar/apps/keepalived` and reject mount points. Set `TRANSACTION_ACTIVE=0` only after activation, health reporting, and final verification succeed.

- [ ] **Step 6: Run focused tests and Bash syntax checks**

Run:

```bash
node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
D:\tools\git\bin\bash.exe -n extras/keepalived/install-keepalived-offline.sh
```

Expected: service-state matrix and ordering tests PASS; Bash syntax exits 0.

- [ ] **Step 7: Commit transactional startup**

```bash
git add extras/keepalived/install-keepalived-offline.sh scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
git commit -m "feat: start keepalived with install rollback"
```

### Task 5: Reconcile Exact Peer-Scoped Firewalld Ownership

**Files:**
- Modify: `extras/keepalived/install-keepalived-offline.sh`
- Modify: `extras/keepalived/uninstall-keepalived.sh:6-177`
- Modify: `scripts/keepalived-runtime.test.mjs`
- Modify: `scripts/keepalived-extra.test.mjs:85-100`

**Interfaces:**
- Consumes: validated `NODE_INTERFACE`, `NODE_PEER_IP`, transaction cleanup from Task 4.
- Produces: `firewall_rule_for_peer(peer)`, `reconcile_firewall_rule()`, `rollback_firewall_journal()`, and `remove_owned_firewall_rule()` using the record `/aifar/apps/keepalived/var/lib/aifar/firewall-rule`.

- [ ] **Step 1: Add failing firewalld ownership tests**

Add a fake `firewall-cmd()` backed by runtime/permanent rule state files. Cover:

- active firewalld adds `rule family="ipv4" source address="192.168.74.133/32" protocol value="112" accept` to both runtime and permanent state;
- explicit interface zone is used, with default-zone fallback only when `--get-zone-of-interface` returns empty or `no zone`;
- an exact pre-existing runtime/permanent rule is not marked owned;
- a partial add failure removes the form added by the current transaction;
- changing peer IP removes only the previously recorded rule and records the new rule;
- rollback reverses added and removed journal entries in reverse order;
- inactive firewalld performs no firewall command and writes no ownership record;
- uninstaller refuses malformed ownership records and removes only forms marked `created=1`.

Update the old uninstaller negative assertion: allow exact `firewall-cmd --remove-rich-rule` operations, but continue rejecting global protocol openings and unrestricted rule removal.

- [ ] **Step 2: Run focused tests and verify firewall functions are missing**

Run: `node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs`

Expected: FAIL for missing `reconcile_firewall_rule` or missing ownership behavior.

- [ ] **Step 3: Implement the exact rule, zone selection, journal, and ownership record**

Use this exact rule generator:

```bash
firewall_rule_for_peer() {
    printf 'rule family="ipv4" source address="%s/32" protocol value="112" accept\n' "$1"
}
```

When `systemctl is-active --quiet firewalld.service` is false, log and return. Otherwise require `firewall-cmd`, select the interface zone, and query runtime and permanent forms with:

```bash
firewall-cmd --zone="$zone" --query-rich-rule="$rule"
firewall-cmd --permanent --zone="$zone" --query-rich-rule="$rule"
```

Append every successful mutation to `$WORK_DIR/firewall-journal.tsv` as `added|runtime|zone|rule`, `added|permanent|zone|rule`, `removed|runtime|zone|rule`, or `removed|permanent|zone|rule`. Rollback reads the journal into an array and applies inverse operations from the last row to the first. Do not use `firewall-cmd --reload` because runtime and permanent forms are updated explicitly.

Write the final ownership record through a root-owned mode-600 temporary file with exactly:

```text
zone=public
rule=rule family="ipv4" source address="192.168.74.133/32" protocol value="112" accept
runtime_created=0|1
permanent_created=0|1
```

Parse old records with a fixed four-key allowlist, no `source`. If an old owned rule differs from the desired rule, verify its exact current presence, journal its removal, and only then install the new ownership record. Wire `rollback_firewall_journal` into Task 4 rollback before restoring files.

- [ ] **Step 4: Extend the uninstaller with exact owned-rule removal**

Back up `var/lib/aifar/firewall-rule` with the other state. After stopping the service and before deleting `$APP_ROOT`, parse the same four-key record, validate `zone`, peer-scoped rule shape, and `0|1` flags, then remove only marked forms that still exist:

```bash
[[ "$runtime_created" == 1 ]] && firewall-cmd --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1 && \
    firewall-cmd --zone="$zone" --remove-rich-rule="$rule"
[[ "$permanent_created" == 1 ]] && firewall-cmd --permanent --zone="$zone" --query-rich-rule="$rule" >/dev/null 2>&1 && \
    firewall-cmd --permanent --zone="$zone" --remove-rich-rule="$rule"
```

If firewalld is unavailable while an ownership record exists, abort after the verified backup and before deleting the installation so ownership is not orphaned.

- [ ] **Step 5: Run firewall/rollback tests and syntax checks**

Run:

```bash
node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
D:\tools\git\bin\bash.exe -n extras/keepalived/install-keepalived-offline.sh
D:\tools\git\bin\bash.exe -n extras/keepalived/uninstall-keepalived.sh
```

Expected: all exact ownership, peer change, partial failure, rollback, uninstall, and static safety tests PASS.

- [ ] **Step 6: Commit firewall lifecycle support**

```bash
git add extras/keepalived/install-keepalived-offline.sh extras/keepalived/uninstall-keepalived.sh scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
git commit -m "feat: manage keepalived peer firewall rule"
```

### Task 6: Finish SELinux Migration, Documentation, and Linux Release Packaging

**Files:**
- Modify: `extras/keepalived/configure-selinux.sh:6-205`
- Modify: `extras/keepalived/install-keepalived-offline.sh`
- Modify: `extras/keepalived/uninstall-keepalived.sh:40-42`
- Modify: `extras/keepalived/README.md`
- Modify: `scripts/keepalived-extra.test.mjs`
- Modify: `scripts/package-release.mjs:40-50`
- Modify: `scripts/release-pipeline.test.mjs:185-205`

**Interfaces:**
- Consumes: all artifacts and lifecycle behavior from Tasks 1-5.
- Produces: repeat-safe SELinux mappings for `libexec`, a current-install SELinux mutation journal that the installer can reverse, complete operator documentation, and verified Linux release modes.

- [ ] **Step 1: Add failing SELinux, documentation, and packaging assertions**

Add tests requiring both SELinux scripts to use literal environment-based awk comparison:

```js
for (const scriptName of ['configure-selinux.sh', 'uninstall-keepalived.sh']) {
  const source = read(scriptName)
  assert.match(source, /PATTERN="\$1" awk '\$1 == ENVIRON\["PATTERN"\]'/)
  assert.doesNotMatch(source, /awk -v pattern="\$1"/)
}
assert.match(read('configure-selinux.sh'), /\/aifar\/apps\/keepalived\/libexec\(\/\.\*\)\?/)
assert.match(read('configure-selinux.sh'), /KEEPALIVED_SELINUX_TRANSACTION_FILE/)
assert.match(read('install-keepalived-offline.sh'), /rollback_selinux_journal/)
```

Extend `scripts/keepalived-runtime.test.mjs` with a two-row SELinux journal and fake `semanage()` call log. Assert `rollback_selinux_journal` processes the rows in reverse, deletes the `created` pattern, and restores the `updated` pattern's previous type. Add README assertions for every config key, both node IPs, VIP, `systemctl status/restart/stop/start`, `ip addr`, `FAULT`, automatic return/preemption, protocol 112, and the retained backup path. Update the release fixture to require all new files and add `check-aggregate-health.sh` to the executable-mode loop.

- [ ] **Step 2: Run focused tests and verify the old helper/docs/package list fail**

Run:

```bash
node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs scripts/release-pipeline.test.mjs
```

Expected: FAIL on old `awk -v pattern`, missing `libexec` mapping/journal rollback, old README startup contract, and absent health-script executable declaration.

- [ ] **Step 3: Fix literal SELinux lookup and migrate the managed mapping record**

Replace `mapping_context()` in both scripts with:

```bash
mapping_context() {
    semanage fcontext -l -C | PATTERN="$1" awk '$1 == ENVIRON["PATTERN"] { print $NF; exit }'
}
```

In `configure-selinux.sh`, separate the complete next ownership record from the current-run rollback journal. Reconcile every previously recorded pattern, then add `/aifar/apps/keepalived/libexec(/.*)?` using reference `/usr/libexec/keepalived`; preserve each pattern's original `created|updated|unchanged` ownership row. On failure, reverse only mutations in the current-run journal. Create and verify `$APP_ROOT/libexec` instead of `$APP_ROOT/scripts`, install the completed ownership record atomically, and retain the existing mode-preservation restrictions.

When the installer invokes the helper, pass `KEEPALIVED_SELINUX_TRANSACTION_FILE="$WORK_DIR/selinux-journal.tsv"`. The helper accepts this environment variable only when it resolves below `/tmp/keepalived-offline.*`, and on success copies the current-run mutation journal there with mode 600. Add `rollback_selinux_journal()` to the installer; it reads rows in reverse and applies only these inverses:

```text
created|pattern|-|applied_type -> semanage fcontext -d pattern
updated|pattern|previous_type|applied_type -> semanage fcontext -m -t previous_type pattern
```

Call `rollback_selinux_journal` during the outer install rollback after firewall reversal and before restoring the old application tree. A standalone zero-argument `configure-selinux.sh` run does not export a journal and keeps its existing self-contained rollback behavior.

- [ ] **Step 4: Rewrite README around the managed workflow**

Document these exact operator steps:

1. copy `keepalived.env.example` to `keepalived.env` on each node;
2. show the 132 priority 150 and 133 priority 100 files with the same VIP/VRID;
3. run only `bash install-keepalived-offline.sh` on each node;
4. inspect `systemctl status keepalived`, `journalctl -u keepalived`, `ip addr show dev ens160`, and `firewall-cmd --list-rich-rules`;
5. explain that an unhealthy local endpoint keeps systemd active but VRRP in `FAULT`;
6. explain failover to 133 and automatic return to 132 after two successful probes;
7. list manual `systemctl start|stop|restart keepalived` commands;
8. explain repeat-install backup/rollback and exact-rule uninstall ownership;
9. retain the offline DNF, SELinux mode, shared RPM, and backup-retention warnings.

- [ ] **Step 5: Mark the health probe executable and update release assertions**

Add `'check-aggregate-health.sh'` to `keepalivedEntry.executables` in `scripts/package-release.mjs` and the tar-mode loop in `scripts/release-pipeline.test.mjs`. Assert Linux package presence of the env example, template, and health script; assert `checksums.txt` contains each; retain the Windows exclusion assertion.

- [ ] **Step 6: Run all focused and script tests**

Run:

```bash
D:\tools\git\bin\bash.exe -n extras/keepalived/install-keepalived-offline.sh
D:\tools\git\bin\bash.exe -n extras/keepalived/check-aggregate-health.sh
D:\tools\git\bin\bash.exe -n extras/keepalived/configure-selinux.sh
D:\tools\git\bin\bash.exe -n extras/keepalived/uninstall-keepalived.sh
node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs scripts/release-pipeline.test.mjs
pnpm test:scripts
```

Expected: all syntax checks exit 0; focused tests and the complete script suite report zero failures.

- [ ] **Step 7: Commit SELinux, docs, and packaging**

```bash
git add extras/keepalived/install-keepalived-offline.sh extras/keepalived/configure-selinux.sh extras/keepalived/uninstall-keepalived.sh extras/keepalived/README.md scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs scripts/package-release.mjs scripts/release-pipeline.test.mjs
git commit -m "docs: complete managed keepalived lifecycle"
```

- [ ] **Step 8: Run the complete local release gate**

Run: `pnpm test:local`

Expected: backend tests, web tests, script tests, backend/web builds, package build, and release checksum verification all exit 0. Confirm the final release output reports successful Linux and Windows directory/archive verification.

- [ ] **Step 9: Inspect final scope and release archive modes**

Run in PowerShell:

```powershell
git status --short
git diff --check HEAD~6..HEAD
$linuxArchive = Get-ChildItem deploy/deployment/*-linux-amd64.tar.gz | Select-Object -First 1
tar -tvzf $linuxArchive.FullName | Select-String 'extras/keepalived/'
```

Expected: only planned Keepalived/test/doc/package files are changed by these tasks; no whitespace errors; the installer, health probe, SELinux helper, and uninstaller show `-rwxr-xr-x`; Windows release contains no `extras/keepalived` directory.

## Real openEuler Acceptance After Local Implementation

Real-server execution is a separate controlled mutation and is not authorized by this plan alone. After explicit authorization, install the same built package on both nodes and verify:

```bash
systemctl is-enabled keepalived.service
systemctl is-active keepalived.service
/aifar/apps/keepalived/sbin/keepalived -t -f /aifar/apps/keepalived/etc/keepalived/keepalived.conf
ip addr show dev ens160
firewall-cmd --list-rich-rules
```

Acceptance sequence:

1. both health URLs return HTTP 2xx with JSON boolean `up: true`, and VIP `192.168.74.130/24` exists only on priority-150 node 132;
2. make node 132 health fail for at least six seconds, then VIP appears only on node 133;
3. restore node 132 health for at least four seconds, then default preemption returns the VIP only to node 132;
4. repeat the installer on one node and confirm a UTC backup is created and service remains active;
5. inject a safe pre-start validation failure in a disposable test run and confirm the previous configuration and service state are restored.
