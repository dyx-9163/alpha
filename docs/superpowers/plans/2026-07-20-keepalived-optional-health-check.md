# Keepalived Optional Health Check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow a missing or commented `KEEPALIVED_HEALTH_URL` to install and start Keepalived with normal VRRP/VIP behavior but without installing or referencing application-health artifacts.

**Architecture:** Keep the zero-argument installer and one configuration template. Parse health-check presence into an explicit `HEALTH_CHECK_ENABLED` flag, render three conditional template blocks, and branch the existing transactional managed-configuration and activation paths without weakening full-root rollback.

**Tech Stack:** Bash, Keepalived 2.4.2 configuration, Node.js `node:test`, openEuler 24.03 LTS SP3 x86_64, systemd, firewalld, SELinux.

## Global Constraints

- Installation root remains exactly `/aifar/apps/keepalived`.
- The installer remains a zero-argument command that reads `keepalived.env` beside the script.
- Missing or fully commented `KEEPALIVED_HEALTH_URL` disables health checking; `KEEPALIVED_HEALTH_URL=` is an error.
- The other six node keys remain required and keep their current validation rules.
- Disabled mode still generates, enables, and starts a BACKUP/unicast VRRP configuration with VIP, priority, VRID, peer firewall rule, and default preemption.
- Disabled mode must not install or retain the managed health script or health URL file under the application root.
- Enabled mode keeps the current HTTP 2xx plus top-level JSON boolean `up=true`, interval 2, timeout 3, fall 3, rise 2, and weight 0 behavior.
- Existing full-root, service, SELinux, and firewall rollback boundaries remain authoritative for repeat-install mode switches.
- SELinux mode must not be changed; firewall ownership remains limited to the exact peer `/32` VRRP protocol 112 rule.
- Preserve LF line endings in all shell, template, and example files.
- Preserve unrelated dirty-worktree changes and stage only files named by each task.

---

### Task 1: Optional parser state and conditional template rendering

**Files:**
- Modify: `scripts/keepalived-runtime.test.mjs:182-248,1618-1730`
- Modify: `scripts/keepalived-extra.test.mjs:37-65`
- Modify: `extras/keepalived/install-keepalived-offline.sh:28-35,710-798`
- Modify: `extras/keepalived/keepalived.conf.tpl:1-32`
- Modify: `extras/keepalived/keepalived.env.example:1-9`

**Interfaces:**
- Produces: Bash global `HEALTH_CHECK_ENABLED`, always integer `0` or `1` after `parse_node_config`.
- Produces: `render_keepalived_config TEMPLATE OUTPUT`, with no unresolved `@[A-Z_]+@` token.
- Consumes: existing `NODE_*` globals and `validate_health_url_shape` from `check-aggregate-health.sh` only when health checking is enabled.

- [ ] **Step 1: Add failing parser and renderer tests**

Add this helper beside `validNodeConfig` in `scripts/keepalived-runtime.test.mjs`:

```js
function nodeConfigWithoutHealth({ commented = false } = {}) {
  return validNodeConfig().replace(
    /^KEEPALIVED_HEALTH_URL=.*\n/m,
    commented
      ? '# KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate\n'
      : ''
  )
}
```

Extend `runNodeConfigHarness` so its generated Bash prints the parsed mode and its result returns it:

```js
writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(installerPath)}'
NODE_ENV='${toMsysPath(nodeEnvPath)}'
RENDERED_CONFIG='${toMsysPath(renderedConfigPath)}'
${ipFunction}
parse_node_config "$NODE_ENV"
validate_node_config
${render ? 'render_keepalived_config "$CONFIG_TEMPLATE" "$RENDERED_CONFIG"' : ''}
printf '%s\n' "$HEALTH_CHECK_ENABLED"
`)

return {
  ...result,
  healthCheckEnabled: result.stdout.trim(),
  renderedConfig: existsSync(renderedConfigPath) ? readFileSync(renderedConfigPath, 'utf8') : ''
}
```

Add the concrete cases:

```js
for (const [name, config] of [
  ['missing', nodeConfigWithoutHealth()],
  ['commented', nodeConfigWithoutHealth({ commented: true })]
]) {
  test(`installer accepts ${name} health URL and renders VRRP without health blocks`, (t) => {
    const result = runNodeConfigHarness(t, { config })
    assert.equal(result.status, 0, result.stderr)
    assert.equal(result.healthCheckEnabled, '0')
    assert.match(result.renderedConfig, /vrrp_instance AIFAR_VI/)
    assert.match(result.renderedConfig, /192\.168\.74\.130\/24 dev ens160/)
    assert.doesNotMatch(result.renderedConfig, /script_user|enable_script_security/)
    assert.doesNotMatch(result.renderedConfig, /vrrp_script|track_script|check_aifar_health/)
  })
}

