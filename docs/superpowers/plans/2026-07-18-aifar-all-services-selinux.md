# AIFAR All-Services SELinux Script Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship one zero-argument, Linux-only shell script that safely discovers installed AIFAR services and applies, verifies, and rolls back their required SELinux port and file-context mappings.

**Architecture:** Add a self-contained operations module under `extras/selinux/`. The shell script uses an explicit service whitelist, validates all discovered ports and paths, records the current run's local policy changes under `/var/lib/aifar-selinux/transactions`, and restores only those changes on failure. Node contract tests run the script against fake SELinux/systemd/Docker commands, while the existing release tests enforce Linux-only packaging and executable tar modes.

**Tech Stack:** Bash, openEuler SELinux tools (`getenforce`, `semanage`, `matchpathcon`, `restorecon`, `ausearch`), systemd CLI, Docker CLI, Node.js built-in test runner, existing release-packaging scripts.

## Global Constraints

- Support openEuler 24.03 LTS SP3 x86_64 and the managed base `/aifar/apps`.
- The operator command is exactly `bash configure-all-selinux.sh`; no arguments are required.
- Use only DNF repositories already configured on the host; do not add public repositories.
- Preserve the entry SELinux mode exactly; Disabled mode is an error.
- Never call `setenforce`, edit `/etc/selinux/config`, install an `audit2allow`-generated policy, manage firewall rules, or restart services.
- Cover Docker/`aifar-agent`, AIFAR Runtime, MySQL, MySQL Router, Redis/Sentinel/Cluster, MinIO, Nacos, Keepalived, and HTTPS ingress.
- Skip absent components, but fail closed for malformed installed configuration, conflicting local rules, missing required types, or verification failures.
- Preserve live HTTPS ingress `:Z` MCS categories and never run `restorecon` on those live private mounts.
- Keep unrelated dirty and untracked repository files untouched.

---

### Task 1: Lock the public contract and preflight behavior

**Files:**
- Create: `scripts/selinux-extra.test.mjs`
- Create: `extras/selinux/configure-all-selinux.sh`

**Interfaces:**
- Consumes: no earlier task output.
- Produces: a zero-argument Bash entry point with `main`, `ensure_root`, `validate_platform`, `ensure_selinux_tools`, `validate_port`, `canonical_managed_path`, `set_status`, and `print_summary` functions. Later tasks extend these functions without changing the operator command.

- [ ] **Step 1: Write failing static contract tests**

Create `scripts/selinux-extra.test.mjs` with repository-level assertions:

```js
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptsDir, '..')
const moduleDir = path.join(rootDir, 'extras', 'selinux')
const scriptPath = path.join(moduleDir, 'configure-all-selinux.sh')
const serviceOrder = [
  'docker', 'aifar-agent', 'aifar-runtime', 'mysql', 'mysql-router',
  'redis', 'minio', 'nacos', 'keepalived', 'https-ingress'
]

test('aggregate SELinux script exposes the approved zero-argument contract', () => {
  const source = readFileSync(scriptPath, 'utf8')
  assert.match(source, /^#!\/usr\/bin\/env bash\n/)
  assert.doesNotMatch(source, /\r/)
  assert.match(source, /main "\$@"/)
  assert.match(source, /\[ "\$#" -eq 0 \]/)
  for (const service of serviceOrder) assert.match(source, new RegExp(service.replace('-', '\\-')))
})

test('aggregate SELinux script cannot weaken host policy', () => {
  const source = readFileSync(scriptPath, 'utf8')
  assert.doesNotMatch(source, /setenforce|\/etc\/selinux\/config|audit2allow|firewall-cmd/i)
  assert.doesNotMatch(source, /systemctl\s+(?:start|stop|restart|enable|disable)/)
  assert.match(source, /semanage fcontext/)
  assert.match(source, /semanage port/)
  assert.match(source, /restorecon/)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `node --test scripts/selinux-extra.test.mjs`

Expected: FAIL with `ENOENT` for `extras/selinux/configure-all-selinux.sh`.

- [ ] **Step 3: Add the preflight and reporting skeleton**

Create the script with strict mode, fixed service order, zero-argument validation, privilege escalation, openEuler/x86_64 checks, exact mode preservation, tool preparation, safe scalar validators, and the final report:

```bash
#!/usr/bin/env bash
set -Eeuo pipefail

