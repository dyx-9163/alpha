# Keepalived Release Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a verified Keepalived 2.4.2 operations module to Linux release packages with offline installation, SELinux Enforcing support, and recoverable uninstallation.

**Architecture:** Keep Linux-only assets together under `extras/keepalived/`. Node script tests enforce the shell safety contracts and source digest, while the release pipeline explicitly copies the module only into Linux packages and includes it in package checksums.

**Tech Stack:** Bash, openEuler DNF/systemd/SELinux tools, Node.js built-in test runner, AIFAR release scripts.

## Global Constraints

- Support only openEuler 24.03 LTS SP3 on x86_64.
- Install Keepalived 2.4.2 only under `/aifar/apps/keepalived`.
- Accept only `keepalived-2.4.2.tar.gz` with size `6350291` and SHA256 `76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b`.
- Use only DNF repositories already configured on the target; do not add public repositories.
- Do not create an active sample configuration, enable the service, or start the service during installation.
- Keep SELinux in its current enabled mode; never switch to Permissive, disable it, or generate broad `audit2allow` policy.
- Back up configuration and helper scripts before uninstallation changes service state or deletes files.
- Do not remove shared RPM dependencies, firewall rules, or backups.
- Include `extras/keepalived/` only in the Linux amd64 release package.
- Preserve all unrelated working-tree changes and commit only files listed by each task.

---

### Task 1: Establish the module contract and verified source installer

**Files:**
- Create: `scripts/keepalived-extra.test.mjs`
- Create: `extras/keepalived/keepalived-2.4.2.tar.gz`
- Create: `extras/keepalived/SHA256SUMS`
- Create: `extras/keepalived/install-keepalived-offline.sh`
- Remove after copying: `install-keepalived-offline.sh`

**Interfaces:**
- Consumes: verified source archive at `C:/Users/Administrator/Downloads/keepalived-2.4.2.tar.gz` and the existing root-level installer.
- Produces: `extras/keepalived/install-keepalived-offline.sh` as a zero-argument installer and the fixed module-level archive/checksum contract used by later tasks.

- [ ] **Step 1: Write the failing module tests**

Create `scripts/keepalived-extra.test.mjs` with the following initial content:

```js
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptsDir, '..')
const moduleDir = path.join(rootDir, 'extras', 'keepalived')
const archiveName = 'keepalived-2.4.2.tar.gz'
const archiveSha256 = '76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b'

function read(relativePath) {
  return readFileSync(path.join(moduleDir, relativePath), 'utf8')
}

function indexOfOrFail(source, fragment) {
  const index = source.indexOf(fragment)
  assert.notEqual(index, -1, `missing ${fragment}`)
  return index
}

test('Keepalived module contains the verified installer artifacts', () => {
  for (const relativePath of [
    archiveName,
    'SHA256SUMS',
    'install-keepalived-offline.sh'
  ]) {
    assert.doesNotThrow(() => readFileSync(path.join(moduleDir, relativePath)))
  }
})

test('Keepalived source archive has the pinned size and digest', () => {
  const archivePath = path.join(moduleDir, archiveName)
  const archive = readFileSync(archivePath)
  assert.equal(statSync(archivePath).size, 6350291)
  assert.equal(createHash('sha256').update(archive).digest('hex'), archiveSha256)
  assert.equal(read('SHA256SUMS'), `${archiveSha256}  ${archiveName}\n`)
})

test('installer verifies the archive before extraction and never activates Keepalived', () => {
  const installer = read('install-keepalived-offline.sh')
  assert.match(installer, /readonly APP_ROOT="\/aifar\/apps\/keepalived"/)
  assert.ok(indexOfOrFail(installer, 'sha256sum --check') < indexOfOrFail(installer, 'tar -tzf'))
  assert.doesNotMatch(installer, /systemctl\s+(?:enable|start|restart)|systemctl\s+enable\s+--now/)
  assert.doesNotMatch(installer, /cp\s+.*keepalived\.conf\.sample.*keepalived\.conf/)
})
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
node --test scripts/keepalived-extra.test.mjs
```

Expected: FAIL because `extras/keepalived/` and its required artifacts do not exist.

- [ ] **Step 3: Copy the verified archive and installer into the module**

