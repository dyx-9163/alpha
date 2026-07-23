# AIFAR Bundle Packager Release Closeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a versioned, auditable GitHub Draft Release pipeline for `AIFARBundlePackager.exe` without tracking the EXE in Git, and close the historical implementation plans with evidence matrices.

**Architecture:** Keep `build.ps1` responsible for testing and producing exactly one local EXE, then add a separate transactional release-asset script that verifies the EXE version and emits the EXE, checksum, and JSON manifest. A tag-only Windows workflow rebuilds those assets and creates a Draft Release; historical plans gain task-level evidence tables instead of fabricated RED logs.

**Tech Stack:** PowerShell 5.1+, Node.js 22 test runner, .NET 8/WinForms, GitHub Actions, GitHub CLI.

## Global Constraints

- Release tags must match `aifar-bundle-packager-vX.Y.Z` with three numeric components.
- Formal release assets must be rebuilt on GitHub Actions from the tag commit; never upload the workspace's existing EXE.
- Keep `deploy/bin/AIFARBundlePackager.exe` ignored and untracked.
- The release directory must contain exactly `AIFARBundlePackager.exe`, `AIFARBundlePackager.exe.sha256`, and `release-manifest.json`.
- The EXE must remain Windows x64, self-contained, single-file, and untrimmed.
- A release asset EXE must be non-empty and smaller than 2 GiB.
- Create a Draft Release only; publishing remains a manual acceptance action.
- Do not reuse a published version tag. Acceptance fixes require a new patch version.
- Preserve the existing bundle schema and GUI behavior.
- Use red-green-refactor for script and workflow behavior.

---

### Task 1: Inject release version metadata into the EXE build

**Files:**
- Modify: `tools/aifar-bundle-packager/build.test.mjs`
- Modify: `tools/aifar-bundle-packager/build.ps1`

**Interfaces:**
- Consumes: optional `-Version X.Y.Z` and a 40-character hexadecimal `-SourceRevisionId` argument.
- Produces: the existing `deploy/bin/AIFARBundlePackager.exe`, with `Version=X.Y.Z`, `FileVersion=X.Y.Z.0`, `AssemblyVersion=X.Y.Z.0`, and `InformationalVersion=X.Y.Z+0123456789abcdef0123456789abcdef01234567`-shaped metadata when both release arguments are supplied.
- Preserves: argument-free local builds and exactly one published EXE.

- [ ] **Step 1: Add failing build-contract tests**

Extend `tools/aifar-bundle-packager/build.test.mjs` with assertions equivalent to:

```js
test('build script validates paired release metadata and injects MSBuild versions', () => {
  const script = readFileSync(buildScriptPath, 'utf8')

  assert.match(script, /\[string\]\$Version/)
  assert.match(script, /\[string\]\$SourceRevisionId/)
  assert.match(script, /\^\d\+\\\.\d\+\\\.\d\+\$/)
  assert.match(script, /\^[0-9a-f]\{40\}\$/i)
  assert.match(script, /Version and SourceRevisionId must be supplied together/i)
  assert.match(script, /-p:Version=/)
  assert.match(script, /-p:FileVersion=/)
  assert.match(script, /-p:AssemblyVersion=/)
  assert.match(script, /-p:InformationalVersion=/)
  assert.match(script, /-p:SourceRevisionId=/)
})
```

Keep the current single-file and sidecar assertions unchanged.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
node --test tools/aifar-bundle-packager/build.test.mjs --test-name-pattern "paired release metadata"
```

Expected: FAIL because `build.ps1` does not expose or inject release metadata.

- [ ] **Step 3: Implement paired release metadata validation**

Change the parameter block in `build.ps1` to:

```powershell
[CmdletBinding()]
param(
    [string]$DotNetPath = 'D:\tools\dotnet\dotnet.exe',
    [string]$Version = '',
    [string]$SourceRevisionId = ''
)
```

Before checking `DotNetPath`, validate the pair and derive versions:

```powershell
$hasVersion = -not [string]::IsNullOrWhiteSpace($Version)
$hasRevision = -not [string]::IsNullOrWhiteSpace($SourceRevisionId)
if ($hasVersion -ne $hasRevision) {
    throw 'Version and SourceRevisionId must be supplied together.'
}
if ($hasVersion -and $Version -notmatch '^\d+\.\d+\.\d+$') {
    throw "Version must use X.Y.Z numeric format: $Version"
}
if ($hasRevision -and $SourceRevisionId -notmatch '^[0-9a-fA-F]{40}$') {
    throw 'SourceRevisionId must be a 40-character Git commit SHA.'
}

