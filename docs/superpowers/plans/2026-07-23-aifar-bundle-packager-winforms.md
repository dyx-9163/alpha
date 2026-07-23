# AIFAR Bundle Packager WinForms Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `AIFARBundlePackager.exe`, a Windows x64 WinForms application that manually selects Java, Web, and output paths and creates protocol-compatible AIFAR artifact bundle ZIP files.

**Architecture:** A platform-neutral Core library owns service definitions, validation, JAR discovery, ZIP creation, hashing, manifest generation, temporary-file cleanup, and output replacement. A thin WinForms project owns path dialogs, service selection, progress/log display, and background execution. An xUnit project verifies Core behavior and the UI state model without automating native dialogs.

**Tech Stack:** C# 12, .NET 8, WinForms, `System.IO.Compression`, `System.Text.Json`, `System.Security.Cryptography`, xUnit.

## Global Constraints

- Target `net8.0` for Core and tests and `net8.0-windows` for WinForms; publish only `win-x64`.
- Publish with `SelfContained=true`, `PublishSingleFile=true`, and `PublishTrimmed=false`.
- Do not call or read `package-aifar-artifact-bundle.cmd` or `.ps1` at runtime.
- Java, Web, and output path fields start empty on every launch and are populated only after an accepted native selection dialog; output is always required, while Java/Web sources are required only for selected service categories.
- Do not persist, infer, or supply path defaults, and do not create a path settings file.
- Keep the fixed service order `oauth`, `permission`, `system`, `file`, `message`, `im`, `contacts`, `meeting`, `gateway`, `web-vue3`.
- Generate schema `aifar-artifact-bundle-v1` with lowercase SHA256, byte size, and `/` ZIP separators.
- Never overwrite an existing output ZIP unless validation, artifact generation, manifest generation, ZIP creation, and staging cleanup have all succeeded.
- Retain existing CMD and PowerShell files for migration comparison.

## File Map

- `tools/aifar-bundle-packager/AifarBundlePackager.sln`: solution containing the three projects.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/AifarBundlePackager.Core.csproj`: dependency-free Core library.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/BundleModels.cs`: request, result, progress, service, and manifest records.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/ServiceCatalog.cs`: fixed service order and exact Java target-directory mapping.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/JarLocator.cs`: runnable-JAR filtering and unique-candidate enforcement.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/BundlePackager.cs`: validation and complete transactional packaging workflow.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/ZipUtilities.cs`: deterministic directory ZIP writing and bounded cleanup retries.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/AifarBundlePackager.WinForms.csproj`: Windows GUI executable and single-file publish properties.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/Program.cs`: WinForms entry point.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/PackagingFormState.cs`: platform-neutral, testable empty-path, selection, service, and busy-state rules.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/MainForm.cs`: native dialogs, event handling, background package execution, progress, and error/success feedback.
- `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/MainForm.Layout.cs`: programmatic WinForms layout and control creation.
- `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj`: xUnit test project.
- `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/TestWorkspace.cs`: disposable filesystem fixture.
- `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/ServiceCatalogTests.cs`: order and mapping tests.
- `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/JarLocatorTests.cs`: unique candidate and exclusion tests.
- `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/BundlePackagerTests.cs`: manifest, hashes, inner/outer ZIP, failure, replacement, and cleanup tests.
- `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/PackagingFormStateTests.cs`: mandatory manual-path and button-state tests.
- `scripts/build-aifar-bundle-packager.ps1`: restore, test, publish, and copy exactly one deliverable EXE to `deploy/bin/AIFARBundlePackager.exe`.

---

### Task 1: Scaffold the solution and lock the service contract

