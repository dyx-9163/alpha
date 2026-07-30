# AIFAR Runtime Rollback Target Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent AIFAR Runtime from rolling back to an already-active release or to a rollback audit record while preserving explicit “roll back to selected release” behavior.

**Architecture:** The AIFAR module owns release eligibility through a read-only registry inspector so HTTP handlers do not duplicate revision rules. Eligibility is calculated per service, exposed in the release-list API, consumed by pure frontend rules, and revalidated inside the orchestration lock before any remote upload or Pod change.

**Tech Stack:** Go 1.24, Chi, SQLite store models, Vue 3, TypeScript, Element Plus, Vitest, Vue Test Utils.

## Global Constraints

- Keep `/api/v2` and the existing worker/task/audit rollback flow.
- Keep “roll back to selected release” semantics; do not infer the previous release.
- Do not add a database migration, alter existing release rows, or change `rollback-artifact.sh`.
- All new user-visible copy must have Chinese and English entries.
- Backend validation is authoritative; frontend disabled states are an additional guard.
- Preserve unrelated dirty-worktree changes, especially MySQL, task-page, and existing i18n changes.
- The per-task commit commands are allowed only in a clean isolated worktree. In the current dirty checkout, replace each commit step with a scoped `git diff -- <task-files>` checkpoint; never stage an entire shared dirty file such as `registry/contract.go` or `web/src/i18n/messages.ts`.

## File Map

- `backend/internal/apps/registry/contract.go`: optional rollback inspection contract.
- `backend/internal/apps/aifar/{module.go,rollback.go}`: domain inspection and double validation.
- `backend/internal/apps/aifar/service_test.go`: domain and lock-time regressions.
- `backend/internal/httpapi/apps_release_handlers.go`: list response orchestration.
- `backend/internal/httpapi/{apps_handlers_test.go,apps_release_delete_test.go}`: response and endpoint tests.
- `web/src/containers/runtime/releaseRules.ts`: pure UI eligibility rules.
- `web/src/containers/runtime/{types.ts,context.ts,AifarRuntimeReleasesTab.vue}`: contract and current marker.
- `web/src/views/ContainersView.vue`: submit exact rollback service scope.
- `web/src/i18n/messages.ts`: Chinese and English copy.

---

### Task 1: Add the rollback eligibility domain contract

**Files:**
- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/apps/aifar/module.go`
- Modify: `backend/internal/apps/aifar/rollback.go`
- Test: `backend/internal/apps/aifar/service_test.go`

**Interfaces:**
- Consumes: `store.AppInstance`, `store.AppRelease`, manifest maps, `currentRevisionForService`, and `rollbackArtifactFromManifest`.
- Produces:

```go
type ArtifactRollbackInspectionRequest struct {
    Instance store.AppInstance
    Release  store.AppRelease
    Manifest map[string]any
}

type ArtifactRollbackInspection struct {
    CurrentServices           []string
    RollbackServices          []string
    RollbackUnavailableReason string
}