First recheck the source, then copy it without transforming the binary archive:

```powershell
$source = 'C:\Users\Administrator\Downloads\keepalived-2.4.2.tar.gz'
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $source).Hash.ToLowerInvariant()
if ((Get-Item -LiteralPath $source).Length -ne 6350291) { throw 'Unexpected Keepalived archive size' }
if ($hash -ne '76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b') { throw 'Unexpected Keepalived archive digest' }
New-Item -ItemType Directory -Force 'extras\keepalived' | Out-Null
Copy-Item -LiteralPath $source -Destination 'extras\keepalived\keepalived-2.4.2.tar.gz'
Copy-Item -LiteralPath 'install-keepalived-offline.sh' -Destination 'extras\keepalived\install-keepalived-offline.sh'
```

Create `extras/keepalived/SHA256SUMS` exactly as:

```text
76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b  keepalived-2.4.2.tar.gz
```

Delete the superseded root-level `install-keepalived-offline.sh` with `apply_patch` only after the module copy is byte-for-byte readable.

- [ ] **Step 4: Add archive verification and the SELinux hook to the installer**

In `extras/keepalived/install-keepalived-offline.sh`, add these constants beside the existing archive name:

```bash
readonly SOURCE_ARCHIVE_SHA256="76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b"
readonly SELINUX_SCRIPT_NAME="configure-selinux.sh"
```

After the existing `SCRIPT_DIR=...` assignment, add:

```bash
SELINUX_SCRIPT="${SCRIPT_DIR}/${SELINUX_SCRIPT_NAME}"
```

Add this function before `build_and_install_keepalived`:

```bash
verify_source_archive() {
    local checksum_file="${WORK_DIR}/SHA256SUMS"

    printf '%s  %s\n' "$SOURCE_ARCHIVE_SHA256" "$SOURCE_ARCHIVE_NAME" >"$checksum_file"
    (
        cd "$SCRIPT_DIR"
        sha256sum --check "$checksum_file"
    ) || die "Keepalived 源码包 SHA256 校验失败"
    [[ "$(stat -c '%s' "$SOURCE_ARCHIVE")" == "6350291" ]] || die "Keepalived 源码包大小不匹配"
    tar -tzf "$SOURCE_ARCHIVE" >/dev/null || die "源码包损坏或不是有效的 tar.gz：$SOURCE_ARCHIVE"
}

configure_selinux_if_enabled() {
    if command -v getenforce >/dev/null 2>&1 && [[ "$(getenforce)" != "Disabled" ]]; then
        [[ -f "$SELINUX_SCRIPT" ]] || die "SELinux 已启用，但缺少脚本：$SELINUX_SCRIPT"
        bash "$SELINUX_SCRIPT"
    fi
}
```

Require `sha256sum` and `stat`, create `WORK_DIR` before verification, replace the existing direct `tar -tzf` validation with `verify_source_archive`, and call `configure_selinux_if_enabled` after `register_systemd_unit` and before `verify_installation`.

- [ ] **Step 5: Run the focused tests and verify GREEN**

Run:

```powershell
node --test scripts/keepalived-extra.test.mjs
```

Expected: all archive and installer tests PASS.

- [ ] **Step 6: Commit the installer and fixed source inputs**

```powershell
git add -- scripts/keepalived-extra.test.mjs extras/keepalived/keepalived-2.4.2.tar.gz extras/keepalived/SHA256SUMS extras/keepalived/install-keepalived-offline.sh
git commit -m "feat: add verified keepalived installer assets"
```

---

### Task 2: Add idempotent SELinux Enforcing adaptation

**Files:**
- Modify: `scripts/keepalived-extra.test.mjs`
- Create: `extras/keepalived/configure-selinux.sh`

**Interfaces:**
- Consumes: `APP_ROOT=/aifar/apps/keepalived` and the distribution reference paths for Keepalived labels.
- Produces: root-only `configure-selinux.sh` and `$APP_ROOT/var/lib/aifar/keepalived-selinux-fcontexts` ownership records consumed by the uninstaller.

- [ ] **Step 1: Add failing SELinux contract tests**

Append to `scripts/keepalived-extra.test.mjs`:

