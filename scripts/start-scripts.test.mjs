import assert from 'node:assert/strict'
import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const repositoryScriptsDir = path.dirname(fileURLToPath(import.meta.url))

function findExecutable(candidates, args = ['--version']) {
  for (const candidate of candidates) {
    if (!candidate) continue
    const result = spawnSync(candidate, args, { stdio: 'ignore' })
    if (!result.error) return candidate
  }
  return undefined
}

const shell = findExecutable([
  process.env.AIFAR_TEST_SH,
  'D:\\tools\\git\\bin\\sh.exe',
  'C:\\Program Files\\Git\\bin\\sh.exe',
  'sh'
])
const powershell = findExecutable([
  process.env.AIFAR_TEST_POWERSHELL,
  'powershell.exe',
  'pwsh'
], ['-NoProfile', '-Command', '$PSVersionTable.PSVersion.ToString()'])
const powershellArgs = ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command']

function tempRoot(t, prefix) {
  const directory = mkdtempSync(path.join(tmpdir(), prefix))
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  return directory
}

function quotePowerShell(value) {
  return `'${String(value).replaceAll("'", "''")}'`
}

function fixtureEnv(overrides = {}) {
  const env = { ...process.env }
  for (const key of Object.keys(env)) {
    if (key.startsWith('AIFAR_')) delete env[key]
  }
  return { ...env, ...overrides }
}

function runShellFixture(t, defaults, env = {}) {
  const root = tempRoot(t, 'aifar-start-sh-')
  mkdirSync(path.join(root, 'config'), { recursive: true })
  mkdirSync(path.join(root, 'bin'), { recursive: true })
  copyFileSync(path.join(repositoryScriptsDir, 'start.sh'), path.join(root, 'start.sh'))
  writeFileSync(path.join(root, 'config', 'defaults.env'), defaults)
  const fakeBin = path.join(root, 'bin', 'aifar-server-linux-amd64')
  writeFileSync(fakeBin, `#!/usr/bin/env sh
printf 'ADDR_SET=%s\\n' "\${AIFAR_ADDR+x}"
printf 'ADDR=<%s>\\n' "\${AIFAR_ADDR-}"
printf 'DEPLOY=<%s>\\n' "\${AIFAR_DEFAULT_DEPLOY_DIR-}"
printf 'STATIC=<%s>\\n' "\${AIFAR_STATIC_DIR-}"
`)
  chmodSync(fakeBin, 0o755)
  const script = path.join(root, 'start.sh').replaceAll('\\', '/')
  return spawnSync(shell, [script, 'foreground'], {
    env: fixtureEnv(env),
    encoding: 'utf8'
  })
}

test('start scripts have repository-mandated line endings', () => {
  const bat = readFileSync(path.join(repositoryScriptsDir, 'start.bat'))
  const ps1 = readFileSync(path.join(repositoryScriptsDir, 'start.ps1'))
  const sh = readFileSync(path.join(repositoryScriptsDir, 'start.sh'))

  assert.doesNotMatch(bat.toString('binary'), /(?<!\r)\n/)
  assert.doesNotMatch(ps1.toString('binary'), /(?<!\r)\n/)
  assert.doesNotMatch(bat.toString('binary'), /\r\r\n/)
  assert.doesNotMatch(ps1.toString('binary'), /\r\r\n/)
  assert.doesNotMatch(sh.toString('binary'), /\r/)
})

test('start.bat only delegates to PowerShell and preserves arguments and exit code', () => {
  const text = readFileSync(path.join(repositoryScriptsDir, 'start.bat'), 'utf8')
  const meaningful = text.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
  assert.deepEqual(meaningful, [
    '@echo off',
    'setlocal',
    'set "ROOT=%~dp0"',
    'powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%ROOT%start.ps1" %*',
    'exit /b %ERRORLEVEL%'
  ])
})

test('start.sh rejects malformed and duplicate defaults without echoing values', { skip: !shell }, (t) => {
  const secret = 'do-not-echo-shell-secret'
  const malformed = runShellFixture(t, `AIFAR_JWT_SECRET=${secret}\nmalformed-${secret}\n`)
  assert.notEqual(malformed.status, 0)
  assert.match(malformed.stderr, /Malformed defaults\.env line 2/)
  assert.doesNotMatch(malformed.stderr, new RegExp(secret))

  const duplicate = runShellFixture(t, 'AIFAR_ADDR=one\nAIFAR_ADDR=two\n')
  assert.notEqual(duplicate.status, 0)
  assert.match(duplicate.stderr, /Duplicate configuration key.*AIFAR_ADDR/)
})

