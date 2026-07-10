$ErrorActionPreference = "Stop"
$script:AifarStartRoot = $PSScriptRoot

function Resolve-AifarEnvironment {
  [CmdletBinding()]
  param(
    [Parameter(Mandatory = $true)]
    [string]$Root,
    [string]$DefaultsPath,
    [System.Collections.IDictionary]$Environment
  )

  if ($null -eq $Environment) {
    $Environment = @{}
    [Environment]::GetEnvironmentVariables("Process").GetEnumerator() | ForEach-Object {
      $Environment[$_.Key] = [string]$_.Value
    }
  }
  if (-not $DefaultsPath) {
    $DefaultsPath = Join-Path $Root "config\defaults.env"
  }

  if (Test-Path -LiteralPath $DefaultsPath) {
    $seenConfigKeys = @{}
    $lineNumber = 0
    Get-Content -LiteralPath $DefaultsPath | ForEach-Object {
      $lineNumber++
      $trimmed = $_.Trim()
      if (-not $trimmed -or $trimmed.StartsWith("#")) {
        return
      }
      if ($trimmed -cnotmatch '^(AIFAR_[A-Z0-9_]+)=(.*)$') {
        throw "Malformed defaults.env line $lineNumber"
      }

      $key = $Matches[1]
      $value = $Matches[2].Trim()
      if ($seenConfigKeys.ContainsKey($key)) {
        throw "Duplicate configuration key in defaults.env: $key"
      }
      $seenConfigKeys[$key] = $true

      if ($value.Length -ge 2) {
        $first = $value.Substring(0, 1)
        $last = $value.Substring($value.Length - 1, 1)
        if (($first -eq '"' -or $first -eq "'") -and $last -eq $first) {
          $value = $value.Substring(1, $value.Length - 2)
        }
      }
      if (-not $Environment.Contains($key)) {
        $Environment[$key] = $value
      }
    }
  }

  $fallbacks = [ordered]@{
    AIFAR_DEFAULT_DEPLOY_DIR = "/aifar/apps"
    AIFAR_STATIC_DIR = Join-Path $Root "web\dist"
    AIFAR_RESOURCE_DIR = Join-Path $Root "resources"
    AIFAR_ADDR = "0.0.0.0:8080"
  }
  foreach ($entry in $fallbacks.GetEnumerator()) {
    if (-not $Environment.Contains($entry.Key)) {
      $Environment[$entry.Key] = $entry.Value
    }
  }
  return $Environment
}

function Set-AifarProcessEnvironment {
  [CmdletBinding()]
  param(
    [Parameter(Mandatory = $true)]
    [System.Collections.IDictionary]$Environment
  )

  foreach ($entry in $Environment.GetEnumerator()) {
    if ([string]$entry.Key -like "AIFAR_*") {
      [Environment]::SetEnvironmentVariable([string]$entry.Key, [string]$entry.Value, "Process")
    }
  }
}

function Invoke-AifarStart {
  [CmdletBinding()]
  param([string[]]$Arguments = @())

  $root = $script:AifarStartRoot
  $environment = Resolve-AifarEnvironment -Root $root
  Set-AifarProcessEnvironment -Environment $environment

  $bin = Join-Path $root "bin\aifar-server-windows-amd64.exe"
  if (-not (Test-Path -LiteralPath $bin)) {
    throw "Missing backend binary: $bin. Build it with scripts\package.ps1 or extract bin from aifar-deployment.zip."
  }
  Push-Location $root
  try {
    & $bin @Arguments
  } finally {
    Pop-Location
  }
}

if ($MyInvocation.InvocationName -ne '.') {
  Invoke-AifarStart -Arguments $args
  if ($null -ne $LASTEXITCODE) {
    exit $LASTEXITCODE
  }
}
