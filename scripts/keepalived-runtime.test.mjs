import assert from 'node:assert/strict'
import {
  chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync
} from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptsDir, '..')
const healthScriptPath = path.join(rootDir, 'extras', 'keepalived', 'check-aggregate-health.sh')
const installerPath = path.join(rootDir, 'extras', 'keepalived', 'install-keepalived-offline.sh')
const uninstallerPath = path.join(rootDir, 'extras', 'keepalived', 'uninstall-keepalived.sh')
const bashPath = 'D:\\tools\\git\\bin\\bash.exe'
const blenderPythonFallback = 'D:\\tools\\Blender 5.1\\5.1\\python\\bin\\python.exe'

function toMsysPath(filePath) {
  const normalized = path.resolve(filePath).replaceAll('\\', '/')
  return normalized.replace(/^([A-Za-z]):/, (_, drive) => `/${drive.toLowerCase()}`)
}

function supportsPython3(candidate) {
  const result = spawnSync(candidate.command, [
    ...candidate.prefixArgs,
    '-c',
    'import sys; raise SystemExit(0 if sys.version_info.major == 3 else 1)'
  ], { encoding: 'utf8' })
  return result.status === 0
}

function discoverPython3() {
  const candidates = [
    ...(process.env.AIFAR_TEST_PYTHON3
      ? [{ command: process.env.AIFAR_TEST_PYTHON3, prefixArgs: [] }]
      : []),
    { command: 'python3', prefixArgs: [] },
    { command: 'python', prefixArgs: [] },
    { command: 'py', prefixArgs: ['-3'] },
    { command: blenderPythonFallback, prefixArgs: [] }
  ]
  const candidate = candidates.find(supportsPython3)
  if (!candidate) {
    throw new Error('No usable Python 3 interpreter found. Set AIFAR_TEST_PYTHON3 to a Python 3 executable.')
  }
  return candidate
}

const realPython = discoverPython3()

function bashQuote(value) {
  return `'${value.replaceAll("'", "'\\''")}'`
}

function bashCommand(command) {
  return path.isAbsolute(command) ? toMsysPath(command) : command
}

function writeRealPythonShim(binPath, pythonArgsPath) {
  writeFileSync(path.join(binPath, 'python3'), `#!/usr/bin/env bash
PATH="\${PATH#'${toMsysPath(binPath)}':}"
printf '%s\\0' "\$@" >"\$PYTHON_ARGS_FILE"
exec ${bashQuote(bashCommand(realPython.command))} ${realPython.prefixArgs.map(bashQuote).join(' ')} "\$@"
`)
}

function readNulDelimited(filePath) {
  return readFileSync(filePath, 'utf8').split('\0').filter(Boolean)
}

function bashEnvironment(binPath, pythonArgsPath, overrides = {}) {
  return {
    ...process.env,
    ...overrides,
    PATH: `${toMsysPath(binPath)}:${process.env.PATH}`,
    PYTHON_ARGS_FILE: toMsysPath(pythonArgsPath)
  }
}

function runHealthHarness(t, { body, status = 0, url = 'http://127.0.0.1:38000/health' }) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-health-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const harnessPath = path.join(fixture, 'harness.sh')
  const urlPath = path.join(fixture, 'keepalived-health-url')
  const binPath = path.join(fixture, 'bin')
  const curlArgsPath = path.join(fixture, 'curl-args')
  const pythonArgsPath = path.join(fixture, 'python-args')
  mkdirSync(binPath)
  writeFileSync(urlPath, url)
  writeRealPythonShim(binPath, pythonArgsPath)
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(healthScriptPath)}'
HEALTH_URL_FILE='${toMsysPath(urlPath)}'
curl() {
    printf '%s\\n' "\$@" >'${toMsysPath(curlArgsPath)}'
    [[ "\${FAKE_CURL_STATUS:-0}" -eq 0 ]] || return "\$FAKE_CURL_STATUS"
    printf '%s' "\${FAKE_CURL_BODY:-}"
}
check_health "\$HEALTH_URL_FILE"
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], {
    encoding: 'utf8',
    env: bashEnvironment(binPath, pythonArgsPath, {
      FAKE_CURL_BODY: body,
      FAKE_CURL_STATUS: String(status)
    })
  })
  return {
    ...result,
    curlArgs: existsSync(curlArgsPath) ? readFileSync(curlArgsPath, 'utf8').trimEnd().split('\n') : [],
    pythonArgs: existsSync(pythonArgsPath) ? readNulDelimited(pythonArgsPath) : []
  }
}

function runParserHarness(t, body) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-parser-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const harnessPath = path.join(fixture, 'harness.sh')
  const binPath = path.join(fixture, 'bin')
  const pythonArgsPath = path.join(fixture, 'python-args')
  mkdirSync(binPath)
  writeRealPythonShim(binPath, pythonArgsPath)
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(healthScriptPath)}'
printf '%s' "\${FAKE_BODY:-}" | json_up_is_true
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], {
    encoding: 'utf8',
    env: bashEnvironment(binPath, pythonArgsPath, { FAKE_BODY: body })
  })
  return {
    ...result,
    pythonArgs: existsSync(pythonArgsPath) ? readNulDelimited(pythonArgsPath) : []
  }
}

function runMainHarness(t) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-main-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const harnessPath = path.join(fixture, 'harness.sh')
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(healthScriptPath)}'
check_health() {
    [[ "\$1" == '/aifar/apps/keepalived/etc/keepalived-health-url' ]]
}
main
if main unexpected; then
    exit 1
else
    [[ "\$?" -eq 2 ]]
