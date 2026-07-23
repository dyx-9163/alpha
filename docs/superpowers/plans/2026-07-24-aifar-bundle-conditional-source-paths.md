# AIFAR Bundle Packager Conditional Source Paths Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Java source root and Web dist conditionally required according to the selected service categories, while always requiring an output ZIP and at least one service.

**Architecture:** `PackagingFormState` is the shared source of UI readiness and source-category requirements. `BundlePackager.Validate` independently enforces the same rules at the Core boundary so callers cannot bypass them. The WinForms layer only renders the computed requirements and continues passing retained path values unchanged.

**Tech Stack:** C# 12, .NET 8, WinForms, xUnit, PowerShell single-file publishing.

## Implementation Status and Evidence

This section is the authoritative current status. The unchecked boxes below are the original execution script and are retained as historical intent; they do not mean the feature is unfinished. Historical RED output was not persisted independently, so this closeout records the committed test-first implementation and repeatable green verification without inventing missing logs.

| Task | Status | Implementation commit | Current evidence |
| --- | --- | --- | --- |
| 1 | Complete | `25414177` | `PackagingFormState` computes Java/Web requirements from the selected service categories, with Java-only, Web-only, mixed, and empty-selection tests. |
| 2 | Complete | `8945d47f` | Core validation independently ignores unused source paths and rejects missing required sources. |
| 3 | Complete | `a993bd40` | WinForms requirement hints and the self-contained EXE were rebuilt and smoke-tested. |

Current repeatable verification is `D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/AifarBundlePackager.sln --configuration Release` (36/36) plus `pnpm test:tools`.

## Global Constraints

- `web-vue3` is the only Web service; every other catalog entry is a Java service.
- Output ZIP and at least one service are always required.
- Unused source paths remain visible but are ignored even if empty or invalid.
- Both source-selector buttons remain enabled while the form is idle.
- Bundle schema, service order, artifact names, hashing, sizes, ZIP layout, transaction behavior, and no-default/no-persistence behavior remain unchanged.
- Follow red-green-refactor and do not modify production behavior before observing the new tests fail.

---

### Task 1: Make form readiness service-aware

**Files:**
- Modify: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/PackagingFormStateTests.cs`
- Modify: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/PackagingFormState.cs`

**Interfaces:**
- Produces: `bool PackagingFormState.RequiresJavaSource`
- Produces: `bool PackagingFormState.RequiresWebDist`
- Updates: `PackagingFormState.CanPackage`

- [ ] **Step 1: Replace the all-path test with failing category tests**

Add tests that clear the default services and select the requested category:

```csharp
[Fact]
public void CanPackage_WebOnlyRequiresWebAndOutputButNotJava()
{
    var state = CreateState();
    state.ClearServices();
    state.SetServiceSelected("web-vue3", selected: true);
    state.TrySetWebDistRoot(@"D:\web\dist");
    state.TrySetOutputPath(@"D:\output\bundle.zip");

    Assert.False(state.RequiresJavaSource);
    Assert.True(state.RequiresWebDist);
    Assert.True(state.CanPackage);
}

[Fact]
public void CanPackage_JavaOnlyRequiresJavaAndOutputButNotWeb()
{
    var state = CreateState();
    state.ClearServices();
    state.SetServiceSelected("gateway", selected: true);
    state.TrySetJavaSourceRoot(@"D:\java");
    state.TrySetOutputPath(@"D:\output\bundle.zip");

    Assert.True(state.RequiresJavaSource);
    Assert.False(state.RequiresWebDist);
    Assert.True(state.CanPackage);
}

[Fact]
public void CanPackage_MixedSelectionRequiresBothSources()
{
    var state = CreateState();
    state.ClearServices();
    state.SetServiceSelected("gateway", selected: true);
    state.SetServiceSelected("web-vue3", selected: true);
    state.TrySetJavaSourceRoot(@"D:\java");
    state.TrySetOutputPath(@"D:\output\bundle.zip");

    Assert.False(state.CanPackage);
    state.TrySetWebDistRoot(@"D:\web\dist");
    Assert.True(state.CanPackage);
}
```

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj --configuration Release --filter PackagingFormStateTests
```

Expected: compilation fails because `RequiresJavaSource` and `RequiresWebDist` do not exist, and the existing readiness behavior still requires both paths.

- [ ] **Step 3: Implement computed requirements and conditional readiness**

Add:

```csharp
public bool RequiresJavaSource =>
    SelectedServices.Any(service =>
        !string.Equals(service, "web-vue3", StringComparison.OrdinalIgnoreCase));