```js
test('SELinux script preserves mode and manages persistent distribution-derived labels', () => {
  const script = read('configure-selinux.sh')
  assert.match(script, /getenforce/)
  assert.match(script, /matchpathcon/)
  assert.match(script, /semanage fcontext/)
  assert.match(script, /restorecon/)
  assert.match(script, /keepalived-selinux-fcontexts/)
  assert.doesNotMatch(script, /setenforce|SELINUX\s*=\s*(?:disabled|permissive)|\/etc\/selinux\/config|audit2allow/i)
})

test('all Keepalived shell scripts use LF endings', () => {
  for (const relativePath of [
    'install-keepalived-offline.sh',
    'configure-selinux.sh'
  ]) {
    assert.doesNotMatch(read(relativePath), /\r/)
  }
})
```

The implementation may use the word `Disabled` only in a strict equality check and error message. If the broad assertion conflicts with that required check, narrow it to forbid `setenforce`, `SELINUX=disabled`, and `SELINUX=permissive` rather than forbidding diagnostic text.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
node --test scripts/keepalived-extra.test.mjs
```

Expected: FAIL because `configure-selinux.sh` does not exist.

- [ ] **Step 3: Implement the SELinux script**

Create `extras/keepalived/configure-selinux.sh` with this structure and exact safety model:

```bash
#!/usr/bin/env bash
set -Eeuo pipefail

readonly APP_ROOT="/aifar/apps/keepalived"
readonly RECORD_FILE="$APP_ROOT/var/lib/aifar/keepalived-selinux-fcontexts"

log() { printf '[keepalived-selinux] %s\n' "$*"; }
die() { printf '[keepalived-selinux] ERROR: %s\n' "$*" >&2; exit 1; }
require_command() { command -v "$1" >/dev/null 2>&1 || die "缺少必要命令：$1"; }

if [[ "$EUID" -ne 0 ]]; then
    require_command sudo
    exec sudo bash "$(readlink -f -- "${BASH_SOURCE[0]}")"
fi
[[ "$#" -eq 0 ]] || die "此脚本不接受参数"
require_command getenforce
[[ "$(getenforce)" != "Disabled" ]] || die "SELinux 当前为 Disabled；脚本不会修改系统 SELinux 模式"

require_command dnf
if ! command -v matchpathcon >/dev/null 2>&1 ||
   ! command -v restorecon >/dev/null 2>&1 ||
   ! command -v semanage >/dev/null 2>&1; then
    dnf --assumeyes --setopt=install_weak_deps=False install policycoreutils policycoreutils-python-utils selinux-policy-targeted
fi
for command_name in matchpathcon restorecon awk grep semanage; do require_command "$command_name"; done

reference_type() {
    local output context type
    output="$(matchpathcon -n "$1" 2>/dev/null)" || die "发行版策略没有参考标签：$1"
    output="${output##*$'\n'}"
    context="${output##* }"
    IFS=: read -r _ _ type _ <<<"$context"
    [[ -n "$type" && "$type" == *_t ]] || die "无法解析参考标签：$1"
    printf '%s\n' "$type"
}

mapping_context() {
    semanage fcontext -l -C | awk -v pattern="$1" '$1 == pattern { print $NF; exit }'
}

apply_mapping() {
    local pattern="$1" reference="$2" type current_context previous_type action
    type="$(reference_type "$reference")"
    current_context="$(mapping_context "$pattern")"
    if [[ -z "$current_context" ]]; then
        semanage fcontext -a -t "$type" "$pattern"
        action=created
        previous_type=-
    else
        IFS=: read -r _ _ previous_type _ <<<"$current_context"
        if [[ "$previous_type" == "$type" ]]; then
            action=unchanged
        else
            semanage fcontext -m -t "$type" "$pattern"
            action=updated
        fi
    fi
    printf '%s|%s|%s|%s\n' "$action" "$pattern" "${previous_type:--}" "$type" >>"$RECORD_FILE.tmp"
}

