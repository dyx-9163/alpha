[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ExecutablePath,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$SourceRevisionId,
    [Parameter(Mandatory = $true)][string]$OutputDirectory
)

$ErrorActionPreference = 'Stop'
$maximumReleaseAssetSize = [int64]2147483648
$expectedExecutableName = 'AIFARBundlePackager.exe'
$expectedChecksumName = 'AIFARBundlePackager.exe.sha256'
$expectedManifestName = 'release-manifest.json'
$utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)

if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw "Version must use X.Y.Z numeric format: $Version"
}
if ($SourceRevisionId -notmatch '^[0-9a-fA-F]{40}$') {
    throw 'SourceRevisionId must be a 40-character Git commit SHA.'
}

$normalizedRevision = $SourceRevisionId.ToLowerInvariant()
$executableFullPath = [System.IO.Path]::GetFullPath($ExecutablePath)
$outputFullPath = [System.IO.Path]::GetFullPath($OutputDirectory)
$outputParent = Split-Path -Parent $outputFullPath

if (-not (Test-Path -LiteralPath $executableFullPath -PathType Leaf)) {
    throw "Release executable does not exist: $executableFullPath"
}
if ([System.IO.Path]::GetFileName($executableFullPath) -cne $expectedExecutableName) {
    throw "Release executable must be named $expectedExecutableName`: $executableFullPath"
}
if (-not (Test-Path -LiteralPath $outputParent -PathType Container)) {
    throw "Release output parent directory does not exist: $outputParent"
}

$executableFile = Get-Item -LiteralPath $executableFullPath -Force
if (($executableFile.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
    throw "Release executable must not be a reparse point: $executableFullPath"
}
if ($executableFile.Length -le 0 -or $executableFile.Length -ge $maximumReleaseAssetSize) {
    throw "Release executable size must be between 1 byte and 2147483647 bytes: $($executableFile.Length)"
}

$stream = [System.IO.File]::OpenRead($executableFullPath)
try {
    if ($stream.ReadByte() -ne [int][char]'M' -or $stream.ReadByte() -ne [int][char]'Z') {
        throw "Release executable is not a PE file: $executableFullPath"
    }
}
finally {
    $stream.Dispose()
}

$versionInfo = [System.Diagnostics.FileVersionInfo]::GetVersionInfo($executableFullPath)
$expectedFileVersion = "$Version.0"
$expectedInformationalVersion = "$Version+$normalizedRevision"
if ($versionInfo.FileVersion -cne $expectedFileVersion) {
    throw "Release executable file version mismatch: expected $expectedFileVersion, got $($versionInfo.FileVersion)"
}
if ([string]::IsNullOrWhiteSpace($versionInfo.ProductVersion) -or
    -not $versionInfo.ProductVersion.Contains($expectedInformationalVersion)) {
    throw "Release executable product version must contain $expectedInformationalVersion`: $($versionInfo.ProductVersion)"
}

if (Test-Path -LiteralPath $outputFullPath) {
    $existingOutput = Get-Item -LiteralPath $outputFullPath -Force
    if (-not $existingOutput.PSIsContainer) {
        throw "Release output path already exists as a file: $outputFullPath"
    }
    if (($existingOutput.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
        throw "Release output directory must not be a reparse point: $outputFullPath"
    }
    if (@(Get-ChildItem -LiteralPath $outputFullPath -Force).Count -ne 0) {
        throw "Release output directory must be empty: $outputFullPath"
    }
}

$workId = [Guid]::NewGuid().ToString('N')
$stagingDirectory = Join-Path $outputParent ".aifar-bundle-packager-release-$workId"
if (Test-Path -LiteralPath $stagingDirectory) {
    throw "Release staging directory already exists: $stagingDirectory"
}

try {
    New-Item -ItemType Directory -Path $stagingDirectory | Out-Null
    $stagedExecutable = Join-Path $stagingDirectory $expectedExecutableName
    $stagedChecksum = Join-Path $stagingDirectory $expectedChecksumName
    $stagedManifest = Join-Path $stagingDirectory $expectedManifestName

    Copy-Item -LiteralPath $executableFullPath -Destination $stagedExecutable
    $deliveredExecutable = Get-Item -LiteralPath $stagedExecutable
    $sha256 = (Get-FileHash -LiteralPath $stagedExecutable -Algorithm SHA256).Hash.ToLowerInvariant()
    $checksumLine = "$sha256  $expectedExecutableName`n"
    [System.IO.File]::WriteAllText($stagedChecksum, $checksumLine, $utf8WithoutBom)

    $manifest = [ordered]@{
        schema = 'aifar-bundle-packager-release-v1'
        version = $Version
        gitCommit = $normalizedRevision
        runtimeIdentifier = 'win-x64'
        fileName = $expectedExecutableName
        size = [int64]$deliveredExecutable.Length
        sha256 = $sha256
        builtAt = [DateTime]::UtcNow.ToString('o')
    }
    $manifestJson = ($manifest | ConvertTo-Json -Depth 4) + "`n"
    [System.IO.File]::WriteAllText($stagedManifest, $manifestJson, $utf8WithoutBom)

    $expectedNames = @($expectedExecutableName, $expectedChecksumName, $expectedManifestName) | Sort-Object
    $actualNames = @(Get-ChildItem -LiteralPath $stagingDirectory -File | Select-Object -ExpandProperty Name | Sort-Object)
    if (($actualNames -join '|') -cne ($expectedNames -join '|')) {
        throw "Release staging contains unexpected files: $($actualNames -join ', ')"
    }
    if ([System.IO.File]::ReadAllText($stagedChecksum) -cne $checksumLine) {
        throw 'Release checksum file verification failed.'
    }
    $verifiedManifest = Get-Content -LiteralPath $stagedManifest -Raw | ConvertFrom-Json
    if ($verifiedManifest.schema -cne 'aifar-bundle-packager-release-v1' -or
        $verifiedManifest.version -cne $Version -or
        $verifiedManifest.gitCommit -cne $normalizedRevision -or
        $verifiedManifest.runtimeIdentifier -cne 'win-x64' -or
        $verifiedManifest.fileName -cne $expectedExecutableName -or
        [int64]$verifiedManifest.size -ne [int64]$deliveredExecutable.Length -or
        $verifiedManifest.sha256 -cne $sha256) {
        throw 'Release manifest verification failed.'
    }

    if (Test-Path -LiteralPath $outputFullPath -PathType Container) {
        Remove-Item -LiteralPath $outputFullPath -Force
    }
    Move-Item -LiteralPath $stagingDirectory -Destination $outputFullPath
    $stagingDirectory = $null

    Write-Host "Created AIFAR Bundle Packager release assets: $outputFullPath"
    Write-Host "SHA256: $sha256"
    Write-Host "Size: $($deliveredExecutable.Length) bytes"
}
finally {
    if ($stagingDirectory -and (Test-Path -LiteralPath $stagingDirectory -PathType Container)) {
        Remove-Item -LiteralPath $stagingDirectory -Recurse -Force
    }
}
