[CmdletBinding()]
param(
  [string]$SourceRoot = "D:\workspace\alpha\backend\alpha-java-cloud",
  [string]$OutputRoot = "D:\workspace\alpha\dist\aifar-artifacts",
  [string[]]$Services = @(),
  [switch]$RequireAll,
  [switch]$NoZip,
  [switch]$Clean
)

$ErrorActionPreference = "Stop"

$serviceModules = [ordered]@{
  oauth      = "alpha-oauth"
  permission = "alpha-permission"
  system     = "alpha-system"
  file       = "alpha-file"
  message    = "alpha-message"
  im         = "alpha-im"
  contacts   = "alpha-contacts"
  meeting    = "alpha-meeting"
  gateway    = "alpha-gateway"
}

$excludedJarPatterns = @(
  "*-sources.jar",
  "*-javadoc.jar",
  "*-tests.jar",
  "*-test.jar",
  "*-plain.jar",
  "original-*.jar"
)

function Resolve-FullPath {
  param([Parameter(Mandatory = $true)][string]$Path)
  $item = Get-Item -LiteralPath $Path -ErrorAction Stop
  return $item.FullName
}

function Test-ExcludedJar {
  param([Parameter(Mandatory = $true)][System.IO.FileInfo]$File)
  foreach ($pattern in $excludedJarPatterns) {
    if ($File.Name -like $pattern) {
      return $true
    }
  }
  return $false
}

