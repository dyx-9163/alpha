# AIFAR Runtime Release History UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make partial current state, rollback impact, and release-record deletion semantics explicit in the Runtime release-history table.

**Architecture:** Extend the existing release-list response with a read-only current-revision map for backend-approved rollback services. Keep eligibility authoritative in the backend, derive display-only scope data in pure frontend helpers, and render the impact preview with Vue VNodes inside the existing Element Plus prompt.

**Tech Stack:** Go 1.24, Chi, Vue 3, TypeScript, Element Plus, Vitest, Vue Test Utils.

## Global Constraints

- Preserve `/api/v2`, existing worker/task/audit flows, and backend eligibility checks.
- Do not add a database migration or execute a real Runtime rollback/delete.
- All new user-visible copy must be present in Chinese and English.
- Follow `design/ant-design-system-portable202606.md` and existing runtime CSS tokens.
- Preserve unrelated dirty worktree changes; do not commit or push.

---

### Task 1: Expose current revisions for approved rollback services

**Files:**
- Modify: `backend/internal/httpapi/apps_release_handlers.go`
- Test: `backend/internal/httpapi/apps_handlers_test.go`
- Test: `backend/internal/httpapi/apps_release_delete_test.go`

**Interfaces:**
- Consumes: `store.AppInstance.Metadata` and `registry.ArtifactRollbackInspection.RollbackServices`.
- Produces: `currentServiceRevisions: map[string]string` on each release response item.

- [ ] **Step 1: Write failing response tests**

Add literal fixtures where `rollbackServices=["gateway","oauth"]`, metadata has `serviceRevisions.gateway="release-live-gateway"`, and the global fallback is `release-live-bundle`. Assert the response contains:

```go
map[string]string{
    "gateway": "release-live-gateway",
    "oauth":   "release-live-bundle",
}
```

- [ ] **Step 2: Verify RED**

Run from `backend/`:

```powershell
go test ./internal/httpapi -run 'TestAIFARReleaseResponse|TestListAIFARReleases' -count=1
```

Expected: assertions fail because `currentServiceRevisions` is absent.

- [ ] **Step 3: Implement the minimal response mapping**

Add a focused helper that parses metadata, reads `serviceRevisions`, falls back to `currentRevision` then `releaseId`, and emits only non-empty revisions for the supplied rollback services. Pass the instance into the response builder or attach the field immediately after inspection without duplicating rollback eligibility.

- [ ] **Step 4: Verify GREEN**

Repeat the focused command and run:

```powershell
go test ./internal/httpapi -count=1
```

Expected: PASS.

---

### Task 2: Derive visible release scope and render direct action reasons

**Files:**
- Modify: `web/src/containers/runtime/releaseRules.ts`
- Modify: `web/src/containers/runtime/releaseRules.test.ts`
- Modify: `web/src/containers/runtime/types.ts`
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeReleasesTab.vue`
- Modify: `web/src/containers/runtime/AifarRuntimeReleasesTab.test.ts`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/containers/runtime/runtime.css`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Produces:

```ts
export type RuntimeReleaseScope = {
  currentServices: string[]
  totalServices: string[]
}

export function runtimeReleaseScope(row: AifarRelease): RuntimeReleaseScope
```

- [ ] **Step 1: Write failing pure and component tests**

Assert an ordered union for `changedServices=['oauth','gateway']`, `currentServices=['oauth']`, and `rollbackServices=['gateway']`. In the component test assert visible text `Current 1/2`, visible `oauth`, `Delete release record`, and direct text for both rollback and delete disabled reasons.

- [ ] **Step 2: Verify RED**

```powershell
Set-Location web
node node_modules/vitest/vitest.mjs run src/containers/runtime/releaseRules.test.ts src/containers/runtime/AifarRuntimeReleasesTab.test.ts
```

Expected: scope helper and new visible copy are absent.

- [ ] **Step 3: Implement minimal scope and table rendering**

Add `currentServiceRevisions?: Record<string,string>` to `AifarRelease`. Implement the ordered-union helper. Expose `releaseScope` through runtime context. Add the current-scope column, direct action-reason elements, bilingual labels, and token-based CSS. Use `containers.deleteRelease` for the delete button.

- [ ] **Step 4: Verify GREEN**

Repeat the focused Vitest command. Expected: PASS.

---

### Task 3: Add the rollback impact preview

**Files:**
- Create: `web/src/containers/runtime/releaseImpact.ts`
- Create: `web/src/containers/runtime/releaseImpact.test.ts`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Produces:

```ts
export type ReleaseRollbackImpactItem = {
  service: string
  currentRevision: string
  targetRevision: string
}

export function runtimeReleaseRollbackImpact(row: AifarRelease, unknownRevision: string): ReleaseRollbackImpactItem[]
```

- [ ] **Step 1: Write a failing helper test**

For `rollbackServices=['gateway','oauth']`, revision mapping containing only `gateway`, and target `release-target`, assert literal items containing the mapped gateway revision and the supplied unknown fallback for oauth.

- [ ] **Step 2: Verify RED**

```powershell
Set-Location web
node node_modules/vitest/vitest.mjs run src/containers/runtime/releaseImpact.test.ts
```

Expected: module does not exist.

- [ ] **Step 3: Implement the helper and safe VNode message**

Build the impact items without mutating the API row. In `rollbackAifarRelease`, pass a VNode message to `ElMessageBox.prompt` containing the target release, affected-service count/list, revision rows, and existing no-data-rollback warning. Keep the textarea validator and request payload unchanged.

- [ ] **Step 4: Verify GREEN**

Run the focused helper test and the existing Runtime component/rule tests. Expected: PASS.

---

### Task 4: Verify the complete affected surface

**Files:**
- Modify: `memory.md` only for the final reusable result.

- [ ] **Step 1: Run backend verification**

```powershell
Set-Location backend
go test ./internal/httpapi -count=1
Set-Location ..
pnpm test
```

- [ ] **Step 2: Run frontend verification**

```powershell
pnpm test:web
pnpm web:build
```

- [ ] **Step 3: Inspect the scoped diff**

```powershell
git diff --check
git status --short
```

Confirm no unrelated runtime, server collector, database, storage, or task behavior changed.

- [ ] **Step 4: Record final evidence**

Append the concise verified conclusion to `memory.md`, including exact test/build results and that no live Runtime rollback/delete was executed.
