import assert from 'node:assert/strict'
import { chmodSync, copyFileSync, cpSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))

function write(root, relativePath, contents = '') {
  const file = path.join(root, relativePath)
  mkdirSync(path.dirname(file), { recursive: true })
  writeFileSync(file, contents)
}

function releaseFixture(t, { safeDefaults = true, buildOutputs = true, binaries = true } = {}) {
  const root = mkdtempSync(path.join(tmpdir(), 'aifar-release-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  mkdirSync(path.join(root, 'scripts'), { recursive: true })
  copyFileSync(path.join(scriptsDir, 'package-release.mjs'), path.join(root, 'scripts', 'package-release.mjs'))
  copyFileSync(path.join(scriptsDir, 'verify-release-checksums.mjs'), path.join(root, 'scripts', 'verify-release-checksums.mjs'))
  copyFileSync(path.join(scriptsDir, 'toolchain.mjs'), path.join(root, 'scripts', 'toolchain.mjs'))
  copyFileSync(path.join(scriptsDir, 'runtime-security-config.mjs'), path.join(root, 'scripts', 'runtime-security-config.mjs'))
  copyFileSync(path.join(scriptsDir, 'normalize-tar-modes.mjs'), path.join(root, 'scripts', 'normalize-tar-modes.mjs'))
  write(root, 'package.json', JSON.stringify({ name: 'aifar-fixture', version: '9.8.7' }))
  write(root, 'config/defaults.env', safeDefaults
    ? `AIFAR_DEFAULT_PASSWORD=\nAIFAR_BOOTSTRAP_PASSWORD=\nAIFAR_JWT_SECRET=\nAIFAR_CREDENTIAL_SECRET=\nAIFAR_PREVIOUS_CREDENTIAL_SECRET=\nAIFAR_ALLOW_INSECURE_DEFAULTS=false\nAIFAR_ALLOW_WEAK_PASSWORDS=false\n`
    : `AIFAR_JWT_SECRET=unsafe-secret-must-not-ship\nAIFAR_ALLOW_INSECURE_DEFAULTS=false\nAIFAR_ALLOW_WEAK_PASSWORDS=false\n`)
  write(root, 'scripts/start.sh', '#!/usr/bin/env sh\n')
  write(root, 'scripts/stop.sh', '#!/usr/bin/env sh\n')
  write(root, 'scripts/start.ps1', '')
  write(root, 'scripts/start.bat', '')
  if (buildOutputs) write(root, 'deploy/dist/index.html', '<!doctype html>')
  write(root, 'resources/.keep', '')
  write(root, 'extras/keepalived/README.md', 'keepalived tools\n')
  write(root, 'extras/keepalived/keepalived-2.4.2.tar.gz', 'fixture archive')
  write(root, 'extras/keepalived/SHA256SUMS', 'fixture checksum\n')
  write(root, 'extras/keepalived/install-keepalived-offline.sh', '#!/usr/bin/env bash\n')
  write(root, 'extras/keepalived/check-aggregate-health.sh', '#!/usr/bin/env bash\n')
  write(root, 'extras/keepalived/keepalived.env.example', 'KEEPALIVED_LOCAL_IP=192.0.2.10\n')
  write(root, 'extras/keepalived/keepalived.conf.tpl', 'state BACKUP\n')
  write(root, 'extras/keepalived/configure-selinux.sh', '#!/usr/bin/env bash\n')
  write(root, 'extras/keepalived/uninstall-keepalived.sh', '#!/usr/bin/env bash\n')
  write(root, 'extras/selinux/README.md', 'aggregate SELinux tool\n')
  write(root, 'extras/selinux/configure-all-selinux.sh', '#!/usr/bin/env bash\n')
  if (binaries) {
    write(root, 'deploy/bin/aifar-server-linux-amd64', 'linux-server')
    write(root, 'deploy/bin/aifar-agent-linux-amd64', 'linux-agent')
    write(root, 'deploy/bin/aifar-server-windows-amd64.exe', 'windows-server')
  }
  return root
}

function runRelease(root, { emptyPath = false, pathValue } = {}) {
  const env = { ...process.env }
  if (emptyPath) {
    for (const key of Object.keys(env)) {
      if (key.toLowerCase() === 'path') delete env[key]
    }
    env[process.platform === 'win32' ? 'Path' : 'PATH'] = ''
  } else if (pathValue !== undefined) {
    for (const key of Object.keys(env)) {
      if (key.toLowerCase() === 'path') delete env[key]
    }
    env[process.platform === 'win32' ? 'Path' : 'PATH'] = pathValue
  }
  return spawnSync(process.execPath, [path.join(root, 'scripts', 'package-release.mjs')], {
    cwd: root,
    env,
    encoding: 'utf8'
  })
}

function runVerify(root) {
  return spawnSync(process.execPath, [path.join(root, 'scripts', 'verify-release-checksums.mjs')], {
    cwd: root,
    encoding: 'utf8'
  })
}

function stagingEntries(root) {
  const stage = path.join(root, 'deploy', '.stage')
  return existsSync(stage) ? readdirSync(stage) : []
}

test('release validates source defaults before requiring or copying build outputs', (t) => {
  const root = releaseFixture(t, { safeDefaults: false, buildOutputs: false, binaries: false })
  const result = runRelease(root)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /Unsafe release security configuration/)
  assert.doesNotMatch(result.stderr, /Missing required package input: deploy\/dist/)
  assert.deepEqual(stagingEntries(root), [])
})

test('release always removes staging when a required binary is missing', (t) => {
  const root = releaseFixture(t, { binaries: false })
  const result = runRelease(root)

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /Missing backend binary/)
  assert.deepEqual(stagingEntries(root), [])
})

