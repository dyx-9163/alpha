[CmdletBinding()]
param(
    [string]$DotNetPath = 'D:\tools\dotnet\dotnet.exe'
)

$ErrorActionPreference = 'Stop'

$repositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$solutionPath = Join-Path $repositoryRoot 'tools\aifar-bundle-packager\AifarBundlePackager.sln'
$projectPath = Join-Path $repositoryRoot 'tools\aifar-bundle-packager\src\AifarBundlePackager.WinForms\AifarBundlePackager.WinForms.csproj'
$deployRoot = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot 'deploy'))
$deliveryDirectory = Join-Path $deployRoot 'bin'
$deliveryPath = Join-Path $deliveryDirectory 'AIFARBundlePackager.exe'
$workId = [Guid]::NewGuid().ToString('N')
$publishDirectory = Join-Path $deployRoot ".aifar-bundle-packager-publish-$workId"
$temporaryDelivery = Join-Path $deliveryDirectory ".AIFARBundlePackager-$workId.exe"

function Invoke-DotNet {
    param([string[]]$Arguments)

    & $DotNetPath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "dotnet command failed with exit code $LASTEXITCODE`: $($Arguments -join ' ')"
    }
}

function Assert-TemporaryPath {
    param([string]$Path)

    $resolvedPath = [System.IO.Path]::GetFullPath($Path)
    $deployPrefix = $deployRoot.TrimEnd('\') + '\'
    if (-not $resolvedPath.StartsWith($deployPrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "Temporary path must stay below the deploy directory: $resolvedPath"
    }
}

if (-not (Test-Path -LiteralPath $DotNetPath -PathType Leaf)) {
    throw "dotnet executable does not exist: $DotNetPath"
}
if (-not (Test-Path -LiteralPath $solutionPath -PathType Leaf)) {
    throw "Packager solution does not exist: $solutionPath"
}
if (-not (Test-Path -LiteralPath $projectPath -PathType Leaf)) {
    throw "Packager WinForms project does not exist: $projectPath"
}

Assert-TemporaryPath -Path $publishDirectory
Assert-TemporaryPath -Path $temporaryDelivery

try {
    Write-Host '[AIFAR Bundle Packager] dotnet test'
    Invoke-DotNet -Arguments @(
        'test',
        $solutionPath,
        '--configuration', 'Release'
    )

    Write-Host '[AIFAR Bundle Packager] dotnet publish'
    Invoke-DotNet -Arguments @(
        'publish',
        $projectPath,
        '--configuration', 'Release',
        '--runtime', 'win-x64',
        '--self-contained', 'true',
        '--output', $publishDirectory,
        '-p:PublishSingleFile=true',
        '-p:PublishTrimmed=false',
        '-p:IncludeNativeLibrariesForSelfExtract=true',
        '-p:DebugType=None',
        '-p:DebugSymbols=false'
    )

    $publishedExecutables = @(Get-ChildItem -LiteralPath $publishDirectory -File -Filter '*.exe')
    if ($publishedExecutables.Count -ne 1) {
        throw "Expected exactly one published EXE, found $($publishedExecutables.Count): $publishDirectory"
    }
    if ($publishedExecutables[0].Name -ne 'AIFARBundlePackager.exe') {
        throw "Unexpected published EXE name: $($publishedExecutables[0].Name)"
    }

    $publishedFiles = @(Get-ChildItem -LiteralPath $publishDirectory -File)
    if ($publishedFiles.Count -ne 1) {
        throw "Single-file publish produced unexpected sidecar files: $($publishedFiles.Name -join ', ')"
    }

    $staleSidecars = if (Test-Path -LiteralPath $deliveryDirectory -PathType Container) {
        @(
            Get-ChildItem -LiteralPath $deliveryDirectory -File |
                Where-Object {
                    $_.Name -like 'AIFARBundlePackager.*' -and
                    $_.Name -ne 'AIFARBundlePackager.exe'
                }
        )
    } else {
        @()
    }
    if ($staleSidecars.Count -gt 0) {
        throw "Unexpected delivery sidecar files exist: $($staleSidecars.Name -join ', ')"
    }

    New-Item -ItemType Directory -Path $deliveryDirectory -Force | Out-Null
    Copy-Item -LiteralPath $publishedExecutables[0].FullName -Destination $temporaryDelivery
    Move-Item -LiteralPath $temporaryDelivery -Destination $deliveryPath -Force

    $deliveryFile = Get-Item -LiteralPath $deliveryPath
    Write-Host "Created AIFAR Bundle Packager: $($deliveryFile.FullName)"
    Write-Host "Size: $($deliveryFile.Length) bytes"
}
finally {
    if (Test-Path -LiteralPath $temporaryDelivery -PathType Leaf) {
        Remove-Item -LiteralPath $temporaryDelivery -Force
    }
    if (Test-Path -LiteralPath $publishDirectory -PathType Container) {
        Remove-Item -LiteralPath $publishDirectory -Recurse -Force
    }
}