test('installer enables health blocks when a valid health URL is present', (t) => {
  const result = runNodeConfigHarness(t)
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.healthCheckEnabled, '1')
  assert.match(result.renderedConfig, /script_user root/)
  assert.match(result.renderedConfig, /vrrp_script check_aifar_health/)
  assert.match(result.renderedConfig, /track_script/)
})

test('installer rejects an explicitly empty health URL', (t) => {
  const result = runNodeConfigHarness(t, {
    config: validNodeConfig().replace(/^KEEPALIVED_HEALTH_URL=.*$/m, 'KEEPALIVED_HEALTH_URL='),
    render: false
  })
  assert.equal(result.status, 1, result.stderr)
  assert.match(result.stderr, /健康 URL 不能为空/)
})
```

Remove the old `missing key` invalid case for health URL and change `runSequentialParseHarness` expectations so the second parse succeeds and prints `0`, proving the flag resets rather than reusing enabled state.

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```powershell
node --test --test-name-pattern "health URL|health blocks|stale node globals" scripts/keepalived-runtime.test.mjs
```

Expected: FAIL because the parser still requires `KEEPALIVED_HEALTH_URL`, has no `HEALTH_CHECK_ENABLED`, and the template always emits health blocks.

- [ ] **Step 3: Implement explicit optional parser state**

Add the global after `NODE_HEALTH_URL`:

```bash
HEALTH_CHECK_ENABLED=0
```

Replace `parse_node_config` with:

```bash
parse_node_config() {
    local file="$1" line="" key="" value=""
    local line_number=0
    declare -A seen=()
    NODE_LOCAL_IP=""
    NODE_PEER_IP=""
    NODE_VIP_CIDR=""
    NODE_INTERFACE=""
    NODE_PRIORITY=""
    NODE_VIRTUAL_ROUTER_ID=""
    NODE_HEALTH_URL=""
    HEALTH_CHECK_ENABLED=0
    [[ -r "$file" ]] || die "缺少节点配置：$file；请复制 keepalived.env.example 后修改"
    while IFS= read -r line || [[ -n "$line" ]]; do
        line_number=$((line_number + 1))
        line="${line%$'\r'}"
        [[ -z "$line" || "$line" == \#* ]] && continue
        [[ "$line" != 'KEEPALIVED_HEALTH_URL=' ]] || die "健康 URL 不能为空"
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
    local variable="" config_key=""
    for key in LOCAL_IP PEER_IP VIP_CIDR INTERFACE PRIORITY VIRTUAL_ROUTER_ID; do
        variable="NODE_$key"
        config_key="KEEPALIVED_$key"
        [[ -n "${seen[$config_key]+x}" && -n "${!variable}" ]] || die "节点配置缺少字段：$config_key"
    done
    if [[ -n "${seen[KEEPALIVED_HEALTH_URL]+x}" ]]; then
        HEALTH_CHECK_ENABLED=1
    fi
}
```

In `validate_node_config`, initialize health URL parsing values empty and validate them only in enabled mode:

```bash
local authority="" host_port="" host=""

if [[ "$HEALTH_CHECK_ENABLED" -eq 1 ]]; then
    authority="${NODE_HEALTH_URL#*://}"
    host_port="${authority%%/*}"
    host="${host_port%%:*}"
    validate_health_url_shape "$NODE_HEALTH_URL" || die "健康 URL 格式无效"
    [[ "$host" == "$NODE_LOCAL_IP" || "$host" == 127.0.0.1 || "$host" == localhost ]] || die "健康 URL 必须指向本机"
fi
```

Keep all existing IP, CIDR, priority, VRID, interface, and route checks outside that conditional.

- [ ] **Step 4: Convert the template to three conditional tokens**

Replace `extras/keepalived/keepalived.conf.tpl` with:

```text
global_defs {
    router_id @ROUTER_ID@
@SCRIPT_SECURITY@
}

@HEALTH_SCRIPT@
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
@TRACK_SCRIPT@
}
```

Replace `render_keepalived_config` with:

```bash
render_keepalived_config() {
    local template="$1" output="$2" content="" router_id=""
    local script_security="" health_script="" track_script=""
    router_id="AIFAR_${NODE_LOCAL_IP//./_}"
    if [[ "$HEALTH_CHECK_ENABLED" -eq 1 ]]; then
        script_security=$'    script_user root\n    enable_script_security'
        health_script=$'vrrp_script check_aifar_health {\n    script "/aifar/apps/keepalived/libexec/check-aggregate-health.sh"\n    interval 2\n    timeout 3\n    fall 3\n    rise 2\n    weight 0\n}\n'
        track_script=$'    track_script {\n        check_aifar_health\n    }'
    fi
    content="$(<"$template")"
    content="${content//@ROUTER_ID@/$router_id}"
    content="${content//@INTERFACE@/$NODE_INTERFACE}"
    content="${content//@VIRTUAL_ROUTER_ID@/$NODE_VIRTUAL_ROUTER_ID}"
    content="${content//@PRIORITY@/$NODE_PRIORITY}"
    content="${content//@LOCAL_IP@/$NODE_LOCAL_IP}"
    content="${content//@PEER_IP@/$NODE_PEER_IP}"
    content="${content//@VIP_CIDR@/$NODE_VIP_CIDR}"
    content="${content//@SCRIPT_SECURITY@/$script_security}"
    content="${content//@HEALTH_SCRIPT@/$health_script}"
    content="${content//@TRACK_SCRIPT@/$track_script}"
    [[ ! "$content" =~ @[A-Z_]+@ ]] || die "Keepalived 模板仍有未替换字段"
    printf '%s\n' "$content" >"$output"
}
```

- [ ] **Step 5: Update the example and static contract tests**

Change the example comment and final line to:

```dotenv
# Configure different LOCAL_IP, PEER_IP and PRIORITY values on each node.
# Uncomment and set HEALTH_URL only when application health should control VIP ownership.
# KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate
```

In `scripts/keepalived-extra.test.mjs`, expect six active keys and the optional commented key:

```js
assert.deepEqual(keys, [
  'KEEPALIVED_LOCAL_IP',
  'KEEPALIVED_PEER_IP',
  'KEEPALIVED_VIP_CIDR',
  'KEEPALIVED_INTERFACE',
  'KEEPALIVED_PRIORITY',
  'KEEPALIVED_VIRTUAL_ROUTER_ID'
])
assert.match(example, /^# KEEPALIVED_HEALTH_URL=http:\/\//m)
```

Update the template test to require `@SCRIPT_SECURITY@`, `@HEALTH_SCRIPT@`, and `@TRACK_SCRIPT@`, while retaining the existing BACKUP, unicast, fall/rise, weight, and no-`nopreempt` assertions.

- [ ] **Step 6: Run Task 1 tests and commit**

Run:

```powershell
node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/install-keepalived-offline.sh
git diff --check
```

Expected: all focused tests PASS, Bash syntax exit 0, and no whitespace errors.

Commit only the five Task 1 files:

```powershell
git add -- scripts/keepalived-runtime.test.mjs scripts/keepalived-extra.test.mjs extras/keepalived/install-keepalived-offline.sh extras/keepalived/keepalived.conf.tpl extras/keepalived/keepalived.env.example
git commit -m "feat: make keepalived health URL optional"
```

---

### Task 2: Transactional health-artifact installation and removal

**Files:**
- Modify: `scripts/keepalived-runtime.test.mjs:944-975,1100-1255,1869-1905`
- Modify: `extras/keepalived/install-keepalived-offline.sh:918-982`

**Interfaces:**
- Consumes: `HEALTH_CHECK_ENABLED` from Task 1.
- Preserves: `install_managed_configuration STAGED_CONFIG` and `activate_keepalived` public Bash function names.
- Produces: enabled mode with formal config, executable health script, and mode-0640 URL file; disabled mode with formal config and neither health artifact.

- [ ] **Step 1: Add a disabled-mode managed-configuration harness and failing tests**

Add `runManagedConfigurationWithoutHealthHarness` next to the existing managed configuration harnesses. It must create an old formal config plus old health script/URL, create a staged `vrrp_instance` containing no health tokens, set `HEALTH_CHECK_ENABLED=0`, call `install_managed_configuration`, and return these exact observations:

```js
return {
  ...result,
  formalConfig: readFileSync(formalConfigPath, 'utf8'),
  healthUrlExists: existsSync(healthUrlPath),
  healthScriptExists: existsSync(healthScriptPath),
  syntaxCalls: readFileSync(syntaxLogPath, 'utf8').trimEnd().split('\n')
}
```

Add:

```js
test('disabled health mode installs syntax-checked config and removes old health artifacts', (t) => {
  const result = runManagedConfigurationWithoutHealthHarness(t)
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.formalConfig, /vrrp_instance AIFAR_VI/)
  assert.doesNotMatch(result.formalConfig, /check_aifar_health|vrrp_script|track_script/)
  assert.equal(result.healthUrlExists, false)
  assert.equal(result.healthScriptExists, false)
  assert.equal(result.syntaxCalls.length, 2)
  assert.match(result.syntaxCalls[0], /-t -f .*\/work\/keepalived\.conf$/)
  assert.match(result.syntaxCalls[1], /-t -f .*\/etc\/keepalived\/keepalived\.conf$/)
})
```

Extend `runServiceHarness` with `healthEnabled = true` and a health execution marker. Set `HEALTH_CHECK_ENABLED` in the generated Bash before `activate_keepalived`, and make the fake installed health script write the marker before exiting. Add:

```js
test('disabled health mode starts the service without executing a health script', (t) => {
  const result = runServiceHarness(t, {
    active: false,
    enabled: false,
    healthEnabled: false,
    healthStatus: 1
  })
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.healthExecuted, false)
  assert.deepEqual(result.calls, [
    'is-active --quiet keepalived.service',
    'is-enabled --quiet keepalived.service',
    'daemon-reload',
    'enable keepalived.service',
    'start keepalived.service',
    'is-active --quiet keepalived.service'
  ])
})
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run:

