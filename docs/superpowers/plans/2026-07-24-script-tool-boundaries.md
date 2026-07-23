# Script and Tool Boundaries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move capability-specific file preparation and packaging utilities out of `scripts/` into `tools/` without changing their artifact contracts or leaving tests undiscovered.

**Architecture:** Keep `scripts/` as the AIFAR Deployment lifecycle and release boundary. Co-locate each auxiliary utility, its entry points, tests, and current README under `tools/`; use one recursive `tools/test-tools.mjs` runner to expose a portable `pnpm test:tools` gate.

**Tech Stack:** Node.js 22 test runner, PowerShell, CMD, .NET 8 WinForms/xUnit, Vue 3/TypeScript/Vitest, GitHub Actions.

## Global Constraints

- Do not change the `aifar-artifact-bundle-v1` manifest, hashing, service ordering, staging cleanup, or failure-preserves-old-output contracts.
- Keep Alpha JAR export's explicit `TargetRoot` behavior and its default `resources/aifar/runtime-v2/services` destination.
- Keep `scripts/test-scripts.mjs` limited to top-level `scripts/*.test.mjs`; auxiliary tool tests must run through `pnpm test:tools`.
- Do not delete, rewrite, or move `scripts/build_openlab_inventory_docx.py` in this implementation.
- Preserve Chinese and English user-visible text together.
- Do not rewrite historical `docs/superpowers/plans/`, `docs/superpowers/specs/`, or earlier `memory.md` entries merely to replace historical paths.

---

### Task 1: Add the auxiliary tool test gate

**Files:**
- Create: `tools/test-tools.test.mjs`
- Create: `tools/test-tools.mjs`
- Modify: `package.json:15-17`
- Modify: `scripts/test-local.mjs:4-11`
- Modify: `.github/workflows/ci.yml:37-47,82-84`

**Interfaces:**
- Produces: `discoverToolTests(rootDirectory: string): string[]`, returning sorted absolute `*.test.mjs` paths below `tools/` while excluding `bin`, `obj`, and `node_modules`.
- Produces: `pnpm test:tools`, the canonical cross-platform auxiliary tool contract gate.

- [ ] **Step 1: Write the failing discovery test**

Create `tools/test-tools.test.mjs` with a temporary tree containing root and nested tests plus ignored `bin` and `obj` tests. Import `discoverToolTests` from `./test-tools.mjs` and assert that only the two supported test paths are returned in lexical order.

```js
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { discoverToolTests } from './test-tools.mjs'

test('discovers tool tests recursively and excludes build outputs', (t) => {
  const root = mkdtempSync(path.join(tmpdir(), 'aifar-tool-tests-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  for (const relative of ['root.test.mjs', 'nested/child.test.mjs', 'nested/bin/ignored.test.mjs', 'obj/ignored.test.mjs']) {
    const file = path.join(root, relative)
    mkdirSync(path.dirname(file), { recursive: true })
    writeFileSync(file, '')
  }

  assert.deepEqual(
    discoverToolTests(root).map((file) => path.relative(root, file).replaceAll('\\', '/')),
    ['nested/child.test.mjs', 'root.test.mjs']
  )
})
```

- [ ] **Step 2: Run the test and verify the missing runner failure**

Run: `node --test tools/test-tools.test.mjs`

Expected: FAIL with `ERR_MODULE_NOT_FOUND` for `tools/test-tools.mjs`.

- [ ] **Step 3: Implement the recursive runner**

Create `tools/test-tools.mjs` using `readdirSync(..., { withFileTypes: true })`, an ignored directory set of `bin`, `obj`, and `node_modules`, and `spawnSync(process.execPath, ['--test', ...tests])`. Export `discoverToolTests`; only execute the runner when `process.argv[1]` resolves to the module file; fail with `[tools test] no *.test.mjs files found` when discovery is empty.

- [ ] **Step 4: Register the gate in local and CI workflows**

Add this root package script immediately after `test:scripts`:

```json
"test:tools": "node tools/test-tools.mjs"
```

Add this `scripts/test-local.mjs` step immediately after script tests:

```js
['tool tests', process.execPath, ['tools/test-tools.mjs']],
```

Add `pnpm test:tools` after `pnpm test:scripts` in both CI jobs. Keep the Windows .NET test and publish steps after the tool contract gate.

- [ ] **Step 5: Run the new gate**

Run: `pnpm test:tools`

Expected: PASS with the discovery test; no `bin` or `obj` test is executed.

- [ ] **Step 6: Commit the test boundary**

```powershell
git add -- tools/test-tools.mjs tools/test-tools.test.mjs package.json scripts/test-local.mjs .github/workflows/ci.yml
git commit -m "test: add auxiliary tool gate"
```

### Task 2: Co-locate the Bundle Packager build and CLI tools