type ArtifactRollbackInspectionModule interface {
    InspectArtifactRollback(context.Context, ArtifactRollbackInspectionRequest) ArtifactRollbackInspection
}
```

- [ ] **Step 1: Write failing domain tests**

Add `TestInspectArtifactRollbackEligibilityByServiceRevision` to `service_test.go`. Seed a successful bundle release where `oauth` is already on `release-target` and `gateway` is on `release-current`; both artifacts must contain `file`, 64-character `sha256`, and `remotePath`. Assert:

```go
CurrentServices == []string{"oauth"}
RollbackServices == []string{"gateway"}
RollbackUnavailableReason == ""
```

Add table cases:

```text
kind=rollback                    -> ROLLBACK_RECORD, no rollback services
all changed services current     -> ALREADY_ACTIVE
missing sha256 or remotePath     -> ARTIFACT_UNAVAILABLE
release status failed            -> ARTIFACT_UNAVAILABLE
```

- [ ] **Step 2: Verify RED**

Run from `backend/`:

```powershell
$env:GOCACHE = Join-Path (git rev-parse --show-toplevel) '.codex-cache/go-build'; go test ./internal/apps/aifar -run 'TestInspectArtifactRollback' -count=1
```

Expected: compilation fails because the inspection contract and method do not exist.

- [ ] **Step 3: Implement the minimal contract and inspector**

Add the types above to `registry/contract.go`. Delegate in `aifar/module.go`:

```go
func (m Module) InspectArtifactRollback(ctx context.Context, req registry.ArtifactRollbackInspectionRequest) registry.ArtifactRollbackInspection {
    if ctx.Err() != nil {
        return registry.ArtifactRollbackInspection{RollbackUnavailableReason: "ARTIFACT_UNAVAILABLE"}
    }
    return inspectArtifactRollback(req.Instance, req.Release, req.Manifest)
}
```

Implement `inspectArtifactRollback` in `rollback.go`. Decision order must be failed/incomplete release, `kind=rollback`, then per-service artifact and revision comparison. Preserve manifest service order in both output slices. If every complete service is current, return `ALREADY_ACTIVE`.

- [ ] **Step 4: Verify GREEN**

Run the focused command again. Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```powershell
git add -- backend/internal/apps/registry/contract.go backend/internal/apps/aifar/module.go backend/internal/apps/aifar/rollback.go backend/internal/apps/aifar/service_test.go
git commit -m "fix: inspect runtime rollback targets"
```

---

### Task 2: Reject invalid targets before and after locking

**Files:**
- Modify: `backend/internal/apps/aifar/rollback.go`
- Test: `backend/internal/apps/aifar/service_test.go`

**Interfaces:**
- Consumes: `inspectArtifactRollback(...)` from Task 1.
- Produces:

```go
func validateArtifactRollbackSelection(instance store.AppInstance, release store.AppRelease, manifest map[string]any, requested []string) error
```

- [ ] **Step 1: Write failing validation tests**

Add `TestValidateArtifactRollbackRejectsAlreadyActiveRelease` and `TestValidateArtifactRollbackRejectsRollbackAuditRecord`. Assert non-nil errors containing `already active` and `audit record` respectively.

Add `TestRollbackArtifactRevalidatesAfterLock`. Pass a stale request instance on another revision, while the fake store returns the latest locked instance already on the target. Assert:

```go
err != nil
remote.joinedUploads() == ""
!strings.Contains(remote.joinedCommands(), "rollback-oauth.sh")
no successful kind=rollback release exists
```

- [ ] **Step 2: Verify RED**

```powershell
$env:GOCACHE = Join-Path (git rev-parse --show-toplevel) '.codex-cache/go-build'; go test ./internal/apps/aifar -run 'TestValidateArtifactRollbackRejects|TestRollbackArtifactRevalidates' -count=1
```

Expected: current code accepts the targets or uploads a rollback script.

- [ ] **Step 3: Implement shared validation**

The helper must enforce:

```text
kind=rollback                         -> rollback audit records cannot be rollback targets
requested service absent/incomplete  -> existing not-rollback-capable error
requested service current=target     -> target release is already active for service <name>
other requested services             -> allowed
```

Call it in `ValidateArtifactRollback` after `findReleaseManifest`. Call it again inside `RollbackArtifact` step 1 after `req.Instance = lockedInstance` and before pending release creation, work-directory creation, or upload. Do not silently remove services from a direct API request.

- [ ] **Step 4: Verify GREEN and preserve valid rollback**

```powershell
go test ./internal/apps/aifar -run 'TestValidateArtifactRollbackRejects|TestRollbackArtifactRevalidates|TestServiceRollsBackAIFARServiceToReleaseArtifact' -count=1
go test ./internal/apps/aifar -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

```powershell
git add -- backend/internal/apps/aifar/rollback.go backend/internal/apps/aifar/service_test.go
git commit -m "fix: reject no-op runtime rollbacks"
```

---

### Task 3: Expose safe targets in the release-list API

**Files:**
- Modify: `backend/internal/httpapi/apps_release_handlers.go`
- Modify: `backend/internal/httpapi/apps_handlers_test.go`
- Modify: `backend/internal/httpapi/apps_release_delete_test.go`