$releaseProperties = @()
if ($hasVersion) {
    $numericVersion = "$Version.0"
    $normalizedRevision = $SourceRevisionId.ToLowerInvariant()
    $releaseProperties = @(
        "-p:Version=$Version",
        "-p:FileVersion=$numericVersion",
        "-p:AssemblyVersion=$numericVersion",
        "-p:InformationalVersion=$Version+$normalizedRevision",
        "-p:SourceRevisionId=$normalizedRevision"
    )
}
```

Build the publish argument array first, append `$releaseProperties`, and pass the combined array to `Invoke-DotNet`. Do not add default versions when release arguments are omitted.

- [ ] **Step 4: Run focused and complete tool tests**

Run:

```powershell
node --test tools/aifar-bundle-packager/build.test.mjs
pnpm test:tools
```

Expected: all tests PASS.

- [ ] **Step 5: Commit Task 1**

```powershell
git add tools/aifar-bundle-packager/build.ps1 tools/aifar-bundle-packager/build.test.mjs
git commit -m "build(packager): inject release version metadata"
```

---

### Task 2: Generate release assets transactionally

**Files:**
- Create: `tools/aifar-bundle-packager/create-release-assets.ps1`
- Create: `tools/aifar-bundle-packager/create-release-assets.test.mjs`

**Interfaces:**
- Consumes: `-ExecutablePath`, `-Version`, `-SourceRevisionId`, and `-OutputDirectory`.
- Produces: exactly three verified files in `OutputDirectory`.
- Produces manifest schema `aifar-bundle-packager-release-v1`.
- Guarantees: failures leave no partially populated new output directory and do not alter an existing non-empty directory.

- [ ] **Step 1: Write failing static and dynamic asset tests**

Create `create-release-assets.test.mjs` with these cases:

```js
test('release asset script exposes the transactional three-file contract', () => {
  const script = readFileSync(assetScriptPath, 'utf8')
  assert.match(script, /AIFARBundlePackager\.exe\.sha256/)
  assert.match(script, /release-manifest\.json/)
  assert.match(script, /aifar-bundle-packager-release-v1/)
  assert.match(script, /Get-FileHash[\s\S]*SHA256/i)
  assert.match(script, /FileVersionInfo/i)
  assert.match(script, /2147483648/)
  assert.match(script, /\.aifar-bundle-packager-release-/)
  assert.match(script, /finally[\s\S]*Remove-Item[\s\S]*Recurse/i)
})

test('release asset script rejects invalid input without partial assets', { skip: process.platform !== 'win32' }, () => {
  const workspace = mkdtempSync(path.join(tmpdir(), 'aifar-release-assets-'))
  try {
    const fakeExe = path.join(workspace, 'AIFARBundlePackager.exe')
    writeFileSync(fakeExe, 'not-a-pe')
    const output = path.join(workspace, 'release')
    const result = spawnSync('powershell.exe', [
      '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', assetScriptPath,
      '-ExecutablePath', fakeExe,
      '-Version', '1.2.3',
      '-SourceRevisionId', '0123456789abcdef0123456789abcdef01234567',
      '-OutputDirectory', output
    ], { encoding: 'utf8' })
    assert.notEqual(result.status, 0)
    assert.equal(existsSync(output), false)
  } finally {
    rmSync(workspace, { recursive: true, force: true })
  }
})
```

Also add a Windows-only success test. Create these two fixture files below the temporary workspace:

```xml
<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <OutputType>WinExe</OutputType>
    <TargetFramework>net8.0-windows</TargetFramework>
    <AssemblyName>AIFARBundlePackager</AssemblyName>
    <RuntimeIdentifier>win-x64</RuntimeIdentifier>
    <SelfContained>false</SelfContained>
    <UseAppHost>true</UseAppHost>
    <Version>1.2.3</Version>
    <FileVersion>1.2.3.0</FileVersion>
    <AssemblyVersion>1.2.3.0</AssemblyVersion>
    <InformationalVersion>1.2.3+0123456789abcdef0123456789abcdef01234567</InformationalVersion>
  </PropertyGroup>
</Project>
```

```csharp
Console.WriteLine("AIFAR Bundle Packager release fixture");
```

Select `D:\tools\dotnet\dotnet.exe` when it exists, otherwise use `dotnet` from `PATH`, and run:

```js
const publish = spawnSync(dotnetPath, [
  'publish', fixtureProjectPath,
  '--configuration', 'Release',
  '--runtime', 'win-x64',
  '--self-contained', 'false',
  '--output', fixturePublishDirectory
], { encoding: 'utf8' })
assert.equal(publish.status, 0, publish.stderr || publish.stdout)
```

Invoke the release script for version `1.2.3` and revision `0123456789abcdef0123456789abcdef01234567`, then verify the exact file set, lowercase checksum line, manifest values, and byte size.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
node --test tools/aifar-bundle-packager/create-release-assets.test.mjs
```

