import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync
} from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const cmdPath = path.join(scriptsDir, 'package-aifar-artifact-bundle.cmd')
const ps1Path = path.join(scriptsDir, 'package-aifar-artifact-bundle.ps1')

const javaModules = {
  oauth: ['alpha-oauth', 'alpha-oauth-server'],
  permission: ['alpha-permission', 'alpha-permission-server'],
  system: ['alpha-system', 'alpha-system-server'],
  file: ['alpha-file', 'alpha-file-server'],
  message: ['alpha-message', 'alpha-message-server'],
  im: ['alpha-im', 'alpha-im-core'],
  contacts: ['alpha-contacts', 'alpha-contacts-core'],
  meeting: ['alpha-meeting', 'alpha-meeting-core'],
  gateway: ['alpha-gateway']
}

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
  const directory = mkdtempSync(path.join(tmpdir(), 'aifar-artifact-bundle-'))
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  return directory
}

function createFixture(root, services = Object.keys(javaModules)) {
  const javaRoot = path.join(root, 'alpha-java-cloud')
  const webRoot = path.join(root, 'alpha-web-vue3', 'dist')

  for (const service of services) {
    const parts = javaModules[service]
    if (!parts) continue
    const target = path.join(javaRoot, ...parts, 'target')
    mkdirSync(target, { recursive: true })
    writeFileSync(path.join(target, `alpha-${service}-5.1.0-RELEASE.jar`), `${service} runnable jar\n`)
  }

  mkdirSync(path.join(webRoot, 'assets'), { recursive: true })
  writeFileSync(path.join(webRoot, 'index.html'), '<!doctype html><title>AIFAR</title>')
  writeFileSync(path.join(webRoot, 'assets', 'app.js'), 'console.log("aifar")')
  return { javaRoot, webRoot }
}

function invokePackager({ cwd, javaRoot, webRoot, outputPath, services }) {
  const args = [
    '-NoProfile',
    '-ExecutionPolicy',
    'Bypass',
    '-File',
    ps1Path,
    '-JavaSourceRoot',
    javaRoot,
    '-WebDistRoot',
    webRoot,
    '-OutputPath',
    outputPath
  ]
  if (services !== undefined) args.push('-Services', services)
  return spawnSync(powershell, args, { cwd, encoding: 'utf8' })
}