mkdir -p "$APP_ROOT/var/lib/aifar"
if [[ ! -s "$RECORD_FILE" ]]; then
    : >"$RECORD_FILE.tmp"
    apply_mapping '/aifar/apps/keepalived/sbin/keepalived' '/usr/sbin/keepalived'
    apply_mapping '/aifar/apps/keepalived/etc(/.*)?' '/etc/keepalived'
    apply_mapping '/aifar/apps/keepalived/scripts(/.*)?' '/usr/libexec/keepalived'
    apply_mapping '/aifar/apps/keepalived/var(/.*)?' '/var/lib/keepalived'
    apply_mapping '/aifar/apps/keepalived/run(/.*)?' '/run/keepalived'
    apply_mapping '/aifar/apps/keepalived/systemd/keepalived\.service' '/usr/lib/systemd/system/keepalived.service'
    install -o root -g root -m 600 "$RECORD_FILE.tmp" "$RECORD_FILE"
    rm -f -- "$RECORD_FILE.tmp"
fi

restorecon -RF "$APP_ROOT"
while IFS='|' read -r _ pattern _ expected_type; do
    current_context="$(mapping_context "$pattern")"
    [[ "$current_context" == *":${expected_type}:"* ]] || die "SELinux 映射校验失败：$pattern"
done <"$RECORD_FILE"
log "SELinux 模式保持为 $(getenforce)，Keepalived 标签已应用"
```

- [ ] **Step 4: Run Bash syntax and focused tests**

Run:

```powershell
bash -n extras/keepalived/configure-selinux.sh
node --test scripts/keepalived-extra.test.mjs
```

Expected: all installer and SELinux-specific tests PASS.

- [ ] **Step 5: Commit the SELinux adapter**

```powershell
git add -- scripts/keepalived-extra.test.mjs extras/keepalived/configure-selinux.sh
git commit -m "feat: add keepalived selinux adapter"
```

---

### Task 3: Add recoverable uninstallation and operator documentation

**Files:**
- Modify: `scripts/keepalived-extra.test.mjs`
- Create: `extras/keepalived/uninstall-keepalived.sh`
- Create: `extras/keepalived/README.md`

**Interfaces:**
- Consumes: `/aifar/apps/keepalived`, the systemd unit link, and the SELinux ownership record from Task 2.
- Produces: `/aifar/backups/keepalived-<UTC timestamp>/` plus a removed custom installation tree; shared RPMs, firewall rules, and backups remain untouched.

- [ ] **Step 1: Add failing uninstall and documentation tests**

Append to `scripts/keepalived-extra.test.mjs`:

```js
test('Keepalived module contains all release artifacts', () => {
  for (const relativePath of [
    'README.md',
    archiveName,
    'SHA256SUMS',
    'install-keepalived-offline.sh',
    'configure-selinux.sh',
    'uninstall-keepalived.sh'
  ]) {
    assert.doesNotThrow(() => readFileSync(path.join(moduleDir, relativePath)))
  }
})

test('Keepalived uninstaller uses LF endings', () => {
  assert.doesNotMatch(read('uninstall-keepalived.sh'), /\r/)
})

test('uninstaller verifies a backup before service changes and exact-path deletion', () => {
  const script = read('uninstall-keepalived.sh')
  const backupVerify = indexOfOrFail(script, 'sha256sum --check BACKUP.sha256')
  const serviceStop = indexOfOrFail(script, 'systemctl stop keepalived.service')
  const installDelete = indexOfOrFail(script, 'rm -rf -- "$APP_ROOT"')
  assert.ok(backupVerify < serviceStop)
  assert.ok(serviceStop < installDelete)
  assert.match(script, /readonly APP_ROOT="\/aifar\/apps\/keepalived"/)
  assert.match(script, /readlink -f -- "\$APP_ROOT"/)
  assert.match(script, /\/aifar\/backups\/keepalived-/)
  assert.doesNotMatch(script, /dnf\s+(?:remove|erase)|yum\s+(?:remove|erase)|firewall-cmd\s+--remove|rm -rf -- \/aifar\/backups/)
})

test('README documents zero-argument lifecycle and retained state', () => {
  const readme = read('README.md')
  assert.match(readme, /bash install-keepalived-offline\.sh/)
  assert.match(readme, /bash configure-selinux\.sh/)
  assert.match(readme, /bash uninstall-keepalived\.sh/)
  assert.match(readme, /\/aifar\/backups\/keepalived-/)
  assert.match(readme, /不会自动启动/)
  assert.match(readme, /不会删除.*RPM/)
  assert.match(readme, /不会删除.*防火墙/)
})
```

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
node --test scripts/keepalived-extra.test.mjs
```