test('start.sh preserves quoted equals, defaults empty values, and process explicit empty', { skip: !shell }, (t) => {
  const fromDefaults = runShellFixture(t, `
    # ignored comment
AIFAR_ADDR="quoted=value"
AIFAR_DEFAULT_DEPLOY_DIR=
`)
  assert.equal(fromDefaults.status, 0, fromDefaults.stderr)
  assert.match(fromDefaults.stdout, /ADDR=<quoted=value>/)
  assert.match(fromDefaults.stdout, /DEPLOY=<>/)

  const fromProcess = runShellFixture(t, 'AIFAR_ADDR=defaults-value\nAIFAR_STATIC_DIR=defaults-static\n', {
    AIFAR_ADDR: '',
    AIFAR_STATIC_DIR: ''
  })
  assert.equal(fromProcess.status, 0, fromProcess.stderr)
  assert.match(fromProcess.stdout, /ADDR_SET=x/)
  assert.match(fromProcess.stdout, /ADDR=<>/)
  assert.match(fromProcess.stdout, /STATIC=<>/)
})

test('start.ps1 parses cleanly and exposes dot-sourceable environment functions', { skip: !powershell }, () => {
  const script = path.join(repositoryScriptsDir, 'start.ps1')
  const command = `$errors = $null; [System.Management.Automation.Language.Parser]::ParseFile(${quotePowerShell(script)}, [ref]$null, [ref]$errors) | Out-Null; if ($errors.Count -gt 0) { $errors | ForEach-Object { [Console]::Error.WriteLine($_.Message) }; exit 1 }; . ${quotePowerShell(script)}; if (-not (Get-Command Resolve-AifarEnvironment -ErrorAction SilentlyContinue)) { exit 2 }`
  const result = spawnSync(powershell, [...powershellArgs, command], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
})

test('PowerShell defaults parser rejects malformed and duplicate keys without echoing values', { skip: !powershell }, (t) => {
  const root = tempRoot(t, 'aifar-start-ps-')
  const malformedPath = path.join(root, 'malformed.env')
  const duplicatePath = path.join(root, 'duplicate.env')
  const illegalKeyPath = path.join(root, 'illegal-key.env')
  const secret = 'do-not-echo-powershell-secret'
  writeFileSync(malformedPath, `AIFAR_JWT_SECRET=${secret}\nmalformed-${secret}\n`)
  writeFileSync(duplicatePath, 'AIFAR_ADDR=one\nAIFAR_ADDR=two\n')
  writeFileSync(illegalKeyPath, 'AIFAR_lower=value\n')
  const script = path.join(repositoryScriptsDir, 'start.ps1')

  const malformedCommand = `. ${quotePowerShell(script)}; Resolve-AifarEnvironment -Root ${quotePowerShell(root)} -DefaultsPath ${quotePowerShell(malformedPath)} -Environment @{}`
  const malformed = spawnSync(powershell, [...powershellArgs, malformedCommand], { encoding: 'utf8' })
  assert.notEqual(malformed.status, 0)
  assert.match(malformed.stderr, /Malformed defaults\.env line 2/)
  assert.doesNotMatch(malformed.stderr, new RegExp(secret))

  const duplicateCommand = `. ${quotePowerShell(script)}; Resolve-AifarEnvironment -Root ${quotePowerShell(root)} -DefaultsPath ${quotePowerShell(duplicatePath)} -Environment @{}`
  const duplicate = spawnSync(powershell, [...powershellArgs, duplicateCommand], { encoding: 'utf8' })
  assert.notEqual(duplicate.status, 0)
  assert.match(duplicate.stderr, /Duplicate configuration key.*AIFAR_ADDR/)

  const illegalKeyCommand = `. ${quotePowerShell(script)}; Resolve-AifarEnvironment -Root ${quotePowerShell(root)} -DefaultsPath ${quotePowerShell(illegalKeyPath)} -Environment @{}`
  const illegalKey = spawnSync(powershell, [...powershellArgs, illegalKeyCommand], { encoding: 'utf8' })
  assert.notEqual(illegalKey.status, 0)
  assert.match(illegalKey.stderr, /Malformed defaults\.env line 1/)
})

test('PowerShell environment merge preserves process and defaults explicit empty values', { skip: !powershell }, (t) => {
  const root = tempRoot(t, 'aifar-start-ps-')
  const defaultsPath = path.join(root, 'defaults.env')
  writeFileSync(defaultsPath, `
    # ignored comment
AIFAR_ADDR=defaults-address
AIFAR_STATIC_DIR=defaults-static
AIFAR_DEFAULT_DEPLOY_DIR='quoted=value'
AIFAR_RESOURCE_DIR=
`)
  const script = path.join(repositoryScriptsDir, 'start.ps1')
  const command = `. ${quotePowerShell(script)}; $resolved = Resolve-AifarEnvironment -Root ${quotePowerShell(root)} -DefaultsPath ${quotePowerShell(defaultsPath)} -Environment @{ AIFAR_ADDR = ''; AIFAR_STATIC_DIR = '' }; [ordered]@{ addr = $resolved['AIFAR_ADDR']; static = $resolved['AIFAR_STATIC_DIR']; deploy = $resolved['AIFAR_DEFAULT_DEPLOY_DIR']; resource = $resolved['AIFAR_RESOURCE_DIR'] } | ConvertTo-Json -Compress`
  const result = spawnSync(powershell, [...powershellArgs, command], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(JSON.parse(result.stdout.trim()), {
    addr: '',
    static: '',
    deploy: 'quoted=value',
    resource: ''
  })
})