**Files:**
- Create: `tools/aifar-bundle-packager/AifarBundlePackager.sln`
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/AifarBundlePackager.Core.csproj`
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/BundleModels.cs`
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/ServiceCatalog.cs`
- Create: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj`
- Create: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/ServiceCatalogTests.cs`

**Interfaces:**
- Produces: `ServiceDefinition`, `BundleRequest`, `BundleResult`, `BundleProgress`, `BundleStage`, manifest records, and `ServiceCatalog.All`.
- `ServiceCatalog.Select(IEnumerable<string>)` returns definitions in catalog order and rejects empty or unknown selections.

- [ ] **Step 1: Install or locate the official .NET 8 SDK**

Use a repository-external SDK location such as `D:\tools\dotnet` and verify:

```powershell
D:\tools\dotnet\dotnet.exe --info
```

Expected: an installed SDK with major version `8` and a `win-x64` RID.

- [ ] **Step 2: Create the solution and projects**

```powershell
$dotnet = 'D:\tools\dotnet\dotnet.exe'
& $dotnet new sln -n AifarBundlePackager -o tools/aifar-bundle-packager
& $dotnet new classlib -n AifarBundlePackager.Core -f net8.0 -o tools/aifar-bundle-packager/src/AifarBundlePackager.Core
& $dotnet new xunit -n AifarBundlePackager.Tests -f net8.0 -o tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests
& $dotnet sln tools/aifar-bundle-packager/AifarBundlePackager.sln add tools/aifar-bundle-packager/src/AifarBundlePackager.Core/AifarBundlePackager.Core.csproj tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj
& $dotnet add tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj reference tools/aifar-bundle-packager/src/AifarBundlePackager.Core/AifarBundlePackager.Core.csproj
```

Expected: solution lists Core and Tests, and the test project restores.

- [ ] **Step 3: Write the failing service-catalog tests**

Test exact ordered names and exact path parts:

```csharp
Assert.Equal(
    ["oauth", "permission", "system", "file", "message", "im", "contacts", "meeting", "gateway", "web-vue3"],
    ServiceCatalog.All.Select(item => item.Service));
Assert.Equal(["alpha-im", "alpha-im-core", "target"], ServiceCatalog.Get("im").TargetParts);
Assert.Equal(["alpha-gateway", "target"], ServiceCatalog.Get("gateway").TargetParts);
Assert.Throws<ArgumentException>(() => ServiceCatalog.Select(["unknown"]));
```

- [ ] **Step 4: Run the focused tests and verify failure**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj --filter ServiceCatalogTests
```

Expected: FAIL because `ServiceCatalog` and its models do not exist.

- [ ] **Step 5: Implement models and the exact service catalog**

Define these public contracts:

```csharp
public sealed record ServiceDefinition(string Service, string Module, IReadOnlyList<string> TargetParts, bool IsWeb = false);
public sealed record BundleRequest(string JavaSourceRoot, string WebDistRoot, string OutputPath, IReadOnlyCollection<string> Services);
public sealed record BundleResult(string OutputPath, long Size, IReadOnlyList<string> Services);
public enum BundleStage { Validating, Discovering, Copying, Hashing, WritingManifest, WritingBundle, Cleaning, Completed }
public sealed record BundleProgress(BundleStage Stage, string Message, int Completed, int Total);
```

Use the exact mappings from the existing PowerShell script and make `Select` de-duplicate input while preserving catalog order.

- [ ] **Step 6: Run tests and commit**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj --filter ServiceCatalogTests
git add tools/aifar-bundle-packager
git commit -m "feat(packager): define bundle service catalog"
```

Expected: all focused tests PASS.

### Task 2: Implement runnable-JAR discovery

**Files:**
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/JarLocator.cs`
- Create: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/TestWorkspace.cs`
- Create: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/JarLocatorTests.cs`

**Interfaces:**
- Consumes: `ServiceDefinition` from Task 1.
- Produces: `JarLocator.FindRunnableJar(string javaSourceRoot, ServiceDefinition definition): string`.

- [ ] **Step 1: Write failing tests for target paths and exclusions**

Create temporary target directories and assert:

```csharp
Assert.Equal(expectedJar, JarLocator.FindRunnableJar(root, ServiceCatalog.Get("permission")));
Assert.Throws<FileNotFoundException>(() => JarLocator.FindRunnableJar(root, ServiceCatalog.Get("oauth")));
Assert.Throws<InvalidDataException>(() => JarLocator.FindRunnableJar(root, ServiceCatalog.Get("im")));
```

Include files named `original-alpha-im-1.jar`, `alpha-im-sources.jar`, `alpha-im-javadoc.jar`, `alpha-im-tests.jar`, `alpha-im-test.jar`, and `alpha-im-plain.jar` and prove they are excluded.

- [ ] **Step 2: Verify the tests fail**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj --filter JarLocatorTests
```

Expected: FAIL because `JarLocator` is absent.

- [ ] **Step 3: Implement unique runnable-JAR discovery**

Resolve `TargetParts` below `javaSourceRoot`, match `${Module}-*.jar`, apply case-insensitive suffix exclusions, and throw errors that include the service name and resolved directory. Return the full path only when exactly one candidate remains.

- [ ] **Step 4: Verify and commit**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj --filter JarLocatorTests
git add tools/aifar-bundle-packager
git commit -m "feat(packager): discover runnable service jars"
```

Expected: all JAR locator tests PASS.

### Task 3: Implement transactional bundle generation

**Files:**
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/ZipUtilities.cs`
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/BundlePackager.cs`
- Create: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/BundlePackagerTests.cs`

**Interfaces:**
- Consumes: `BundleRequest`, `ServiceCatalog.Select`, and `JarLocator.FindRunnableJar`.
- Produces: `BundlePackager.Package(BundleRequest request, IProgress<BundleProgress>? progress = null): BundleResult`.
- Produces manifest JSON with `schema`, `app`, `kind`, and ordered `services` containing `service`, `module`, `artifact`, `fileName`, `sha256`, and `size`.

- [ ] **Step 1: Write failing validation and subset tests**

Assert whitespace paths, non-existent Java/Web paths, missing `index.html`, non-`.zip` output, directory output, and empty services throw contextual exceptions. Assert selecting `gateway`, `im`, `meeting`, and `web-vue3` writes exactly those four services in catalog order.

- [ ] **Step 2: Write failing archive-contract tests**

Open the produced outer ZIP with `ZipArchive` and assert:

```csharp
Assert.Equal("aifar-artifact-bundle-v1", manifest.Schema);
Assert.All(archive.Entries, entry => Assert.DoesNotContain('\\', entry.FullName));
Assert.NotNull(archive.GetEntry("artifacts/im/alpha-im.jar"));
Assert.NotNull(archive.GetEntry("artifacts/web-vue3/web-vue3.zip"));
```

Open the inner Web ZIP and prove its roots are `index.html` and `assets/...`, never `dist/index.html`. Recompute SHA256 and length from each outer artifact entry and compare with manifest values.

- [ ] **Step 3: Write failing transaction and cleanup tests**

Pre-create the target ZIP with sentinel bytes, cause a missing JAR failure, and assert the sentinel remains unchanged. On success assert it is replaced. After success and failure, assert the output directory contains no `.aifar-artifact-bundle-*` staging directory or temporary ZIP.

- [ ] **Step 4: Run tests and verify failure**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj --filter BundlePackagerTests
```

Expected: FAIL because `BundlePackager` and ZIP utilities do not exist.

- [ ] **Step 5: Implement ZIP and cleanup utilities**

`ZipUtilities.CreateFromDirectory` must enumerate files, derive relative names with `Path.GetRelativePath`, replace directory separators with `/`, and write with `CompressionLevel.Optimal`. `DeleteWithRetry` must attempt deletion at most 20 times with a 500 ms delay and throw a contextual `IOException` after the final failure.

- [ ] **Step 6: Implement packaging and safe replacement**

