# Scheme 0 Closeout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Safely finish, test, review, and commit every existing Scheme 0 working-tree change without altering public API, database schema, or user-visible behavior beyond the approved fail-closed security and release fixes.

**Architecture:** Preserve the current dirty checkout as the source baseline, then close one behavior domain at a time. Characterize existing work, apply new fixes test-first, commit exact scopes, and require task-level plus final review.

**Tech Stack:** Go 1.24, SQLite/modernc, Chi, Vue 3, TypeScript, Pinia, Vitest, Node.js 22, PowerShell, POSIX shell, GitHub Actions.

## Global Constraints

- Preserve every pre-existing tracked and untracked change; never reset, checkout, stash, or overwrite unrelated work.
- Keep `/api/v2`, terminal WebSocket, SSE payloads, task/audit machine codes, UI routes, permissions, confirmations, and topology behavior unchanged.
- Add no database table, column, or migration; keep `enc:v1:` ciphertext compatible.
- Never write a real password, token, private key, or encryption secret to source, tests, logs, reports, or commits.
- New backend user-visible text must have zh/en entries; new frontend copy must use existing i18n.
- Existing uncommitted implementation is user baseline. Characterize it before changing it; strict RED/GREEN applies to each newly fixed blocker.
- Do not run real-server mutating E2E.

---

### Task 1: Worker terminal outcome hardening