Expected: FAIL because `uninstall-keepalived.sh` and `README.md` do not exist.

- [ ] **Step 3: Implement the recoverable uninstaller**

Create `extras/keepalived/uninstall-keepalived.sh`. Use these exact ordered components:

```bash
#!/usr/bin/env bash
set -Eeuo pipefail

readonly APP_ROOT="/aifar/apps/keepalived"
readonly BACKUP_ROOT="/aifar/backups"
readonly UNIT_LINK="/etc/systemd/system/keepalived.service"
readonly SELINUX_RECORD="$APP_ROOT/var/lib/aifar/keepalived-selinux-fcontexts"
BACKUP_DIR=""

log() { printf '[keepalived-uninstaller] %s\n' "$*"; }
die() { printf '[keepalived-uninstaller] ERROR: %s\n' "$*" >&2; exit 1; }
require_command() { command -v "$1" >/dev/null 2>&1 || die "缺少必要命令：$1"; }

if [[ "$EUID" -ne 0 ]]; then
    require_command sudo
    exec sudo bash "$(readlink -f -- "${BASH_SOURCE[0]}")"
fi
[[ "$#" -eq 0 ]] || die "此脚本不接受参数"
for command_name in readlink systemctl install cp find sort xargs sha256sum date dirname rm; do require_command "$command_name"; done
[[ -d "$APP_ROOT" ]] || die "未找到安装目录：$APP_ROOT"
[[ "$(readlink -f -- "$APP_ROOT")" == "$APP_ROOT" ]] || die "拒绝删除非预期安装路径"

unit_target="$(readlink -f -- "$UNIT_LINK" 2>/dev/null || true)"
if [[ -n "$unit_target" && "$unit_target" != "$APP_ROOT/systemd/keepalived.service" ]]; then
    die "系统 keepalived.service 不属于此安装：$unit_target"
fi

umask 077
BACKUP_DIR="$BACKUP_ROOT/keepalived-$(date -u +%Y%m%dT%H%M%SZ)"
install -d -o root -g root -m 700 "$BACKUP_DIR"
for relative in etc scripts systemd/keepalived.service var/lib/aifar/keepalived-selinux-fcontexts; do
    if [[ -e "$APP_ROOT/$relative" ]]; then
        install -d -m 700 "$BACKUP_DIR/$(dirname "$relative")"
        cp -a -- "$APP_ROOT/$relative" "$BACKUP_DIR/$relative"
    fi
done
cat >"$BACKUP_DIR/uninstall-manifest.txt" <<EOF
installed_root=$APP_ROOT
unit_target=${unit_target:-none}
created_at_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF
(
    cd "$BACKUP_DIR"
    find . -type f ! -name BACKUP.sha256 -print0 | sort -z | xargs -0 sha256sum >BACKUP.sha256
    test -s uninstall-manifest.txt
    sha256sum --check BACKUP.sha256
)

systemctl stop keepalived.service
systemctl disable keepalived.service || true
if [[ -L "$UNIT_LINK" && "$(readlink -f -- "$UNIT_LINK")" == "$APP_ROOT/systemd/keepalived.service" ]]; then
    rm -f -- "$UNIT_LINK"
fi
systemctl daemon-reload

if [[ -s "$SELINUX_RECORD" ]]; then
    require_command semanage
    while IFS='|' read -r action pattern previous_type applied_type; do
        case "$action" in
            created) semanage fcontext -d "$pattern" ;;
            updated) semanage fcontext -m -t "$previous_type" "$pattern" ;;
            unchanged) : ;;
            *) die "未知 SELinux 映射记录：$action" ;;
        esac
    done <"$SELINUX_RECORD"
fi

[[ "$(readlink -f -- "$APP_ROOT")" == "$APP_ROOT" ]] || die "删除前安装路径发生变化，已停止卸载"
rm -rf -- "$APP_ROOT"
[[ ! -e "$APP_ROOT" ]] || die "安装目录删除失败：$APP_ROOT"
if command -v restorecon >/dev/null 2>&1 && [[ -d /aifar/apps ]]; then restorecon -F /aifar/apps; fi
log "卸载完成，备份保留在：$BACKUP_DIR"
```