**Files:**
- Move: `scripts/build-aifar-bundle-packager.ps1` -> `tools/aifar-bundle-packager/build.ps1`
- Move: `scripts/aifar-bundle-packager-build.test.mjs` -> `tools/aifar-bundle-packager/build.test.mjs`
- Move: `scripts/package-aifar-artifact-bundle.cmd` -> `tools/aifar-bundle-packager/cli/package-aifar-artifact-bundle.cmd`
- Move: `scripts/package-aifar-artifact-bundle.ps1` -> `tools/aifar-bundle-packager/cli/package-aifar-artifact-bundle.ps1`
- Move: `scripts/package-aifar-artifact-bundle.test.mjs` -> `tools/aifar-bundle-packager/cli/package-aifar-artifact-bundle.test.mjs`
- Create: `tools/aifar-bundle-packager/README.md`
- Modify: `.github/workflows/ci.yml:44-47`
- Modify: `.gitignore:54-65`

**Interfaces:**
- Produces: `tools/aifar-bundle-packager/build.ps1 [-DotNetPath <path>]`, delivering only `deploy/bin/AIFARBundlePackager.exe`.
- Produces: `tools/aifar-bundle-packager/cli/package-aifar-artifact-bundle.cmd [all|service,...]`, retaining the PowerShell CLI bundle protocol.

- [ ] **Step 1: Change the build contract test to require tool-local paths**

In the moved `build.test.mjs`, define `toolDir` as its own directory and `repositoryRoot` as `path.resolve(toolDir, '..', '..')`. Resolve `build.ps1` directly below `toolDir`, and assert CI contains:

```js
assert.match(workflow, /\.\/tools\/aifar-bundle-packager\/build\.ps1/i)
```

- [ ] **Step 2: Run the tool gate and verify the new-path failure**

Run: `pnpm test:tools`

Expected: FAIL because `tools/aifar-bundle-packager/build.ps1` and the updated CI path do not exist yet.

- [ ] **Step 3: Move the build script and preserve repository-root resolution**

Move the script with `apply_patch` and change only its root calculation:

```powershell
$repositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
```

Keep solution/project paths, temporary path containment, test-before-publish, sidecar rejection, atomic EXE replacement, and `finally` cleanup unchanged.

- [ ] **Step 4: Move the CLI entry points and tests unchanged**

Move the CMD, PowerShell, and Node test together into `tools/aifar-bundle-packager/cli/`. Their sibling-path resolution remains valid because all three files stay together.

- [ ] **Step 5: Update CI and ignore rules**

Change the Windows publish command to:

```yaml
run: ./tools/aifar-bundle-packager/build.ps1 -DotNetPath (Get-Command dotnet).Source
```

Replace the twelve exact `/scripts/.aifar-artifact-bundle-...` ignore entries with one reusable rule:

```gitignore
.aifar-artifact-bundle-*
```

- [ ] **Step 6: Add the current Bundle Packager README**

Document the GUI solution, `build.ps1`, `cli/` alternative, output locations, `pnpm test:tools`, and the direct Windows build command. State that the GUI and CLI share the bundle protocol but do not call each other at runtime.

- [ ] **Step 7: Run Bundle Packager contract tests**

Run: `pnpm test:tools`

Expected: PASS for build, CLI bundle, and test-runner contracts.

Run: `D:\tools\dotnet\dotnet.exe test tools/aifar-bundle-packager/AifarBundlePackager.sln --configuration Release`

Expected: all xUnit tests PASS.

- [ ] **Step 8: Commit the Bundle Packager relocation**

```powershell
git add -- .github/workflows/ci.yml .gitignore scripts tools/aifar-bundle-packager
git commit -m "refactor: co-locate bundle packaging tools"
```

### Task 3: Move Alpha JAR export and correct the upload hint

**Files:**
- Move: `scripts/export-alpha-jars.cmd` -> `tools/alpha-jar-export/export-alpha-jars.cmd`
- Move: `scripts/export-alpha-jars.ps1` -> `tools/alpha-jar-export/export-alpha-jars.ps1`
- Move: `scripts/export-alpha-jars.test.mjs` -> `tools/alpha-jar-export/export-alpha-jars.test.mjs`
- Create: `tools/alpha-jar-export/README.md`
- Modify: `web/src/i18n/messages.ts:164,1049`
- Modify: `web/src/containers/runtime/runtimeRules.test.ts:1-75`

**Interfaces:**
- Produces: `tools/alpha-jar-export/export-alpha-jars.ps1`, accepting the existing `SourceRoot`, `TargetRoot`, `Services`, `RequireAll`, and `Clean` parameters.
- Preserves: default output `resources/aifar/runtime-v2/services/<service>/target/<module>.jar`.

- [ ] **Step 1: Move and update the export test for the tool directory depth**

Rename `repositoryScriptsDir` to `toolDir`. In both fixtures, copy the script into `tools/alpha-jar-export/`; in the default-target test, retain the assertion below the fixture repository root:

```js
const fixtureToolDir = path.join(root, 'tools', 'alpha-jar-export')
const script = path.join(fixtureToolDir, 'export-alpha-jars.ps1')
```