fi
`)
  return spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
}

function validNodeConfig(overrides = {}) {
  const values = {
    KEEPALIVED_LOCAL_IP: '192.168.74.132',
    KEEPALIVED_PEER_IP: '192.168.74.133',
    KEEPALIVED_VIP_CIDR: '192.168.74.130/24',
    KEEPALIVED_INTERFACE: 'ens160',
    KEEPALIVED_PRIORITY: '150',
    KEEPALIVED_VIRTUAL_ROUTER_ID: '130',
    KEEPALIVED_HEALTH_URL: 'http://192.168.74.132:38000/health/aggregate',
    ...overrides
  }
  return Object.entries(values).map(([key, value]) => `${key}=${value}`).join('\n') + '\n'
}

const validIpFunction = `ip() {
    case "\$*" in
        'link show dev ens160') return 0 ;;
        '-o -4 addr show dev ens160') printf '2: ens160 inet 192.168.74.132/24 scope global ens160\\n' ;;
        '-4 route get 192.168.74.130') printf '192.168.74.130 dev ens160 src 192.168.74.132\\n' ;;
        *) return 1 ;;
    esac
}`

function runNodeConfigHarness(t, {
  config = validNodeConfig(),
  ipFunction = validIpFunction,
  render = true
} = {}) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-node-config-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const harnessPath = path.join(fixture, 'harness.sh')
  const nodeEnvPath = path.join(fixture, 'keepalived.env')
  const renderedConfigPath = path.join(fixture, 'keepalived.conf')
  writeFileSync(nodeEnvPath, config)
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(installerPath)}'
NODE_ENV='${toMsysPath(nodeEnvPath)}'
RENDERED_CONFIG='${toMsysPath(renderedConfigPath)}'
${ipFunction}
parse_node_config "\$NODE_ENV"
validate_node_config
${render ? 'render_keepalived_config "$CONFIG_TEMPLATE" "$RENDERED_CONFIG"' : ''}
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  return {
    ...result,
    renderedConfig: existsSync(renderedConfigPath) ? readFileSync(renderedConfigPath, 'utf8') : ''
  }
}

function runSequentialParseHarness(t) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-sequential-parse-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const harnessPath = path.join(fixture, 'harness.sh')
  const validEnvPath = path.join(fixture, 'valid.env')
  const missingEnvPath = path.join(fixture, 'missing.env')
  writeFileSync(validEnvPath, validNodeConfig())
  writeFileSync(missingEnvPath, validNodeConfig().replace(/^KEEPALIVED_HEALTH_URL=.*\n/m, ''))
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(installerPath)}'
parse_node_config '${toMsysPath(validEnvPath)}'
parse_node_config '${toMsysPath(missingEnvPath)}'
`)
  return spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
}

function writeInstallerFixture(fixture) {
  const appRoot = path.join(fixture, 'aifar', 'apps', 'keepalived')
  const backupRoot = path.join(fixture, 'aifar', 'backups')
  const unitLink = path.join(fixture, 'etc', 'systemd', 'system', 'keepalived.service')
  const fixtureInstallerPath = path.join(fixture, 'install-keepalived-offline.sh')
  let installer = readFileSync(installerPath, 'utf8')
  installer = installer
    .replaceAll('/aifar/apps/keepalived', toMsysPath(appRoot))
    .replaceAll('/aifar/backups', toMsysPath(backupRoot))
    .replaceAll('/etc/systemd/system/keepalived.service', toMsysPath(unitLink))
  writeFileSync(fixtureInstallerPath, installer)
  writeFileSync(
    path.join(fixture, 'check-aggregate-health.sh'),
    readFileSync(healthScriptPath, 'utf8')
  )
  return { appRoot, backupRoot, fixtureInstallerPath, unitLink }
}

function writeUninstallerFixture(fixture) {
  const appRoot = path.join(fixture, 'aifar', 'apps', 'keepalived')
  const backupRoot = path.join(fixture, 'aifar', 'backups')
  const unitLink = path.join(fixture, 'etc', 'systemd', 'system', 'keepalived.service')
  const fixtureUninstallerPath = path.join(fixture, 'uninstall-keepalived.sh')
  let uninstaller = readFileSync(uninstallerPath, 'utf8')
  uninstaller = uninstaller
    .replaceAll('/aifar/apps/keepalived', toMsysPath(appRoot))
    .replaceAll('/aifar/backups', toMsysPath(backupRoot))
    .replaceAll('/etc/systemd/system/keepalived.service', toMsysPath(unitLink))
  writeFileSync(fixtureUninstallerPath, uninstaller)
  return { appRoot, fixtureUninstallerPath }
}

const peerRule = (peer) => `rule family="ipv4" source address="${peer}/32" protocol value="112" accept`

function fakeFirewallFunction({ callLogPath, runtimePath, permanentPath }) {
  return `firewall-cmd() {
    local form=runtime operation='' rule='' argument=''
    printf '%s\\n' "\$*" >>'${toMsysPath(callLogPath)}'
    for argument in "\$@"; do
        case "\$argument" in
            --permanent) form=permanent ;;
            --query-rich-rule=*) operation=query; rule="\${argument#*=}" ;;
            --add-rich-rule=*) operation=add; rule="\${argument#*=}" ;;
            --remove-rich-rule=*) operation=remove; rule="\${argument#*=}" ;;
            --get-zone-of-interface=*)
                [[ "\${FAKE_INTERFACE_ZONE_STATUS:-0}" -eq 0 ]] || return "\$FAKE_INTERFACE_ZONE_STATUS"
                printf '%s\\n' "\${FAKE_INTERFACE_ZONE:-}"
                return 0
                ;;
            --get-default-zone)
                printf '%s\\n' "\${FAKE_DEFAULT_ZONE:-public}"
                return 0
                ;;
        esac
    done
    local state='${toMsysPath(runtimePath)}'
    [[ "\$form" == permanent ]] && state='${toMsysPath(permanentPath)}'
    case "\$operation" in
        query) grep -Fqx -- "\$rule" "\$state" ;;
        add)
            [[ "\${FAKE_FAIL_ADD_FORM:-}" != "\$form" ]] || return 55
            grep -Fqx -- "\$rule" "\$state" || printf '%s\\n' "\$rule" >>"\$state"
            ;;
        remove)
            [[ "\${FAKE_FAIL_REMOVE_FORM:-}" != "\$form" ]] || return 56
            grep -Fvx -- "\$rule" "\$state" >"\$state.tmp" || true
            mv -f -- "\$state.tmp" "\$state"
            ;;
        *) return 97 ;;
    esac
}`
}

function writeFirewallRecord(recordPath, { zone = 'public', rule, runtimeCreated, permanentCreated }) {
  mkdirSync(path.dirname(recordPath), { recursive: true })
  writeFileSync(recordPath, `zone=${zone}\nrule=${rule}\nruntime_created=${runtimeCreated}\npermanent_created=${permanentCreated}\n`)
}

