import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import {
  existsSync,
  mkdtempSync,
  mkdirSync,
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

const toolDir = path.dirname(fileURLToPath(import.meta.url))
const assetScriptPath = path.join(toolDir, 'create-release-assets.ps1')
const revision = '0123456789abcdef0123456789abcdef01234567'

function runPowerShell(argumentsList) {
  return spawnSync('powershell.exe', [
    '-NoProfile',
    '-ExecutionPolicy',
    'Bypass',
    '-File',
    assetScriptPath,
    ...argumentsList
  ], { encoding: 'utf8' })
}

function findDotNet() {
  const bundled = 'D:\\tools\\dotnet\\dotnet.exe'
  return existsSync(bundled) ? bundled : 'dotnet'
}

function publishFixture(workspace) {
  const projectDirectory = path.join(workspace, 'fixture')
  const publishDirectory = path.join(workspace, 'publish')
  mkdirSync(projectDirectory, { recursive: true })
  const projectPath = path.join(projectDirectory, 'Fixture.csproj')
  writeFileSync(projectPath, `
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>WinExe</OutputType>
    <TargetFramework>net8.0-windows</TargetFramework>
    <AssemblyName>AIFARBundlePackager</AssemblyName>
    <RuntimeIdentifier>win-x64</RuntimeIdentifier>
    <SelfContained>false</SelfContained>
    <UseAppHost>true</UseAppHost>
    <ImplicitUsings>enable</ImplicitUsings>
    <Version>1.2.3</Version>
    <FileVersion>1.2.3.0</FileVersion>
    <AssemblyVersion>1.2.3.0</AssemblyVersion>
    <InformationalVersion>1.2.3+${revision}</InformationalVersion>
  </PropertyGroup>
</Project>
`.trimStart())
  writeFileSync(
    path.join(projectDirectory, 'Program.cs'),
    'Console.WriteLine("AIFAR Bundle Packager release fixture");\n'
  )
  const result = spawnSync(findDotNet(), [
    'publish',
    projectPath,
    '--configuration',
    'Release',
    '--runtime',
    'win-x64',
    '--self-contained',
    'false',
    '--output',
    publishDirectory
  ], { encoding: 'utf8' })
  assert.equal(result.status, 0, result.stderr || result.stdout)
  return path.join(publishDirectory, 'AIFARBundlePackager.exe')
}

test('release asset script exposes the transactional three-file contract', () => {
  const script = readFileSync(assetScriptPath, 'utf8')

  assert.match(script, /AIFARBundlePackager\.exe\.sha256/)
  assert.match(script, /release-manifest\.json/)
  assert.match(script, /aifar-bundle-packager-release-v1/)
  assert.match(script, /Get-FileHash[\s\S]*SHA256/i)
  assert.match(script, /FileVersionInfo/i)
  assert.match(script, /2147483648/)
  assert.match(script, /\.aifar-bundle-packager-release-/)
  assert.match(script, /finally[\s\S]*Remove-Item[\s\S]*Recurse/i)
})

test('release asset script rejects invalid input without partial assets', {
  skip: process.platform !== 'win32'
}, () => {
  const workspace = mkdtempSync(path.join(tmpdir(), 'aifar-release-assets-invalid-'))
  try {
    const fakeExe = path.join(workspace, 'AIFARBundlePackager.exe')
    const output = path.join(workspace, 'release')
    writeFileSync(fakeExe, 'not-a-pe')

    const result = runPowerShell([
      '-ExecutablePath', fakeExe,
      '-Version', '1.2.3',
      '-SourceRevisionId', revision,
      '-OutputDirectory', output
    ])

    assert.notEqual(result.status, 0)
    assert.equal(existsSync(output), false)
  } finally {
    rmSync(workspace, { recursive: true, force: true })
  }
})

test('release asset script emits verified assets and preserves non-empty outputs', {
  skip: process.platform !== 'win32',
  timeout: 120_000
}, () => {
  const workspace = mkdtempSync(path.join(tmpdir(), 'aifar-release-assets-success-'))
  try {
    const executable = publishFixture(workspace)
    const output = path.join(workspace, 'release')
    const result = runPowerShell([
      '-ExecutablePath', executable,
      '-Version', '1.2.3',
      '-SourceRevisionId', revision,
      '-OutputDirectory', output
    ])
    assert.equal(result.status, 0, result.stderr || result.stdout)

    assert.deepEqual(readdirSync(output).sort(), [
      'AIFARBundlePackager.exe',
      'AIFARBundlePackager.exe.sha256',
      'release-manifest.json'
    ])
    const deliveredExe = path.join(output, 'AIFARBundlePackager.exe')
    const bytes = readFileSync(deliveredExe)
    const sha256 = createHash('sha256').update(bytes).digest('hex')
    assert.equal(
      readFileSync(path.join(output, 'AIFARBundlePackager.exe.sha256'), 'utf8'),
      `${sha256}  AIFARBundlePackager.exe\n`
    )
    const manifest = JSON.parse(readFileSync(path.join(output, 'release-manifest.json'), 'utf8'))
    assert.equal(manifest.schema, 'aifar-bundle-packager-release-v1')
    assert.equal(manifest.version, '1.2.3')
    assert.equal(manifest.gitCommit, revision)
    assert.equal(manifest.runtimeIdentifier, 'win-x64')
    assert.equal(manifest.fileName, 'AIFARBundlePackager.exe')
    assert.equal(manifest.size, bytes.length)
    assert.equal(manifest.sha256, sha256)
    assert.equal(Number.isNaN(Date.parse(manifest.builtAt)), false)

    const protectedOutput = path.join(workspace, 'protected-release')
    mkdirSync(protectedOutput)
    const sentinel = path.join(protectedOutput, 'keep.txt')
    writeFileSync(sentinel, 'keep')
    const rejected = runPowerShell([
      '-ExecutablePath', executable,
      '-Version', '1.2.3',
      '-SourceRevisionId', revision,
      '-OutputDirectory', protectedOutput
    ])
    assert.notEqual(rejected.status, 0)
    assert.equal(readFileSync(sentinel, 'utf8'), 'keep')
    assert.deepEqual(readdirSync(protectedOutput), ['keep.txt'])
  } finally {
    rmSync(workspace, { recursive: true, force: true })
  }
})
