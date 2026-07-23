# Command-line implementation of the AIFAR artifact bundle protocol.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$JavaSourceRoot,

    [Parameter(Mandatory = $true)]
    [string]$WebDistRoot,

    [Parameter(Mandatory = $true)]
    [string]$OutputPath,

    [string]$Services = 'all'
)

$ErrorActionPreference = 'Stop'

$serviceDefinitions = [ordered]@{
    oauth = [ordered]@{
        Module = 'alpha-oauth'
        TargetParts = @('alpha-oauth', 'alpha-oauth-server', 'target')
    }
    permission = [ordered]@{
        Module = 'alpha-permission'
        TargetParts = @('alpha-permission', 'alpha-permission-server', 'target')
    }
    system = [ordered]@{
        Module = 'alpha-system'
        TargetParts = @('alpha-system', 'alpha-system-server', 'target')
    }
    file = [ordered]@{
        Module = 'alpha-file'
        TargetParts = @('alpha-file', 'alpha-file-server', 'target')
    }
    message = [ordered]@{
        Module = 'alpha-message'
        TargetParts = @('alpha-message', 'alpha-message-server', 'target')
    }
    im = [ordered]@{
        Module = 'alpha-im'
        TargetParts = @('alpha-im', 'alpha-im-core', 'target')
    }
    contacts = [ordered]@{
        Module = 'alpha-contacts'
        TargetParts = @('alpha-contacts', 'alpha-contacts-core', 'target')
    }
    meeting = [ordered]@{
        Module = 'alpha-meeting'
        TargetParts = @('alpha-meeting', 'alpha-meeting-core', 'target')
    }
    gateway = [ordered]@{
        Module = 'alpha-gateway'
        TargetParts = @('alpha-gateway', 'target')
    }
    'web-vue3' = [ordered]@{
        Module = 'web-vue3'
    }
}

function Get-SelectedServices {
    param([string]$Value)

    if ([string]::IsNullOrWhiteSpace($Value)) {
        $Value = 'all'
    }

    $requested = @()
    foreach ($rawToken in @($Value -split ',')) {
        $token = $rawToken.Trim().ToLowerInvariant()
        if ([string]::IsNullOrWhiteSpace($token)) {
            throw "The service list contains an empty item: '$Value'."
        }
        $requested += $token
    }

    if ($requested -contains 'all') {
        if ($requested.Count -ne 1) {
            throw "'all' cannot be combined with individual services."
        }
        return @($serviceDefinitions.Keys)
    }

    $selected = @{}
    foreach ($service in $requested) {
        if (-not ($serviceDefinitions.Keys -contains $service)) {
            throw "Unsupported service '$service'. Supported values: $($serviceDefinitions.Keys -join ', ')."
        }
        $selected[$service] = $true
    }

    return @($serviceDefinitions.Keys | Where-Object { $selected.ContainsKey($_) })
}

function Join-PathParts {
    param(
        [string]$Root,
        [string[]]$Parts
    )

    $result = $Root
    foreach ($part in $Parts) {
        $result = Join-Path -Path $result -ChildPath $part
    }
    return $result
}

function Get-RunnableJar {
    param(
        [string]$Service,
        [System.Collections.IDictionary]$Definition
    )

    $targetDirectory = Join-PathParts -Root $JavaSourceRoot -Parts $Definition.TargetParts
    if (-not (Test-Path -LiteralPath $targetDirectory -PathType Container)) {
        throw "Service '$Service' target directory does not exist: $targetDirectory"
    }

    $pattern = "$($Definition.Module)-*.jar"
    $candidates = @(
        Get-ChildItem -LiteralPath $targetDirectory -File -Filter $pattern |
            Where-Object {
                $_.Name -notlike 'original-*' -and
                $_.Name -notmatch '(?i)(-sources|-javadoc|-tests?|-plain)\.jar$'
            }
    )

    if ($candidates.Count -eq 0) {
        throw "Service '$Service' has no runnable JAR matching '$pattern' in: $targetDirectory"
    }
    if ($candidates.Count -gt 1) {
        throw "Service '$Service' has multiple runnable JAR candidates in '$targetDirectory': $($candidates.Name -join ', ')"
    }
    return $candidates[0].FullName
}

function New-ManifestItem {
    param(
        [string]$Service,
        [string]$Module,
        [string]$Artifact,
        [string]$FileName,
        [string]$LocalPath
    )

    $file = Get-Item -LiteralPath $LocalPath
    return [ordered]@{
        service = $Service
        module = $Module
        artifact = $Artifact
        fileName = $FileName
        sha256 = (Get-FileHash -LiteralPath $LocalPath -Algorithm SHA256).Hash.ToLowerInvariant()
        size = [long]$file.Length
    }
}

function Remove-PathWithRetry {
    param(
        [string]$Path,
        [switch]$Recurse,
        [int]$Attempts = 20,
        [int]$DelayMilliseconds = 500
    )

    if ([string]::IsNullOrWhiteSpace($Path) -or -not (Test-Path -LiteralPath $Path)) {
        return
    }

    $lastError = $null
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        try {
            Remove-Item -LiteralPath $Path -Recurse:$Recurse -Force
            return
        }
        catch {
            $lastError = $_
            if ($attempt -lt $Attempts) {
                Start-Sleep -Milliseconds $DelayMilliseconds
            }
        }
    }

    throw "Unable to remove temporary path after $Attempts attempts: $Path. $($lastError.Exception.Message)"
}