public bool RequiresWebDist =>
    _selectedServices.Contains("web-vue3");

public bool CanPackage =>
    !IsBusy &&
    _selectedServices.Count > 0 &&
    !string.IsNullOrWhiteSpace(OutputPath) &&
    (!RequiresJavaSource || !string.IsNullOrWhiteSpace(JavaSourceRoot)) &&
    (!RequiresWebDist || !string.IsNullOrWhiteSpace(WebDistRoot));
```

Do not clear either path in `SetServiceSelected`, `SelectAllServices`, or `ClearServices`.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the command from Step 2. Expected: all `PackagingFormStateTests` pass.

- [ ] **Step 5: Commit**

```powershell
git add tools/aifar-bundle-packager/src/AifarBundlePackager.Core/PackagingFormState.cs tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/PackagingFormStateTests.cs
git commit -m "feat(packager): derive required paths from services"
```

### Task 2: Make Core validation ignore unused paths

**Files:**
- Modify: `tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/BundlePackagerTests.cs`
- Modify: `tools/aifar-bundle-packager/src/AifarBundlePackager.Core/BundlePackager.cs`

**Interfaces:**
- Consumes: `ServiceCatalog.Select(IReadOnlyCollection<string>)`
- Updates: internal `BundlePackager.Validate(BundleRequest)` behavior
- Preserves: `BundlePackager.Package(BundleRequest, IProgress<BundleProgress>?)`

- [ ] **Step 1: Write failing Web-only and Java-only packaging tests**

Add real archive assertions:

```csharp
[Fact]
public void Package_WebOnlyIgnoresJavaPath()
{
    using var workspace = new TestWorkspace();
    var webRoot = workspace.CreateDirectory("web");
    workspace.CreateFile("web/index.html", "<html>web</html>");
    var output = workspace.Combine("output", "web-only.zip");

    var result = BundlePackager.Package(new BundleRequest(
        workspace.Combine("missing-java"), webRoot, output, ["web-vue3"]));

    Assert.Equal(["web-vue3"], result.Services);
    using var archive = ZipFile.OpenRead(output);
    Assert.NotNull(archive.GetEntry("artifacts/web-vue3/web-vue3.zip"));
}

[Fact]
public void Package_JavaOnlyIgnoresWebPath()
{
    using var workspace = new TestWorkspace();
    var javaRoot = workspace.CreateDirectory("java");
    workspace.CreateBytes("java/alpha-gateway/target/alpha-gateway-1.0.jar", [1, 2, 3]);
    var output = workspace.Combine("output", "java-only.zip");

    var result = BundlePackager.Package(new BundleRequest(
        javaRoot, workspace.Combine("missing-web"), output, ["gateway"]));

    Assert.Equal(["gateway"], result.Services);
    using var archive = ZipFile.OpenRead(output);
    Assert.NotNull(archive.GetEntry("artifacts/gateway/alpha-gateway.jar"));
}
```

Update the required-path test so mixed selection still rejects each missing required source, while Java-only and Web-only no longer do.

- [ ] **Step 2: Run focused tests and verify RED**

Run:

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/AifarBundlePackager.Tests.csproj --configuration Release --filter BundlePackagerTests
```

Expected: Web-only fails on the invalid Java path and Java-only fails on the invalid Web path.

