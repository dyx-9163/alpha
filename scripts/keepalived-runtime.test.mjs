import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptsDir, '..')
const healthScriptPath = path.join(rootDir, 'extras', 'keepalived', 'check-aggregate-health.sh')
const installerPath = path.join(rootDir, 'extras', 'keepalived', 'install-keepalived-offline.sh')
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
