import assert from 'node:assert/strict'
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const repositoryScriptsDir = path.dirname(fileURLToPath(import.meta.url))

function findPowerShell() {
  for (const candidate of [process.env.AIFAR_TEST_POWERSHELL, 'powershell.exe', 'pwsh']) {
    if (!candidate) continue
    const result = spawnSync(candidate, ['-NoProfile', '-Command', '$PSVersionTable.PSVersion.ToString()'], { stdio: 'ignore' })
    if (!result.error) return candidate
  }
  return undefined
}

const powershell = findPowerShell()

function tempRoot(t) {
  const directory = mkdtempSync(path.join(tmpdir(), 'aifar-export-alpha-jars-'))
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  return directory
}

test('export-alpha-jars copies selected jars into runtime-v2 service targets', { skip: !powershell }, (t) => {
  const root = tempRoot(t)
  const scriptDir = path.join(root, 'scripts')
  const script = path.join(scriptDir, 'export-alpha-jars.ps1')
  const sourceRoot = path.join(root, 'alpha-java-cloud')
  const targetRoot = path.join(root, 'resources', 'aifar', 'runtime-v2', 'services')
  const moduleTarget = path.join(sourceRoot, 'alpha-gateway', 'target')
  const serviceTarget = path.join(targetRoot, 'gateway', 'target')

  mkdirSync(scriptDir, { recursive: true })
  mkdirSync(moduleTarget, { recursive: true })
  mkdirSync(serviceTarget, { recursive: true })
  copyFileSync(path.join(repositoryScriptsDir, 'export-alpha-jars.ps1'), script)
  writeFileSync(path.join(moduleTarget, 'alpha-gateway-5.1.0-RELEASE.jar'), 'new gateway jar')
  writeFileSync(path.join(serviceTarget, 'stale.jar'), 'old gateway jar')

  const result = spawnSync(powershell, [
    '-NoProfile',
    '-ExecutionPolicy',
    'Bypass',
    '-File',
    script,
    '-SourceRoot',
    sourceRoot,
    '-TargetRoot',
    targetRoot,
    '-Services',
    'gateway',
    '-RequireAll'
  ], {
    cwd: root,
    encoding: 'utf8'
  })

  assert.equal(result.status, 0, result.stderr)
  const copied = path.join(serviceTarget, 'alpha-gateway.jar')
  assert.equal(readFileSync(copied, 'utf8'), 'new gateway jar')
  assert.equal(existsSync(path.join(serviceTarget, 'stale.jar')), false)
})

test('export-alpha-jars defaults to the current repository runtime-v2 services directory', { skip: !powershell }, (t) => {
  const root = tempRoot(t)
  const scriptDir = path.join(root, 'scripts')
  const script = path.join(scriptDir, 'export-alpha-jars.ps1')
  const sourceRoot = path.join(root, 'alpha-java-cloud')
  const moduleTarget = path.join(sourceRoot, 'alpha-oauth', 'target')

  mkdirSync(scriptDir, { recursive: true })
  mkdirSync(moduleTarget, { recursive: true })
  copyFileSync(path.join(repositoryScriptsDir, 'export-alpha-jars.ps1'), script)
  writeFileSync(path.join(moduleTarget, 'alpha-oauth-5.1.0-RELEASE.jar'), 'new oauth jar')

  const result = spawnSync(powershell, [
    '-NoProfile',
    '-ExecutionPolicy',
    'Bypass',
    '-File',
    script,
    '-SourceRoot',
    sourceRoot,
    '-Services',
    'oauth',
    '-RequireAll'
  ], {
    cwd: root,
    encoding: 'utf8'
  })

  assert.equal(result.status, 0, result.stderr)
  const copied = path.join(root, 'resources', 'aifar', 'runtime-v2', 'services', 'oauth', 'target', 'alpha-oauth.jar')
  assert.equal(readFileSync(copied, 'utf8'), 'new oauth jar')
})