readonly MANAGED_BASE="/aifar/apps"
readonly TRANSACTION_BASE="/var/lib/aifar-selinux/transactions"
readonly SERVICE_ORDER="docker aifar-agent aifar-runtime mysql mysql-router redis minio nacos keepalived https-ingress"
ENTRY_MODE=""
declare -A SERVICE_STATUS=()

die() { printf '[aifar-selinux] ERROR: %s\n' "$*" >&2; exit 1; }
log() { printf '[aifar-selinux] %s\n' "$*"; }
set_status() { SERVICE_STATUS["$1"]="$2"; }

ensure_root() {
  if [[ $(id -u) -eq 0 ]]; then return 0; fi
  command -v sudo >/dev/null 2>&1 || die 'root privileges are required'
  exec sudo -n bash "$0" "$@"
}

validate_platform() {
  [[ $(uname -m) == x86_64 ]] || die 'only x86_64 is supported'
  [[ -r /etc/os-release ]] || die '/etc/os-release is unavailable'
  # shellcheck disable=SC1091
  . /etc/os-release
  [[ ${ID:-} == openEuler ]] || die 'only openEuler is supported'
  [[ ${VERSION_ID:-} == 24.03* ]] || die 'openEuler 24.03 is required'
}

validate_port() {
  [[ $1 =~ ^[0-9]+$ ]] || return 1
  (( 10#$1 >= 1 && 10#$1 <= 65535 ))
}

canonical_managed_path() {
  local root=$1 candidate=$2 resolved
  [[ $root == /* && $candidate == /* ]] || return 1
  resolved=$(readlink -m -- "$candidate") || return 1
  [[ $resolved == "$root" || $resolved == "$root"/* ]] || return 1
  printf '%s\n' "$resolved"
}

ensure_selinux_tools() {
  local command_name
  for command_name in getenforce semanage matchpathcon restorecon; do
    command -v "$command_name" >/dev/null 2>&1 && continue
    command -v dnf >/dev/null 2>&1 || die "missing $command_name and dnf"
    dnf -y install policycoreutils policycoreutils-python-utils selinux-policy-targeted
    break
  done
  for command_name in getenforce semanage matchpathcon restorecon; do
    command -v "$command_name" >/dev/null 2>&1 || die "missing required command: $command_name"
  done
}

print_summary() {
  local service
  for service in $SERVICE_ORDER; do printf '%-20s %s\n' "$service" "${SERVICE_STATUS[$service]:-SKIPPED}"; done
  printf '%-20s %s (unchanged)\n' 'SELinux mode' "$ENTRY_MODE"
  printf '%-20s SUCCESS\n' 'Result'
}

main() {
  [[ $# -eq 0 ]] || die 'this script accepts no arguments'
  ensure_root "$@"
  validate_platform
  ensure_selinux_tools
  ENTRY_MODE=$(getenforce)
  [[ $ENTRY_MODE != Disabled ]] || die 'SELinux is Disabled; no policy was changed'
  print_summary
  [[ $(getenforce) == "$ENTRY_MODE" ]] || die 'SELinux mode changed unexpectedly'
}

main "$@"
```

- [ ] **Step 4: Run static and syntax tests**

Run:

```bash
node --test scripts/selinux-extra.test.mjs
bash -n extras/selinux/configure-all-selinux.sh
```

Expected: both commands exit 0.

- [ ] **Step 5: Commit the contract and skeleton**

```bash
git add scripts/selinux-extra.test.mjs extras/selinux/configure-all-selinux.sh
git commit -m "test: define aggregate selinux script contract"
```

---

### Task 2: Implement persistent mappings, transaction recording, and rollback

**Files:**
- Modify: `scripts/selinux-extra.test.mjs`
- Modify: `extras/selinux/configure-all-selinux.sh`

**Interfaces:**
- Consumes: Task 1 validators and status map.
- Produces: `begin_transaction`, `record_port_change`, `record_fcontext_change`, `ensure_port_type`, `reference_type`, `ensure_fcontext`, `safe_restorecon`, `verify_file_type`, `rollback_transaction`, and `finish_transaction`. Service adapters in Task 3 call only these helpers.

- [ ] **Step 1: Add failing fake-command transaction tests**

Extend the Node test with a temporary fake-command harness. The harness copies the script to a temporary fixture, writes fake `getenforce`, `semanage`, `matchpathcon`, `restorecon`, `systemctl`, `docker`, `dnf`, `uname`, and `id` executables, prepends them to `PATH`, and records invocations in `AIFAR_TEST_LOG`. Add tests with these exact outcomes:

```js
test('new local rules are rolled back when later verification fails', (t) => {
  const fixture = selinuxFixture(t, {
    installed: ['mysql'],
    failRestorecon: true
  })
  const result = runAggregate(fixture)
  assert.notEqual(result.status, 0)
  const calls = readFileSync(fixture.log, 'utf8')
  assert.match(calls, /semanage port -a -t mysqld_port_t -p tcp 3306/)
  assert.match(calls, /semanage port -d -p tcp 3306/)
  assert.match(result.stderr, /rollback/i)
})

test('matching rules are idempotent and are not deleted', (t) => {
  const fixture = selinuxFixture(t, {
    installed: ['mysql'],
    portMappings: { '3306/tcp': 'mysqld_port_t' }
  })
  const result = runAggregate(fixture)
  assert.equal(result.status, 0, result.stderr)
  const calls = readFileSync(fixture.log, 'utf8')
  assert.doesNotMatch(calls, /semanage port -(?:a|d|m)/)
  assert.match(result.stdout, /mysql\s+UNCHANGED/)
})

test('conflicting local port ownership fails without overwriting it', (t) => {
  const fixture = selinuxFixture(t, {
    installed: ['mysql'],
    localPortMappings: { '3306/tcp': 'custom_db_port_t' }
  })
  const result = runAggregate(fixture)
  assert.notEqual(result.status, 0)
  assert.doesNotMatch(readFileSync(fixture.log, 'utf8'), /semanage port -m/)
  assert.match(result.stderr, /custom_db_port_t/)
})
```

The fake `semanage` must answer `port -l`, `port -l -C`, `fcontext -l -C`, and record `-a/-m/-d` mutations so rollback can be asserted without a real SELinux host.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `node --test scripts/selinux-extra.test.mjs`

Expected: FAIL because transaction and mapping helpers are missing.

- [ ] **Step 3: Implement the transaction journal and traps**

Add root-only transaction initialization and a reverse-order rollback journal:

```bash
TRANSACTION_DIR=""
JOURNAL_FILE=""
TRANSACTION_ACTIVE=0

begin_transaction() {
  umask 077
  install -d -m 0700 "$TRANSACTION_BASE"
  TRANSACTION_DIR="$TRANSACTION_BASE/$(date -u +%Y%m%dT%H%M%SZ)-$$"
  install -d -m 0700 "$TRANSACTION_DIR"
  JOURNAL_FILE="$TRANSACTION_DIR/journal.tsv"
  : >"$JOURNAL_FILE"
  TRANSACTION_ACTIVE=1
}

journal() { printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" >>"$JOURNAL_FILE"; }

rollback_transaction() {
  [[ $TRANSACTION_ACTIVE -eq 1 && -s $JOURNAL_FILE ]] || return 0
  tac "$JOURNAL_FILE" | while IFS=$'\t' read -r action key applied previous; do
    case "$action" in
      port-created) semanage port -d -p tcp "$key" ;;
      fcontext-created) semanage fcontext -d "$key" ;;
      fcontext-updated) semanage fcontext -m -t "$previous" "$key" ;;
    esac
  done
  TRANSACTION_ACTIVE=0
}

on_error() {
  local rc=$?
  trap - ERR INT TERM
  rollback_transaction || true
  printf '[aifar-selinux] rollback transaction: %s\n' "$TRANSACTION_DIR" >&2
  exit "$rc"
}
```

Install `trap on_error ERR INT TERM` only after `begin_transaction`. Successful completion sets `TRANSACTION_ACTIVE=0` but retains the journal.

- [ ] **Step 4: Implement conflict-safe port and fcontext helpers**

Use exact local ownership checks and distribution-reference types:

```bash
local_port_type() {
  semanage port -l -C 2>/dev/null | awk -v p="$1" '$2=="tcp" {for(i=3;i<=NF;i++){gsub(",","",$i); if($i==p){print $1; exit}}}'
}

effective_port_has_type() {
  semanage port -l 2>/dev/null | awk -v t="$1" -v p="$2" '
    $1==t && $2=="tcp" {for(i=3;i<=NF;i++){gsub(",","",$i); n=split($i,r,"-"); if((n==1&&r[1]==p)||(n==2&&p>=r[1]&&p<=r[2])) found=1}}
    END{exit(found?0:1)}'
}

ensure_port_type() {
  local component=$1 type=$2 port=$3 local_type
  validate_port "$port" || die "$component has invalid port: $port"
  local_type=$(local_port_type "$port" || true)
  [[ -z $local_type || $local_type == "$type" ]] || die "$component port $port belongs to $local_type"
  effective_port_has_type "$type" "$port" && return 1
  semanage port -a -t "$type" -p tcp "$port"
  journal port-created "$port" "$type" ''
  effective_port_has_type "$type" "$port" || die "$component port verification failed: $port"
  return 0
}

reference_type() {
  matchpathcon "$1" 2>/dev/null | awk '{n=split($2,c,":"); if(n>=3) print c[3]}'
}

ensure_fcontext() {
  local component=$1 pattern=$2 reference=$3 type previous
  type=$(reference_type "$reference")
  [[ $type == *_t ]] || die "$component reference type unavailable: $reference"
  previous=$(semanage fcontext -l -C 2>/dev/null | awk -v p="$pattern" '$1==p {print $NF; exit}')
  if [[ -z $previous ]]; then
    semanage fcontext -a -t "$type" "$pattern"
    journal fcontext-created "$pattern" "$type" ''
  elif [[ $previous != *":$type:"* && $previous != "$type" ]]; then
    semanage fcontext -m -t "$type" "$pattern"
    journal fcontext-updated "$pattern" "$type" "$(context_type "$previous")"
  else
    return 1
  fi
  return 0
}
```

Add `safe_restorecon` and `verify_file_type` so paths are canonicalized under the component root before relabeling and the effective post-`restorecon` type must equal the reference type.

- [ ] **Step 5: Run focused tests**

Run:

```bash
node --test scripts/selinux-extra.test.mjs
bash -n extras/selinux/configure-all-selinux.sh
```

Expected: transaction, rollback, idempotence, and conflict tests PASS.

- [ ] **Step 6: Commit the policy transaction core**

```bash
git add scripts/selinux-extra.test.mjs extras/selinux/configure-all-selinux.sh
git commit -m "feat: add transactional selinux rule helpers"
```

---

### Task 3: Add the complete service whitelist and safe discovery adapters

**Files:**
- Modify: `scripts/selinux-extra.test.mjs`
- Modify: `extras/selinux/configure-all-selinux.sh`

**Interfaces:**
- Consumes: Task 2 `ensure_port_type`, `ensure_fcontext`, `safe_restorecon`, validators, transaction journal, and statuses.
- Produces: `configure_docker`, `configure_aifar_agent`, `configure_aifar_runtime`, `configure_mysql`, `configure_mysql_router`, `configure_redis`, `configure_minio`, `configure_nacos`, `configure_keepalived`, and `configure_https_ingress`. Each function returns 0 for installed-and-valid, 10 for absent, and non-zero for failure; `run_component NAME FUNCTION` converts that result to `APPLIED`, `UNCHANGED`, `SKIPPED`, or `FAILED`.

- [ ] **Step 1: Add failing service-matrix tests**

Add table-driven fixture cases asserting discovery, actual-port parsing, and safe fallbacks:

```js
const serviceCases = [
  ['docker', '2376', 'docker_port_t'],
  ['mysql', '3307', 'mysqld_port_t'],
  ['mysql-router', '7446', 'mysqld_port_t'],
  ['redis', '6380', 'redis_port_t'],
  ['minio', '9100', 'http_port_t'],
  ['aifar-runtime', '38000', 'http_port_t']
]

for (const [service, port, type] of serviceCases) {
  test(`${service} applies its discovered port type`, (t) => {
    const fixture = selinuxFixture(t, { installed: [service], configuredPort: port })
    const result = runAggregate(fixture)
    assert.equal(result.status, 0, result.stderr)
    assert.match(readFileSync(fixture.log, 'utf8'), new RegExp(`semanage port -a -t ${type} -p tcp ${port}`))
  })
}

test('redis cluster derives and validates the bus port', (t) => {
  const fixture = selinuxFixture(t, { installed: ['redis'], configuredPort: '6380', redisCluster: true })
  const result = runAggregate(fixture)
  assert.equal(result.status, 0, result.stderr)
  assert.match(readFileSync(fixture.log, 'utf8'), /redis_port_t -p tcp 16380/)
})

test('nacos verifies listeners but does not mislabel grpc or raft as HTTP', (t) => {
  const fixture = selinuxFixture(t, { installed: ['nacos'] })
  const result = runAggregate(fixture)
  assert.equal(result.status, 0, result.stderr)
  const calls = readFileSync(fixture.log, 'utf8')
  assert.doesNotMatch(calls, /http_port_t -p tcp (?:7848|9848|9849)/)
})

test('running HTTPS ingress preserves private MCS labels', (t) => {
  const fixture = selinuxFixture(t, { installed: ['https-ingress'], ingressRunning: true })
  const result = runAggregate(fixture)
  assert.equal(result.status, 0, result.stderr)
  const calls = readFileSync(fixture.log, 'utf8')
  assert.doesNotMatch(calls, /restorecon.*(?:conf\.d|tls)/)
  assert.match(calls, /stat.*(?:conf\.d|tls)/)
})

test('an out-of-root discovered path fails before semanage or restorecon', (t) => {
  const fixture = selinuxFixture(t, { installed: ['minio'], dataDir: '/srv/not-owned' })
  const result = runAggregate(fixture)
  assert.notEqual(result.status, 0)
  assert.doesNotMatch(readFileSync(fixture.log, 'utf8'), /fcontext.*\/srv\/not-owned|restorecon.*\/srv\/not-owned/)
})
```

Also add one absent-host test expecting all ten components to be `SKIPPED` and no `semanage` mutation.

- [ ] **Step 2: Run the service tests to verify they fail**

Run: `node --test scripts/selinux-extra.test.mjs`

Expected: FAIL because service adapters are not implemented.

- [ ] **Step 3: Implement unit ownership and configuration readers**

Add safe readers that never execute configuration as shell:

```bash
unit_fragment() { systemctl show -p FragmentPath --value "$1" 2>/dev/null || true; }
unit_exec_start() { systemctl show -p ExecStart --value "$1" 2>/dev/null || true; }
read_key_value() { awk -F= -v k="$2" '$1==k {print substr($0,index($0,"=")+1); exit}' "$1" 2>/dev/null; }
read_conf_directive() { awk -v k="$2" '$1==k {print $2; exit}' "$1" 2>/dev/null; }

managed_unit_present() {
  local unit=$1 expected_root=$2 fragment
  fragment=$(unit_fragment "$unit")
  [[ -n $fragment ]] || return 1
  [[ $(unit_exec_start "$unit") == *"$expected_root"* ]] || die "$unit is not owned by $expected_root"
}
```

Use targeted regular expressions for ports from known unit/config formats. Every extracted port passes `validate_port`; every extracted path passes `canonical_managed_path` against its exact component root.

- [ ] **Step 4: Implement service adapters using one explicit matrix**

Implement the ten adapters with these exact policy decisions:

```bash
configure_mysql() {
  local root="$MANAGED_BASE/mysql" port changed=0
  [[ -d $root || -n $(unit_fragment aifar-mysql.service) ]] || return 10
  port=$(read_key_value "$root/conf/my.cnf" port || true)
  port=${port:-3306}
  ensure_port_type mysql mysqld_port_t "$port" && changed=1 || true
  ensure_fcontext mysql "$root/mysql/bin/mysqld" /usr/sbin/mysqld && changed=1 || true
  ensure_fcontext mysql "$root/conf(/.*)?" /etc/my.cnf && changed=1 || true
  ensure_fcontext mysql "$root/data(/.*)?" /var/lib/mysql && changed=1 || true
  ensure_fcontext mysql "$root/logs(/.*)?" /var/log/mysqld.log && changed=1 || true
  apply_component_contexts mysql "$root" "$changed"
}

configure_redis() {
  local root="$MANAGED_BASE/redis" port sentinel cluster changed=0
  [[ -d $root || -n $(unit_fragment aifar-redis.service) ]] || return 10
  port=$(read_conf_directive "$root/conf/redis.conf" port); port=${port:-6379}
  sentinel=$(read_conf_directive "$root/conf/sentinel.conf" port || true)
  cluster=$(read_conf_directive "$root/conf/redis.conf" cluster-enabled || true)
  ensure_port_type redis redis_port_t "$port" && changed=1 || true
  [[ -z $sentinel ]] || { ensure_port_type redis redis_port_t "$sentinel" && changed=1 || true; }
  [[ $cluster != yes ]] || { ensure_port_type redis redis_port_t "$((10#$port + 10000))" && changed=1 || true; }
  apply_reference_tree redis "$root" /usr/bin/redis-server /etc/redis /var/lib/redis /var/log/redis "$changed"
}
```

Apply the same explicit structure to:

- Docker: parse `-H tcp://...:<port>` from managed `docker.service`; map its data root from daemon JSON only when under `/aifar/apps/docker`; map `/usr/local/bin` executables narrowly and agent paths `/etc/aifar`, `/var/lib/aifar-agent`, `/var/log/aifar-agent` from generic reference directories.
- AIFAR Runtime: derive published ports from `/aifar/apps/admin/runtime/agent/runtime-spec.json` with a strict numeric JSON-line parser and validate bind sources discovered by `docker inspect` only for containers with `aifar.app=aifar`; map exact sources below `/aifar/apps/admin` to the target's `container_file_t` reference.
- MySQL Router: parse base port and map base through base+3 as `mysqld_port_t`; use MySQL or generic reference types for binary/config/state.
- MinIO: parse `MINIO_OPTS`, unit arguments, and `minio.env`; default only after install evidence; map binary/config/data/log/run using generic reference paths and reject configured data paths outside the managed MinIO root.
- Nacos: parse `server.port`, verify `8848/9848/9849/7848` or their configured equivalents, map JDK/Nacos executable/config/data/log paths using generic references, and do not add TCP types for gRPC/Raft.
- Keepalived: require the unit to reference `/aifar/apps/keepalived`; resolve labels from `/usr/sbin/keepalived`, `/etc/keepalived`, `/usr/libexec/keepalived`, `/var/lib/keepalived`, and `/run/keepalived` exactly as the existing component script does.
- HTTPS ingress: derive the module directory from its managed unit, restrict it to an `extras/aifar-https-ingress` directory, verify `80/443` effective HTTP mappings, verify `container_file_t` on running private `:Z` sources without `restorecon`, and use persistent base mappings only while stopped.

Use `run_component` to count helper mutations. A component with zero mutations and successful verification is `UNCHANGED`; one or more mutations is `APPLIED`; return 10 is `SKIPPED`.

- [ ] **Step 5: Add component failure diagnostics**

On a component error, set `FAILED`, print its name and expected/actual path or port, and print only recent AVC records when available:

```bash
print_recent_avc() {
  command -v ausearch >/dev/null 2>&1 || return 0
  ausearch -m AVC,USER_AVC -ts recent 2>/dev/null | tail -n 80 || true
}

run_component() {
  local name=$1 function_name=$2 before=$MUTATION_COUNT rc
  if "$function_name"; then
    [[ $MUTATION_COUNT -gt $before ]] && set_status "$name" APPLIED || set_status "$name" UNCHANGED
    return 0
  fi
  rc=$?
  [[ $rc -eq 10 ]] && { set_status "$name" SKIPPED; return 0; }
  set_status "$name" FAILED
  print_recent_avc
  return "$rc"
}
```

- [ ] **Step 6: Run the full service-fixture suite**

Run:

```bash
node --test scripts/selinux-extra.test.mjs
bash -n extras/selinux/configure-all-selinux.sh
```

Expected: all service discovery, validation, MCS-preservation, idempotence, and rollback tests PASS.

- [ ] **Step 7: Commit the complete aggregate script**

```bash
git add scripts/selinux-extra.test.mjs extras/selinux/configure-all-selinux.sh
git commit -m "feat: configure selinux for all installed services"
```

---

### Task 4: Document and package the module only for Linux

**Files:**
- Create: `extras/selinux/README.md`
- Modify: `scripts/package-release.mjs:40-60`
- Modify: `scripts/release-pipeline.test.mjs:25-45,178-203`
- Modify: `scripts/selinux-extra.test.mjs`

**Interfaces:**
- Consumes: Task 3 final script path and zero-argument contract.
- Produces: `selinuxEntry` package metadata and release tests that guarantee Linux-only inclusion, checksum coverage, and `0755` tar mode.

- [ ] **Step 1: Write failing documentation and release tests**

Add README assertions to `scripts/selinux-extra.test.mjs`:

```js
test('README documents direct execution and safety boundaries', () => {
  const readme = readFileSync(path.join(moduleDir, 'README.md'), 'utf8')
  assert.match(readme, /bash configure-all-selinux\.sh/)
  assert.match(readme, /openEuler 24\.03 LTS SP3/)
  assert.match(readme, /\/var\/lib\/aifar-selinux\/transactions/)
  assert.match(readme, /Enforcing/)
  assert.match(readme, /APPLIED|UNCHANGED|SKIPPED|FAILED/)
  assert.doesNotMatch(readme, /setenforce 0|SELINUX=disabled/i)
})
```

Extend the release fixture to create `extras/selinux/README.md` and `extras/selinux/configure-all-selinux.sh`. Extend the Linux-only release test to assert:

```js
assert.equal(existsSync(path.join(linux, 'extras/selinux/configure-all-selinux.sh')), true)
assert.equal(existsSync(path.join(windows, 'extras/selinux')), false)
assert.match(readFileSync(path.join(linux, 'checksums.txt'), 'utf8'), /extras\/selinux\/configure-all-selinux\.sh/)
assert.match(listing.stdout, /^-rwxr-xr-x.*extras\/selinux\/configure-all-selinux\.sh$/m)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `node --test scripts/selinux-extra.test.mjs scripts/release-pipeline.test.mjs`

Expected: FAIL because README and `selinuxEntry` are missing.

- [ ] **Step 3: Write the operator README**

Document the exact direct command, supported host, covered services, automatic DNF prerequisite behavior, statuses, transaction directory, Disabled refusal, live ingress MCS protection, and the explicit non-goals. Include this primary invocation:

```bash
cd extras/selinux
bash configure-all-selinux.sh
```

State that no service is restarted and no firewall rule is changed.

- [ ] **Step 4: Add the Linux-only package entry**

Add beside `keepalivedEntry`:

```js
const selinuxEntry = {
  kind: 'dir',
  source: 'extras/selinux',
  target: 'extras/selinux',
  required: true,
  executables: ['configure-all-selinux.sh']
}
```

Change only the Linux target to:

```js
packageEntries: [keepalivedEntry, selinuxEntry],
```

Keep the Windows target's `packageEntries: []` unchanged.

- [ ] **Step 5: Run focused script and release tests**

Run: `pnpm test:scripts`

Expected: all script tests PASS, including Linux inclusion, Windows exclusion, checksum coverage, and archive mode.

- [ ] **Step 6: Commit documentation and packaging**

```bash
git add extras/selinux/README.md scripts/package-release.mjs scripts/release-pipeline.test.mjs scripts/selinux-extra.test.mjs
git commit -m "build: package aggregate selinux tools on linux"
```

---

### Task 5: Run completion gates and record verified boundaries

**Files:**
- Modify only if a gate exposes a defect: `extras/selinux/configure-all-selinux.sh`, `extras/selinux/README.md`, `scripts/selinux-extra.test.mjs`, `scripts/release-pipeline.test.mjs`, `scripts/package-release.mjs`

**Interfaces:**
- Consumes: all earlier tasks.
- Produces: verified local implementation and Linux release artifacts. It does not mutate a target server.

- [ ] **Step 1: Run formatting and focused checks**

Run:

```bash
bash -n extras/selinux/configure-all-selinux.sh
node --test scripts/selinux-extra.test.mjs
git diff --check
```

Expected: all commands exit 0 and no CRLF or whitespace errors are reported.

- [ ] **Step 2: Run the project script-test gate**

Run: `pnpm test:scripts`

Expected: all Node script tests PASS.

- [ ] **Step 3: Run the full local gate**

Run from the repository root with a writable Go cache when the inherited cache is locked:

```powershell
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\go-build'
pnpm test:local
```

Expected: backend tests, frontend tests, script tests, frontend/backend builds, package creation, and release checksum verification all PASS.

- [ ] **Step 4: Inspect the real Linux archive**

Run:

```powershell
tar -tvzf deploy/deployment/aifar-deployment-*-linux-amd64.tar.gz | Select-String 'extras/selinux/(README.md|configure-all-selinux.sh)'
```

Expected: both files appear and `configure-all-selinux.sh` is `-rwxr-xr-x`.

Also verify the Windows ZIP contains no `extras/selinux/` entry.

- [ ] **Step 5: Review final scope and safety invariants**

Run:

```powershell
git diff --name-only HEAD~3..HEAD
rg -n -i 'setenforce|/etc/selinux/config|audit2allow|firewall-cmd|systemctl (start|stop|restart|enable|disable)' extras/selinux
```

Expected: only planned SELinux module/test/packaging files are present; the forbidden-pattern search returns no matches except explanatory README statements that explicitly say the operations are not performed.

- [ ] **Step 6: Commit only if verification required a correction**

If no correction was needed, do not create an empty commit. If a correction was needed:

```bash
git add extras/selinux scripts/selinux-extra.test.mjs scripts/release-pipeline.test.mjs scripts/package-release.mjs
git commit -m "fix: complete aggregate selinux verification"
```

- [ ] **Step 7: Report the target-host boundary**

State explicitly that local fake-command and release verification passed, but the script has not been executed against `192.168.74.132` or `192.168.74.133`. Running it on either server is a separate mutating operation that requires explicit user authorization after reviewing the final script.
