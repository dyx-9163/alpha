# Keepalived Direct systemd Unit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Keepalived unit symlink with a managed regular file at `/etc/systemd/system/keepalived.service`, preserving safe migration, rollback, uninstall, SELinux labeling, and reboot auto-start behavior.

**Architecture:** Keep the Autoconf-generated unit under `/aifar/apps/keepalived/systemd` only as an installation source. Render a final unit containing `RequiresMountsFor=/aifar/apps/keepalived`, atomically install it as a regular `/etc/systemd/system` file, and treat absent, legacy-link, and managed-file unit states as explicit transaction states. Installation and uninstallation back up and restore the exact original unit type and contents while refusing foreign units or concurrent replacement.

**Tech Stack:** Bash 4+, systemd, Keepalived 2.4.2, SELinux tools, Node.js `node:test`, PowerShell/Git Bash test host.

## Global Constraints

- `/etc/systemd/system/keepalived.service` must be a regular root:root 0644 file after installation and must never be a symlink.
- The final unit must contain `ExecStart=/aifar/apps/keepalived/sbin/keepalived` and `RequiresMountsFor=/aifar/apps/keepalived`.
- Installation must run `systemctl daemon-reload`, `systemctl enable keepalived.service`, and the existing start/restart plus active-state verification.
- A legacy AIFAR symlink to `/aifar/apps/keepalived/systemd/keepalived.service` is migratable; foreign links, foreign regular units, and foreign loaded fragments must fail before overwrite.
- Rollback must restore the original absent, legacy-link, or managed-file state before restoring enabled/active state.
- Uninstall must back up and remove an owned direct unit, remain compatible with the legacy AIFAR link, and never delete an external unit.
- Keepalived binaries, configuration, health artifacts, firewall ownership, and runtime data remain under `/aifar/apps/keepalived`.
- Do not connect to or mutate real servers in this implementation plan; live reboot validation requires separate authorization.
- Preserve unrelated existing worktree changes and stage only files listed by each task.

## File Structure

- Modify `extras/keepalived/install-keepalived-offline.sh`: classify unit ownership, render/install a direct unit, capture unit transaction state, and restore it on failure.
- Modify `extras/keepalived/uninstall-keepalived.sh`: back up/remove/restore direct and legacy unit states during uninstall.
- Modify `scripts/keepalived-runtime.test.mjs`: exercise direct-unit rendering, migration, rollback, uninstall, and conflict behavior with filesystem/systemctl fakes.
- Modify `scripts/keepalived-extra.test.mjs`: enforce static packaging and safety contracts, including the absence of symlink registration.
- Modify `extras/keepalived/README.md`: document the direct unit, reboot semantics, migration, diagnostics, and uninstall behavior.

---

### Task 1: Install and Roll Back a Direct Unit

**Files:**
- Modify: `scripts/keepalived-extra.test.mjs:78-114`
- Modify: `scripts/keepalived-runtime.test.mjs:250-330,957-1070,1879-2010`
- Modify: `extras/keepalived/install-keepalived-offline.sh:26-43,185-242,604-687,916-940,999-1021,1048-1069`

**Interfaces:**
- Consumes: generated source unit at `BUILT_UNIT=/aifar/apps/keepalived/systemd/keepalived.service`.
- Produces: `UNIT_FILE=/etc/systemd/system/keepalived.service`; `UNIT_STATE` with exact values `absent`, `legacy-link`, or `managed-file`; `classify_unit_state`; `render_systemd_unit SOURCE OUTPUT`; `install_systemd_unit`; `restore_install_unit_state`.

- [ ] **Step 1: Add static failing tests for the direct-unit contract**

Replace the old symlink assertion in `scripts/keepalived-extra.test.mjs` and extend the installer contract test with:

```js
  assert.match(installer, /readonly UNIT_FILE="\/etc\/systemd\/system\/keepalived\.service"/)
  assert.match(installer, /readonly BUILT_UNIT="\$\{APP_ROOT\}\/systemd\/keepalived\.service"/)
  assert.match(installer, /RequiresMountsFor=\$APP_ROOT/)
  assert.match(installer, /install -o root -g root -m 0644 "\$rendered_unit" "\$unit_tmp"/)
  assert.match(installer, /mv -f -- "\$unit_tmp" "\$UNIT_FILE"/)
  assert.match(installer, /\[\[ -f "\$UNIT_FILE" && ! -L "\$UNIT_FILE" \]\]/)
  assert.doesNotMatch(installer, /systemctl link/)
  const installStart = indexOfOrFail(installer, 'install_systemd_unit()')
  const installEnd = indexOfOrFail(installer.slice(installStart), '\n}') + installStart
  const installBody = installer.slice(installStart, installEnd)
  assert.doesNotMatch(installBody, /ln -s/)
```

- [ ] **Step 2: Run the static test and verify RED**

Run:

```powershell
node --test --test-name-pattern "installer verifies source and requires managed configuration" scripts/keepalived-extra.test.mjs
```

Expected: FAIL because the installer still declares `UNIT_LINK`, calls `systemctl link`, and restores links with `ln -s`.

- [ ] **Step 3: Add failing runtime tests for rendering, migration, conflict rejection, and rollback**

Update `writeInstallerFixture` so the `/etc/systemd/system/keepalived.service` replacement is exposed as `unitFile`. Add a fixture helper that creates one of the three original states, sources the real installer, fakes `systemctl`, `install`, `restorecon`, and `mountpoint`, then invokes the requested function. Its result must expose `status`, `stderr`, `unitKind`, `unitContents`, `unitTarget`, and `calls`.

Add these tests using that helper:

```js
test('installer renders and installs a direct systemd unit', (t) => {
  const result = runDirectUnitHarness(t, { originalState: 'absent', action: 'install' })
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.unitKind, 'file')
  assert.match(result.unitContents, /^RequiresMountsFor=\/aifar\/apps\/keepalived$/m)
  assert.match(result.unitContents, /^ExecStart=\/aifar\/apps\/keepalived\/sbin\/keepalived/m)
  assert.equal(result.calls.filter((call) => call === 'daemon-reload').length, 1)
})

test('installer migrates the legacy AIFAR unit link to a regular file', (t) => {
  const result = runDirectUnitHarness(t, { originalState: 'legacy-link', action: 'install' })
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.unitKind, 'file')
})

for (const originalState of ['foreign-link', 'foreign-file']) {
  test(`installer rejects ${originalState} before replacing it`, (t) => {
    const result = runDirectUnitHarness(t, { originalState, action: 'capture' })
    assert.equal(result.status, 1)
    assert.match(result.stderr, /keepalived\.service.*不属于此安装|其他 Keepalived unit/)
    assert.notEqual(result.unitKind, 'absent')
  })
}

for (const originalState of ['absent', 'legacy-link', 'managed-file']) {
  test(`installer rollback restores ${originalState} unit state`, (t) => {
    const result = runDirectUnitHarness(t, { originalState, action: 'rollback' })
    assert.equal(result.status, 0, result.stderr)
    assert.equal(result.unitKind, originalState === 'legacy-link' ? 'link' : originalState === 'managed-file' ? 'file' : 'absent')
    if (originalState === 'legacy-link') assert.match(result.unitTarget, /\/systemd\/keepalived\.service$/)
    if (originalState === 'managed-file') assert.equal(result.unitContents, result.originalUnitContents)
  })
}
```

- [ ] **Step 4: Run runtime tests and verify RED**

Run:

```powershell
node --test --test-name-pattern "direct systemd unit|legacy AIFAR unit link|rejects foreign|unit state" scripts/keepalived-runtime.test.mjs
```

Expected: FAIL because the named installer interfaces and direct-file behavior do not exist.

- [ ] **Step 5: Replace installer unit constants and captured state**

Replace the unit constants/state declarations with:

```bash
readonly UNIT_FILE="/etc/systemd/system/keepalived.service"
readonly BUILT_UNIT="${APP_ROOT}/systemd/keepalived.service"
UNIT_STATE='absent'
UNIT_LINK_TARGET=''
UNIT_FILE_SHA256=''
INSTALLED_UNIT_SHA256=''
```

Add ownership helpers:

```bash
is_managed_unit_file() {
    local file="$1"
    [[ -f "$file" && ! -L "$file" ]] || return 1
    grep -Fq "ExecStart=$APP_ROOT/sbin/keepalived" "$file"
}

classify_unit_state() {
    local fragment='' fragment_target=''

    UNIT_STATE='absent'
    UNIT_LINK_TARGET=''
    UNIT_FILE_SHA256=''
    if [[ -L "$UNIT_FILE" ]]; then
        UNIT_LINK_TARGET="$(readlink -f -- "$UNIT_FILE" 2>/dev/null || true)"
        [[ "$UNIT_LINK_TARGET" == "$BUILT_UNIT" ]] || die "系统 keepalived.service 不属于此安装：$UNIT_LINK_TARGET"
        UNIT_STATE='legacy-link'
    elif [[ -e "$UNIT_FILE" ]]; then
        is_managed_unit_file "$UNIT_FILE" || die "系统 keepalived.service 不属于此安装：$UNIT_FILE"
        UNIT_STATE='managed-file'
        UNIT_FILE_SHA256="$(sha256sum "$UNIT_FILE" | awk '{print $1}')"
    fi

    fragment="$(systemctl show -p FragmentPath --value keepalived.service 2>/dev/null || true)"
    if [[ -n "$fragment" ]]; then
        fragment_target="$(readlink -f -- "$fragment" 2>/dev/null || true)"
        case "$fragment_target" in
            "$BUILT_UNIT"|"$UNIT_FILE") ;;
            *) die "已加载的 keepalived.service 不属于此安装：$fragment_target" ;;
        esac
    fi
}

unit_state_matches_capture() {
    local current_sha=''
    case "$UNIT_STATE" in
        absent)
            [[ ! -e "$UNIT_FILE" && ! -L "$UNIT_FILE" ]]
            ;;
        legacy-link)
            [[ -L "$UNIT_FILE" ]] || return 1
            [[ "$(readlink -f -- "$UNIT_FILE" 2>/dev/null || true)" == "$UNIT_LINK_TARGET" ]]
            ;;
        managed-file)
            [[ -f "$UNIT_FILE" && ! -L "$UNIT_FILE" ]] || return 1
            current_sha="$(sha256sum "$UNIT_FILE" | awk '{print $1}')"
            [[ "$current_sha" == "$UNIT_FILE_SHA256" ]]
            ;;
        *) return 1 ;;
    esac
}
```

Make `capture_service_state` call `classify_unit_state` after capturing active/enabled state.

- [ ] **Step 6: Back up the exact original unit state**

In `create_install_backup`, after creating `BACKUP_DIR`, add:

```bash
if [[ "$UNIT_STATE" == 'managed-file' ]]; then
    cp -a -- "$UNIT_FILE" "$BACKUP_DIR/systemd-unit"
fi
printf 'app_root_existed=%s\nservice_was_active=%s\nservice_was_enabled=%s\nunit_state=%s\nunit_link_target=%s\nunit_file_sha256=%s\n' \
    "$APP_ROOT_EXISTED" "$SERVICE_WAS_ACTIVE" "$SERVICE_WAS_ENABLED" \
    "$UNIT_STATE" "$UNIT_LINK_TARGET" "$UNIT_FILE_SHA256" \
    >"$BACKUP_DIR/install-state.txt"
```

Remove `UNIT_LINK_EXISTED` from the old state file. The existing checksum command must include `systemd-unit` automatically.

- [ ] **Step 7: Render and atomically install the regular unit**

Replace `register_systemd_unit` with:

```bash
render_systemd_unit() {
    local source="$1" output="$2"
    awk -v mount_line="RequiresMountsFor=$APP_ROOT" '
        BEGIN { in_unit=0; saw_unit=0; emitted_mount=0 }
        /^\[Unit\]$/ { saw_unit=1; in_unit=1; print; next }
        in_unit && /^RequiresMountsFor=/ {
            if (!emitted_mount && $0 == mount_line) { print; emitted_mount=1 }
            next
        }
        in_unit && /^\[/ {
            if (!emitted_mount) { print mount_line; emitted_mount=1 }
            in_unit=0
        }
        { print }
        END {
            if (in_unit && !emitted_mount) print mount_line
            if (!saw_unit) exit 42
        }
    ' "$source" >"$output" || die "无法生成直接 systemd unit"
}

install_systemd_unit() {
    local rendered_unit="$WORK_DIR/keepalived.service"
    local unit_tmp="$UNIT_FILE.tmp.$$"

    [[ -f "$BUILT_UNIT" && ! -L "$BUILT_UNIT" ]] || die "未生成 systemd 单元：$BUILT_UNIT"
    grep -Fq "ExecStart=$APP_ROOT/sbin/keepalived" "$BUILT_UNIT" || die "生成的 systemd unit 未引用自定义安装目录"
    render_systemd_unit "$BUILT_UNIT" "$rendered_unit"
    grep -Fxq "RequiresMountsFor=$APP_ROOT" "$rendered_unit" || die "systemd unit 缺少安装目录挂载依赖"
    unit_state_matches_capture || die "keepalived.service 在安装期间被外部修改，拒绝覆盖"
    install -d -o root -g root -m 0755 "$(dirname -- "$UNIT_FILE")"
    install -o root -g root -m 0644 "$rendered_unit" "$unit_tmp"
    if [[ -e "$UNIT_FILE" || -L "$UNIT_FILE" ]]; then
        rm -f -- "$UNIT_FILE"
    fi
    mv -f -- "$unit_tmp" "$UNIT_FILE"
    [[ -f "$UNIT_FILE" && ! -L "$UNIT_FILE" ]] || die "keepalived.service 必须安装为普通文件"
    INSTALLED_UNIT_SHA256="$(sha256sum "$UNIT_FILE" | awk '{print $1}')"
    if command -v restorecon >/dev/null 2>&1; then
        restorecon -F "$UNIT_FILE"
    fi
    systemctl daemon-reload
}
```

Replace the `register_systemd_unit` call in `main` with `install_systemd_unit`. Keep `activate_keepalived` unchanged so it performs a fresh daemon reload before enable/start.

- [ ] **Step 8: Restore original unit state before service state during rollback**

Add:

```bash
restore_install_unit_state() {
    local current_sha=''

    if [[ -L "$UNIT_FILE" ]]; then
        printf '[keepalived-installer] ERROR: unit 已被外部替换为软链接，回滚拒绝覆盖：%s\n' "$UNIT_FILE" >&2
        return 1
    elif [[ -f "$UNIT_FILE" ]]; then
        is_managed_unit_file "$UNIT_FILE" || {
            printf '[keepalived-installer] ERROR: unit 已被外部修改，回滚拒绝覆盖：%s\n' "$UNIT_FILE" >&2
            return 1
        }
        current_sha="$(sha256sum "$UNIT_FILE" | awk '{print $1}')"
        [[ -n "$INSTALLED_UNIT_SHA256" && "$current_sha" == "$INSTALLED_UNIT_SHA256" ]] || {
            printf '[keepalived-installer] ERROR: unit 已在安装后被外部修改，回滚拒绝覆盖：%s\n' "$UNIT_FILE" >&2
            return 1
        }
        rm -f -- "$UNIT_FILE" || return 1
    elif [[ -e "$UNIT_FILE" ]]; then
        return 1
    fi

    case "$UNIT_STATE" in
        absent) ;;
        legacy-link)
            [[ "$UNIT_LINK_TARGET" == "$BUILT_UNIT" ]] || return 1
            ln -s -- "$UNIT_LINK_TARGET" "$UNIT_FILE" || return 1
            ;;
        managed-file)
            [[ -f "$BACKUP_DIR/systemd-unit" ]] || return 1
            current_sha="$(sha256sum "$BACKUP_DIR/systemd-unit" | awk '{print $1}')"
            [[ "$current_sha" == "$UNIT_FILE_SHA256" ]] || return 1
            install -o root -g root -m 0644 "$BACKUP_DIR/systemd-unit" "$UNIT_FILE" || return 1
            ;;
        *) return 1 ;;
    esac
    systemctl daemon-reload
}
```