Validate the output path and at least one service for every bundle, then validate only the Java/Web source categories used by the selected services. Create GUID staging and temporary ZIP paths in the output directory. Generate all artifacts and manifest, create the temporary outer ZIP, delete staging successfully, and only then call `File.Move(temporaryZip, outputPath, true)`. In `finally`, clean any remaining staging or temporary path and combine cleanup errors without hiding the primary error.

- [ ] **Step 7: Run Core tests and commit**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj
git add tools/aifar-bundle-packager
git commit -m "feat(packager): generate transactional artifact bundles"
```

Expected: catalog, locator, and bundle tests all PASS.

### Task 4: Build the mandatory-manual-selection WinForms UI

**Files:**
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/AifarBundlePackager.WinForms.csproj`
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/Program.cs`
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/PackagingFormState.cs`
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/MainForm.cs`
- Create: `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/MainForm.Layout.cs`
- Create: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/PackagingFormStateTests.cs`
- Modify: `tools/aifar-bundle-packager/AifarBundlePackager.sln`
- Modify: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj`

**Interfaces:**
- Consumes: `ServiceCatalog.All`, `BundleRequest`, `BundlePackager.Package`, and `BundleProgress`.
- Produces: `PackagingFormState` with empty string path properties, selected services, `IsBusy`, and computed `CanPackage`.

- [ ] **Step 1: Scaffold WinForms and references**

```powershell
$dotnet = 'D:\tools\dotnet\dotnet.exe'
& $dotnet new winforms -n AifarBundlePackager.WinForms -f net8.0-windows -o tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms
& $dotnet add tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/AifarBundlePackager.WinForms.csproj reference tools/aifar-bundle-packager/src/AifarBundlePackager.Core/AifarBundlePackager.Core.csproj
& $dotnet add tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj reference tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/AifarBundlePackager.WinForms.csproj
& $dotnet sln tools/aifar-bundle-packager/AifarBundlePackager.sln add tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/AifarBundlePackager.WinForms.csproj
```

- [ ] **Step 2: Write failing form-state tests**

```csharp
var state = new PackagingFormState(ServiceCatalog.All.Select(item => item.Service));
Assert.Equal(string.Empty, state.JavaSourceRoot);
Assert.Equal(string.Empty, state.WebDistRoot);
Assert.Equal(string.Empty, state.OutputPath);
Assert.False(state.CanPackage);
state.SetJavaSourceRoot(javaRoot);
state.SetWebDistRoot(webRoot);
state.SetOutputPath(output);
Assert.True(state.CanPackage);
state.IsBusy = true;
Assert.False(state.CanPackage);
```

Also prove an absent dialog result causes no setter call and therefore preserves the current value, and clearing all services disables packaging.

- [ ] **Step 3: Run and verify failure**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj --filter PackagingFormStateTests
```

Expected: FAIL because `PackagingFormState` is absent.

- [ ] **Step 4: Implement state and layout**

Build a fixed minimum-size, DPI-aware Chinese interface using three read-only text boxes, two `FolderBrowserDialog` flows, one `SaveFileDialog` with empty `FileName`, ten checked service boxes, 全选/清空 buttons, 开始打包, progress bar, status label, read-only log box, and 打开输出目录. Do not read environment variables, working-directory paths, registry values, or settings files to initialize any path.

- [ ] **Step 5: Implement native-dialog and packaging events**

Only assign a path when the matching dialog returns `DialogResult.OK`. Recalculate `CanPackage` after every accepted path or service change. Run `BundlePackager.Package` through `Task.Run`, marshal progress through `Progress<BundleProgress>`, disable mutable controls while busy, preserve logs on failure, and use `ProcessStartInfo(outputDirectory) { UseShellExecute = true }` for opening the result folder.