```powershell
node --test --test-name-pattern "disabled health mode" scripts/keepalived-runtime.test.mjs
```

Expected: FAIL because managed configuration requires a health reference and activation always executes the health script.

- [ ] **Step 3: Branch managed configuration atomically by mode**

Replace `install_managed_configuration` with:

```bash
install_managed_configuration() {
    local staged_config="$1"
    local validation_config="$WORK_DIR/keepalived.validation.conf"
    local config_tmp="$FORMAL_CONFIG.tmp.$$"
    local health_url_tmp="$HEALTH_URL_FILE.tmp.$$"
    local health_script_target="$APP_ROOT/libexec/check-aggregate-health.sh"
    local health_script_tmp="$APP_ROOT/libexec/check-aggregate-health.sh.tmp.$$"
    local line=''

    install -d -o root -g root -m 750 "$APP_ROOT/etc/keepalived" "$APP_ROOT/libexec" "$APP_ROOT/var/lib/aifar"
    if [[ "$HEALTH_CHECK_ENABLED" -eq 1 ]]; then
        install -o root -g root -m 750 "$HEALTH_SCRIPT_SOURCE" "$health_script_tmp"
        : >"$validation_config"
        while IFS= read -r line || [[ -n "$line" ]]; do
            printf '%s\n' "${line//$health_script_target/$health_script_tmp}" >>"$validation_config"
        done <"$staged_config"
        grep -Fq "$health_script_tmp" "$validation_config" || die "临时配置未引用待安装的健康检查脚本"
        "$APP_ROOT/sbin/keepalived" -t -f "$validation_config"
        printf '%s\n' "$NODE_HEALTH_URL" >"$WORK_DIR/keepalived-health-url"
        install -o root -g root -m 640 "$WORK_DIR/keepalived-health-url" "$health_url_tmp"
        install -o root -g root -m 640 "$staged_config" "$config_tmp"
        mv -f -- "$health_script_tmp" "$health_script_target"
        mv -f -- "$health_url_tmp" "$HEALTH_URL_FILE"
        mv -f -- "$config_tmp" "$FORMAL_CONFIG"
    else
        if grep -Eq '(^|[[:space:]])(vrrp_script|track_script)([[:space:]]|$)|check_aifar_health|check-aggregate-health\.sh' "$staged_config"; then
            die "禁用健康检查时配置不得引用健康检查脚本"
        fi
        "$APP_ROOT/sbin/keepalived" -t -f "$staged_config"
        install -o root -g root -m 640 "$staged_config" "$config_tmp"
        mv -f -- "$config_tmp" "$FORMAL_CONFIG"
        rm -f -- "$health_script_target" "$HEALTH_URL_FILE"
    fi
    "$APP_ROOT/sbin/keepalived" -t -f "$FORMAL_CONFIG"
}
```