function Get-RelativePath {
  param(
    [Parameter(Mandatory = $true)][string]$BasePath,
    [Parameter(Mandatory = $true)][string]$Path
  )
  $baseUri = [System.Uri]::new(($BasePath.TrimEnd('\') + '\'))
  $pathUri = [System.Uri]::new($Path)
  return [System.Uri]::UnescapeDataString($baseUri.MakeRelativeUri($pathUri).ToString()).Replace('/', '\')
}

function Find-ServiceJar {
  param(
    [Parameter(Mandatory = $true)][string]$Root,
    [Parameter(Mandatory = $true)][string]$ModuleName
  )

  $moduleDirs = Get-ChildItem -LiteralPath $Root -Directory -Recurse -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -eq $ModuleName }

  $searchRoots = @()
  foreach ($dir in $moduleDirs) {
    $searchRoots += $dir.FullName
  }

  $direct = Join-Path $Root $ModuleName
  if ((Test-Path -LiteralPath $direct -PathType Container) -and -not ($searchRoots -contains (Resolve-FullPath $direct))) {
    $searchRoots += (Resolve-FullPath $direct)
  }

  if (-not $searchRoots.Count) {
    $searchRoots = @($Root)
  }

  $candidates = @()
  foreach ($searchRoot in $searchRoots) {
    $jars = Get-ChildItem -LiteralPath $searchRoot -Recurse -File -Filter "*.jar" -ErrorAction SilentlyContinue |
      Where-Object {
        -not (Test-ExcludedJar $_) -and
        ($_.FullName -match "\\target\\" -or $_.FullName -match "\\build\\libs\\") -and
        $_.BaseName.StartsWith($ModuleName, [System.StringComparison]::OrdinalIgnoreCase)
      }
    $candidates += $jars
  }

  $candidates = $candidates |
    Sort-Object FullName -Unique |
    Sort-Object LastWriteTimeUtc, Length -Descending

  if (-not $candidates.Count) {
    return $null
  }
  return $candidates[0]
}

function New-CleanDirectory {
  param([Parameter(Mandatory = $true)][string]$Path)
  if ((Test-Path -LiteralPath $Path) -and $Clean) {
    Remove-Item -LiteralPath $Path -Recurse -Force
  }
  New-Item -ItemType Directory -Force -Path $Path | Out-Null
}

if (-not (Test-Path -LiteralPath $SourceRoot -PathType Container)) {
  throw "SourceRoot does not exist: $SourceRoot"
}

$sourceFull = Resolve-FullPath $SourceRoot
$rawServices = if ($Services.Count) { $Services } else { @($serviceModules.Keys) }
$selectedServices = @()
foreach ($value in $rawServices) {
  foreach ($item in ($value -split ",")) {
    $normalized = $item.Trim().ToLowerInvariant()
    if (-not $normalized) {
      continue
    }
    if ($normalized.StartsWith("alpha-", [System.StringComparison]::OrdinalIgnoreCase)) {
      $normalized = $normalized.Substring(6)
    }
    $selectedServices += $normalized
  }
}
$selectedServices = $selectedServices | Select-Object -Unique
foreach ($service in $selectedServices) {
  if (-not $serviceModules.Contains($service)) {
    throw "Unsupported service '$service'. Supported services: $($serviceModules.Keys -join ', ')"
  }
}

$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$packageName = "aifar-alpha-jars-$timestamp"
$packageDir = Join-Path $OutputRoot $packageName
$artifactDir = Join-Path $packageDir "artifacts"

New-CleanDirectory $packageDir
New-CleanDirectory $artifactDir

$manifestServices = @()
$missing = @()

foreach ($service in $selectedServices) {
  $moduleName = $serviceModules[$service]
  $jar = Find-ServiceJar -Root $sourceFull -ModuleName $moduleName
  if ($null -eq $jar) {
    $missing += $service
    Write-Warning "No runnable jar found for $service ($moduleName)."
    continue
  }

  $serviceDir = Join-Path $artifactDir $service
  New-Item -ItemType Directory -Force -Path $serviceDir | Out-Null

  $targetName = "$moduleName.jar"
  $targetPath = Join-Path $serviceDir $targetName
  Copy-Item -LiteralPath $jar.FullName -Destination $targetPath -Force

  $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $targetPath).Hash.ToLowerInvariant()
  $relativeArtifact = Get-RelativePath -BasePath $packageDir -Path $targetPath
  $relativeSource = Get-RelativePath -BasePath $sourceFull -Path $jar.FullName

  $manifestServices += [ordered]@{
    service      = $service
    module       = $moduleName
    artifact     = $relativeArtifact.Replace('\', '/')
    fileName     = $targetName
    source       = $relativeSource.Replace('\', '/')
    sha256       = $hash
    size         = (Get-Item -LiteralPath $targetPath).Length
    sourceMtime  = $jar.LastWriteTimeUtc.ToString("o")
  }

  Write-Host "Added $service <= $relativeSource"
}

if ($RequireAll -and $missing.Count) {
  throw "Missing required services: $($missing -join ', ')"
}

if (-not $manifestServices.Count) {
  throw "No service jars were exported."
}

$manifest = [ordered]@{
  schema      = "aifar-artifact-bundle-v1"
  app         = "aifar"
  kind        = "alpha-java-cloud-jars"
  generatedAt = (Get-Date).ToUniversalTime().ToString("o")
  sourceRoot  = $sourceFull
  services    = $manifestServices
}

$manifestPath = Join-Path $packageDir "manifest.json"
$manifestJson = $manifest | ConvertTo-Json -Depth 8
$utf8NoBom = [System.Text.UTF8Encoding]::new($false)
[System.IO.File]::WriteAllText($manifestPath, $manifestJson, $utf8NoBom)

if (-not $NoZip) {
  $zipPath = Join-Path $OutputRoot "$packageName.zip"
  if (Test-Path -LiteralPath $zipPath) {
    Remove-Item -LiteralPath $zipPath -Force
  }
  Compress-Archive -Path (Join-Path $packageDir "*") -DestinationPath $zipPath -Force
  Write-Host "Package directory: $packageDir"
  Write-Host "Package zip:       $zipPath"
} else {
  Write-Host "Package directory: $packageDir"
}

if ($missing.Count) {
  Write-Warning "Skipped missing services: $($missing -join ', ')"
}