**Files:**
- Modify: `backend/internal/worker/manager.go`
- Modify: `backend/internal/worker/manager_test.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Preserve `Job`, `Manager.Start*`, `Manager.Cancel`, task statuses, cancel responses, and realtime event names.
- Produce exactly one persisted terminal state and one terminal event pair per task.

- [x] Add focused tests for a normal job error, cancellation while waiting for a slot, cancel/panic precedence, exact terminal event counts, lock/slot release, and sensitive panic masking.
- [x] Run `go test -count=1 ./internal/worker` from `backend/`; record which new assertions fail and why.
- [x] Make the minimal terminal-claim, cleanup, or masking changes needed for the tests; do not add leases or retry semantics.
- [x] Run `go test -count=10 ./internal/worker` and `go test -count=1 ./internal/i18n`.
- [x] Commit only this scope as `fix(worker): make terminal outcomes atomic`.

### Task 2: Production security policy and development override

**Files:**
- Modify: `backend/internal/config/config.go`
- Create/Modify: `backend/internal/config/security.go`, `backend/internal/config/security_test.go`
- Modify: `config/defaults.env`, `scripts/dev.mjs`, `scripts/run-backend.mjs`, `README.md`

**Interfaces:**
- `AIFAR_ALLOW_INSECURE_DEFAULTS=true` is valid only with `127.0.0.1`, `localhost`, or `::1`.
- Explicit false wins over development defaults; structural secret errors are never bypassed.

- [x] Add failing tests for wildcard/empty/non-loopback addresses, loopback IPv4/IPv6/localhost, explicit false, and equal current/previous secrets under override.
- [x] Run `go test -count=1 ./internal/config` and the focused Node test; confirm the new cases fail for policy reasons.
- [x] Implement one shared loopback decision and make both backend validation and dev launchers honor it.
- [x] Keep distributable password/JWT/credential values blank and document the exact production behavior.
- [x] Run config and script tests, then commit as `fix(security): fail closed outside loopback`.

### Task 3: Credential-key validation and atomic rotation

**Files:**
- Modify: `backend/internal/store/crypto.go`, `backend/internal/store/store.go`
- Create/Modify: `backend/internal/store/secret_rotation.go`, `backend/internal/store/secret_rotation_test.go`
- Modify: `backend/cmd/aifar-server/main.go`
- Create/Modify: `backend/internal/config/credential_rotation_test.go`

**Interfaces:**
- Preserve `Open`, `OpenWithSecret`, and read-only compatibility wrappers.
- Validate all known encrypted columns before HTTP; rotate only when previous secret is supplied.

- [x] Add failing tests for wrong current/no previous, wrong previous rollback, mixed current/previous ciphertext, read-only no-write behavior, idempotent second start, and secret-free errors.
- [x] Run `go test -count=1 ./internal/store ./internal/config`; confirm the new validation cases fail.
- [x] Reuse one target list for validation and rotation, close query rows before updates, and keep all rotation writes in one transaction.
- [x] Wire validation/rotation before bootstrap and HTTP startup; clear the previous derived key after success.
- [x] Run `go test -count=1 ./internal/store ./internal/config ./cmd/aifar-server`, then commit as `fix(store): validate and rotate credential secrets`.

### Task 4: Realtime last-known cache

**Files:**
- Modify: `web/src/stores/realtime.ts`
- Create/Modify: `web/src/stores/realtime.test.ts`
- Modify: `web/package.json`, `pnpm-lock.yaml`, root `package.json`
- Create/Modify: `scripts/test-web.mjs`

**Interfaces:**
- Preserve EventSource URL and event envelope handling.
- Preserve unrelated/absent cache entries and use `version -> collectedAt -> updatedAt` freshness.

- [x] Add tests for empty success retention, unchanged revision, equal-version later collection, real SSE envelope parsing, and concurrent old GET versus newer SSE.
- [x] Run the focused Vitest file and capture expected failures for any missing behavior.
- [x] Implement the smallest merge/freshness changes; do not add tombstones, TTL, polling, or backend cleanup.
- [x] Run `pnpm test:web`, then commit as `fix(web-realtime): harden status snapshot cache`.

### Task 5: Containers and AIFAR Runtime extraction guardrails

**Files:**
- Modify: `web/src/views/ContainersView.vue`, `web/src/containers/runtime/context.ts`
- Create/Modify: helper/API/rule/composable modules under `web/src/containers/`
- Create: `containerHelpers.test.ts`, `dockerApi.test.ts`, `runtime/api.test.ts`, `runtime/runtimeRules.test.ts`, `runtime/useAifarRuntimeLogViewport.test.ts`

**Interfaces:**
- Preserve every Docker/Runtime endpoint, method, payload, FormData field, EventSource URL, task tracking action, permission, and confirmation.
- Keep `ContainersView.vue` as page orchestrator and freeze the current workspace/tabs/dialogs/context boundary.

- [x] Add pure behavior tests for cache keys, image identity/deduplication, Docker realtime merge, bootstrap degradation, Docker requests, all Runtime requests, artifact/config/format/selectors, and virtual log viewport calculations.
- [x] Run the focused tests; existing untested helpers may pass characterization tests, but any new correction must be proven RED before implementation.
- [x] Correct only behavior exposed by failing tests and finish type-safe wiring; do not add a component-test stack or further controller extraction.
- [x] Run `pnpm test:web` and `pnpm web:build`.
- [x] Commit the complete compiling extraction as `refactor(web): finish containers runtime extraction`.

### Task 6: Portable toolchain and startup parsing

**Files:**
- Modify: `scripts/toolchain.mjs`, `scripts/setup-dev.mjs`
- Modify: `scripts/start.sh`, `scripts/start.ps1`, `scripts/start.bat`
- Create/Modify: focused Node/shell/PowerShell fixture tests

**Interfaces:**
- Accept only `AIFAR_[A-Z0-9_]+=value`, reject malformed/duplicate keys, strip one matching quote pair, and let an existing environment variable—including empty—win.
- Preserve bat-to-PowerShell delegation and platform line endings.

- [x] Add fixture tests for malformed, duplicate, quoted, explicit-empty, loopback, system-toolchain, and missing-toolchain cases.
- [x] Run the focused tests and confirm parser/toolchain gaps fail.
- [x] Implement equivalent shell and PowerShell semantics without sourcing or evaluating arbitrary config content.
- [x] Verify shell/PowerShell syntax and `git ls-files --eol` output.
- [x] Commit as `fix(tooling): make startup and toolchain behavior portable`.

### Task 7: Fail-closed release and complete archive verification

**Files:**
- Modify: `scripts/package-release.mjs`, `scripts/verify-release-checksums.mjs`, `scripts/test-local.mjs`, root `package.json`
- Create/Modify: `scripts/runtime-security-config.mjs`, its tests, and release fixture tests

**Interfaces:**
- Keep package directories, binary names, archive names, and runtime contents unchanged.
- Require current-version Linux and Windows directories plus archives; extracted contents must match their checksum manifests.

- [x] Add thin-fixture tests for unsafe defaults before copy, archive command failure, staging cleanup, missing platform, missing archive, stale-version false positive, extraction, and checksum mismatch.
- [x] Run `pnpm test:scripts` and confirm new cases fail.
- [x] Move security validation before resource copy, wrap staging in unconditional cleanup, make archive failures fatal, and verify only the current package version.
- [x] Remove the duplicate release call after `package` in `test:local`.
- [x] Run `pnpm test:scripts` and a thin fixture package/verify cycle, then commit as `fix(release): require verifiable platform archives`.

### Task 8: CI, documentation, and final verification

**Files:**
- Create/Modify: `.github/workflows/ci.yml`
- Modify: `AGENTS.md`, `README.md`, `design/enterprise-architecture-optimization-tasks.md`, `design/minimal-optimization-standard.md`, `memory.md`

**Interfaces:**
- CI runs fast tests/builds on every PR and race tests on Ubuntu; full 4.6 GB packaging stays out of normal PR CI.
- Documentation must describe versioned migrations and the actual validation commands.

- [x] Add CI steps for backend/web/script tests, web/backend builds, Ubuntu Worker/Store race tests, and Windows/Linux startup syntax/fixture checks.
- [x] Synchronize project guidance and task checkboxes with the current source; append the final reusable conclusion to `memory.md` without secrets or long logs.
- [x] Run `pnpm test`, `pnpm test:web`, `pnpm test:scripts`, `pnpm web:build`, `pnpm backend:build`, and `git diff --check`.
- [x] Run one final `pnpm test:local`; confirm both current-version archives exist, extract, and verify.
- [x] Commit as `ci/docs: enforce scheme-0 gates and sync guidance`.
- [x] Dispatch a whole-branch code review, fix all Critical/Important findings in one fix wave, and re-run affected tests plus the final fast gate.