The exact-path `rm -f` is inside the existing active transaction. Any directory, permission, syntax, or later-step failure must propagate so `rollback_install_transaction` restores the verified full old root.

- [ ] **Step 4: Skip post-start health execution and verify final artifact state**

Change the health warning in `activate_keepalived` to:

```bash
if [[ "$HEALTH_CHECK_ENABLED" -eq 1 ]] && ! "$APP_ROOT/libexec/check-aggregate-health.sh"; then
    log "WARNING: 健康检查当前不可用；服务保持 active，VRRP 实例将保持 FAULT"
fi
```

Append to `verify_installation`:

```bash
if [[ "$HEALTH_CHECK_ENABLED" -eq 1 ]]; then
    [[ -x "$APP_ROOT/libexec/check-aggregate-health.sh" ]] || die "健康检查脚本不存在或不可执行"
    [[ -f "$HEALTH_URL_FILE" && ! -L "$HEALTH_URL_FILE" ]] || die "健康 URL 文件不存在或类型无效"
else
    [[ ! -e "$APP_ROOT/libexec/check-aggregate-health.sh" && ! -L "$APP_ROOT/libexec/check-aggregate-health.sh" ]] || die "禁用健康检查时仍存在健康检查脚本"
    [[ ! -e "$HEALTH_URL_FILE" && ! -L "$HEALTH_URL_FILE" ]] || die "禁用健康检查时仍存在健康 URL 文件"
fi
```