- [ ] **Step 4: Write the operator README**

Create `extras/keepalived/README.md` with these sections and commands:

````markdown
# Keepalived 2.4.2 离线工具

仅支持 openEuler 24.03 LTS SP3 x86_64，安装目录固定为 `/aifar/apps/keepalived`。

## 安装

保持本目录六个文件在一起，以 root 执行：

```bash
bash install-keepalived-offline.sh
```

安装程序校验源码包 SHA256，使用服务器当前 DNF 仓库安装编译依赖。它不会生成生产配置，也不会自动启动或启用 Keepalived。

## SELinux

安装脚本会在 SELinux 已启用时调用适配脚本，也可重复执行：

```bash
bash configure-selinux.sh
```

该脚本保持 Enforcing，不会关闭 SELinux 或切换 Permissive。

## 配置和启动

```bash
/aifar/apps/keepalived/sbin/keepalived -t -f /aifar/apps/keepalived/etc/keepalived/keepalived.conf
systemctl enable --now keepalived
```

## 卸载

```bash
bash uninstall-keepalived.sh
```

卸载前会备份到 `/aifar/backups/keepalived-<UTC时间戳>/`。脚本不会删除共享 RPM、备份或无法确认归属的防火墙规则。
````

- [ ] **Step 5: Run focused tests and Bash syntax checks**

Run:

```powershell
bash -n extras/keepalived/install-keepalived-offline.sh
bash -n extras/keepalived/configure-selinux.sh
bash -n extras/keepalived/uninstall-keepalived.sh
node --test scripts/keepalived-extra.test.mjs
```

Expected: all focused tests PASS and all three scripts parse successfully.

- [ ] **Step 6: Commit the lifecycle tools**

```powershell
git add -- scripts/keepalived-extra.test.mjs extras/keepalived/uninstall-keepalived.sh extras/keepalived/README.md
git commit -m "feat: add recoverable keepalived uninstall"
```

---

### Task 4: Include the module only in Linux releases

**Files:**
- Modify: `scripts/package-release.mjs`
- Modify: `scripts/release-pipeline.test.mjs`

**Interfaces:**
- Consumes: the complete `extras/keepalived/` module from Tasks 1-3.
- Produces: Linux packages containing `extras/keepalived/` with executable shell scripts and checksum entries; Windows packages exclude the module.

- [ ] **Step 1: Extend the release fixture and add a failing platform-scope test**

In `releaseFixture`, create representative required module files:

```js
write(root, 'extras/keepalived/README.md', 'keepalived tools\n')
write(root, 'extras/keepalived/keepalived-2.4.2.tar.gz', 'fixture archive')
write(root, 'extras/keepalived/SHA256SUMS', 'fixture checksum\n')
write(root, 'extras/keepalived/install-keepalived-offline.sh', '#!/usr/bin/env bash\n')
write(root, 'extras/keepalived/configure-selinux.sh', '#!/usr/bin/env bash\n')
write(root, 'extras/keepalived/uninstall-keepalived.sh', '#!/usr/bin/env bash\n')
```

Add this test:

```js
test('release includes Keepalived tools only in Linux packages and checksums them', (t) => {
  const root = releaseFixture(t)
  const result = runRelease(root)
  assert.equal(result.status, 0, result.stderr)

  const deployment = path.join(root, 'deploy', 'deployment')
  const linux = path.join(deployment, 'aifar-fixture-9.8.7-linux-amd64')
  const windows = path.join(deployment, 'aifar-fixture-9.8.7-windows-amd64')
  assert.equal(existsSync(path.join(linux, 'extras/keepalived/install-keepalived-offline.sh')), true)
  assert.equal(existsSync(path.join(windows, 'extras/keepalived')), false)
  assert.match(readFileSync(path.join(linux, 'checksums.txt'), 'utf8'), /extras\/keepalived\/keepalived-2\.4\.2\.tar\.gz/)
})
```

- [ ] **Step 2: Run the release test and verify RED**

Run:

```powershell
node --test --test-name-pattern "Keepalived tools only" scripts/release-pipeline.test.mjs
```