Expected: FAIL because `create-release-assets.ps1` does not exist.

- [ ] **Step 3: Implement strict input and PE metadata validation**

Create the PowerShell parameter block:

```powershell
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ExecutablePath,
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$SourceRevisionId,
    [Parameter(Mandatory = $true)][string]$OutputDirectory
)
```

Resolve full paths, require file name `AIFARBundlePackager.exe`, require a numeric `X.Y.Z` version and 40-character SHA, reject symlink/reparse-point inputs, require size `1..2147483647`, verify the first two bytes are `MZ`, and inspect:

```powershell
$versionInfo = [System.Diagnostics.FileVersionInfo]::GetVersionInfo($executableFullPath)
$expectedFileVersion = "$Version.0"
$expectedInformationalVersion = "$Version+$normalizedRevision"
```

Require `FileVersion == $expectedFileVersion` and `ProductVersion` to contain `$expectedInformationalVersion`.

- [ ] **Step 4: Implement staging, checksum, manifest, and atomic directory delivery**

Use a sibling GUID directory whose name starts with `.aifar-bundle-packager-release-`. Copy the EXE, calculate SHA256, write the lowercase two-space checksum line with UTF-8 without BOM, and serialize:

```powershell
[ordered]@{
    schema = 'aifar-bundle-packager-release-v1'
    version = $Version
    gitCommit = $normalizedRevision
    runtimeIdentifier = 'win-x64'
    fileName = 'AIFARBundlePackager.exe'
    size = [int64]$deliveryExe.Length
    sha256 = $sha256
    builtAt = [DateTime]::UtcNow.ToString('o')
}
```

Re-read all three staged assets, verify their values and exact names, then move the staging directory to the final output. Reject non-empty existing outputs; if the final directory exists and is empty, remove only that exact empty directory immediately before the final move. Clean the staging directory in `finally`.

- [ ] **Step 5: Run asset tests and all tool tests**

Run:

```powershell
node --test tools/aifar-bundle-packager/create-release-assets.test.mjs
pnpm test:tools
```

Expected: all tests PASS and the success fixture reports exactly three assets.

- [ ] **Step 6: Commit Task 2**

```powershell
git add tools/aifar-bundle-packager/create-release-assets.ps1 tools/aifar-bundle-packager/create-release-assets.test.mjs
git commit -m "build(packager): generate verified release assets"
```

---

### Task 3: Add the tag-only Draft Release workflow

**Files:**
- Create: `.github/workflows/aifar-bundle-packager-release.yml`
- Create: `tools/aifar-bundle-packager/release-workflow.test.mjs`

**Interfaces:**
- Consumes: tags matching `aifar-bundle-packager-v*`.
- Produces: a GitHub Draft Release titled `AIFAR Bundle Packager vX.Y.Z`.
- Uploads: the exact three release assets.

- [ ] **Step 1: Write failing workflow contract tests**

Create `release-workflow.test.mjs` and assert:

```js
test('release workflow is tag-only, versioned, tested, and draft-only', () => {
  const workflow = readFileSync(workflowPath, 'utf8')
  assert.match(workflow, /tags:[\s\S]*aifar-bundle-packager-v\*/)
  assert.doesNotMatch(workflow, /pull_request:/)
  assert.match(workflow, /contents:\s*write/)
  assert.match(workflow, /runs-on:\s*windows-latest/)
  assert.match(workflow, /pnpm test:tools/)
  assert.match(workflow, /pnpm test:scripts/)
  assert.match(workflow, /dotnet test tools\/aifar-bundle-packager\/AifarBundlePackager\.sln/)
  assert.match(workflow, /build\.ps1[\s\S]*-Version[\s\S]*-SourceRevisionId/)
  assert.match(workflow, /create-release-assets\.ps1/)
  assert.match(workflow, /gh release create[\s\S]*--draft[\s\S]*--verify-tag/)
  assert.doesNotMatch(workflow, /gh release create[^\r\n]*--target/)
})
```