- [ ] **Step 3: Implement conditional path normalization and validation**

In `Validate`, select services first and derive:

```csharp
var services = ServiceCatalog.Select(request.Services);
var requiresJavaSource = services.Any(item => !item.IsWeb);
var requiresWebDist = services.Any(item => item.IsWeb);
```

Always validate and normalize `OutputPath`. Only call `RequireSelectedPath`, `Path.GetFullPath`, and `Directory.Exists` for a source category when its requirement flag is true. Store `string.Empty` for an unused source path. Keep `index.html` and output-overlap validation inside the Web-required branch.

- [ ] **Step 4: Run Core tests and verify GREEN**

Run the command from Step 2, then run:

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/AifarBundlePackager.sln --configuration Release
```

Expected: all packager tests pass.

- [ ] **Step 5: Commit**

```powershell
git add tools/aifar-bundle-packager/src/AifarBundlePackager.Core/BundlePackager.cs tools/aifar-bundle-packager/tests/AifarBundlePackager.Tests/BundlePackagerTests.cs
git commit -m "feat(packager): allow independent source categories"
```

### Task 3: Render conditional requirements and republish

**Files:**
- Modify: `tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/MainForm.cs`
- Modify: `docs/superpowers/plans/2026-07-23-aifar-bundle-packager-winforms.md`
- Rebuild: `deploy/bin/AIFARBundlePackager.exe`

**Interfaces:**
- Consumes: `PackagingFormState.RequiresJavaSource`, `RequiresWebDist`, and `CanPackage`
- Produces: requirement text that lists only required missing paths and identifies unused categories

- [ ] **Step 1: Update requirement-message logic**

In `RefreshState`, add missing paths conditionally:

```csharp
if (_state.RequiresJavaSource && string.IsNullOrWhiteSpace(_state.JavaSourceRoot))
{
    missing.Add("Java 源码根目录");
}
if (_state.RequiresWebDist && string.IsNullOrWhiteSpace(_state.WebDistRoot))
{
    missing.Add("Web dist 目录");
}
```

When no values are missing, show one of:

```csharp
_state.RequiresJavaSource && !_state.RequiresWebDist
    ? "本次仅打包 Java 服务，Web dist 路径不使用。"
    : !_state.RequiresJavaSource && _state.RequiresWebDist
        ? "本次仅打包 web-vue3，Java 源码路径不使用。"
        : "路径和服务已选择，可以开始打包。";
```

Keep both source buttons enabled whenever the form is idle.

- [ ] **Step 2: Update the original implementation plan compatibility text**

Replace statements that all three paths are always required with the approved category-driven rules, while retaining the requirement that all paths start empty and are manually selected when needed.

- [ ] **Step 3: Run final automated gates**

```powershell
D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/AifarBundlePackager.sln --configuration Release
pnpm test:scripts
git diff --check
```

Expected: all .NET and script tests pass, with no whitespace errors.

- [ ] **Step 4: Publish the final EXE**

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/build-aifar-bundle-packager.ps1
```

Expected: `deploy/bin/AIFARBundlePackager.exe` is the only matching delivery file and has non-zero size.

- [ ] **Step 5: Perform GUI and archive smoke checks**

Launch the EXE and confirm the main window opens. Verify Web-only readiness without a Java path and Java-only readiness without a Web path through the state tests and generated archive tests. Confirm no default or persistence references exist with:

```powershell
rg -n 'D:\\workspace\\alpha|LocalAppData|settings\.json|GetEnvironmentVariable|Environment\.GetEnvironment' tools/aifar-bundle-packager
```

Expected: no matches.

- [ ] **Step 6: Commit**

```powershell
git add tools/aifar-bundle-packager/src/AifarBundlePackager.WinForms/MainForm.cs docs/superpowers/plans/2026-07-23-aifar-bundle-packager-winforms.md
git commit -m "fix(packager): show conditional source requirements"
```