- [ ] **Step 6: Verify tests and build, then commit**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj
D:\tools\dotnet\dotnet.exe build tools/aifar-bundle-packager/AifarBundlePackager.sln -c Release
git add tools/aifar-bundle-packager
git commit -m "feat(packager): add WinForms packaging interface"
```

Expected: all tests PASS and all three projects build in Release.

### Task 5: Publish one self-contained EXE

**Files:**
- Modify: `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/AifarBundlePackager.WinForms.csproj`
- Create: `scripts/build-aifar-bundle-packager.ps1`

**Interfaces:**
- Consumes: the solution and WinForms project.
- Produces: `deploy/bin/AIFARBundlePackager.exe` as the sole delivery artifact.

- [ ] **Step 1: Add publish properties**

Set:

```xml
<RuntimeIdentifier>win-x64</RuntimeIdentifier>
<SelfContained>true</SelfContained>
<PublishSingleFile>true</PublishSingleFile>
<PublishTrimmed>false</PublishTrimmed>
<DebugType>none</DebugType>
<DebugSymbols>false</DebugSymbols>
```

- [ ] **Step 2: Add the reproducible build script**

The script accepts an optional `DotNetPath`, defaults to `D:\tools\dotnet\dotnet.exe`, fails if it is absent, runs solution tests, publishes to a GUID temporary directory under `deploy`, verifies exactly one `.exe` and no DLLs, replaces `deploy/bin/AIFARBundlePackager.exe`, and removes the temporary directory in `finally`.

- [ ] **Step 3: Publish and verify the artifact shape**

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/build-aifar-bundle-packager.ps1
Get-ChildItem deploy/bin/AIFARBundlePackager.exe | Select-Object FullName,Length
```

Expected: tests PASS, publish succeeds, the EXE exists and has non-zero length, and no adjacent `AifarBundlePackager*.dll`, `.deps.json`, or `.runtimeconfig.json` is delivered.

- [ ] **Step 4: Commit**

```powershell
git add tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/AifarBundlePackager.WinForms.csproj scripts/build-aifar-bundle-packager.ps1
git commit -m "build(packager): publish self-contained Windows executable"
```

### Task 6: Run protocol-parity and GUI acceptance

**Files:**
- Modify only if acceptance reveals a defect in files from Tasks 1-5.

**Interfaces:**
- Consumes: `deploy/bin/AIFARBundlePackager.exe` and the current real Alpha Java/Web outputs.
- Produces: verified full and partial bundle ZIPs outside tracked source paths.

- [ ] **Step 1: Run the complete automated gate**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/AifarBundlePackager.sln -c Release
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/build-aifar-bundle-packager.ps1
git diff --check
```

Expected: all tests PASS, publish succeeds, and `git diff --check` prints no errors.

- [ ] **Step 2: Launch and inspect mandatory manual selection**

Start `deploy/bin/AIFARBundlePackager.exe`. Verify all three path fields are empty and read-only. Confirm Web-only requires Web plus output, Java-only requires Java plus output, and mixed selection requires both sources plus output. Cancel each dialog once and verify the existing displayed value remains unchanged.

- [ ] **Step 3: Generate a partial real bundle**

Manually choose the real Java root and Web `dist`, select only `gateway`, `im`, `meeting`, `web-vue3`, choose a temporary output ZIP, package it, and inspect it with `ZipArchive`. Verify service order, file names, hashes, sizes, `/` entries, and Web root structure match the current PowerShell contract.

- [ ] **Step 4: Generate a full real bundle when disk space permits**

Select all ten services and repeat manifest and archive validation. If local disk capacity is insufficient for the known multi-gigabyte bundle, record the capacity limitation and retain the automated synthetic full-selection coverage rather than deleting unrelated files.

- [ ] **Step 5: Final commit if acceptance required fixes**

```powershell
git add tools/aifar-bundle-packager scripts/build-aifar-bundle-packager.ps1
git commit -m "fix(packager): address acceptance findings"
```

Skip this commit only when acceptance required no source changes.
