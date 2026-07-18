import assert from 'node:assert/strict'
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptsDir, '..')
const healthScriptPath = path.join(rootDir, 'extras', 'keepalived', 'check-aggregate-health.sh')
const bashPath = 'D:\\tools\\git\\bin\\bash.exe'

function toMsysPath(filePath) {
  const normalized = path.resolve(filePath).replaceAll('\\', '/')
  return normalized.replace(/^([A-Za-z]):/, (_, drive) => `/${drive.toLowerCase()}`)
}

function runHealthHarness(t, { body, status = 0, url = 'http://127.0.0.1:38000/health' }) {
  const fixture = mkdtempSync(path.join(os.tmpdir(), 'aifar-keepalived-health-test-'))
  t.after(() => rmSync(fixture, { force: true, recursive: true }))
  const harnessPath = path.join(fixture, 'harness.sh')
  const urlPath = path.join(fixture, 'keepalived-health-url')
  const bashEnvPath = path.join(fixture, 'bash-env.sh')
  writeFileSync(urlPath, url)
  writeFileSync(bashEnvPath, `python3() {
  node -e 'let input = ""; process.stdin.setEncoding("utf8"); process.stdin.on("data", (chunk) => { input += chunk }); process.stdin.on("end", () => { try { const value = JSON.parse(input); process.exit(value !== null && typeof value === "object" && !Array.isArray(value) && value.up === true ? 0 : 1) } catch { process.exit(1) } })'
}
`)
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
  return spawnSync(bashPath, [toMsysPath(harnessPath)], {
    encoding: 'utf8',
    env: {
      ...process.env,
      BASH_ENV: toMsysPath(bashEnvPath),
      FAKE_CURL_BODY: body,
      FAKE_CURL_STATUS: String(status)
    }
  })
}

test('aggregate health probe exposes its guarded public contract', () => {
  assert.equal(existsSync(healthScriptPath), true, 'missing extras/keepalived/check-aggregate-health.sh')
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
