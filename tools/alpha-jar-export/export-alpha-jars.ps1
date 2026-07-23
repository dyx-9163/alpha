# Copies Alpha runnable JARs into AIFAR Runtime resource targets.
[CmdletBinding()]
param(
  [string]$SourceRoot = "D:\workspace\alpha\backend\alpha-java-cloud",
  [string]$TargetRoot = "",
  [string[]]$Services = @(),
  [switch]$RequireAll,
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

function Reset-ServiceTarget {
  param([Parameter(Mandatory = $true)][string]$Path)
  if ((Test-Path -LiteralPath $Path) -and $Clean) {
    Remove-Item -LiteralPath $Path -Recurse -Force
  }
  New-Item -ItemType Directory -Force -Path $Path | Out-Null
  Get-ChildItem -LiteralPath $Path -File -Filter "*.jar" -ErrorAction SilentlyContinue |
    Remove-Item -Force
}

if (-not (Test-Path -LiteralPath $SourceRoot -PathType Container)) {
  throw "SourceRoot does not exist: $SourceRoot"
}

$sourceFull = Resolve-FullPath $SourceRoot
if ([string]::IsNullOrWhiteSpace($TargetRoot)) {
  $scriptRoot = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
  $repositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $scriptRoot '..\..'))
  $TargetRoot = Join-Path $repositoryRoot "resources\aifar\runtime-v2\services"
}
$targetFull = [System.IO.Path]::GetFullPath($TargetRoot)
New-Item -ItemType Directory -Force -Path $targetFull | Out-Null
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

$missing = @()
$exported = @()

foreach ($service in $selectedServices) {
  $moduleName = $serviceModules[$service]
  $jar = Find-ServiceJar -Root $sourceFull -ModuleName $moduleName
  if ($null -eq $jar) {
    $missing += $service
    Write-Warning "No runnable jar found for $service ($moduleName)."
    continue
  }

  $serviceDir = Join-Path $targetFull $service
  $targetDir = Join-Path $serviceDir "target"
  Reset-ServiceTarget -Path $targetDir
  $targetName = "$moduleName.jar"
  $targetPath = Join-Path $targetDir $targetName
  Copy-Item -LiteralPath $jar.FullName -Destination $targetPath -Force

  $relativeSource = Get-RelativePath -BasePath $sourceFull -Path $jar.FullName
  $relativeTarget = Get-RelativePath -BasePath $targetFull -Path $targetPath
  $exported += $service
  Write-Host "Updated $service <= $relativeSource -> $relativeTarget"
}

if ($RequireAll -and $missing.Count) {
  throw "Missing required services: $($missing -join ', ')"
}

if (-not $exported.Count) {
  throw "No service jars were copied."
}

Write-Host "Runtime services target: $targetFull"

if ($missing.Count) {
  Write-Warning "Skipped missing services: $($missing -join ', ')"
}