Expected: FAIL because the Linux package does not contain `extras/keepalived/`.

- [ ] **Step 3: Add platform-specific package entries**

Add this helper beside `commonEntries` in `scripts/package-release.mjs`:

```js
const keepalivedEntry = {
  kind: 'dir',
  source: 'extras/keepalived',
  target: 'extras/keepalived',
  required: true,
  executables: [
    'install-keepalived-offline.sh',
    'configure-selinux.sh',
    'uninstall-keepalived.sh'
  ]
}
```

Add `packageEntries: [keepalivedEntry]` to the Linux target and `packageEntries: []` to the Windows target. After directory copying in `copyEntry`, apply executable modes:

```js
if (entry.kind === 'dir') {
  cpSync(sourcePath, targetPath, { dereference: true, force: true, recursive: true })
  for (const relativePath of entry.executables || []) {
    chmodBestEffort(path.join(targetPath, relativePath), 0o755)
  }
  return
}
```

In `buildPackage`, copy platform entries after `commonEntries`:

```js
for (const entry of commonEntries) copyEntry(entry, packageDir)
for (const entry of target.packageEntries || []) copyEntry(entry, packageDir)
```

In `ensureRequiredBuildOutputs`, verify both common and per-target required entries before staging:

```js
const requiredEntries = [
  ...commonEntries,
  ...targets.flatMap((target) => target.packageEntries || [])
].filter((entry) => entry.required)
for (const entry of requiredEntries) {
  const sourcePath = path.join(rootDir, entry.source)
  if (!existsSync(sourcePath)) throw new Error(`Missing required package input: ${entry.source}`)
}
```

- [ ] **Step 4: Run release tests and checksum verification**

Run:

```powershell
node --test scripts/release-pipeline.test.mjs
```

Expected: all release-pipeline tests PASS, including Linux inclusion and Windows exclusion.

- [ ] **Step 5: Commit release integration**

```powershell
git add -- scripts/package-release.mjs scripts/release-pipeline.test.mjs
git commit -m "build: package keepalived tools on linux"
```

---

### Task 5: Run full verification and close the documentation loop

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: all module and release-pipeline changes.
- Produces: verified Linux/Windows release artifacts and a concise reusable project memory entry.

- [ ] **Step 1: Run all script tests**

Run:

```powershell
pnpm test:scripts
```

Expected: all script tests PASS with zero failures.

- [ ] **Step 2: Run the complete local release gate**

Run:

```powershell
pnpm test:local
```

Expected: backend, frontend, scripts, builds, package staging, and `release:verify` all PASS. Both platform directories and archives are regenerated under `deploy/deployment/`.

- [ ] **Step 3: Inspect the real release outputs**

Run:

```powershell
$linux = 'deploy\deployment\aifar-deployment-0.1.0-linux-amd64'
$windows = 'deploy\deployment\aifar-deployment-0.1.0-windows-amd64'
Get-ChildItem -LiteralPath "$linux\extras\keepalived" | Select-Object Name,Length
if (Test-Path -LiteralPath "$windows\extras\keepalived") { throw 'Windows package unexpectedly contains Keepalived tools' }
Select-String -LiteralPath "$linux\checksums.txt" -Pattern 'extras/keepalived/keepalived-2.4.2.tar.gz'
```

Expected: all six artifacts exist in Linux, none exist in Windows, and the Linux checksum file lists the source archive.

- [ ] **Step 4: Run final safety checks**

Run:

```powershell
git -c safe.directory=D:/workspace/aifar-deployment diff --check
git -c safe.directory=D:/workspace/aifar-deployment status --short
```

Expected: no whitespace errors; only intended task files plus pre-existing unrelated changes are present.

- [ ] **Step 5: Append the reusable conclusion to memory**

Append one concise `2026-07-18` problem/conclusion pair to `memory.md` recording the Linux-only module path, archive digest, safe uninstall backup behavior, SELinux Enforcing behavior, and verification result. Do not record credentials or long logs.

- [ ] **Step 6: Commit only the memory update if repository convention requires it**

Do not stage unrelated existing changes. If `memory.md` is intentionally kept as a working-tree notebook in this branch, leave it modified and report that fact rather than mixing it into an implementation commit.