function runFirewallHarness(t, {
  active = true,
  interfaceZone = 'work',
  interfaceZoneStatus = 0,
  defaultZone = 'public',
  peer = '192.168.74.133',
  runtimeRules = [],
  permanentRules = [],
  oldRecord,
  failAddForm = '',
  rollbackOnFailure = false
} = {}) {
  const fixture = mkdtempSync(path.join(rootDir, '.aifar-keepalived-firewall-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { appRoot, fixtureInstallerPath } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const workDir = path.join(fixture, 'work')
  const callLogPath = path.join(fixture, 'firewall-calls')
  const runtimePath = path.join(fixture, 'runtime-rules')
  const permanentPath = path.join(fixture, 'permanent-rules')
  const recordPath = path.join(appRoot, 'var', 'lib', 'aifar', 'firewall-rule')
  mkdirSync(workDir, { recursive: true })
  writeFileSync(runtimePath, runtimeRules.join('\n') + (runtimeRules.length ? '\n' : ''))
  writeFileSync(permanentPath, permanentRules.join('\n') + (permanentRules.length ? '\n' : ''))
  if (oldRecord) writeFirewallRecord(recordPath, oldRecord)
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
${fakeInstallFunction()}
WORK_DIR='${toMsysPath(workDir)}'
NODE_INTERFACE=ens160
NODE_PEER_IP='${peer}'
FAKE_INTERFACE_ZONE='${interfaceZone}'
FAKE_INTERFACE_ZONE_STATUS='${interfaceZoneStatus}'
FAKE_DEFAULT_ZONE='${defaultZone}'
FAKE_FAIL_ADD_FORM='${failAddForm}'
systemctl() { [[ "\$*" == 'is-active --quiet firewalld.service' && '${active ? 1 : 0}' -eq 1 ]]; }
command() { [[ "\$1" == '-v' && "\$2" == 'firewall-cmd' ]] && return 0; builtin command "\$@"; }
${fakeFirewallFunction({ callLogPath, runtimePath, permanentPath })}
${rollbackOnFailure ? `TRANSACTION_ACTIVE=1
rollback_install_transaction() { rollback_firewall_journal; }
reconcile_firewall_rule
TRANSACTION_ACTIVE=0` : 'reconcile_firewall_rule'}
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  const readRules = (statePath) => readFileSync(statePath, 'utf8').split('\n').filter(Boolean)
  return {
    ...result,
    calls: existsSync(callLogPath) ? readFileSync(callLogPath, 'utf8').trimEnd().split('\n') : [],
    runtimeRules: readRules(runtimePath),
    permanentRules: readRules(permanentPath),
    record: existsSync(recordPath) ? readFileSync(recordPath, 'utf8') : ''
  }
}

function runFirewallRollbackHarness(t) {
  const fixture = mkdtempSync(path.join(rootDir, '.aifar-keepalived-firewall-rollback-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { fixtureInstallerPath } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const workDir = path.join(fixture, 'work')
  const callLogPath = path.join(fixture, 'firewall-calls')
  const runtimePath = path.join(fixture, 'runtime-rules')
  const permanentPath = path.join(fixture, 'permanent-rules')
  const oldRule = peerRule('192.168.74.140')
  const newRule = peerRule('192.168.74.133')
  mkdirSync(workDir)
  writeFileSync(runtimePath, `${newRule}\n`)
  writeFileSync(permanentPath, `${newRule}\n`)
  writeFileSync(path.join(workDir, 'firewall-journal.tsv'), [
    `removed\truntime\tpublic\t${oldRule}`,
    `removed\tpermanent\tpublic\t${oldRule}`,
    `added\truntime\tpublic\t${newRule}`,
    `added\tpermanent\tpublic\t${newRule}`
  ].join('\n') + '\n')
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
WORK_DIR='${toMsysPath(workDir)}'
${fakeFirewallFunction({ callLogPath, runtimePath, permanentPath })}
rollback_firewall_journal
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  const readRules = (statePath) => readFileSync(statePath, 'utf8').split('\n').filter(Boolean)
  return { ...result, calls: readFileSync(callLogPath, 'utf8').trimEnd().split('\n'), runtimeRules: readRules(runtimePath), permanentRules: readRules(permanentPath) }
}

function runUninstallFirewallHarness(t, { record, recordSymlink = false, runtimeRules = [], permanentRules = [] }) {
  const fixture = mkdtempSync(path.join(rootDir, '.aifar-keepalived-uninstall-firewall-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { appRoot, fixtureUninstallerPath } = writeUninstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const callLogPath = path.join(fixture, 'firewall-calls')
  const runtimePath = path.join(fixture, 'runtime-rules')
  const permanentPath = path.join(fixture, 'permanent-rules')
  const recordPath = path.join(appRoot, 'var', 'lib', 'aifar', 'firewall-rule')
  const recordTargetPath = path.join(fixture, 'external-firewall-record')
  writeFileSync(runtimePath, runtimeRules.join('\n') + (runtimeRules.length ? '\n' : ''))
  writeFileSync(permanentPath, permanentRules.join('\n') + (permanentRules.length ? '\n' : ''))
  mkdirSync(path.dirname(recordPath), { recursive: true })
  const initialRecordPath = recordSymlink ? recordTargetPath : recordPath
  if (typeof record === 'string') writeFileSync(initialRecordPath, record)
  else writeFirewallRecord(initialRecordPath, record)
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
${recordSymlink ? `ln -s -- '${toMsysPath(recordTargetPath)}' '${toMsysPath(recordPath)}'
[[ -L '${toMsysPath(recordPath)}' ]] || exit 96` : ''}
source '${toMsysPath(fixtureUninstallerPath)}'
${fakeFirewallFunction({ callLogPath, runtimePath, permanentPath })}
remove_owned_firewall_rule
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], {
    encoding: 'utf8',
    env: { ...process.env, MSYS: 'winsymlinks:sys' }
  })
  const readRules = (statePath) => readFileSync(statePath, 'utf8').split('\n').filter(Boolean)
  return {
    ...result,
    calls: existsSync(callLogPath) ? readFileSync(callLogPath, 'utf8').trimEnd().split('\n') : [],
    runtimeRules: readRules(runtimePath),
    permanentRules: readRules(permanentPath),
    appRootExists: existsSync(appRoot),
    recordExists: existsSync(recordPath)
  }
}

function runMutationJournalFailureHarness(t, { mutation, form }) {
  const fixture = mkdtempSync(path.join(rootDir, '.aifar-keepalived-firewall-journal-failure-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { fixtureInstallerPath } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const workDir = path.join(fixture, 'work')
  const callLogPath = path.join(fixture, 'firewall-calls')
  const runtimePath = path.join(fixture, 'runtime-rules')
  const permanentPath = path.join(fixture, 'permanent-rules')
  const rule = peerRule('192.168.74.133')
  mkdirSync(path.join(workDir, 'firewall-journal.tsv'), { recursive: true })
  writeFileSync(runtimePath, mutation === 'remove' && form === 'runtime' ? `${rule}\n` : '')
  writeFileSync(permanentPath, mutation === 'remove' && form === 'permanent' ? `${rule}\n` : '')
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
WORK_DIR='${toMsysPath(workDir)}'
${fakeFirewallFunction({ callLogPath, runtimePath, permanentPath })}
mutate_firewall_rule '${mutation}' '${form}' public '${rule}'
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  const readRules = (statePath) => readFileSync(statePath, 'utf8').split('\n').filter(Boolean)
  return {
    ...result,
    calls: existsSync(callLogPath) ? readFileSync(callLogPath, 'utf8').trimEnd().split('\n') : [],
    runtimeRules: readRules(runtimePath),
    permanentRules: readRules(permanentPath)
  }
}

function fakeSystemctlFunction(callLogPath) {
  return `systemctl() {
    printf '%s\\n' "\$*" >>'${toMsysPath(callLogPath)}'
    case "\$*" in
        'is-active --quiet keepalived.service')
            if [[ "\$FAKE_ACTIVATION_ATTEMPTED" -eq 1 && "\$FAKE_FINAL_ACTIVE_FAILURE" -eq 1 ]]; then
                return 1
            fi
            [[ "\$FAKE_ACTIVE" -eq 1 ]]
            ;;
        'is-enabled --quiet keepalived.service') [[ "\$FAKE_ENABLED" -eq 1 ]] ;;
        'enable keepalived.service') FAKE_ENABLED=1 ;;
        'disable keepalived.service')
            FAKE_ENABLED=0
            rm -f -- "\$UNIT_LINK"
            ;;
        'start keepalived.service'|'restart keepalived.service')
            FAKE_ACTIVE=1
            FAKE_ACTIVATION_ATTEMPTED=1
            ;;
        'stop keepalived.service') FAKE_ACTIVE=0 ;;
        'daemon-reload') ;;
        'show -p FragmentPath --value keepalived.service') ;;
        *) return 97 ;;
    esac
}`
}

function fakeInstallFunction() {
  return `install() {
    local directory_mode=0
    local mode=''
    while [[ "\$#" -gt 0 ]]; do
        case "\$1" in
            -d) directory_mode=1; shift ;;
            -o|-g) shift 2 ;;
            -m) mode="\$2"; shift 2 ;;
            *) break ;;
        esac
    done
    if [[ "\$directory_mode" -eq 1 ]]; then
        mkdir -p -- "\$@"
    else
        cp -- "\$1" "\$2"
        [[ -z "\$mode" ]] || chmod "\$mode" "\$2"
    fi
}`
}

function runServiceHarness(t, {
  active,
  enabled,
  healthStatus = 0,
  finalActiveFailure = false
}) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-service-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { appRoot, fixtureInstallerPath } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const callLogPath = path.join(fixture, 'systemctl-calls')
  const installedHealthScript = path.join(appRoot, 'libexec', 'check-aggregate-health.sh')
  mkdirSync(path.dirname(installedHealthScript), { recursive: true })
  writeFileSync(installedHealthScript, `#!/usr/bin/env bash\nexit ${healthStatus}\n`)
  chmodSync(installedHealthScript, 0o750)
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
FAKE_ACTIVE=${active ? 1 : 0}
FAKE_ENABLED=${enabled ? 1 : 0}
FAKE_ACTIVATION_ATTEMPTED=0
FAKE_FINAL_ACTIVE_FAILURE=${finalActiveFailure ? 1 : 0}
${fakeSystemctlFunction(callLogPath)}
capture_service_state
activate_keepalived
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  return {
    ...result,
    calls: existsSync(callLogPath) ? readFileSync(callLogPath, 'utf8').trimEnd().split('\n') : []
  }
}

function runRollbackHarness(t, { active, enabled, unitLinkExisted = false }) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-rollback-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { appRoot, fixtureInstallerPath, unitLink } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const callLogPath = path.join(fixture, 'systemctl-calls')
  mkdirSync(appRoot, { recursive: true })
  writeFileSync(path.join(appRoot, 'failed-install-marker'), 'failed')
  if (unitLinkExisted) {
    mkdirSync(path.dirname(unitLink), { recursive: true })
    writeFileSync(unitLink, 'owned-link')
  }
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
FAKE_ACTIVE=1
FAKE_ENABLED=1
FAKE_ACTIVATION_ATTEMPTED=0
FAKE_FINAL_ACTIVE_FAILURE=0
${fakeSystemctlFunction(callLogPath)}
mountpoint() { return 1; }
${unitLinkExisted ? `readlink() {
    if [[ "\$*" == "-f -- \$UNIT_LINK" || "\$*" == "-- \$UNIT_LINK" ]]; then
        printf '%s\\n' "\$EXPECTED_UNIT"
    else
        command readlink "\$@"
    fi
}
ln() {
    if [[ "\$1" == '-s' && "\$2" == '--' && "\$3" == "\$EXPECTED_UNIT" && "\$4" == "\$UNIT_LINK" ]]; then
        printf '%s\\n' "\$3" >"\$4"
    else
        command ln "\$@"
    fi
}` : ''}
APP_ROOT_EXISTED=0
SERVICE_WAS_ACTIVE=${active ? 1 : 0}
SERVICE_WAS_ENABLED=${enabled ? 1 : 0}
UNIT_LINK_EXISTED=${unitLinkExisted ? 1 : 0}
UNIT_LINK_TARGET=${unitLinkExisted ? '"$EXPECTED_UNIT"' : "''"}
rollback_install_transaction
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  return {
    ...result,
    appRootExists: existsSync(appRoot),
    unitLinkExists: existsSync(unitLink),
    unitLinkTarget: existsSync(unitLink) ? readFileSync(unitLink, 'utf8').trim() : '',
    calls: existsSync(callLogPath) ? readFileSync(callLogPath, 'utf8').trimEnd().split('\n') : []
  }
}

function runBackupHarness(t, { appRootExists = true, mountpointStatus = 1 } = {}) {
  const fixture = mkdtempSync(path.join(rootDir, '.aifar-keepalived-backup-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { appRoot, backupRoot, fixtureInstallerPath } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const statePath = path.join(fixture, 'transaction-state')
  if (appRootExists) {
    mkdirSync(path.join(appRoot, 'nested'), { recursive: true })
    writeFileSync(path.join(appRoot, 'nested', 'prior-state'), 'before-update')
  }
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
${fakeInstallFunction()}
systemctl() {
    case "\$*" in
        'is-active --quiet keepalived.service') return 0 ;;
        'is-enabled --quiet keepalived.service') return 0 ;;
        'show -p FragmentPath --value keepalived.service') return 0 ;;
        *) return 97 ;;
    esac
}
mountpoint() { return ${mountpointStatus}; }
capture_service_state
create_install_backup
printf '%s|%s|%s|%s\n' "\$TRANSACTION_ACTIVE" "\$APP_ROOT_EXISTED" "\$SERVICE_WAS_ACTIVE" "\$SERVICE_WAS_ENABLED" >'${toMsysPath(statePath)}'
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  const backupDirectories = existsSync(backupRoot)
    ? readdirSync(backupRoot).map((name) => path.join(backupRoot, name))
    : []
  return {
    ...result,
    backupDirectories,
    state: existsSync(statePath) ? readFileSync(statePath, 'utf8').trim() : ''
  }
}

function runCleanupRollbackFailureHarness(t) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-cleanup-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { fixtureInstallerPath } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const rollbackMarkerPath = path.join(fixture, 'rollback-called')
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
rollback_install_transaction() {
    printf 'called\n' >'${toMsysPath(rollbackMarkerPath)}'
    return 1
}
TRANSACTION_ACTIVE=1
exit 37
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  return { ...result, rollbackCalled: existsSync(rollbackMarkerPath) }
}

function runManagedConfigurationHarness(t, { stagedStatus = 0 } = {}) {
  const fixture = mkdtempSync(path.join(rootDir, '.aifar-keepalived-config-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { appRoot, fixtureInstallerPath } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const workDir = path.join(fixture, 'work')
  const stagedConfigPath = path.join(workDir, 'keepalived.conf')
  const syntaxLogPath = path.join(fixture, 'syntax-calls')
  const binaryPath = path.join(appRoot, 'sbin', 'keepalived')
  const formalConfigPath = path.join(appRoot, 'etc', 'keepalived', 'keepalived.conf')
  mkdirSync(path.dirname(binaryPath), { recursive: true })
  mkdirSync(path.dirname(formalConfigPath), { recursive: true })
  mkdirSync(workDir, { recursive: true })
  writeFileSync(formalConfigPath, 'previous-config\n')
  writeFileSync(stagedConfigPath, 'managed-config\n')
  writeFileSync(binaryPath, `#!/usr/bin/env bash
printf '%s\\n' "\$*" >>'${toMsysPath(syntaxLogPath)}'
if [[ "\$*" == '-t -f ${toMsysPath(stagedConfigPath)}' ]]; then
    exit ${stagedStatus}
fi
exit 0
`)
  chmodSync(binaryPath, 0o750)
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
${fakeInstallFunction()}
WORK_DIR='${toMsysPath(workDir)}'
NODE_HEALTH_URL='http://127.0.0.1:38000/health/aggregate'
install_managed_configuration '${toMsysPath(stagedConfigPath)}'
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  return {
    ...result,
    formalConfig: readFileSync(formalConfigPath, 'utf8'),
    healthUrl: existsSync(path.join(appRoot, 'etc', 'keepalived', 'keepalived-health-url'))
      ? readFileSync(path.join(appRoot, 'etc', 'keepalived', 'keepalived-health-url'), 'utf8')
      : '',
    installedHealthScript: existsSync(path.join(appRoot, 'libexec', 'check-aggregate-health.sh')),
    syntaxCalls: existsSync(syntaxLogPath)
      ? readFileSync(syntaxLogPath, 'utf8').trimEnd().split('\n')
      : []
  }
}

function runExistingRootRollbackHarness(t) {
  const fixture = mkdtempSync(path.join(rootDir, '.aifar-keepalived-root-rollback-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const { appRoot, fixtureInstallerPath } = writeInstallerFixture(fixture)
  const harnessPath = path.join(fixture, 'harness.sh')
  const callLogPath = path.join(fixture, 'systemctl-calls')
  const priorStatePath = path.join(appRoot, 'nested', 'prior-state')
  const failedStatePath = path.join(appRoot, 'failed-state')
  mkdirSync(path.dirname(priorStatePath), { recursive: true })
  writeFileSync(priorStatePath, 'before-update')
  writeFileSync(harnessPath, `#!/usr/bin/env bash
set -Eeuo pipefail
source '${toMsysPath(fixtureInstallerPath)}'
${fakeInstallFunction()}
FAKE_ACTIVE=1
FAKE_ENABLED=1
FAKE_ACTIVATION_ATTEMPTED=0
FAKE_FINAL_ACTIVE_FAILURE=0
${fakeSystemctlFunction(callLogPath)}
mountpoint() { return 1; }
capture_service_state
create_install_backup
rm -f -- '${toMsysPath(priorStatePath)}'
printf 'failed-update\n' >'${toMsysPath(failedStatePath)}'
rollback_install_transaction
`)
  const result = spawnSync(bashPath, [toMsysPath(harnessPath)], { encoding: 'utf8' })
  return {
    ...result,
    failedStateExists: existsSync(failedStatePath),
    priorState: existsSync(priorStatePath) ? readFileSync(priorStatePath, 'utf8') : '',
    calls: existsSync(callLogPath) ? readFileSync(callLogPath, 'utf8').trimEnd().split('\n') : []
  }
}

test('aggregate health probe exposes its guarded public contract', () => {
  assert.equal(existsSync(healthScriptPath), true, 'missing extras/keepalived/check-aggregate-health.sh')
  const source = readFileSync(healthScriptPath, 'utf8')
  assert.match(source, /if \[\[ "\$\{BASH_SOURCE\[0\]\}" == "\$0" \]\]; then/)
})

test('Python 3 interpreter selection is portable instead of requiring one workstation path', () => {
  const source = readFileSync(fileURLToPath(import.meta.url), 'utf8')
  assert.doesNotMatch(source, /^const realPythonPath = /m)
  assert.ok(realPython.command)
  assert.ok(Array.isArray(realPython.prefixArgs))
  assert.equal(supportsPython3(realPython), true)
})

test('aggregate health main uses the fixed URL file for zero arguments and rejects arguments', (t) => {
  const result = runMainHarness(t)
  assert.equal(result.status, 0, result.stderr)
})

test('installer parses validates and renders a valid managed node configuration', (t) => {
  const result = runNodeConfigHarness(t)
  assert.equal(result.status, 0, result.stderr)
  assert.doesNotMatch(result.renderedConfig, /@[A-Z_]+@/)
  assert.match(result.renderedConfig, /router_id AIFAR_192_168_74_132/)
  assert.match(result.renderedConfig, /interface ens160/)
  assert.match(result.renderedConfig, /virtual_router_id 130/)
  assert.match(result.renderedConfig, /priority 150/)
  assert.match(result.renderedConfig, /unicast_src_ip 192\.168\.74\.132/)
  assert.match(result.renderedConfig, /\n        192\.168\.74\.133\n/)
  assert.match(result.renderedConfig, /192\.168\.74\.130\/24 dev ens160/)
})

const invalidNodeConfigs = [
  {
    name: 'missing key',
    config: validNodeConfig().replace(/^KEEPALIVED_HEALTH_URL=.*\n/m, '')
  },
  {
    name: 'duplicate key',
    config: `${validNodeConfig()}KEEPALIVED_PRIORITY=140\n`
  },
  {
    name: 'unknown key',
    config: `${validNodeConfig()}KEEPALIVED_STATE=MASTER\n`
  },
  {
    name: 'equal local and peer addresses',
    config: validNodeConfig({ KEEPALIVED_PEER_IP: '192.168.74.132' })
  },
  {
    name: 'VIP equal to a node address',
    config: validNodeConfig({ KEEPALIVED_VIP_CIDR: '192.168.74.132/24' }),
    ipFunction: `ip() {
    case "\$*" in
        'link show dev ens160') return 0 ;;
        '-o -4 addr show dev ens160') printf '2: ens160 inet 192.168.74.132/24 scope global ens160\\n' ;;
        '-4 route get 192.168.74.132') printf '192.168.74.132 dev ens160 src 192.168.74.132\\n' ;;
        *) return 1 ;;
    esac
}`
  },
  {
    name: 'priority 0',
    config: validNodeConfig({ KEEPALIVED_PRIORITY: '0' })
  },
  {
    name: 'priority 255',
    config: validNodeConfig({ KEEPALIVED_PRIORITY: '255' })
  },
  {
    name: 'virtual router id 0',
    config: validNodeConfig({ KEEPALIVED_VIRTUAL_ROUTER_ID: '0' })
  },
  {
    name: 'virtual router id 256',
    config: validNodeConfig({ KEEPALIVED_VIRTUAL_ROUTER_ID: '256' })
  },
  {
    name: 'remote health host',
    config: validNodeConfig({ KEEPALIVED_HEALTH_URL: 'http://192.168.74.133:38000/health/aggregate' })
  },
  {
    name: 'URL credentials',
    config: validNodeConfig({ KEEPALIVED_HEALTH_URL: 'http://user:pass@192.168.74.132:38000/health' })
  },
  {
    name: 'malformed CIDR',
    config: validNodeConfig({ KEEPALIVED_VIP_CIDR: '192.168.74.130/24/extra' })
  },
  {
    name: 'oversized CIDR prefix',
    config: validNodeConfig({ KEEPALIVED_VIP_CIDR: '192.168.74.130/18446744073709551617' })
  },
  {
    name: 'oversized priority',
    config: validNodeConfig({ KEEPALIVED_PRIORITY: '18446744073709551617' })
  },
  {
    name: 'oversized virtual router id',
    config: validNodeConfig({ KEEPALIVED_VIRTUAL_ROUTER_ID: '18446744073709551617' })
  }
]

for (const invalidCase of invalidNodeConfigs) {
  test(`installer rejects node configuration with ${invalidCase.name}`, (t) => {
    const result = runNodeConfigHarness(t, {
      config: invalidCase.config,
      ipFunction: invalidCase.ipFunction,
      render: false
    })
    assert.equal(result.status, 1, `unexpected status for ${invalidCase.name}\n${result.stderr}`)
  })
}

test('installer rejects command substitution syntax without evaluating node configuration', (t) => {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-command-substitution-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const markerPath = path.join(fixture, 'evaluated')
  const injectedValue = `$(touch\${IFS}${toMsysPath(markerPath)})`
  const result = runNodeConfigHarness(t, {
    config: validNodeConfig({ KEEPALIVED_LOCAL_IP: injectedValue }),
    render: false
  })
  assert.equal(result.status, 1, result.stderr)
  assert.equal(existsSync(markerPath), false, 'node configuration was evaluated')
})

test('installer does not reuse stale node globals when a later parse omits a required key', (t) => {
  const result = runSequentialParseHarness(t)
  assert.equal(result.status, 1, result.stderr)
  assert.match(result.stderr, /KEEPALIVED_HEALTH_URL/)
})

test('installer restarts a service that was previously active and enabled', (t) => {
  const result = runServiceHarness(t, { active: true, enabled: true })
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(result.calls, [
    'is-active --quiet keepalived.service',
    'is-enabled --quiet keepalived.service',
    'daemon-reload',
    'enable keepalived.service',
    'restart keepalived.service',
    'is-active --quiet keepalived.service'
  ])
})

test('installer enables and starts a service that was previously inactive and disabled', (t) => {
  const result = runServiceHarness(t, { active: false, enabled: false })
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(result.calls, [
    'is-active --quiet keepalived.service',
    'is-enabled --quiet keepalived.service',
    'daemon-reload',
    'enable keepalived.service',
    'start keepalived.service',
    'is-active --quiet keepalived.service'
  ])
})

test('installer keeps an active service when the aggregate health command fails', (t) => {
  const result = runServiceHarness(t, { active: false, enabled: false, healthStatus: 1 })
  assert.equal(result.status, 0, result.stderr)
  assert.match(
    result.stdout,
    /\[keepalived-installer\] WARNING: 健康检查当前不可用；服务保持 active，VRRP 实例将保持 FAULT/
  )
  assert.ok(result.calls.includes('start keepalived.service'))
})

test('installer fails activation when the final active-state check fails', (t) => {
  const result = runServiceHarness(t, {
    active: false,
    enabled: false,
    finalActiveFailure: true
  })
  assert.equal(result.status, 1, result.stderr)
  assert.match(result.stderr, /\[keepalived-installer\] ERROR: keepalived\.service 启动失败/)
  assert.equal(result.calls.at(-1), 'is-active --quiet keepalived.service')
})

test('rollback restores a previously active and enabled service', (t) => {
  const result = runRollbackHarness(t, { active: true, enabled: true })
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.appRootExists, false)
  assert.deepEqual(result.calls, [
    'stop keepalived.service',
    'daemon-reload',
    'enable keepalived.service',
    'restart keepalived.service',
    'daemon-reload'
  ])
})

test('rollback restores a previously inactive and disabled service', (t) => {
  const result = runRollbackHarness(t, { active: false, enabled: false })
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.appRootExists, false)
  assert.deepEqual(result.calls, [
    'stop keepalived.service',
    'daemon-reload',
    'disable keepalived.service',
    'stop keepalived.service',
    'daemon-reload'
  ])
})

test('rollback restores a previously disabled inactive owned unit link after disable removes it', (t) => {
  const result = runRollbackHarness(t, {
    active: false,
    enabled: false,
    unitLinkExisted: true
  })
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.unitLinkExists, true)
  assert.match(result.unitLinkTarget, /\/systemd\/keepalived\.service$/)
  const disableIndex = result.calls.indexOf('disable keepalived.service')
  assert.notEqual(disableIndex, -1)
  assert.ok(disableIndex < result.calls.lastIndexOf('daemon-reload'))
})

test('installer verifies a full-root backup before activating the transaction', (t) => {
  const result = runBackupHarness(t)
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.state, '1|1|1|1')
  assert.equal(result.backupDirectories.length, 1)
  const backupDirectory = result.backupDirectories[0]
  assert.equal(
    readFileSync(path.join(backupDirectory, 'installed-root', 'nested', 'prior-state'), 'utf8'),
    'before-update'
  )
  assert.match(readFileSync(path.join(backupDirectory, 'install-state.txt'), 'utf8'), /app_root_existed=1/)
  assert.match(readFileSync(path.join(backupDirectory, 'BACKUP.sha256'), 'utf8'), /installed-root\/nested\/prior-state/)
})

test('installer refuses a transaction whose existing app root is a mount point', (t) => {
  const result = runBackupHarness(t, { mountpointStatus: 0 })
  assert.equal(result.status, 1, result.stderr)
  assert.equal(result.state, '')
})

test('first install verifies an empty-root transaction backup before host mutation', (t) => {
  const result = runBackupHarness(t, { appRootExists: false })
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.state, '1|0|1|1')
  assert.equal(result.backupDirectories.length, 1)
  const backupDirectory = result.backupDirectories[0]
  assert.equal(existsSync(path.join(backupDirectory, 'installed-root')), false)
  assert.match(readFileSync(path.join(backupDirectory, 'BACKUP.sha256'), 'utf8'), /install-state\.txt/)
})

