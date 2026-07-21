import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptsDir, '..')
const moduleDir = path.join(rootDir, 'extras', 'selinux')
const scriptPath = path.join(moduleDir, 'configure-all-selinux.sh')
const bashPath = 'D:\\tools\\git\\bin\\bash.exe'

const serviceFunctions = [
  'configure_docker',
  'configure_aifar_agent',
  'configure_aifar_runtime',
  'configure_mysql',
  'configure_mysql_router',
  'configure_redis',
  'configure_minio',
  'configure_nacos',
  'configure_keepalived',
  'configure_https_ingress'
]

function toMsysPath(filePath) {
  const normalized = path.resolve(filePath).replaceAll('\\', '/')
  return normalized.replace(/^([A-Za-z]):/, (_, drive) => `/${drive.toLowerCase()}`)
}

function runBashHarness(t, body) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-selinux-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const harnessPath = path.join(fixture, 'harness.sh')
  const statePath = path.join(fixture, 'port-state')
  const baseStatePath = path.join(fixture, 'base-port-state')
  const fcontextStatePath = path.join(fixture, 'fcontext-state')
  const logPath = path.join(fixture, 'calls.log')
  const journalPath = path.join(fixture, 'journal.tsv')
  mkdirSync(path.join(fixture, 'extras', 'aifar-https-ingress', 'conf.d'), { recursive: true })
  mkdirSync(path.join(fixture, 'extras', 'aifar-https-ingress', 'tls'), { recursive: true })
  writeFileSync(statePath, '')
  writeFileSync(baseStatePath, '')
  writeFileSync(fcontextStatePath, '')
  writeFileSync(logPath, '')
  writeFileSync(journalPath, '')
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(scriptPath)}'
STATE_FILE='${toMsysPath(statePath)}'
BASE_STATE_FILE='${toMsysPath(baseStatePath)}'
FCONTEXT_STATE_FILE='${toMsysPath(fcontextStatePath)}'
CALL_LOG='${toMsysPath(logPath)}'
JOURNAL_FILE='${toMsysPath(journalPath)}'
TRANSACTION_DIR='${toMsysPath(fixture)}'
TRANSACTION_ACTIVE=1

semanage() {
  printf 'semanage %s\\n' "$*" >>"$CALL_LOG"
  case "$1 $2 \${3:-}" in
    'port -l -C')
      [[ -s "$STATE_FILE" ]] && cat "$STATE_FILE"
      ;;
    'port -l ')
      [[ -s "$BASE_STATE_FILE" ]] && cat "$BASE_STATE_FILE"
      [[ -s "$STATE_FILE" ]] && cat "$STATE_FILE"
      ;;
    'port -a -t')
      case "$4" in
        container_port_t|http_port_t|mysqld_port_t|redis_port_t) ;;
        *) return 1 ;;
      esac
      if grep -Eq "^[^ ]+ tcp ([^,]*, ?)*$7(,|$)|^[^ ]+ tcp $7$" "$BASE_STATE_FILE"; then
        return 1
      fi
      printf '%s tcp %s\\n' "$4" "$7" >>"$STATE_FILE"
      ;;
    'port -m -t')
      case "$4" in
        container_port_t|http_port_t|mysqld_port_t|redis_port_t) ;;
        *) return 1 ;;
      esac
      printf '%s tcp %s\\n' "$4" "$7" >"$STATE_FILE"
      ;;
    'port -d -p')
      : >"$STATE_FILE"
      ;;
    'fcontext -l -C')
      [[ -s "$FCONTEXT_STATE_FILE" ]] && cat "$FCONTEXT_STATE_FILE"
      ;;
    'fcontext -a -t')
      printf '%s all files system_u:object_r:%s:s0\\n' "$5" "$4" >>"$FCONTEXT_STATE_FILE"
      ;;
    'fcontext -m -t')
      awk -v p="$5" -v t="$4" '$1==p {print p " all files system_u:object_r:" t ":s0"; next} {print}' "$FCONTEXT_STATE_FILE" >"$FCONTEXT_STATE_FILE.tmp"
      mv "$FCONTEXT_STATE_FILE.tmp" "$FCONTEXT_STATE_FILE"
      ;;
    'fcontext -d '*)
      awk -v p="$3" '$1!=p {print}' "$FCONTEXT_STATE_FILE" >"$FCONTEXT_STATE_FILE.tmp"
      mv "$FCONTEXT_STATE_FILE.tmp" "$FCONTEXT_STATE_FILE"
      ;;
    *) return 1 ;;
  esac
}

