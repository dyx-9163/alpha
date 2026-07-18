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