test('cleanup preserves the original failure status when rollback also fails', (t) => {
  const result = runCleanupRollbackFailureHarness(t)
  assert.equal(result.status, 37, result.stderr)
  assert.equal(result.rollbackCalled, true)
  assert.match(result.stderr, /回滚也未完整成功/)
})

test('installer validates staged and atomically installed managed configuration', (t) => {
  const result = runManagedConfigurationHarness(t)
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.formalConfig, 'managed-config\n')
  assert.equal(result.healthUrl, 'http://127.0.0.1:38000/health/aggregate\n')
  assert.equal(result.installedHealthScript, true)
  assert.equal(result.syntaxCalls.length, 2)
  assert.match(result.syntaxCalls[0], /-t -f .*\/work\/keepalived\.conf$/)
  assert.match(result.syntaxCalls[1], /-t -f .*\/etc\/keepalived\/keepalived\.conf$/)
})

test('installer leaves the formal configuration unchanged when staged syntax is invalid', (t) => {
  const result = runManagedConfigurationHarness(t, { stagedStatus: 1 })
  assert.equal(result.status, 1, result.stderr)
  assert.equal(result.formalConfig, 'previous-config\n')
  assert.equal(result.syntaxCalls.length, 1)
})

test('rollback replaces a failed update with the verified full previous root', (t) => {
  const result = runExistingRootRollbackHarness(t)
  assert.equal(result.status, 0, result.stderr)
  assert.equal(result.priorState, 'before-update')
  assert.equal(result.failedStateExists, false)
  assert.deepEqual(result.calls.slice(-5), [
    'stop keepalived.service',
    'daemon-reload',
    'enable keepalived.service',
    'restart keepalived.service',
    'daemon-reload'
  ])
})

