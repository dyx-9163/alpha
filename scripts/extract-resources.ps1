$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$zipPath = Join-Path $root "aifar-deployment.zip"
$target = Join-Path $root "resources"
if (-not (Test-Path -LiteralPath $zipPath)) {
  throw "Missing $zipPath"
}
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
try {
  foreach ($entry in $zip.Entries) {
    $normalized = $entry.FullName.Replace("/", "\")
    if (-not $normalized.StartsWith("resources\")) { continue }
    if ($normalized.EndsWith("\")) { continue }
    $relative = $normalized.Substring("resources\".Length)
    $path = Join-Path $target $relative
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $path) | Out-Null
    [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $path, $true)
  }
} finally {
  $zip.Dispose()
}
Write-Host "Resources extracted to $target"
