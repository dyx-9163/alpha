import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const repositoryRoot = path.resolve(scriptsDir, '..')
const projectPath = path.join(
  repositoryRoot,
  'tools',
  'aifar-bundle-packager',
  'src',
  'AifarBundlePackager.WinForms',
  'AifarBundlePackager.WinForms.csproj'
)
const buildScriptPath = path.join(scriptsDir, 'build-aifar-bundle-packager.ps1')

test('WinForms project publishes a self-contained untrimmed win-x64 single file', () => {
  const project = readFileSync(projectPath, 'utf8')

  assert.match(project, /<RuntimeIdentifier>win-x64<\/RuntimeIdentifier>/)
  assert.match(project, /<SelfContained>true<\/SelfContained>/)
  assert.match(project, /<PublishSingleFile>true<\/PublishSingleFile>/)
  assert.match(project, /<PublishTrimmed>false<\/PublishTrimmed>/)
  assert.match(project, /<IncludeNativeLibrariesForSelfExtract>true<\/IncludeNativeLibrariesForSelfExtract>/)
  assert.match(project, /<DebugType>none<\/DebugType>/)
  assert.match(project, /<DebugSymbols>false<\/DebugSymbols>/)
})

test('build script tests before publish and delivers only AIFARBundlePackager.exe', () => {
  const script = readFileSync(buildScriptPath, 'utf8')

  assert.match(script, /param\([\s\S]*\$DotNetPath\s*=\s*'D:\\tools\\dotnet\\dotnet\.exe'/i)
  assert.match(script, /dotnet test/i)
  assert.match(script, /dotnet publish/i)
  assert.match(script, /\.aifar-bundle-packager-publish-/i)
  assert.match(script, /Join-Path \$repositoryRoot 'deploy'/i)
  assert.match(script, /Join-Path \$deployRoot 'bin'/i)
  assert.match(script, /AIFARBundlePackager\.exe/i)
  assert.match(script, /Get-ChildItem[\s\S]*-Filter '\*\.exe'/i)
  assert.match(script, /\.Count -ne 1/i)
  assert.match(script, /finally[\s\S]*Remove-Item[\s\S]*-Recurse/i)
})