test('active firewalld adds exact peer-scoped runtime and permanent rules in the interface zone', (t) => {
  const rule = peerRule('192.168.74.133')
  const result = runFirewallHarness(t)
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(result.runtimeRules, [rule])
  assert.deepEqual(result.permanentRules, [rule])
  assert.equal(result.record, `zone=work\nrule=${rule}\nruntime_created=1\npermanent_created=1\n`)
  assert.ok(result.calls.includes('--get-zone-of-interface=ens160'))
  assert.equal(result.calls.includes('--get-default-zone'), false)
  assert.equal(result.calls.some((call) => call === '--reload'), false)
})

test('firewall reconciliation falls back to the default zone only when the interface has no zone', (t) => {
  const result = runFirewallHarness(t, { interfaceZone: 'no zone', defaultZone: 'trusted' })
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.record, /^zone=trusted$/m)
  assert.deepEqual(result.calls.slice(0, 2), ['--get-zone-of-interface=ens160', '--get-default-zone'])
})

test('firewall reconciliation does not hide an interface-zone lookup failure behind the default zone', (t) => {
  const result = runFirewallHarness(t, { interfaceZoneStatus: 54 })
  assert.equal(result.status, 1, result.stderr)
  assert.equal(result.calls.includes('--get-default-zone'), false)
  assert.equal(result.record, '')
})