- [ ] **Step 5: Add both mode-switch rollback cases**

Create one harness that starts with a verified old installation root, calls `create_install_backup`, calls `install_managed_configuration` in the opposite mode, then deliberately returns status 73 before `TRANSACTION_ACTIVE=0`. Run it once with old artifacts present/new mode disabled, and once with old artifacts absent/new mode enabled.

Add the assertions:

```js
for (const direction of ['enabled-to-disabled', 'disabled-to-enabled']) {
  test(`failed ${direction} health mode switch restores the complete previous root`, (t) => {
    const result = runHealthModeRollbackHarness(t, direction)
    assert.equal(result.status, 73, result.stderr)
    assert.equal(result.oldConfigRestored, true)
    assert.equal(result.oldHealthScriptStateRestored, true)
    assert.equal(result.oldHealthUrlStateRestored, true)
  })
}
```

The harness must use the real `create_install_backup`, `cleanup`, and `rollback_install_transaction` functions with only systemd, mountpoint, install, and Keepalived binary calls faked. It must not call a real service or delete outside its fixture root.

- [ ] **Step 6: Run Task 2 tests and commit**

Run:

```powershell
node --test scripts/keepalived-runtime.test.mjs
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/install-keepalived-offline.sh
git diff --check
```

Expected: all runtime tests PASS, Bash syntax exit 0, no whitespace errors.

Commit only the two Task 2 files:

```powershell
git add -- scripts/keepalived-runtime.test.mjs extras/keepalived/install-keepalived-offline.sh
git commit -m "feat: install keepalived without optional health artifacts"
```

---

### Task 3: Operator documentation and release-contract closure

**Files:**
- Modify: `extras/keepalived/README.md:4-94`
- Modify: `scripts/keepalived-extra.test.mjs:24-111,138-158`
- Modify: `memory.md` by appending one concise reusable conclusion without credentials or long logs

**Interfaces:**
- Documents: one package and one zero-argument installer supporting enabled and disabled health modes.
- Verifies: release artifact list remains unchanged; disabled mode changes installation behavior, not Linux package contents.

- [ ] **Step 1: Add failing documentation contract assertions**

Add to `scripts/keepalived-extra.test.mjs`:

```js
test('README documents optional health mode and its VIP consequence', () => {
  const readme = read('README.md')
  assert.match(readme, /KEEPALIVED_HEALTH_URL.*可选/s)
  assert.match(readme, /注释.*KEEPALIVED_HEALTH_URL.*不安装健康检查脚本/s)
  assert.match(readme, /业务接口异常.*仍可能持有 VIP/s)
  assert.match(readme, /KEEPALIVED_HEALTH_URL=.*空值.*错误/s)
})
```