In `rollback_install_transaction`, call `restore_install_unit_state` immediately after restoring `APP_ROOT` and before `systemctl enable|disable|restart|stop`. Delete the old late link-removal/link-recreation block and its second daemon reload.

- [ ] **Step 9: Verify the installed unit rather than only the build source**

Change `verify_installation` unit checks to:

```bash
local unit_file="$UNIT_FILE"
[[ -f "$unit_file" && ! -L "$unit_file" ]] || die "systemd unit 不是普通文件：$unit_file"
grep -Fq "ExecStart=$APP_ROOT/sbin/keepalived" "$unit_file" || die "systemd unit 未引用自定义安装目录"
grep -Fxq "RequiresMountsFor=$APP_ROOT" "$unit_file" || die "systemd unit 缺少安装目录挂载依赖"
```

Remove `ln` from the installer's required command list only if no non-unit code uses it after this task.

- [ ] **Step 10: Run focused tests and verify GREEN**

Run:

```powershell
node --test --test-name-pattern "installer verifies source|direct systemd unit|legacy AIFAR unit link|rejects foreign|unit state|rollback restores" scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/install-keepalived-offline.sh
```

Expected: all selected tests PASS and Bash syntax exits 0.

- [ ] **Step 11: Commit installer behavior**

```powershell
git add -- extras/keepalived/install-keepalived-offline.sh scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
git commit -m "fix: install Keepalived systemd unit directly"
```

---

### Task 2: Uninstall and Restore Direct or Legacy Units

**Files:**
- Modify: `scripts/keepalived-runtime.test.mjs:730-860,2268-2320`
- Modify: `scripts/keepalived-extra.test.mjs:185-215`
- Modify: `extras/keepalived/uninstall-keepalived.sh:8-19,129-185,439-483,515-529,551-553`

**Interfaces:**
- Consumes: Task 1 state names `absent`, `legacy-link`, and `managed-file`, plus owned-unit detection by `ExecStart=/aifar/apps/keepalived/sbin/keepalived`.
- Produces: `capture_uninstall_unit_state`; backup file `$BACKUP_DIR/systemd-unit`; `restore_uninstall_unit_and_service` supporting direct and legacy units.

- [ ] **Step 1: Add failing direct-unit uninstall tests**

Parameterize `runUninstallTransactionHarness` with `unitState = 'managed-file'`. Make its default fixture write the owned unit as a regular file at the rewritten `/etc/systemd/system` path; retain `unitState='legacy-link'` for migration compatibility and add `foreign-file`/`foreign-link` fixtures.

Add:

```js
for (const unitState of ['managed-file', 'legacy-link']) {
  test(`uninstaller removes owned ${unitState} unit after verified backup`, (t) => {
    const result = runUninstallTransactionHarness(t, { unitState })
    assert.equal(result.status, 0, result.stderr)
    assert.equal(result.unitKind, 'absent')
    assert.match(result.backupManifest, new RegExp(`unit_state=${unitState}`))
    if (unitState === 'managed-file') assert.equal(result.backedUpUnitContents, result.originalUnitContents)
  })
}

for (const unitState of ['foreign-file', 'foreign-link']) {
  test(`uninstaller refuses ${unitState}`, (t) => {
    const result = runUninstallTransactionHarness(t, { unitState })
    assert.equal(result.status, 1)
    assert.notEqual(result.unitKind, 'absent')
    assert.equal(result.calls.some((call) => /systemctl\|(stop|disable)/.test(call)), false)
  })
}

test('uninstaller rollback restores the original direct unit bytes', (t) => {
  const result = runUninstallTransactionHarness(t, { unitState: 'managed-file', failDisable: true })
  assert.equal(result.status, 61, result.stderr)
  assert.equal(result.unitKind, 'file')
  assert.equal(result.unitContents, result.originalUnitContents)
})
```

- [ ] **Step 2: Run uninstall tests and verify RED**

Run:

```powershell
node --test --test-name-pattern "uninstaller removes owned|uninstaller refuses foreign|original direct unit bytes" scripts/keepalived-runtime.test.mjs
```

Expected: FAIL because the harness and uninstaller still assume `UNIT_LINK` is a symlink.