test('exact pre-existing firewall rules are retained but not marked owned', (t) => {
  const rule = peerRule('192.168.74.133')
  const result = runFirewallHarness(t, { runtimeRules: [rule], permanentRules: [rule] })
  assert.equal(result.status, 0, result.stderr)
  assert.match(result.record, /runtime_created=0\npermanent_created=0\n$/)
  assert.equal(result.calls.some((call) => call.includes('--add-rich-rule=')), false)
})

test('partial firewall add failure rolls back the runtime form added by this transaction', (t) => {
  const result = runFirewallHarness(t, { failAddForm: 'permanent', rollbackOnFailure: true })
  assert.equal(result.status, 1, result.stderr)
  assert.deepEqual(result.runtimeRules, [])
  assert.deepEqual(result.permanentRules, [])
  assert.equal(result.record, '')
  assert.ok(result.calls.some((call) => call.includes('--remove-rich-rule=')))
})

test('changing peer removes only the previously owned exact rule and records the new peer', (t) => {
  const oldRule = peerRule('192.168.74.140')
  const newRule = peerRule('192.168.74.133')
  const unrelated = peerRule('192.168.74.141')
  const result = runFirewallHarness(t, {
    runtimeRules: [oldRule, unrelated],
    permanentRules: [oldRule, unrelated],
    oldRecord: { zone: 'work', rule: oldRule, runtimeCreated: 1, permanentCreated: 1 }
  })
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(result.runtimeRules, [unrelated, newRule])
  assert.deepEqual(result.permanentRules, [unrelated, newRule])
  assert.equal(result.record, `zone=work\nrule=${newRule}\nruntime_created=1\npermanent_created=1\n`)
})

