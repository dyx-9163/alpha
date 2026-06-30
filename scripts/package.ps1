$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$toolRoot = $env:AIFAR_TOOL_ROOT
if (-not $toolRoot) { $toolRoot = "D:\tools" }
$nodeDir = Join-Path $toolRoot "node"
$goDir = Join-Path $toolRoot "go"
$nodeGlobal = Join-Path $toolRoot "node-global"
$goPath = Join-Path $toolRoot "gopath"
$goCache = $env:AIFAR_GO_CACHE
if (-not $goCache) { $goCache = Join-Path $root ".cache\go-build" }
$env:Path = "$nodeDir;$nodeGlobal;$goDir\bin;$goPath\bin;$env:Path"
$env:GOROOT = $goDir
$env:GOPATH = $goPath
$env:GOCACHE = $goCache
New-Item -ItemType Directory -Force -Path $goCache | Out-Null
$pnpm = Join-Path $nodeGlobal "pnpm.cmd"
Push-Location $root
try {
  & $pnpm install
  & node scripts/package-build.mjs
} finally {
  Pop-Location
}
Write-Host "Package artifacts generated under $(Join-Path $root 'deploy\deployment')"