- [ ] **Step 3: Replace uninstall unit state and ownership logic**

Use these declarations and helpers:

```bash
readonly UNIT_FILE="/etc/systemd/system/keepalived.service"
readonly BUILT_UNIT="$APP_ROOT/systemd/keepalived.service"
UNIT_STATE='absent'
UNIT_LINK_TARGET=''
UNIT_FILE_SHA256=''

is_managed_unit_file() {
    local file="$1"
    [[ -f "$file" && ! -L "$file" ]] || return 1
    grep -Fq "ExecStart=$APP_ROOT/sbin/keepalived" "$file"
}

capture_uninstall_unit_state() {
    UNIT_STATE='absent'
    UNIT_LINK_TARGET=''
    UNIT_FILE_SHA256=''
    if [[ -L "$UNIT_FILE" ]]; then
        UNIT_LINK_TARGET="$(readlink -f -- "$UNIT_FILE" 2>/dev/null || true)"
        [[ "$UNIT_LINK_TARGET" == "$BUILT_UNIT" ]] || die "系统 keepalived.service 不属于此安装：$UNIT_LINK_TARGET"
        UNIT_STATE='legacy-link'
    elif [[ -e "$UNIT_FILE" ]]; then
        is_managed_unit_file "$UNIT_FILE" || die "系统 keepalived.service 不属于此安装：$UNIT_FILE"
        UNIT_STATE='managed-file'
        UNIT_FILE_SHA256="$(sha256sum "$UNIT_FILE" | awk '{print $1}')"
    fi
}
```

Update `validate_unit_ownership` so loaded `FragmentPath` may resolve only to `BUILT_UNIT` or `UNIT_FILE`; call `capture_uninstall_unit_state` during preflight after ownership validation.

- [ ] **Step 4: Include the direct unit in the verified uninstall backup**

In `create_and_verify_backup`, before writing the manifest:

```bash
if [[ "$UNIT_STATE" == 'managed-file' ]]; then
    cp -a -- "$UNIT_FILE" "$BACKUP_DIR/systemd-unit"
fi
```

Replace the old link manifest fields with:

```text
unit_state=$UNIT_STATE
unit_link_target=$UNIT_LINK_TARGET
unit_file_sha256=$UNIT_FILE_SHA256
```

The existing `find ... | sha256sum` command must checksum the copied direct unit.

- [ ] **Step 5: Remove only the owned unit during uninstall**

Replace the link-only deletion in `perform_uninstall_mutations` with:

```bash
case "$UNIT_STATE" in
    managed-file)
        is_managed_unit_file "$UNIT_FILE" || die "keepalived.service changed before removal"
        [[ "$(sha256sum "$UNIT_FILE" | awk '{print $1}')" == "$UNIT_FILE_SHA256" ]] || die "keepalived.service changed before removal"
        ;;
    legacy-link)
        [[ -L "$UNIT_FILE" && "$(readlink -f -- "$UNIT_FILE")" == "$BUILT_UNIT" ]] || die "keepalived.service changed before removal"
        ;;
    absent) ;;
    *) die "invalid captured keepalived.service state" ;;
esac
if [[ "$UNIT_STATE" != 'absent' ]]; then
    rm -f -- "$UNIT_FILE"
fi
systemctl daemon-reload
```

- [ ] **Step 6: Restore exact unit type and bytes on uninstall failure**

Replace `restore_uninstall_unit_and_service` with logic equivalent to:

```bash
restore_uninstall_unit_and_service() {
    local fragment='' fragment_target='' restored_sha='' rollback_status=0

    UNIT_ROLLBACK_CONFLICT=0
    if [[ -e "$UNIT_FILE" || -L "$UNIT_FILE" ]]; then
        UNIT_ROLLBACK_CONFLICT=1
        return 1
    fi
    case "$UNIT_STATE" in
        absent) ;;
        legacy-link)
            [[ "$UNIT_LINK_TARGET" == "$BUILT_UNIT" ]] || return 1
            ln -s -- "$UNIT_LINK_TARGET" "$UNIT_FILE" || return 1
            ;;
        managed-file)
            [[ -f "$BACKUP_DIR/systemd-unit" ]] || return 1
            restored_sha="$(sha256sum "$BACKUP_DIR/systemd-unit" | awk '{print $1}')"
            [[ "$restored_sha" == "$UNIT_FILE_SHA256" ]] || return 1
            install -o root -g root -m 0644 "$BACKUP_DIR/systemd-unit" "$UNIT_FILE" || return 1
            ;;
        *) return 1 ;;
    esac
    systemctl daemon-reload || return 1
    fragment="$(systemctl show -p FragmentPath --value keepalived.service 2>/dev/null || true)"
    fragment_target="$(readlink -f -- "$fragment" 2>/dev/null || true)"
    if [[ "$UNIT_STATE" == 'legacy-link' ]]; then
        [[ "$fragment_target" == "$BUILT_UNIT" ]] || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
    elif [[ "$UNIT_STATE" == 'managed-file' ]]; then
        [[ "$fragment_target" == "$UNIT_FILE" ]] || { UNIT_ROLLBACK_CONFLICT=1; return 1; }
    fi
    if [[ "$SERVICE_WAS_ENABLED" -eq 1 ]]; then
        systemctl enable keepalived.service || rollback_status=1
    else
        systemctl disable keepalived.service || rollback_status=1
    fi
    if [[ "$SERVICE_WAS_ACTIVE" -eq 1 ]]; then
        systemctl restart keepalived.service || rollback_status=1
    else
        systemctl stop keepalived.service || rollback_status=1
    fi
    return "$rollback_status"
}
```

Keep the existing foreign-concurrent-replacement rule: if any new path appears before restore, set `UNIT_ROLLBACK_CONFLICT=1` and do not enable/restart it.

- [ ] **Step 7: Update static uninstall contracts**

Extend `scripts/keepalived-extra.test.mjs` with:

```js
  assert.match(script, /unit_state=\$UNIT_STATE/)
  assert.match(script, /cp -a -- "\$UNIT_FILE" "\$BACKUP_DIR\/systemd-unit"/)
  assert.match(script, /is_managed_unit_file "\$UNIT_FILE"/)
  assert.match(script, /rm -f -- "\$UNIT_FILE"/)
  assert.doesNotMatch(script, /UNIT_LINK_EXISTED/)
```

- [ ] **Step 8: Run focused uninstall tests and syntax checks**

Run:

```powershell
node --test --test-name-pattern "uninstaller removes owned|uninstaller refuses foreign|original direct unit bytes|rollback does not control a foreign unit" scripts/keepalived-runtime.test.mjs
node --test --test-name-pattern "uninstaller verifies a backup" scripts/keepalived-extra.test.mjs
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/uninstall-keepalived.sh
```

Expected: all selected tests PASS and Bash syntax exits 0.

- [ ] **Step 9: Commit uninstall behavior**

```powershell
git add -- extras/keepalived/uninstall-keepalived.sh scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
git commit -m "fix: uninstall direct Keepalived systemd unit"
```

---

### Task 3: Documentation and Full Verification

**Files:**
- Modify: `extras/keepalived/README.md:50-140`
- Modify: `scripts/keepalived-extra.test.mjs:130-180`
- Verify: `extras/keepalived/configure-selinux.sh`
- Verify: `extras/selinux/configure-all-selinux.sh`

**Interfaces:**
- Consumes: final direct-unit behavior from Tasks 1 and 2.
- Produces: operator documentation and release-level evidence that no unit symlink registration remains.

- [ ] **Step 1: Add failing README contract tests**

Add:

```js
test('README documents the direct Keepalived systemd unit and reboot behavior', () => {
  const readme = read('README.md')
  assert.match(readme, /\/etc\/systemd\/system\/keepalived\.service.*普通文件/s)
  assert.match(readme, /RequiresMountsFor=\/aifar\/apps\/keepalived/)
  assert.match(readme, /重启.*无需.*daemon-reload/s)
  assert.match(readme, /旧版.*软链接.*迁移/s)
  assert.match(readme, /卸载.*备份.*unit/s)
  assert.doesNotMatch(readme, /systemctl link/)
})
```

- [ ] **Step 2: Run README test and verify RED**

Run:

```powershell
node --test --test-name-pattern "README documents the direct Keepalived systemd unit" scripts/keepalived-extra.test.mjs
```

Expected: FAIL because the README still describes the previous unit/link behavior incompletely.

- [ ] **Step 3: Update operator documentation**

Document these exact operational facts in `extras/keepalived/README.md`:

```markdown
安装器仍把 Keepalived 本体安装到 `/aifar/apps/keepalived`，但 systemd 实际加载的
`/etc/systemd/system/keepalived.service` 是 root:root、0644 的普通文件，不是指向安装树的软链接。
unit 包含 `RequiresMountsFor=/aifar/apps/keepalived`，因此安装树位于独立挂载时，systemd 会先等待挂载可用。
安装和更新会执行 `systemctl daemon-reload` 与 `systemctl enable keepalived.service`；正常服务器重启无需人工执行 `daemon-reload`。

重复安装会把旧版 AIFAR unit 软链接迁移为普通文件。安装器不会覆盖第三方 Keepalived unit。
卸载前会把直接 unit 连同安装树写入校验备份，随后停止、禁用并删除本模块拥有的 unit；卸载回滚会恢复原文件内容或旧版链接。
```

Add diagnostic commands:

```bash
test -f /etc/systemd/system/keepalived.service
test ! -L /etc/systemd/system/keepalived.service
systemctl show keepalived.service -p LoadState -p ActiveState -p UnitFileState -p FragmentPath --no-pager
grep -E '^(RequiresMountsFor|ExecStart)=' /etc/systemd/system/keepalived.service
```

- [ ] **Step 4: Verify SELinux scope remains correct**

Inspect both SELinux scripts and keep the existing `/aifar/apps/keepalived` mappings for the generated source unit and application tree. Do not add a custom fcontext for `/etc/systemd/system/keepalived.service`; the installer must call `restorecon -F` on the one final file so it receives the distribution's standard unit label.

Add a static assertion to the installer contract:

```js
  assert.match(installer, /restorecon -F "\$UNIT_FILE"/)
  assert.doesNotMatch(installer, /restorecon\s+-[^\n]*R[^\n]*\/etc\/systemd\/system/)
```

- [ ] **Step 5: Run all Keepalived and script verification**

Run:

```powershell
node --test scripts/keepalived-extra.test.mjs scripts/keepalived-runtime.test.mjs
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/install-keepalived-offline.sh
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/uninstall-keepalived.sh
& 'D:\tools\git\bin\bash.exe' -n extras/keepalived/configure-selinux.sh
pnpm test:scripts
git diff --check
```

Expected: Keepalived tests and the complete script suite report zero failures; all Bash syntax checks and `git diff --check` exit 0.

- [ ] **Step 6: Run packaging verification proportional to the Linux-only change**

Run:

```powershell
pnpm package
pnpm release:verify
```

Expected: Linux directory/archive contain the updated Keepalived installer, uninstaller, README, and executable script modes; Windows release continues to exclude the Linux-only module; checksum verification exits 0. This command copies the large offline resource tree, so run it only once after all focused tests are green.

- [ ] **Step 7: Commit documentation and final test contracts**

```powershell
git add -- extras/keepalived/README.md scripts/keepalived-extra.test.mjs
git commit -m "docs: explain direct Keepalived systemd unit"
```

- [ ] **Step 8: Record but do not execute live acceptance**

After separate authorization, live openEuler acceptance must run:

```bash
test -f /etc/systemd/system/keepalived.service
test ! -L /etc/systemd/system/keepalived.service
stat -c '%U:%G %a %F %C' /etc/systemd/system/keepalived.service
systemctl show keepalived.service -p LoadState -p ActiveState -p SubState -p UnitFileState -p FragmentPath --no-pager
grep -E '^(RequiresMountsFor|ExecStart)=' /etc/systemd/system/keepalived.service
```

Expected before reboot: root:root, mode 644, regular file, standard systemd unit label, FragmentPath exactly `/etc/systemd/system/keepalived.service`, enabled and active. A controlled server reboot must then show Keepalived automatically active and the expected VIP state without manually running `systemctl daemon-reload`.