test('archive command startup failure is fatal and still removes staging', (t) => {
  const root = releaseFixture(t)
  const result = runRelease(root, { emptyPath: true })

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /archive/i)
  assert.deepEqual(stagingEntries(root), [])
})

test('failed archive commands remove partial archive files', (t) => {
  const root = releaseFixture(t)
  const fakeBin = mkdtempSync(path.join(tmpdir(), 'aifar-release-tools-'))
  t.after(() => rmSync(fakeBin, { recursive: true, force: true }))
  if (process.platform === 'win32') {
    writeFileSync(path.join(fakeBin, 'tar.cmd'), '@echo off\r\n> "%~2" echo partial\r\nexit /b 7\r\n')
  } else {
    const fakeTar = path.join(fakeBin, 'tar')
    writeFileSync(fakeTar, '#!/usr/bin/env sh\nprintf partial > "$2"\nexit 7\n')
    chmodSync(fakeTar, 0o755)
  }

  const result = runRelease(root, { pathValue: fakeBin })

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /Could not create tar archive/)
  assert.equal(existsSync(path.join(root, 'deploy/deployment/aifar-fixture-9.8.7-linux-amd64.tar.gz')), false)
  assert.deepEqual(stagingEntries(root), [])
})

test('release verifier requires the exact current platform directories and archives', (t) => {
  const root = releaseFixture(t)
  const release = runRelease(root)
  assert.equal(release.status, 0, release.stderr)

  const verify = runVerify(root)
  assert.equal(verify.status, 0, verify.stderr)

  rmSync(path.join(root, 'deploy/deployment/aifar-fixture-9.8.7-windows-amd64'), { recursive: true, force: true })
  const missingPlatform = runVerify(root)
  assert.notEqual(missingPlatform.status, 0)
  assert.match(missingPlatform.stderr, /expected exactly current Linux and Windows package directories/)

  const staleRoot = releaseFixture(t)
  assert.equal(runRelease(staleRoot).status, 0)
  mkdirSync(path.join(staleRoot, 'deploy/deployment/aifar-fixture-0.0.1-linux-amd64'), { recursive: true })
  const stale = runVerify(staleRoot)
  assert.notEqual(stale.status, 0)
  assert.match(stale.stderr, /extra: aifar-fixture-0\.0\.1-linux-amd64/)

  const missingArchiveRoot = releaseFixture(t)
  assert.equal(runRelease(missingArchiveRoot).status, 0)
  rmSync(path.join(missingArchiveRoot, 'deploy/deployment/aifar-fixture-9.8.7-windows-amd64.zip'), { force: true })
  const missingArchive = runVerify(missingArchiveRoot)
  assert.notEqual(missingArchive.status, 0)
  assert.match(missingArchive.stderr, /expected exactly current Linux and Windows release archives/)
})