test('firewall journal rollback reverses added and removed entries from last to first', (t) => {
  const oldRule = peerRule('192.168.74.140')
  const result = runFirewallRollbackHarness(t)
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(result.runtimeRules, [oldRule])
  assert.deepEqual(result.permanentRules, [oldRule])
  const mutations = result.calls.filter((call) => /--(?:add|remove)-rich-rule=/.test(call))
  assert.match(mutations[0], /^--permanent .*--remove-rich-rule=/)
  assert.match(mutations[1], /^--zone=.*--remove-rich-rule=/)
  assert.match(mutations[2], /^--permanent .*--add-rich-rule=/)
  assert.match(mutations[3], /^--zone=.*--add-rich-rule=/)
})

test('inactive firewalld performs no firewall command and writes no ownership record', (t) => {
  const result = runFirewallHarness(t, { active: false })
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(result.calls, [])
  assert.equal(result.record, '')
})

test('uninstaller rejects a malformed firewall ownership record', (t) => {
  const result = runUninstallFirewallHarness(t, {
    record: 'zone=public\nrule=rule family="ipv4" protocol value="112" accept\nruntime_created=1\npermanent_created=1\n'
  })
  assert.equal(result.status, 1, result.stderr)
  assert.match(result.stderr, /防火墙.*记录|firewall/i)
})