- [ ] **Step 2: Run the export test and verify the default-path failure**

Run: `node --test tools/alpha-jar-export/export-alpha-jars.test.mjs`

Expected: FAIL because the moved PowerShell script still resolves the parent of `tools/alpha-jar-export` rather than the repository root.

- [ ] **Step 3: Fix only the moved script's repository-root calculation**

Replace the default target calculation with:

```powershell
$scriptRoot = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
$repositoryRoot = [System.IO.Path]::GetFullPath((Join-Path $scriptRoot '..\..'))
$TargetRoot = Join-Path $repositoryRoot 'resources\aifar\runtime-v2\services'
```

- [ ] **Step 4: Add the export README**

Document that the tool copies runnable Alpha JARs into Runtime resource targets and does not create an upload ZIP. Include explicit examples for default output and `-TargetRoot` override, plus `-RequireAll` and `-Clean` behavior.

- [ ] **Step 5: Add a failing user-visible hint assertion**

Import `messages` from `../../i18n/messages` in `runtimeRules.test.ts` and add:

```ts
it('directs bundle uploads to the Bundle Packager output', () => {
  expect(messages.zh['apps.aifarUpdateBundleHint']).toContain('AIFARBundlePackager.exe')
  expect(messages.en['apps.aifarUpdateBundleHint']).toContain('AIFARBundlePackager.exe')
  expect(String(messages.zh['apps.aifarUpdateBundleHint'])).not.toContain('export-alpha-jars')
  expect(String(messages.en['apps.aifarUpdateBundleHint'])).not.toContain('export-alpha-jars')
})
```

Run: `pnpm --dir web test -- runtimeRules.test.ts`

Expected: FAIL because both messages still reference `scripts/export-alpha-jars.ps1`.

- [ ] **Step 6: Correct both localized messages**

Use these meanings without changing the stable key:

```text
zh: 请使用 AIFARBundlePackager.exe 生成更新包并上传，平台会按 manifest 自动更新对应服务。
en: Use AIFARBundlePackager.exe to create and upload the update bundle; the platform updates matching services from the manifest.
```

- [ ] **Step 7: Run export and web tests**

Run: `pnpm test:tools`

Expected: all auxiliary tool tests PASS.

Run: `pnpm --dir web test -- runtimeRules.test.ts`

Expected: `runtimeRules.test.ts` PASS.

- [ ] **Step 8: Commit the export relocation and hint correction**

```powershell
git add -- scripts tools/alpha-jar-export web/src/i18n/messages.ts web/src/containers/runtime/runtimeRules.test.ts
git commit -m "refactor: move Alpha JAR export into tools"
```

### Task 4: Verify active references and record the repository conclusion

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: `pnpm test:scripts`, `pnpm test:tools`, the web test suite, and the Bundle Packager build.
- Produces: a reusable `memory.md` entry describing the final `scripts/` versus `tools/` boundary and the unresolved OpenLab file risk.

- [ ] **Step 1: Prove no active old path remains**

Run:

```powershell
rg -n "scripts[/\\](build-aifar-bundle-packager|aifar-bundle-packager-build|package-aifar-artifact-bundle|export-alpha-jars)" package.json README.md .github scripts tools web backend extras config
```

Expected: no matches. Historical docs and earlier memory entries are intentionally excluded.

- [ ] **Step 2: Run the project script and tool gates**

Run: `pnpm test:scripts`

Expected: PASS with no Bundle Packager, CLI bundle, or Alpha export tests in the script test list.

Run: `pnpm test:tools`

Expected: PASS with the moved tests discovered recursively.

- [ ] **Step 3: Run the frontend suite**

Run: `pnpm test:web`

Expected: all Vitest tests PASS, including the localized Bundle Packager hint assertion.

- [ ] **Step 4: Build the Windows Bundle Packager**

Run:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File tools/aifar-bundle-packager/build.ps1 -DotNetPath D:\tools\dotnet\dotnet.exe
```

Expected: .NET tests PASS and exactly `deploy/bin/AIFARBundlePackager.exe` is delivered; temporary publish paths are removed.

- [ ] **Step 5: Check whitespace and repository state**

Run: `git diff --check`

Expected: no output and exit code 0.

Run: `git status --short`

Expected: only the intended moves, README, gate, CI, i18n test, and `memory.md` changes remain.

- [ ] **Step 6: Append the reusable project memory entry**

Under `## 2026-07-24`, append one issue/conclusion pair stating that auxiliary packaging/export tools now live below `tools/`, `scripts/` remains the AIFAR lifecycle/release boundary, `pnpm test:tools` owns tool tests, and the unreferenced sensitive OpenLab DOCX generator remains an explicitly unresolved cleanup item.

- [ ] **Step 7: Commit the closeout record**

```powershell
git add -- memory.md
git commit -m "docs: record script and tool cleanup"
```