matchpathcon() {
  case "$1" in
    /var/lib/mysql) printf '%s %s\\n' "$1" system_u:object_r:mysqld_db_t:s0 ;;
    /var/lib/containers/storage/volumes/*) printf '%s %s\\n' "$1" system_u:object_r:container_file_t:s0 ;;
    *) printf '%s %s\\n' "$1" system_u:object_r:bin_t:s0 ;;
  esac
}

${body}
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  return {
    ...result,
    calls: readFileSync(logPath, 'utf8'),
    state: readFileSync(statePath, 'utf8'),
    baseState: readFileSync(baseStatePath, 'utf8'),
    fcontextState: readFileSync(fcontextStatePath, 'utf8')
  }
}

test('aggregate SELinux script exposes the approved zero-argument contract', () => {
  assert.equal(existsSync(scriptPath), true, 'missing extras/selinux/configure-all-selinux.sh')
  const source = readFileSync(scriptPath, 'utf8')
  assert.match(source, /^#!\/usr\/bin\/env bash\n/)
  assert.doesNotMatch(source, /\r/)
  assert.match(source, /\[\[ \$# -eq 0 \]\]/)
  assert.match(source, /main "\$@"/)
  for (const functionName of serviceFunctions) {
    assert.match(source, new RegExp(`${functionName}\\(\\)`))
  }
})

test('aggregate SELinux script cannot weaken host policy or manage services and firewall', () => {
  const source = readFileSync(scriptPath, 'utf8')
  assert.doesNotMatch(source, /setenforce|\/etc\/selinux\/config|audit2allow|firewall-cmd/i)
  assert.doesNotMatch(source, /systemctl\s+(?:start|stop|restart|enable|disable)/)
  assert.match(source, /semanage fcontext/)
  assert.match(source, /semanage port/)
  assert.match(source, /restorecon/)
})

test('aggregate SELinux script contains transaction and safe discovery boundaries', () => {
  const source = readFileSync(scriptPath, 'utf8')
  for (const fragment of [
    '/var/lib/aifar-selinux/transactions',
    'rollback_transaction()',
    'ensure_port_type()',
    'ensure_fcontext()',
    'canonical_managed_path()',
    'preserve live private MCS labels'
  ]) {
    assert.match(source, new RegExp(fragment.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')))
  }
})

test('Keepalived release artifacts alone are not treated as an installed service', () => {
  const source = readFileSync(scriptPath, 'utf8')
  const start = source.indexOf('configure_keepalived()')
  const end = source.indexOf('discover_https_ingress_root()', start)
  assert.notEqual(start, -1)
  assert.notEqual(end, -1)
  const body = source.slice(start, end)
  assert.match(body, /! -x "\$root\/sbin\/keepalived"/)
  assert.match(body, /unit_fragment keepalived\.service/)
})

test('new port rules are journaled and removed by current-transaction rollback', (t) => {
  const result = runBashHarness(t, `
ensure_port_type mysql mysqld_port_t 3306
grep -q '^mysqld_port_t tcp 3306$' "$STATE_FILE"
rollback_transaction
[[ ! -s "$STATE_FILE" ]]
`)
  assert.equal(result.status, 0, JSON.stringify({ stderr: result.stderr, calls: result.calls, state: result.state }))
  assert.match(result.calls, /semanage port -a -t mysqld_port_t -p tcp 3306/)
  assert.match(result.calls, /semanage port -d -p tcp 3306/)
})

test('matching port rules are a successful no-op', (t) => {
  const result = runBashHarness(t, `
printf 'mysqld_port_t tcp 3306\\n' >"$STATE_FILE"
ensure_port_type mysql mysqld_port_t 3306
[[ "$MUTATION_COUNT" == 0 ]]
`)
  assert.equal(result.status, 0, JSON.stringify({ stderr: result.stderr, calls: result.calls, fcontextState: result.fcontextState }))
  assert.doesNotMatch(result.calls, /semanage port -(?:a|m|d)/)
})

test('conflicting local port rules fail without overwriting ownership', (t) => {
  const result = runBashHarness(t, `
printf 'custom_db_port_t tcp 3306\\n' >"$STATE_FILE"
ensure_port_type mysql mysqld_port_t 3306
`)
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /custom_db_port_t/)
  assert.match(result.state, /^custom_db_port_t tcp 3306$/m)
  assert.doesNotMatch(result.calls, /semanage port -(?:a|m|d)/)
})

test('distribution port mappings can be locally overridden and rolled back', (t) => {
  const result = runBashHarness(t, `
printf 'http_cache_port_t tcp 8080\\n' >"$BASE_STATE_FILE"
ensure_port_type aifar-runtime http_port_t 8080
grep -q '^http_port_t tcp 8080$' "$STATE_FILE"
rollback_transaction
[[ ! -s "$STATE_FILE" ]]
grep -q '^http_cache_port_t tcp 8080$' "$BASE_STATE_FILE"
`)
  assert.equal(result.status, 0, JSON.stringify({ stderr: result.stderr, calls: result.calls, state: result.state, baseState: result.baseState }))
  assert.match(result.calls, /semanage port -m -t http_port_t -p tcp 8080/)
  assert.match(result.calls, /semanage port -d -p tcp 8080/)
})

test('file-context additions use a distribution reference and roll back', (t) => {
  const result = runBashHarness(t, `
ensure_fcontext mysql '/aifar/apps/mysql/data(/.*)?' /var/lib/mysql
grep -q '^/aifar/apps/mysql/data(/.\\*)? all files system_u:object_r:mysqld_db_t:s0$' "$FCONTEXT_STATE_FILE"
rollback_transaction
[[ ! -s "$FCONTEXT_STATE_FILE" ]]
`)
  assert.equal(result.status, 0, JSON.stringify({ stderr: result.stderr, calls: result.calls, fcontextState: result.fcontextState }))
  assert.match(result.calls, /semanage fcontext -a -t mysqld_db_t/)
  assert.match(result.calls, /semanage fcontext -d \/aifar\/apps\/mysql\/data/)
})

test('matching escaped file-context rules are an idempotent no-op without awk warnings', (t) => {
  const result = runBashHarness(t, `
printf '%s\\n' '/aifar/apps/aifar-https-ingress/start\\.sh all files system_u:object_r:shell_exec_t:s0' >"$FCONTEXT_STATE_FILE"
reference_type() { printf '%s\\n' shell_exec_t; }
ensure_fcontext https-ingress '/aifar/apps/aifar-https-ingress/start\\.sh' /usr/bin/bash
[[ "$MUTATION_COUNT" == 0 ]]
`)
  assert.equal(result.status, 0, JSON.stringify({ stderr: result.stderr, calls: result.calls, fcontextState: result.fcontextState }))
  assert.doesNotMatch(result.stderr, /awk: warning/)
  assert.doesNotMatch(result.calls, /semanage fcontext -(?:a|m|d)/)
})

test('missing container SELinux policy is installed before Docker port mapping', (t) => {
  const result = runBashHarness(t, `
PACKAGE_INSTALLED=0
rpm() {
  [[ "$1" == '-q' && "$2" == 'container-selinux' && "$PACKAGE_INSTALLED" == 1 ]]
}
dnf() {
  printf 'dnf %s\\n' "$*" >>"$CALL_LOG"
  [[ "$*" == '-y install container-selinux' ]]
  PACKAGE_INSTALLED=1
}
ensure_container_selinux_policy
[[ "$PACKAGE_INSTALLED" == 1 ]]
`)
  assert.equal(result.status, 0, JSON.stringify({ stderr: result.stderr, calls: result.calls }))
  assert.match(result.calls, /dnf -y install container-selinux/)
})

test('MySQL bundled shared libraries use the distribution library type', (t) => {
  const result = runBashHarness(t, `
unit_fragment() { printf '%s\\n' /etc/systemd/system/aifar-mysql.service; }
read_key_value() { printf '%s\\n' 3306; }
ensure_port_type() { :; }
apply_existing_path_mapping() { printf 'mapping %s\\n' "$*" >>"$CALL_LOG"; }
configure_mysql
`)
  assert.equal(result.status, 0, JSON.stringify({ stderr: result.stderr, calls: result.calls }))
  assert.match(result.calls, /mapping mysql \/aifar\/apps\/mysql \/aifar\/apps\/mysql\/mysql\/lib \/usr\/lib64/)
})

test('recursive restorecon stays on the managed filesystem', (t) => {
  const result = runBashHarness(t, `
restorecon() { printf 'restorecon %s\\n' "$*" >>"$CALL_LOG"; }
safe_restorecon test "$TRANSACTION_DIR" "$TRANSACTION_DIR"
`)
  assert.equal(result.status, 0, JSON.stringify({ stderr: result.stderr, calls: result.calls }))
  assert.match(result.calls, /restorecon -RF -x /)
})

const discoveredPortCases = [
  {
    name: 'Docker',
    body: `
unit_fragment() { printf '%s\\n' /etc/systemd/system/docker.service; }
unit_exec_start() { printf '%s\\n' '/usr/local/bin/dockerd -H tcp://0.0.0.0:2376'; }
ensure_container_selinux_policy() { :; }
apply_existing_path_mapping() { :; }
configure_docker
`,
    expected: ['container_port_t tcp 2376']
  },
  {
    name: 'MySQL',
    body: `
unit_fragment() { printf '%s\\n' /etc/systemd/system/aifar-mysql.service; }
read_key_value() { printf '%s\\n' 3307; }
apply_existing_path_mapping() { :; }
configure_mysql
`,
    expected: ['mysqld_port_t tcp 3307']
  },
  {
    name: 'MySQL Router',
    body: `
unit_fragment() { printf '%s\\n' /etc/systemd/system/aifar-mysql-router.service; }
discover_mysql_router_base_port() { printf '%s\\n' 7446; }
apply_existing_path_mapping() { :; }
configure_mysql_router
`,
    expected: [
      'mysqld_port_t tcp 7446',
      'mysqld_port_t tcp 7447',
      'mysqld_port_t tcp 7448',
      'mysqld_port_t tcp 7449'
    ]
  },
  {
    name: 'Redis cluster',
    body: `
unit_fragment() { printf '%s\\n' /etc/systemd/system/aifar-redis.service; }
discover_redis_ports() { printf '%s\\n' 6380 16380 26380; }
apply_existing_path_mapping() { :; }
configure_redis
`,
    expected: ['redis_port_t tcp 6380', 'redis_port_t tcp 16380', 'redis_port_t tcp 26380']
  },
  {
    name: 'MinIO',
    body: `
unit_fragment() { printf '%s\\n' /etc/systemd/system/aifar-minio.service; }
discover_minio_ports() { printf '%s\\n' 9100 9101; }
apply_existing_path_mapping() { :; }
configure_minio
`,
    expected: ['http_port_t tcp 9100', 'http_port_t tcp 9101']
  },
  {
    name: 'AIFAR Runtime',
    body: `
aifar_runtime_installed() { return 0; }
discover_aifar_runtime_ports() { printf '%s\\n' 8080 38000; }
configure_aifar_runtime_bind_mounts() { :; }
configure_aifar_runtime
`,
    expected: ['http_port_t tcp 8080', 'http_port_t tcp 38000']
  }
]

for (const fixtureCase of discoveredPortCases) {
  test(`${fixtureCase.name} applies discovered port types`, (t) => {
    const result = runBashHarness(t, fixtureCase.body)
    assert.equal(result.status, 0, result.stderr)
    for (const expected of fixtureCase.expected) {
      assert.match(result.state, new RegExp(`^${expected}$`, 'm'))
    }
  })
}

test('AIFAR Runtime discovers published ports from managed containers', (t) => {
  const result = runBashHarness(t, `
docker() {
  case "$1" in
    ps) printf '%s\\n' managed-container ;;
    inspect) printf '%s\\n' 18080 38080 ;;
  esac
}
ports="$(discover_aifar_runtime_ports)"
grep -qx 18080 <<<"$ports"
grep -qx 38080 <<<"$ports"
! grep -qx 8080 <<<"$ports"
`)
  assert.equal(result.status, 0, result.stderr)
})

test('Nacos does not mislabel gRPC and Raft ports as HTTP', (t) => {
  const result = runBashHarness(t, `
unit_fragment() { printf '%s\\n' /etc/systemd/system/aifar-nacos.service; }
apply_existing_path_mapping() { :; }
verify_nacos_ports() { :; }
configure_nacos
[[ ! -s "$STATE_FILE" ]]
`)
  assert.equal(result.status, 0, result.stderr)
  assert.doesNotMatch(result.calls, /semanage port -a/)
})

test('AIFAR agent uses exact approved roots instead of the filesystem root', (t) => {
  const result = runBashHarness(t, `
unit_fragment() { printf '%s\\n' /etc/systemd/system/aifar-agent.service; }
unit_exec_start() { printf '%s\\n' '{ path=/usr/local/bin/aifar-agent ; }'; }
apply_existing_path_mapping() {
  [[ "$2" != / ]]
  printf 'agent-root %s\\n' "$2" >>"$CALL_LOG"
}
configure_aifar_agent
`)
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.calls, /agent-root \/usr\/local\/bin\/aifar-agent/)
  assert.match(result.calls, /agent-root \/etc\/aifar/)
})

test('MinIO accepts only explicit storage roots for local volumes', (t) => {
  const result = runBashHarness(t, `
[[ "$(validate_minio_data_path /data/minio/disk1/minio)" == /data/minio/disk1/minio ]]
[[ "$(validate_minio_data_path /mnt/minio/data)" == /mnt/minio/data ]]
if validate_minio_data_path /etc; then exit 1; fi
if validate_minio_data_path /var/lib; then exit 1; fi
`)
  assert.equal(result.status, 0, result.stderr)
})

test('running HTTPS ingress verifies private MCS mounts without restorecon', (t) => {
  const result = runBashHarness(t, `
unit_fragment() { printf '%s\\n' /etc/systemd/system/aifar-https-ingress.service; }
unit_exec_start() { printf '%s\\n' "{ path=$TRANSACTION_DIR/extras/aifar-https-ingress/start.sh ; }"; }
effective_port_has_type() { return 0; }
ingress_container_running() { return 0; }
verify_container_mount_type() { printf 'verify %s\\n' "$2" >>"$CALL_LOG"; }
apply_existing_path_mapping() { printf 'apply %s\\n' "$3" >>"$CALL_LOG"; }
configure_https_ingress
`)
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.calls, /verify .*conf\.d/)
  assert.match(result.calls, /verify .*tls/)
  assert.doesNotMatch(result.calls, /restorecon .*conf\.d|restorecon .*tls/)
  assert.doesNotMatch(result.calls, /apply .*conf\.d|apply .*tls/)
})

test('HTTPS ingress discovery accepts the managed deployment root', (t) => {
  const result = runBashHarness(t, `
unit_exec_start() { printf '%s\\n' '{ path=/aifar/apps/aifar-https-ingress/start.sh ; }'; }
[[ "$(discover_https_ingress_root)" == /aifar/apps/aifar-https-ingress ]]
`)
  assert.equal(result.status, 0, result.stderr)
})