Add assertions for the tag-to-SHA check, current tracked-file size guard, exact three asset paths, release title, commit/RID/size/SHA256 release notes, and `GH_TOKEN: ${{ github.token }}`.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
node --test tools/aifar-bundle-packager/release-workflow.test.mjs
```

Expected: FAIL because the workflow does not exist.

- [ ] **Step 3: Implement the Windows release workflow**

Create a workflow with:

```yaml
name: AIFAR Bundle Packager Release

on:
  push:
    tags:
      - "aifar-bundle-packager-v*"

permissions:
  contents: write

jobs:
  release:
    runs-on: windows-latest
    timeout-minutes: 30
```

Use checkout v4 with `fetch-depth: 0`, Node 22, .NET 8, and pnpm 11.7.0. In PowerShell, validate `github.ref_name` against `^aifar-bundle-packager-v(\d+\.\d+\.\d+)$`, verify the tag commit equals `${{ github.sha }}`, and write `PACKAGER_VERSION` to `$env:GITHUB_ENV`.

Run all three automated gates, verify the EXE is not tracked, and scan tracked files for sizes greater than 100,000,000 bytes. Then call:

```powershell
./tools/aifar-bundle-packager/build.ps1 `
  -DotNetPath (Get-Command dotnet).Source `
  -Version $env:PACKAGER_VERSION `
  -SourceRevisionId '${{ github.sha }}'

./tools/aifar-bundle-packager/create-release-assets.ps1 `
  -ExecutablePath deploy/bin/AIFARBundlePackager.exe `
  -Version $env:PACKAGER_VERSION `
  -SourceRevisionId '${{ github.sha }}' `
  -OutputDirectory deploy/aifar-bundle-packager-release
```

Recompute the checksum and manifest values in a separate PowerShell step. Generate release notes under `$env:RUNNER_TEMP`, then invoke `gh release create` with the tag, title, notes file, three explicit assets, `--draft`, and `--verify-tag`.

- [ ] **Step 4: Run workflow and complete tool contract tests**

Run:

```powershell
node --test tools/aifar-bundle-packager/release-workflow.test.mjs
pnpm test:tools
```

Expected: all tests PASS.

- [ ] **Step 5: Commit Task 3**

```powershell
git add .github/workflows/aifar-bundle-packager-release.yml tools/aifar-bundle-packager/release-workflow.test.mjs
git commit -m "ci(packager): create draft releases from tags"
```

---

### Task 4: Close historical plans with task-level evidence

**Files:**
- Create: `tools/aifar-bundle-packager/plan-closeout.test.mjs`
- Modify: `docs/superpowers/plans/2026-07-23-aifar-bundle-packager-winforms.md`
- Modify: `docs/superpowers/plans/2026-07-24-aifar-bundle-conditional-source-paths.md`
- Modify: `tools/aifar-bundle-packager/README.md`

**Interfaces:**
- Produces: an authoritative `Implementation Status and Evidence` section in each historical plan.
- Preserves: original task instructions and checkboxes as historical execution scripts.

- [ ] **Step 1: Write failing closeout-document tests**

Create `plan-closeout.test.mjs` and require both plans to contain:

```js
for (const plan of plans) {
  assert.match(plan, /## Implementation Status and Evidence/)
  assert.match(plan, /authoritative current status/i)
  assert.match(plan, /historical RED output was not persisted/i)
  assert.match(plan, /pnpm test:tools/)
  assert.match(plan, /36\/36/)
}
```

Require the WinForms plan to list Tasks 1-6 and current `tools/aifar-bundle-packager/build.ps1`; require the conditional plan to list Tasks 1-3 and the three implementation commits. Require the README to describe the tag pattern, three release assets, Draft acceptance, and that the EXE is not stored in Git.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
node --test tools/aifar-bundle-packager/plan-closeout.test.mjs
```

Expected: FAIL because the evidence sections and Release documentation are absent.

- [ ] **Step 3: Add evidence matrices without fabricating RED logs**

Insert immediately before `## Global Constraints` in each plan:

```markdown
## Implementation Status and Evidence

This section is the authoritative current status. The original checkboxes below remain as the historical execution script. Historical RED output was not persisted independently; current regression coverage and implementation commits are recorded instead of reconstructing logs.
```

Add one table row per task with status, final commit, current implementation files, current test files, and current evidence. Use these rewritten current-branch commit IDs:

```text
WinForms Task 1: f8f18ffc
WinForms Task 2: 9633dae3
WinForms Task 3: c3499664
WinForms Task 4: 1c2e37b6
WinForms Task 5: a49316be
WinForms Task 6: 17b8de49
Conditional Task 1: 25414177
Conditional Task 2: 8945d47f
Conditional Task 3: a993bd40
```