**Interfaces:**
- Consumes: `registry.ArtifactRollbackInspectionModule`.
- Produces JSON fields `currentServices`, `rollbackServices`, `rollbackUnavailableReason`, and corrected `rollbackAvailable`.

- [ ] **Step 1: Write failing response tests**

Change `aifarReleaseResponseItem` test calls to pass an inspection. Assert exact output:

```go
currentServices == []string{"oauth"}
rollbackServices == []string{"gateway"}
rollbackAvailable == true
```

Add authenticated `TestListAIFARReleasesReportsRollbackEligibility`: save an AIFAR instance with per-service revisions plus current and old release manifests, GET the releases endpoint, and assert the current row is `ALREADY_ACTIVE`/disabled while the old row returns `rollbackServices=["oauth"]`.

- [ ] **Step 2: Verify RED**

```powershell
$env:GOCACHE = Join-Path (git rev-parse --show-toplevel) '.codex-cache/go-build'; go test ./internal/httpapi -run 'TestAIFARReleaseResponse|TestListAIFARReleases' -count=1
```

Expected: helper signature or assertions fail because inspection fields are absent.

- [ ] **Step 3: Implement handler orchestration**

Resolve `registry.ArtifactRollbackInspectionModule` once in `listAIFARReleases`. For each release call:

```go
inspection := inspector.InspectArtifactRollback(r.Context(), registry.ArtifactRollbackInspectionRequest{
    Instance: instance, Release: release, Manifest: manifest,
})
```

Pass it into `aifarReleaseResponseItem` and map the three new fields. Set `rollbackAvailable` only when status is success, reason is empty, and `len(RollbackServices) > 0`. If the module lacks the inspector, return empty service arrays plus `ARTIFACT_UNAVAILABLE`; never retain the unsafe old boolean fallback.

- [ ] **Step 4: Verify GREEN**

```powershell
go test ./internal/httpapi -run 'TestAIFARReleaseResponse|TestListAIFARReleases' -count=1
go test ./internal/apps/aifar ./internal/httpapi -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```powershell
git add -- backend/internal/httpapi/apps_release_handlers.go backend/internal/httpapi/apps_handlers_test.go backend/internal/httpapi/apps_release_delete_test.go
git commit -m "fix: expose safe runtime rollback targets"
```

---

### Task 4: Apply frontend target rules and explicit copy

**Files:**
- Create: `web/src/containers/runtime/releaseRules.ts`
- Create: `web/src/containers/runtime/releaseRules.test.ts`
- Modify: `web/src/containers/runtime/types.ts`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Produces:

```ts
export type RollbackUnavailableReason = '' | 'ROLLBACK_RECORD' | 'ALREADY_ACTIVE' | 'ARTIFACT_UNAVAILABLE'
export function runtimeReleaseRollbackDisabledReason(row: AifarRelease, options: { canManage: boolean; deniedText: string; t: RuntimeTranslate }): string
export function runtimeReleaseRollbackServices(row: AifarRelease): string[]
```

- [ ] **Step 1: Write failing pure-rule tests**

Create `releaseRules.test.ts`. Verify reason mappings:

```text
ROLLBACK_RECORD      -> containers.releaseRollbackAuditRecord
ALREADY_ACTIVE       -> containers.releaseRollbackAlreadyActive
ARTIFACT_UNAVAILABLE -> containers.releaseRollbackUnavailable
```

Also verify permission denial, missing release ID, an available release returning an empty disabled reason, and `runtimeReleaseRollbackServices` returning only a deduplicated defensive copy of `rollbackServices`, never `changedServices`.

- [ ] **Step 2: Verify RED**

From `web/`:

```powershell
node node_modules/vitest/vitest.mjs run src/containers/runtime/releaseRules.test.ts
```

Expected: module/type exports do not exist.

- [ ] **Step 3: Implement types, rules, view delegation, and copy**

Extend `AifarRelease` with `currentServices`, `rollbackServices`, and `rollbackUnavailableReason`. Replace the local `ContainersView.vue` reason logic with the pure helper and submit:

```ts
services: runtimeReleaseRollbackServices(row)
```

Add exact translations:

```text
zh: 回滚到此版本；所选服务已处于此版本；回滚记录仅用于审计，请选择实际制品发布
en: Roll back to this release; The selected services are already running this release.; Rollback records are audit-only; select an artifact release.
```

Preserve the warning that business data is not automatically rolled back.

- [ ] **Step 4: Verify GREEN**

Run the focused Vitest command again. Expected: PASS.

- [ ] **Step 5: Commit Task 4**

```powershell
git add -- web/src/containers/runtime/releaseRules.ts web/src/containers/runtime/releaseRules.test.ts web/src/containers/runtime/types.ts web/src/views/ContainersView.vue web/src/i18n/messages.ts
git commit -m "fix: clarify runtime rollback targets"
```

---

### Task 5: Render current-service state

**Files:**
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/containers/runtime/AifarRuntimeReleasesTab.vue`
- Create: `web/src/containers/runtime/AifarRuntimeReleasesTab.test.ts`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Produces context helpers `releaseCurrentServicesText(row: AifarRelease): string` and `releaseIsCurrent(row: AifarRelease): boolean`.

