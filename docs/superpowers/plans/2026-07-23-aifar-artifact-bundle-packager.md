# AIFAR Artifact Bundle Packager Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a directly runnable Windows CMD that packages all or selected AIFAR Runtime service artifacts with a complete validated manifest.

**Architecture:** A small CMD wrapper owns editable path configuration and accepts one optional comma-separated service argument. A PowerShell helper performs strict service selection, exact runnable-JAR discovery, web-dist archiving, hashing, manifest generation, staging cleanup and final ZIP replacement. Node script tests invoke the helper with isolated fixtures and inspect the produced archives through PowerShell.

**Tech Stack:** Windows CMD, Windows PowerShell 5.1-compatible PowerShell, Node.js built-in test runner, .NET `System.IO.Compression`.

## Global Constraints

- CMD config defaults are `D:\workspace\alpha\backend\alpha-java-cloud`, `D:\workspace\alpha\fronted\alpha-web-vue3\dist` and `%CD%\aifar-batch-update.zip`.
- Invocation accepts no argument or `all` for all ten services, or one comma-separated service list.
- Output paths never contain Maven `target` or an outer web `dist` directory.
- The inner `web-vue3.zip` opens directly at `index.html`.
- Manifest schema is `aifar-artifact-bundle-v1` and includes real SHA256 and byte size values.
- Source trees are read-only; only the configured output ZIP and temporary adjacent staging directory are written.

---

### Task 1: Script contract tests and packager implementation

**Files:**
- Create: `scripts/package-aifar-artifact-bundle.cmd`
- Create: `scripts/package-aifar-artifact-bundle.ps1`
- Create: `scripts/package-aifar-artifact-bundle.test.mjs`

**Interfaces:**
- Consumes: CMD first argument as `all` or comma-separated service names.
- Produces: `aifar-batch-update.zip` containing root `manifest.json` and `artifacts/<service>/<file>` entries.
- PowerShell internal interface: `-JavaSourceRoot <path> -WebDistRoot <path> -OutputPath <zip> -Services <selection>`.

- [ ] **Step 1: Write the failing CMD contract and partial-package test**

Create `scripts/package-aifar-artifact-bundle.test.mjs` with fixture helpers that:

```js
const serviceFixtures = {
  gateway: ['alpha-gateway', ['alpha-gateway', 'target']],
  im: ['alpha-im', ['alpha-im', 'alpha-im-core', 'target']],
  meeting: ['alpha-meeting', ['alpha-meeting', 'alpha-meeting-core', 'target']]
}

// Create versioned runtime jars plus web-dist/index.html, invoke the PS1 with
// Services=gateway,im,meeting,web-vue3, expand both ZIPs, and assert:
// - manifest service order is im, meeting, gateway, web-vue3;
// - artifacts have normalized names and no target directory;
// - every manifest SHA256/size matches the packaged file;
// - inner web ZIP has index.html at its root and no dist/index.html.
```

Also read the CMD as text and assert it contains the three exact editable configuration assignments and forwards the raw `%*` value as the service selection. Using `%*` is required because CMD positional expansion treats commas as argument separators and `%~1` would keep only the first selected service.

- [ ] **Step 2: Run the new test and verify RED**

Run:

```powershell
node --test scripts/package-aifar-artifact-bundle.test.mjs
```

Expected: FAIL because `package-aifar-artifact-bundle.cmd` and `.ps1` do not exist.

- [ ] **Step 3: Implement the minimal CMD wrapper**

Create the CMD with this contract:

```cmd
@echo off
setlocal
set "JAVA_SOURCE_ROOT=D:\workspace\alpha\backend\alpha-java-cloud"
set "WEB_DIST_ROOT=D:\workspace\alpha\fronted\alpha-web-vue3\dist"
set "OUTPUT_PATH=%CD%\aifar-batch-update.zip"
set "SERVICES=%*"
if not defined SERVICES set "SERVICES=all"
set "SCRIPT_DIR=%~dp0"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%SCRIPT_DIR%package-aifar-artifact-bundle.ps1" -JavaSourceRoot "%JAVA_SOURCE_ROOT%" -WebDistRoot "%WEB_DIST_ROOT%" -OutputPath "%OUTPUT_PATH%" -Services "%SERVICES%"
exit /b %ERRORLEVEL%
```