function New-ZipFromDirectory {
    param(
        [string]$SourceDirectory,
        [string]$DestinationPath
    )

    $sourceFullPath = [System.IO.Path]::GetFullPath($SourceDirectory)
    $sourcePrefix = $sourceFullPath
    if (-not $sourcePrefix.EndsWith([System.IO.Path]::DirectorySeparatorChar.ToString())) {
        $sourcePrefix += [System.IO.Path]::DirectorySeparatorChar
    }

    $fileStream = [System.IO.File]::Open(
        $DestinationPath,
        [System.IO.FileMode]::CreateNew,
        [System.IO.FileAccess]::ReadWrite,
        [System.IO.FileShare]::None
    )
    $archive = New-Object System.IO.Compression.ZipArchive(
        $fileStream,
        [System.IO.Compression.ZipArchiveMode]::Create,
        $false
    )
    try {
        foreach ($file in Get-ChildItem -LiteralPath $sourceFullPath -Recurse -File) {
            $entryName = $file.FullName.Substring($sourcePrefix.Length).Replace('\', '/')
            [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
                $archive,
                $file.FullName,
                $entryName,
                [System.IO.Compression.CompressionLevel]::Optimal
            ) | Out-Null
        }
    }
    finally {
        $archive.Dispose()
        $fileStream.Dispose()
    }
}

$stagingDirectory = $null
$temporaryArchive = $null
$exitCode = 0
$failureMessages = New-Object System.Collections.Generic.List[string]
$completedOutputPath = $null
$completedServices = @()

try {
    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem

    $selectedServices = @(Get-SelectedServices -Value $Services)
    $outputFullPath = [System.IO.Path]::GetFullPath($OutputPath)
    if (-not [string]::Equals([System.IO.Path]::GetExtension($outputFullPath), '.zip', [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "OutputPath must end with .zip: $outputFullPath"
    }
    if (Test-Path -LiteralPath $outputFullPath -PathType Container) {
        throw "OutputPath points to a directory: $outputFullPath"
    }

    $outputDirectory = [System.IO.Path]::GetDirectoryName($outputFullPath)
    if (-not (Test-Path -LiteralPath $outputDirectory -PathType Container)) {
        New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
    }

    $workId = [Guid]::NewGuid().ToString('N')
    $stagingDirectory = Join-Path $outputDirectory ".aifar-artifact-bundle-$workId"
    $temporaryArchive = Join-Path $outputDirectory ".aifar-artifact-bundle-$workId.zip"
    New-Item -ItemType Directory -Path $stagingDirectory | Out-Null

    $manifestServices = @()
    foreach ($service in $selectedServices) {
        $definition = $serviceDefinitions[$service]
        $artifactDirectory = Join-Path (Join-Path $stagingDirectory 'artifacts') $service
        New-Item -ItemType Directory -Path $artifactDirectory -Force | Out-Null

        if ($service -eq 'web-vue3') {
            if (-not (Test-Path -LiteralPath $WebDistRoot -PathType Container)) {
                throw "Service 'web-vue3' dist directory does not exist: $WebDistRoot"
            }
            if (-not (Test-Path -LiteralPath (Join-Path $WebDistRoot 'index.html') -PathType Leaf)) {
                throw "Service 'web-vue3' requires index.html in: $WebDistRoot"
            }
            $fileName = 'web-vue3.zip'
            $artifactLocalPath = Join-Path $artifactDirectory $fileName
            New-ZipFromDirectory -SourceDirectory $WebDistRoot -DestinationPath $artifactLocalPath
        }
        else {
            $sourceJar = Get-RunnableJar -Service $service -Definition $definition
            $fileName = "$($definition.Module).jar"
            $artifactLocalPath = Join-Path $artifactDirectory $fileName
            Copy-Item -LiteralPath $sourceJar -Destination $artifactLocalPath
        }

        $artifactPath = "artifacts/$service/$fileName"
        $manifestServices += New-ManifestItem `
            -Service $service `
            -Module $definition.Module `
            -Artifact $artifactPath `
            -FileName $fileName `
            -LocalPath $artifactLocalPath
    }

    $manifest = [ordered]@{
        schema = 'aifar-artifact-bundle-v1'
        app = 'aifar'
        kind = 'aifar-service-artifacts'
        services = $manifestServices
    }
    $manifestJson = $manifest | ConvertTo-Json -Depth 6
    $utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText((Join-Path $stagingDirectory 'manifest.json'), $manifestJson, $utf8WithoutBom)

    New-ZipFromDirectory -SourceDirectory $stagingDirectory -DestinationPath $temporaryArchive

    Move-Item -LiteralPath $temporaryArchive -Destination $outputFullPath -Force
    $temporaryArchive = $null
    $completedOutputPath = $outputFullPath
    $completedServices = $selectedServices
}
catch {
    $exitCode = 1
    $failureMessages.Add($_.Exception.Message)
}
finally {
    try {
        Remove-PathWithRetry -Path $stagingDirectory -Recurse
    }
    catch {
        $exitCode = 1
        $failureMessages.Add($_.Exception.Message)
    }
    try {
        Remove-PathWithRetry -Path $temporaryArchive
    }
    catch {
        $exitCode = 1
        $failureMessages.Add($_.Exception.Message)
    }
}

if ($exitCode -ne 0) {
    foreach ($message in $failureMessages) {
        [Console]::Error.WriteLine("ERROR: $message")
    }
    exit $exitCode
}

Write-Host "Created AIFAR artifact bundle: $completedOutputPath"
Write-Host "Services: $($completedServices -join ', ')"
exit $exitCode