function expandArchive(archivePath, destinationPath) {
  mkdirSync(destinationPath, { recursive: true })
  const command = '& { param($archive, $destination) Expand-Archive -LiteralPath $archive -DestinationPath $destination -Force }'
  const result = spawnSync(powershell, ['-NoProfile', '-Command', command, archivePath, destinationPath], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
}

function archiveEntries(archivePath) {
  const command = '& { param($archivePath) Add-Type -AssemblyName System.IO.Compression.FileSystem; $archive = [IO.Compression.ZipFile]::OpenRead($archivePath); try { $names = @($archive.Entries | ForEach-Object { $_.FullName }); ConvertTo-Json -InputObject $names -Compress } finally { $archive.Dispose() } }'
  const result = spawnSync(powershell, ['-NoProfile', '-Command', command, archivePath], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr)
  return JSON.parse(result.stdout.trim())
}

function sha256(filePath) {
  return createHash('sha256').update(readFileSync(filePath)).digest('hex')
}

function readExpandedManifest(root, outputPath, directoryName = 'expanded') {
  const expanded = path.join(root, directoryName)
  expandArchive(outputPath, expanded)
  return {
    expanded,
    manifest: JSON.parse(readFileSync(path.join(expanded, 'manifest.json'), 'utf8'))
  }
}

function assertNoStagingResidue(root) {
  assert.deepEqual(
    readdirSync(root).filter((name) => name.startsWith('.aifar-artifact-bundle-')),
    []
  )
}

test('CMD exposes editable source/output configuration and forwards one optional service selector', () => {
  const source = readFileSync(cmdPath, 'utf8')

  assert.match(source, /set "JAVA_SOURCE_ROOT=D:\\workspace\\alpha\\backend\\alpha-java-cloud"/i)
  assert.match(source, /set "WEB_DIST_ROOT=D:\\workspace\\alpha\\fronted\\alpha-web-vue3\\dist"/i)
  assert.match(source, /set "OUTPUT_PATH=%CD%\\aifar-batch-update\.zip"/i)
  assert.match(source, /set "SERVICES=%\*"/i)
  assert.match(source, /if not defined SERVICES set "SERVICES=all"/i)
  assert.match(source, /-Services "%SERVICES%"/i)
})

test('PowerShell packager retries transient staging cleanup locks before reporting success', () => {
  const source = readFileSync(ps1Path, 'utf8')

  assert.match(source, /function Remove-PathWithRetry/i)
  assert.match(source, /Start-Sleep -Milliseconds/i)
  assert.match(source, /Write-Host "Created AIFAR artifact bundle:[\s\S]*exit \$exitCode/i)
})

test('CMD preserves an unquoted comma-separated service selector as one value', { skip: process.platform !== 'win32' || !powershell }, (t) => {
  const root = tempRoot(t)
  const fixture = createFixture(root, ['gateway', 'im', 'meeting'])
  const fixtureScripts = path.join(root, 'scripts')
  mkdirSync(fixtureScripts, { recursive: true })
  copyFileSync(ps1Path, path.join(fixtureScripts, path.basename(ps1Path)))

  const fixtureCmd = path.join(fixtureScripts, path.basename(cmdPath))
  const cmdSource = readFileSync(cmdPath, 'utf8')
    .replace('D:\\workspace\\alpha\\backend\\alpha-java-cloud', fixture.javaRoot)
    .replace('D:\\workspace\\alpha\\fronted\\alpha-web-vue3\\dist', fixture.webRoot)
  writeFileSync(fixtureCmd, cmdSource)

  const result = spawnSync('cmd.exe', [
    '/d',
    '/c',
    fixtureCmd,
    'gateway,im,meeting,web-vue3'
  ], { cwd: root, encoding: 'utf8' })

  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`)
  const outputPath = path.join(root, 'aifar-batch-update.zip')
  const { manifest } = readExpandedManifest(root, outputPath)
  assert.deepEqual(manifest.services.map(({ service }) => service), ['im', 'meeting', 'gateway', 'web-vue3'])
})

test('selected services produce normalized artifacts, complete metadata, and a root-level web dist archive', { skip: !powershell }, (t) => {
  const root = tempRoot(t)
  const { javaRoot, webRoot } = createFixture(root, ['gateway', 'im', 'meeting'])
  const outputPath = path.join(root, 'aifar-batch-update.zip')
  const result = invokePackager({
    cwd: root,
    javaRoot,
    webRoot,
    outputPath,
    services: 'gateway,im,meeting,web-vue3'
  })

  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`)
  assert.equal(existsSync(outputPath), true)

  const outerEntries = archiveEntries(outputPath)
  assert.equal(outerEntries.some((name) => name.includes('\\')), false)
  assert.equal(outerEntries.includes('artifacts/im/alpha-im.jar'), true)

  const { expanded, manifest } = readExpandedManifest(root, outputPath)

  assert.equal(manifest.schema, 'aifar-artifact-bundle-v1')
  assert.equal(manifest.app, 'aifar')
  assert.equal(manifest.kind, 'aifar-service-artifacts')
  assert.deepEqual(manifest.services.map(({ service }) => service), ['im', 'meeting', 'gateway', 'web-vue3'])

  for (const item of manifest.services) {
    const artifactPath = path.join(expanded, ...item.artifact.split('/'))
    assert.equal(existsSync(artifactPath), true, item.artifact)
    assert.equal(item.artifact.includes('target'), false)
    assert.equal(item.size, readFileSync(artifactPath).length)
    assert.equal(item.sha256, sha256(artifactPath))
  }

  assert.deepEqual(manifest.services.map(({ artifact }) => artifact), [
    'artifacts/im/alpha-im.jar',
    'artifacts/meeting/alpha-meeting.jar',
    'artifacts/gateway/alpha-gateway.jar',
    'artifacts/web-vue3/web-vue3.zip'
  ])

  const webExpanded = path.join(root, 'web-expanded')
  const webArchive = path.join(expanded, 'artifacts', 'web-vue3', 'web-vue3.zip')
  const webEntries = archiveEntries(webArchive)
  assert.equal(webEntries.some((name) => name.includes('\\')), false)
  assert.equal(webEntries.includes('index.html'), true)
  assert.equal(webEntries.includes('assets/app.js'), true)
  expandArchive(webArchive, webExpanded)
  assert.equal(existsSync(path.join(webExpanded, 'index.html')), true)
  assert.equal(existsSync(path.join(webExpanded, 'assets', 'app.js')), true)
  assert.equal(existsSync(path.join(webExpanded, 'dist')), false)
})

test('omitted services and explicit all both package all ten services in canonical order', { skip: !powershell }, (t) => {
  const root = tempRoot(t)
  const { javaRoot, webRoot } = createFixture(root)
  const expected = ['oauth', 'permission', 'system', 'file', 'message', 'im', 'contacts', 'meeting', 'gateway', 'web-vue3']

  const defaultOutput = path.join(root, 'default.zip')
  const defaultResult = invokePackager({ cwd: root, javaRoot, webRoot, outputPath: defaultOutput })
  assert.equal(defaultResult.status, 0, `${defaultResult.stdout}\n${defaultResult.stderr}`)
  const defaultBundle = readExpandedManifest(root, defaultOutput, 'default-expanded')
  assert.deepEqual(defaultBundle.manifest.services.map(({ service }) => service), expected)

  const allOutput = path.join(root, 'all.zip')
  const allResult = invokePackager({ cwd: root, javaRoot, webRoot, outputPath: allOutput, services: 'all' })
  assert.equal(allResult.status, 0, `${allResult.stdout}\n${allResult.stderr}`)
  const allBundle = readExpandedManifest(root, allOutput, 'all-expanded')
  assert.deepEqual(allBundle.manifest.services.map(({ service }) => service), expected)

  for (const item of allBundle.manifest.services) {
    const expectedModule = item.service === 'web-vue3' ? 'web-vue3' : `alpha-${item.service}`
    const expectedFileName = item.service === 'web-vue3' ? 'web-vue3.zip' : `${expectedModule}.jar`
    assert.equal(item.module, expectedModule)
    assert.equal(item.fileName, expectedFileName)
    assert.equal(item.artifact, `artifacts/${item.service}/${expectedFileName}`)
  }
  assertNoStagingResidue(root)
})

test('service selection is case-insensitive, trimmed, deduplicated, and emitted in canonical order', { skip: !powershell }, (t) => {
  const root = tempRoot(t)
  const { javaRoot, webRoot } = createFixture(root, ['gateway', 'im'])
  const outputPath = path.join(root, 'deduplicated.zip')
  const result = invokePackager({
    cwd: root,
    javaRoot,
    webRoot,
    outputPath,
    services: ' Gateway, im, GATEWAY '
  })

  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`)
  const { manifest } = readExpandedManifest(root, outputPath)
  assert.deepEqual(manifest.services.map(({ service }) => service), ['im', 'gateway'])
  assertNoStagingResidue(root)
})

for (const [label, services] of [
  ['unknown service', 'gateway,unknown'],
  ['empty list item', 'gateway,,im'],
  ['all combined with an individual service', 'all,gateway']
]) {
  test(`rejects ${label} without creating an output archive`, { skip: !powershell }, (t) => {
    const root = tempRoot(t)
    const { javaRoot, webRoot } = createFixture(root, ['gateway', 'im'])
    const outputPath = path.join(root, 'invalid.zip')
    const result = invokePackager({ cwd: root, javaRoot, webRoot, outputPath, services })

    assert.notEqual(result.status, 0)
    assert.equal(existsSync(outputPath), false)
    assertNoStagingResidue(root)
  })
}

test('a missing selected JAR fails without leaving output or staging files', { skip: !powershell }, (t) => {
  const root = tempRoot(t)
  const { javaRoot, webRoot } = createFixture(root, [])
  const outputPath = path.join(root, 'missing.zip')
  const result = invokePackager({ cwd: root, javaRoot, webRoot, outputPath, services: 'oauth' })

  assert.notEqual(result.status, 0)
  assert.match(`${result.stdout}\n${result.stderr}`, /oauth/i)
  assert.equal(existsSync(outputPath), false)
  assertNoStagingResidue(root)
})

test('ambiguous runnable JARs fail without replacing an existing output archive', { skip: !powershell }, (t) => {
  const root = tempRoot(t)
  const { javaRoot, webRoot } = createFixture(root, ['gateway'])
  const gatewayTarget = path.join(javaRoot, 'alpha-gateway', 'target')
  writeFileSync(path.join(gatewayTarget, 'alpha-gateway-5.2.0-RELEASE.jar'), 'second runnable jar')
  writeFileSync(path.join(gatewayTarget, 'alpha-gateway-5.1.0-RELEASE-sources.jar'), 'ignored source jar')
  const outputPath = path.join(root, 'existing.zip')
  writeFileSync(outputPath, 'previous bundle')

  const result = invokePackager({ cwd: root, javaRoot, webRoot, outputPath, services: 'gateway' })

  assert.notEqual(result.status, 0)
  assert.match(`${result.stdout}\n${result.stderr}`, /gateway/i)
  assert.equal(readFileSync(outputPath, 'utf8'), 'previous bundle')
  assertNoStagingResidue(root)
})