test('release verifier extracts archives and rechecks internal checksums', (t) => {
  const root = releaseFixture(t)
  const release = runRelease(root)
  assert.equal(release.status, 0, release.stderr)

  const deployment = path.join(root, 'deploy', 'deployment')
  const packageName = 'aifar-fixture-9.8.7-linux-amd64'
  const tamperRoot = mkdtempSync(path.join(tmpdir(), 'aifar-release-tamper-'))
  t.after(() => rmSync(tamperRoot, { recursive: true, force: true }))
  cpSync(path.join(deployment, packageName), path.join(tamperRoot, packageName), { recursive: true })
  writeFileSync(path.join(tamperRoot, packageName, 'VERSION'), 'tampered-after-checksum\n')
  const archivePath = path.join(deployment, `${packageName}.tar.gz`)
  const archive = spawnSync('tar', ['-czf', archivePath, '-C', tamperRoot, packageName], { encoding: 'utf8' })
  assert.equal(archive.status, 0, archive.stderr)

  const verify = runVerify(root)
  assert.notEqual(verify.status, 0)
  assert.match(verify.stderr, /Checksum mismatch/)
})

test('release includes Keepalived tools only in Linux packages and checksums them', (t) => {
  const root = releaseFixture(t)
  const result = runRelease(root)
  assert.equal(result.status, 0, result.stderr)

  const deployment = path.join(root, 'deploy', 'deployment')
  const linux = path.join(deployment, 'aifar-fixture-9.8.7-linux-amd64')
  const windows = path.join(deployment, 'aifar-fixture-9.8.7-windows-amd64')
  for (const relativePath of [
    'install-keepalived-offline.sh',
    'check-aggregate-health.sh',
    'keepalived.env.example',
    'keepalived.conf.tpl'
  ]) {
    assert.equal(existsSync(path.join(linux, 'extras/keepalived', relativePath)), true)
  }
  assert.equal(existsSync(path.join(windows, 'extras/keepalived')), false)
  const checksums = readFileSync(path.join(linux, 'checksums.txt'), 'utf8')
  for (const relativePath of [
    'keepalived-2.4.2.tar.gz',
    'check-aggregate-health.sh',
    'keepalived.env.example',
    'keepalived.conf.tpl'
  ]) assert.match(checksums, new RegExp(`extras/keepalived/${relativePath.replaceAll('.', '\\.')}`))

  const archivePath = path.join(deployment, 'aifar-fixture-9.8.7-linux-amd64.tar.gz')
  const listing = spawnSync('tar', ['-tvzf', archivePath], { encoding: 'utf8' })
  assert.equal(listing.status, 0, listing.stderr)
  for (const script of [
    'install-keepalived-offline.sh',
    'check-aggregate-health.sh',
    'configure-selinux.sh',
    'uninstall-keepalived.sh'
  ]) {
    assert.match(listing.stdout, new RegExp(`^-rwxr-xr-x.*extras/keepalived/${script}$`, 'm'))
  }
})

test('release includes aggregate SELinux tool only in Linux packages with executable mode', (t) => {
  const root = releaseFixture(t)
  const result = runRelease(root)
  assert.equal(result.status, 0, result.stderr)

  const deployment = path.join(root, 'deploy', 'deployment')
  const linux = path.join(deployment, 'aifar-fixture-9.8.7-linux-amd64')
  const windows = path.join(deployment, 'aifar-fixture-9.8.7-windows-amd64')
  assert.equal(existsSync(path.join(linux, 'extras/selinux/configure-all-selinux.sh')), true)
  assert.equal(existsSync(path.join(linux, 'extras/selinux/README.md')), true)
  assert.equal(existsSync(path.join(windows, 'extras/selinux')), false)
  assert.match(
    readFileSync(path.join(linux, 'checksums.txt'), 'utf8'),
    /extras\/selinux\/configure-all-selinux\.sh/
  )

  const archivePath = path.join(deployment, 'aifar-fixture-9.8.7-linux-amd64.tar.gz')
  const listing = spawnSync('tar', ['-tvzf', archivePath], { encoding: 'utf8' })
  assert.equal(listing.status, 0, listing.stderr)
  assert.match(listing.stdout, /^-rwxr-xr-x.*extras\/selinux\/configure-all-selinux\.sh$/m)
})

test('test:local uses the script test runner and does not release twice after package', () => {
  const source = readFileSync(path.join(scriptsDir, 'test-local.mjs'), 'utf8')
  assert.match(source, /scripts\/test-scripts\.mjs/)
  assert.equal((source.match(/scripts\/package-release\.mjs/g) ?? []).length, 0)
  assert.equal((source.match(/scripts\/package-build\.mjs/g) ?? []).length, 1)
})

test('release final staging cleanup is best-effort', () => {
  const source = readFileSync(path.join(scriptsDir, 'package-release.mjs'), 'utf8')
  assert.doesNotMatch(source, /finally\s*\{[\s\S]*rmSync\(stagingRoot/)
  assert.match(source, /removePathBestEffort\(stagingRoot/)
})