Run:

```powershell
node --test --test-name-pattern "README documents optional health mode" scripts/keepalived-extra.test.mjs
```

Expected: FAIL until README describes the new contract.

- [ ] **Step 2: Update README with exact operator behavior**

Update the configuration section to say six keys are required and `KEEPALIVED_HEALTH_URL` is optional. Show both forms:

```dotenv
# No application health tracking; VRRP priority and peer state alone control VIP ownership.
# KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate
```

```dotenv
# Application health tracking enabled.
KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate
```

Document all of these exact operational facts:

- deleting or commenting the line disables health tracking;
- an active empty assignment is an error;
- disabled mode installs and starts Keepalived but does not install the health script or URL file;
- disabled mode has no `vrrp_script` or `track_script` in formal configuration;
- application failure does not trigger `FAULT`, so a node may keep VIP while its business endpoint is unhealthy;
- repeat installation can enable or disable the mode transactionally;
- firewall, SELinux, service commands, backups, and uninstall remain the same.

- [ ] **Step 3: Run all Keepalived and script tests**

Run:

```powershell
node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs scripts/release-pipeline.test.mjs
pnpm test:scripts
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/install-keepalived-offline.sh
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/configure-selinux.sh
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/uninstall-keepalived.sh
git diff --check
```

Expected: every Node test passes, all three Bash syntax checks exit 0, and `git diff --check` is clean.

- [ ] **Step 4: Run the full local release gate**

Use a writable workspace Go cache:

```powershell
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\gocache'
pnpm test:local
pnpm release:verify
```

Expected: backend tests, 161 web tests or the current higher count, all script tests, frontend/backend builds, packaging, and Linux/Windows directory plus archive checksum verification pass. If `test:local` reaches its outer timeout during archive verification, record the completed stages and require the separate fresh `pnpm release:verify` command to pass before completion.

- [ ] **Step 5: Append project memory and commit Task 3**

Append a concise entry to `memory.md` stating the final optional-health contract and actual verification result. Do not include node passwords, tokens, private keys, complete connection strings, or long logs.

Stage only the README, static test file, and the exact memory hunk if it can be isolated without staging pre-existing user edits. If `memory.md` already contains unrelated unstaged edits that cannot be isolated safely, leave it unstaged and explicitly report that preservation.

```powershell
git add -- extras/keepalived/README.md scripts/keepalived-extra.test.mjs
git diff --cached --check
git commit -m "docs: explain keepalived optional health mode"
```

- [ ] **Step 6: Record real Linux acceptance as a separately authorized operation**

Do not connect to or mutate a server merely because local implementation is complete. With explicit target-host authorization, verify both modes on openEuler 24.03 LTS SP3 x86_64:

```bash
systemctl is-active keepalived.service
systemctl is-enabled keepalived.service
/aifar/apps/keepalived/sbin/keepalived -t -f /aifar/apps/keepalived/etc/keepalived/keepalived.conf
grep -E 'vrrp_script|track_script|check_aifar_health' /aifar/apps/keepalived/etc/keepalived/keepalived.conf
test ! -e /aifar/apps/keepalived/libexec/check-aggregate-health.sh
test ! -e /aifar/apps/keepalived/etc/keepalived/keepalived-health-url
ip -4 addr show dev ens160
```

For disabled mode, the grep command must return 1, both `test ! -e` commands return 0, the service remains active/enabled, and VIP ownership follows VRRP priority/peer state. Re-enable the URL only when authorized to verify health-driven `FAULT` and recovery behavior.

---

## Final Review Checklist

- [ ] Missing and commented health URL both select disabled mode.
- [ ] Explicit empty health URL fails with a specific error.
- [ ] Enabled mode is behavior-compatible with the currently deployed configuration.
- [ ] Disabled configuration contains no health-related directives or paths.
- [ ] Disabled installation contains no managed health script or URL file.
- [ ] Enabled-to-disabled and disabled-to-enabled failures restore the complete previous root and service state.
- [ ] No firewall, SELinux mode, VRRP priority, peer, VIP, or preemption semantics changed.
- [ ] Relevant tests, Bash syntax, full script suite, packaging, and release verification have fresh passing output.
- [ ] Only intended files are staged or committed; unrelated dirty-worktree changes remain untouched.
