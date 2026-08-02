# Runtime Release Deletion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make old Runtime release records deletable when they are not active, and expose authoritative delete eligibility to the UI.

**Architecture:** The backend derives active release IDs from instance metadata and is the single authority for list-time and delete-time eligibility. Historical manifest ancestry remains audit data rather than a hard dependency, while retention separately preserves active per-service revisions.

**Tech Stack:** Go 1.24, Chi, SQLite, Vue 3, TypeScript, Element Plus, Vitest.

## Global Constraints

- Preserve unrelated dirty-worktree changes and do not commit or push.
- DELETE remains a synchronous control-plane record deletion with audit logging; it does not remove remote runtime content.
- All new user-visible text is bilingual.
- Backend delete-time validation remains authoritative even when the frontend disables an action.

---

### Task 1: Backend deletion semantics and retention

**Files:**
- Modify: `backend/internal/httpapi/apps_release_delete_test.go`
- Modify: `backend/internal/httpapi/apps_release_handlers.go`
- Modify: `backend/internal/store/store_test.go`
- Modify: `backend/internal/store/app_instances.go`

**Interfaces:**
- Consumes: instance metadata fields `currentRevision`, `releaseId`, and `serviceRevisions`.
- Produces: `aifarReleaseDeleteBlockReason` that blocks active revisions and in-progress releases only; `DeleteOldAppReleases` that keeps active revisions without preserving all ancestors.

- [ ] **Step 1: Write failing backend tests**

Add literal fixtures proving a service-active release is blocked, `baseReleaseId`/`rollbackTo` audit references do not block an inactive release, and a five-item linear chain is reduced to three while an older active service revision is retained.

- [ ] **Step 2: Verify backend tests fail**

Run `go test ./internal/httpapi -run 'TestAIFARReleaseDeleteBlockReason|TestListAIFARReleasesReportsRollbackEligibility'` and `go test ./internal/store -run 'TestAppReleaseRetention'` from `backend` with a worktree-local `GOCACHE`.

- [ ] **Step 3: Implement minimal backend behavior**

Derive a set of active release IDs from global and service metadata; remove hard blocking on manifest ancestry; replace recursive retention protection with active revision protection.

- [ ] **Step 4: Verify backend tests pass**

Repeat the targeted commands and run `go test ./internal/httpapi ./internal/store`.

### Task 2: API deletion eligibility contract

**Files:**
- Modify: `backend/internal/httpapi/apps_release_delete_test.go`
- Modify: `backend/internal/httpapi/apps_release_handlers.go`

**Interfaces:**
- Produces list item fields `deleteAvailable`, `deleteUnavailableReason`, and `deleteUnavailableDetails`.

- [ ] **Step 1: Extend the list-response test and verify RED**

Assert that the active release reports `deleteAvailable=false` with `AIFAR_RELEASE_DELETE_CURRENT`, while an inactive historical release reports `deleteAvailable=true` and an empty reason.

- [ ] **Step 2: Add the response fields and verify GREEN**

Calculate the same block reason used by DELETE for every list row and serialize its code/details without localizing the stable reason code.

### Task 3: Frontend eligibility display

**Files:**
- Modify: `web/src/containers/runtime/types.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeReleasesTab.test.ts`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: backend `deleteAvailable`, `deleteUnavailableReason`, and `deleteUnavailableDetails`.
- Produces: a disabled Delete button with a bilingual reason tooltip.

- [ ] **Step 1: Write a failing component test and verify RED**

Use one current row with `AIFAR_RELEASE_DELETE_CURRENT`, one active row with `AIFAR_RELEASE_DELETE_ACTIVE`, and one eligible historical row; assert only the historical row's Delete button is enabled.

- [ ] **Step 2: Implement types, reason mapping, and bilingual copy**

Map stable backend codes to `containers.releaseDeleteCurrentUnavailable` and `containers.releaseDeleteActiveUnavailable`; keep role and missing-ID checks first.

- [ ] **Step 3: Verify component and full frontend tests pass**

Run `pnpm test:web -- AifarRuntimeReleasesTab.test.ts`, then `pnpm test:web` and `pnpm web:build`.

### Task 4: Final verification and handoff

**Files:**
- Modify: `memory.md`

- [ ] **Step 1: Review the focused diff**

Confirm no MySQL, database health, storage, or task-page changes were altered by this fix.

- [ ] **Step 2: Run affected backend and frontend gates**

Run `go test ./internal/httpapi ./internal/store`, `pnpm test:web`, and `pnpm web:build`.

- [ ] **Step 3: Record the verified conclusion**

Append only the reusable problem, semantics, changed boundaries, and test results to `memory.md`; do not include secrets or long logs.
