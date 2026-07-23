import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const toolDir = path.dirname(fileURLToPath(import.meta.url))
const repositoryRoot = path.resolve(toolDir, '..', '..')
const projectPath = path.join(
  toolDir,
  'src',
  'AifarBundlePackager.WinForms',
  'AifarBundlePackager.WinForms.csproj'
)
const buildScriptPath = path.join(toolDir, 'build.ps1')
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
  assert.equal(existsSync(buildScriptPath), true, 'tool-local build.ps1 must exist')
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

test('build script validates paired release metadata and injects MSBuild versions', () => {
  const script = readFileSync(buildScriptPath, 'utf8')

  assert.match(script, /\[string\]\$Version/)
  assert.match(script, /\[string\]\$SourceRevisionId/)
  assert.match(script, /Version and SourceRevisionId must be supplied together/i)
  assert.match(script, /Version must use X\.Y\.Z numeric format/i)
  assert.match(script, /SourceRevisionId must be a 40-character Git commit SHA/i)
  assert.match(script, /\^\\d\+\\\.\\d\+\\\.\\d\+\$/)
  assert.match(script, /\^\[0-9a-fA-F\]\{40\}\$/)
  assert.match(script, /-p:Version=/)
  assert.match(script, /-p:FileVersion=/)
  assert.match(script, /-p:AssemblyVersion=/)
  assert.match(script, /-p:InformationalVersion=/)
  assert.match(script, /-p:SourceRevisionId=/)
})

test('Windows CI compiles, tests, and publishes the WinForms packager', () => {
  const workflow = readFileSync(workflowPath, 'utf8')

  assert.match(workflow, /uses:\s*actions\/setup-dotnet@v4/i)
  assert.match(workflow, /dotnet-version:\s*['"]?8\.0\.x/i)
  assert.match(workflow, /dotnet test tools\/aifar-bundle-packager\/AifarBundlePackager\.sln/i)
  assert.match(workflow, /\.\/tools\/aifar-bundle-packager\/build\.ps1/i)
  assert.match(workflow, /runner\.os\s*==\s*'Windows'/i)
})