Document the latest gate as `.NET 36/36`, `tools 17/17` before new tests, and `scripts 290/290`; after adding new tool tests, replace the tool count with the final observed number before commit.

- [ ] **Step 4: Document the Release flow in the tool README**

Add a `GitHub Release` section covering:

```text
tag: aifar-bundle-packager-vX.Y.Z
assets: AIFARBundlePackager.exe, AIFARBundlePackager.exe.sha256, release-manifest.json
state: Draft until downloaded checksum, clean Windows x64 launch, and Java-only/Web-only/mixed smoke pass
storage: source and scripts are tracked; the EXE is never tracked in Git
```

- [ ] **Step 5: Run documentation and tool tests**

Run:

```powershell
node --test tools/aifar-bundle-packager/plan-closeout.test.mjs
pnpm test:tools
git diff --check
```

Expected: all tests PASS with no whitespace errors.

- [ ] **Step 6: Commit Task 4**

```powershell
git add tools/aifar-bundle-packager/plan-closeout.test.mjs tools/aifar-bundle-packager/README.md docs/superpowers/plans/2026-07-23-aifar-bundle-packager-winforms.md docs/superpowers/plans/2026-07-24-aifar-bundle-conditional-source-paths.md
git commit -m "docs(packager): close implementation evidence"
```

---

### Task 5: Run full local release acceptance without publishing

**Files:**
- Modify only if acceptance reveals a defect in Tasks 1-4.
- Modify at completion: `memory.md`

**Interfaces:**
- Consumes: current commit SHA and version `1.0.0` for local validation only.
- Produces: a versioned local EXE and a temporary three-file release directory outside tracked paths.
- Does not produce: a Git tag, GitHub Release, remote push, or tracked EXE.

- [ ] **Step 1: Run all automated gates**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/AifarBundlePackager.sln --configuration Release
pnpm test:tools
pnpm test:scripts
git diff --check
```

Expected: 0 failures.

- [ ] **Step 2: Build a versioned EXE from the current commit**

```powershell
$currentCommit = git -c safe.directory=D:/workspace/aifar-deployment rev-parse HEAD
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/aifar-bundle-packager/build.ps1 -DotNetPath D:\tools\dotnet\dotnet.exe -Version 1.0.0 -SourceRevisionId $currentCommit
```

Expected: build tests pass and exactly `deploy/bin/AIFARBundlePackager.exe` is delivered.

- [ ] **Step 3: Generate and independently verify local release assets**

Use a GUID directory below `deploy`:

```powershell
$currentCommit = git -c safe.directory=D:/workspace/aifar-deployment rev-parse HEAD
$releaseDirectory = Join-Path (Resolve-Path deploy) ('.aifar-bundle-packager-acceptance-' + [Guid]::NewGuid().ToString('N'))
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/aifar-bundle-packager/create-release-assets.ps1 -ExecutablePath deploy/bin/AIFARBundlePackager.exe -Version 1.0.0 -SourceRevisionId $currentCommit -OutputDirectory $releaseDirectory
```

Require exactly three files, recompute SHA256, parse the JSON manifest, compare EXE `FileVersionInfo`, then remove only this validated GUID acceptance directory.

- [ ] **Step 4: Run a hidden GUI startup smoke**

Start the EXE with `Start-Process -PassThru -WindowStyle Hidden`, confirm the process remains alive for three seconds, then stop only that process. Expected: the process starts and remains running until the test closes it.

- [ ] **Step 5: Verify Git safety and repository state**

```powershell
git ls-files --error-unmatch deploy/bin/AIFARBundlePackager.exe
git status --short
```

Expected: the first command fails because the EXE is untracked/ignored; status contains only intended source, workflow, test, documentation, and memory changes before the final commit. Scan the implementation commit range and require zero blobs over 100,000,000 bytes.

- [ ] **Step 6: Record completion and commit**

Append a concise problem/conclusion entry to `memory.md`, run `git diff --check`, and commit only the completion record or any acceptance-driven fixes:

```powershell
git add memory.md
git commit -m "docs: record bundle packager release closeout"
```

Do not tag, push, or create a GitHub Release in this task.

- [ ] **Step 7: Schedule the user-requested shutdown only after successful completion**

After every preceding task is complete, all final gates pass, commits succeed, and the final repository state has been captured, run:

```powershell
shutdown.exe /s /t 60 /c "AIFAR Bundle Packager release closeout completed successfully."
```

Expected: Windows reports that shutdown is scheduled. Do not run this command after a failed or incomplete gate. The 60-second delay is reserved for returning the final completion report.