test('uninstaller removes only exact firewall forms marked created', (t) => {
  const rule = peerRule('192.168.74.133')
  const result = runUninstallFirewallHarness(t, {
    record: { zone: 'public', rule, runtimeCreated: 1, permanentCreated: 0 },
    runtimeRules: [rule],
    permanentRules: [rule]
  })
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(result.runtimeRules, [])
  assert.deepEqual(result.permanentRules, [rule])
  assert.equal(result.calls.some((call) => call.startsWith('--permanent')), false)
})

test('uninstaller rejects an empty firewall ownership record without mutating rules or installation state', (t) => {
  const rule = peerRule('192.168.74.133')
  const result = runUninstallFirewallHarness(t, {
    record: '',
    runtimeRules: [rule],
    permanentRules: [rule]
  })
  assert.equal(result.status, 1, result.stderr)
  assert.deepEqual(result.runtimeRules, [rule])
  assert.deepEqual(result.permanentRules, [rule])
  assert.equal(result.appRootExists, true)
  assert.equal(result.recordExists, true)
})

test('uninstaller rejects a symlink firewall ownership record without mutating rules or installation state', (t) => {
  const rule = peerRule('192.168.74.133')
  const result = runUninstallFirewallHarness(t, {
    record: { zone: 'public', rule, runtimeCreated: 1, permanentCreated: 1 },
    recordSymlink: true,
    runtimeRules: [rule],
    permanentRules: [rule]
  })
  assert.equal(result.status, 1, result.stderr)
  assert.notEqual(result.status, 96, 'test environment did not create an MSYS-recognized symlink')
  assert.deepEqual(result.runtimeRules, [rule])
  assert.deepEqual(result.permanentRules, [rule])
  assert.equal(result.appRootExists, true)
  assert.equal(result.recordExists, true)
})

for (const mutation of ['add', 'remove']) {
  for (const form of ['runtime', 'permanent']) {
    test(`firewall ${mutation} ${form} mutation is not attempted when write-ahead journal append fails`, (t) => {
      const rule = peerRule('192.168.74.133')
      const result = runMutationJournalFailureHarness(t, { mutation, form })
      assert.equal(result.status, 1, result.stderr)
      assert.equal(result.calls.some((call) => call.includes(`--${mutation}-rich-rule=`)), false)
      assert.deepEqual(result.runtimeRules, mutation === 'remove' && form === 'runtime' ? [rule] : [])
      assert.deepEqual(result.permanentRules, mutation === 'remove' && form === 'permanent' ? [rule] : [])
    })
  }
}

test('installer rejects a missing configured interface', (t) => {
  const result = runNodeConfigHarness(t, {
    ipFunction: `ip() { return 1; }`,
    render: false
  })
  assert.equal(result.status, 1, result.stderr)
})

test('installer rejects a local address not bound to the configured interface', (t) => {
  const result = runNodeConfigHarness(t, {
    ipFunction: `ip() {
    case "\$*" in
        'link show dev ens160') return 0 ;;
        '-o -4 addr show dev ens160') printf '2: ens160 inet 192.168.74.140/24 scope global ens160\\n' ;;
        *) return 1 ;;
    esac
}`,
    render: false
  })
  assert.equal(result.status, 1, result.stderr)
})

test('installer rejects a VIP routed through the wrong interface', (t) => {
  const result = runNodeConfigHarness(t, {
    ipFunction: `ip() {
    case "\$*" in
        'link show dev ens160') return 0 ;;
        '-o -4 addr show dev ens160') printf '2: ens160 inet 192.168.74.132/24 scope global ens160\\n' ;;
        '-4 route get 192.168.74.130') printf '192.168.74.130 dev ens192 src 192.168.74.132\\n' ;;
        *) return 1 ;;
    esac
}`,
    render: false
  })
  assert.equal(result.status, 1, result.stderr)
})

test('aggregate health invokes the embedded Python parser through a real Python 3 interpreter', (t) => {
  const result = runParserHarness(t, '{"status":{"up":true},"up":true}')
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(result.pythonArgs.slice(0, 1), ['-c'])
  assert.match(result.pythonArgs[1], /json\.load\(sys\.stdin\)/)
})

const healthCases = [
  { body: '{"status":{"up":true},"up":true}', status: 0, expected: 0 },
  { body: '{"up":false}', status: 0, expected: 1 },
  { body: '{"up":"true"}', status: 0, expected: 1 },
  { body: '{"up":true', status: 0, expected: 1 },
  { body: '{"up":true}', status: 22, expected: 1 },
  { body: '{"up":true}', status: 28, expected: 1 }
]

for (const healthCase of healthCases) {
  test(`aggregate health returns ${healthCase.expected} for curl status ${healthCase.status} and body ${healthCase.body}`, (t) => {
    const result = runHealthHarness(t, healthCase)
    assert.equal(result.status, healthCase.expected, result.stderr)
    if (healthCase.status === 0 && healthCase.expected === 0) {
      assert.deepEqual(result.curlArgs, [
        '--fail', '--silent', '--show-error', '--connect-timeout', '1', '--max-time', '2', '--',
        'http://127.0.0.1:38000/health'
      ])
      assert.deepEqual(result.pythonArgs.slice(0, 1), ['-c'])
    }
  })
}

for (const invalidUrl of [
  'http://127.0.0.1:38000/health\nhttp://127.0.0.2:38000/health',
  'http://user:pass@127.0.0.1:38000/health',
  'http://127.0.0.1:0/health',
  'http://127.0.0.1:65536/health'
]) {
  test(`aggregate health rejects invalid URL input ${JSON.stringify(invalidUrl)}`, (t) => {
    const result = runHealthHarness(t, {
      body: '{"up":true}',
      url: invalidUrl
    })
    assert.equal(result.status, 1, result.stderr)
  })
}