- [ ] **Step 1: Write a failing component test**

Mount `AifarRuntimeReleasesTab` with a provided runtime context. One row has `currentServices=['oauth']`; a second has none. Stub Element Plus table components using existing Vue Test Utils patterns. Assert the first row has `data-testid="release-current-services"`, text `Current`, title/tooltip content containing `oauth`, and button text `Roll back to this release`; assert the second row has no current tag.

- [ ] **Step 2: Verify RED**

```powershell
node node_modules/vitest/vitest.mjs run src/containers/runtime/AifarRuntimeReleasesTab.test.ts
```

Expected: the current marker does not exist.

- [ ] **Step 3: Implement the marker**

Change the release-ID column to a slot containing the ID and an Element Plus small info tag when `releaseIsCurrent(row)` is true. Wrap it in the existing tooltip and expose current service names through `releaseCurrentServicesText`. Add `containers.releaseCurrent` and `containers.releaseCurrentServices` in Chinese and English. Use existing spacing/tokens only.

- [ ] **Step 4: Verify GREEN**

```powershell
node node_modules/vitest/vitest.mjs run src/containers/runtime/AifarRuntimeReleasesTab.test.ts src/containers/runtime/releaseRules.test.ts src/containers/runtime/runtimeRules.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit Task 5**

```powershell
git add -- web/src/containers/runtime/context.ts web/src/views/ContainersView.vue web/src/containers/runtime/AifarRuntimeReleasesTab.vue web/src/containers/runtime/AifarRuntimeReleasesTab.test.ts web/src/i18n/messages.ts
git commit -m "feat: mark active runtime releases"
```

---

### Task 6: Full verification and handoff

**Files:**
- Update: `memory.md` with the final verified result.
- Modify only task-owned implementation files if verification exposes a regression.

- [ ] **Step 1: Run focused backend packages**

```powershell
Set-Location backend; $env:GOCACHE = Join-Path (git rev-parse --show-toplevel) '.codex-cache/go-build'; go test ./internal/apps/aifar ./internal/httpapi -count=1
```

- [ ] **Step 2: Run all frontend tests**

```powershell
pnpm test:web
```

- [ ] **Step 3: Run all backend tests**

```powershell
pnpm test
```

- [ ] **Step 4: Build frontend and backend**

```powershell
pnpm web:build
pnpm backend:build
```

- [ ] **Step 5: Audit the scoped diff**

```powershell
git diff --check
git status --short
```

Inspect diffs only for the files in the File Map. Confirm unrelated dirty files and unrelated hunks in shared files remain intact.

- [ ] **Step 6: Record final evidence**

Append the concise problem/conclusion to `memory.md`. Do not create an empty commit; do not stage unrelated pre-existing memory or i18n changes. Report exact test/build results and explicitly state that no live Runtime rollback was executed unless separately authorized.