- [ ] **Step 4: Implement the PowerShell packager**

Create a PowerShell 5.1-compatible script that defines an ordered service table with `Name`, `Module` and exact relative target directory; parses `all` or the comma list; validates only selected sources; excludes `*-sources.jar`, `*-javadoc.jar`, `*-tests.jar`, `*-test.jar`, `*-plain.jar` and `original-*.jar`; requires exactly one `${module}-*.jar`; copies it as `${module}.jar`; and builds the inner web ZIP from `WebDistRoot\*`.

For every staged artifact calculate:

```powershell
$hash = (Get-FileHash -LiteralPath $artifactFile -Algorithm SHA256).Hash.ToLowerInvariant()
$size = (Get-Item -LiteralPath $artifactFile).Length
```

Serialize a depth-safe manifest using `ConvertTo-Json -Depth 6`, write UTF-8 without BOM, create the final ZIP with .NET compression, validate the staged ZIP exists and is nonempty, then replace only `OutputPath`. Wrap staging cleanup in `finally`.

- [ ] **Step 5: Run the partial-package test and verify GREEN**

Run:

```powershell
node --test scripts/package-aifar-artifact-bundle.test.mjs
```

Expected: PASS for CMD config, partial selection, manifest integrity and inner web structure.

- [ ] **Step 6: Add selection and failure tests**

Extend the same test file with cases for:

```text
all / no explicit selection -> ten manifest entries
gateway,gateway -> one gateway entry
unknown -> nonzero exit and no output
all,gateway -> nonzero exit and no output
selected service missing jar -> nonzero exit naming the service and no output
selected service with two runnable jars -> nonzero exit naming ambiguity and no output
```

- [ ] **Step 7: Run focused tests and script suite**

Run:

```powershell
node --test scripts/package-aifar-artifact-bundle.test.mjs
pnpm test:scripts
```

Expected: all focused tests and all repository script tests pass without warnings.

- [ ] **Step 8: Commit the tested implementation**

```powershell
git add scripts/package-aifar-artifact-bundle.cmd scripts/package-aifar-artifact-bundle.ps1 scripts/package-aifar-artifact-bundle.test.mjs
git commit -m "feat: package AIFAR artifact update bundles"
```

### Task 2: Real-source acceptance verification

**Files:**
- Verify: `scripts/package-aifar-artifact-bundle.cmd`
- Verify: generated `aifar-batch-update.zip` in a disposable workspace output directory

**Interfaces:**
- Consumes: the configured Alpha Java source tree and Alpha web dist tree.
- Produces: a real full package and a selected-service package accepted by the backend manifest contract.

- [ ] **Step 1: Run a real selected-service package**

Run the CMD from a disposable current directory so its configured `%CD%` output does not overwrite repository artifacts:

```powershell
cmd.exe /d /c D:\workspace\aifar-deployment\scripts\package-aifar-artifact-bundle.cmd gateway,im,meeting,web-vue3
```

Expected: exit code 0 and a ZIP containing exactly four manifest services in canonical order.

- [ ] **Step 2: Inspect the real selected package**

Expand it to a temporary directory and verify manifest SHA256/size against all four packaged files; expand `web-vue3.zip` and verify root `index.html`.

- [ ] **Step 3: Run a real full package**

Run:

```powershell
cmd.exe /d /c D:\workspace\aifar-deployment\scripts\package-aifar-artifact-bundle.cmd
```

Expected: exit code 0, ten manifest services and ten matching artifacts.

- [ ] **Step 4: Run final repository verification**

Run:

```powershell
pnpm test:scripts
git -c safe.directory=D:/workspace/aifar-deployment diff --check
```

Expected: all tests pass and diff check prints no errors.
