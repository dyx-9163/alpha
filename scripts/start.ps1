$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$defaultsPath = Join-Path $root "config\defaults.env"
if (Test-Path -LiteralPath $defaultsPath) {
  Get-Content -LiteralPath $defaultsPath | ForEach-Object {
    $line = $_.Trim()
    if ($line -and -not $line.StartsWith("#") -and $line.Contains("=")) {
      $key, $value = $line.Split("=", 2)
      if ($key -and -not [Environment]::GetEnvironmentVariable($key.Trim(), "Process")) {
        [Environment]::SetEnvironmentVariable($key.Trim(), $value.Trim().Trim('"').Trim("'"), "Process")
      }
    }
  }
}
if (-not $env:AIFAR_DEFAULT_PASSWORD) { $env:AIFAR_DEFAULT_PASSWORD = "Oversea.123" }
if (-not $env:AIFAR_BOOTSTRAP_PASSWORD) { $env:AIFAR_BOOTSTRAP_PASSWORD = $env:AIFAR_DEFAULT_PASSWORD }
if (-not $env:AIFAR_DEFAULT_DEPLOY_DIR) { $env:AIFAR_DEFAULT_DEPLOY_DIR = "/aifar/apps" }
if (-not $env:AIFAR_STATIC_DIR) { $env:AIFAR_STATIC_DIR = Join-Path $root "web\dist" }
if (-not $env:AIFAR_RESOURCE_DIR) { $env:AIFAR_RESOURCE_DIR = Join-Path $root "resources" }
if (-not $env:AIFAR_ADDR) { $env:AIFAR_ADDR = "0.0.0.0:8080" }
$bin = Join-Path $root "bin\aifar-server-windows-amd64.exe"
if (-not (Test-Path -LiteralPath $bin)) {
  throw "Missing backend binary: $bin. Build it with scripts\package.ps1 or extract bin from aifar-deployment.zip."
}
Push-Location $root
try {
  & $bin
} finally {
  Pop-Location
}
