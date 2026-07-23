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
const workflowPath = path.join(repositoryRoot, '.github', 'workflows', 'ci.yml')

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

  const staleSidecarCheck = script.indexOf('$staleSidecars =')
  const deliveryReplacement = script.indexOf('Move-Item -LiteralPath $temporaryDelivery')
  assert.ok(staleSidecarCheck >= 0, 'missing stale sidecar validation')
  assert.ok(deliveryReplacement >= 0, 'missing delivery replacement')
  assert.ok(
    staleSidecarCheck < deliveryReplacement,
    'delivery sidecars must be validated before replacing the EXE'
  )
})

test('Windows CI compiles, tests, and publishes the WinForms packager', () => {
  const workflow = readFileSync(workflowPath, 'utf8')

  assert.match(workflow, /uses:\s*actions\/setup-dotnet@v4/i)
  assert.match(workflow, /dotnet-version:\s*['"]?8\.0\.x/i)
  assert.match(workflow, /dotnet test tools\/aifar-bundle-packager\/AifarBundlePackager\.sln/i)
  assert.match(workflow, /build-aifar-bundle-packager\.ps1/i)
  assert.match(workflow, /runner\.os\s*==\s*'Windows'/i)
})
