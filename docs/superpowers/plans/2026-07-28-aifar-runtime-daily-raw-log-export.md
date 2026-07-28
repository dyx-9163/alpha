# AIFAR Runtime Daily Raw Log Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace time-window record filtering with one-server, one-local-date, byte-identical host log snapshots, including top-level active files when the selected date is the server's current date.

**Architecture:** Persist the selected server-local date while retaining existing UTC day boundaries for compatibility. Let the target server validate and resolve its own timezone/day boundaries, then snapshot validated files through open descriptors and stream the unchanged archive to the existing local archive repository.

**Tech Stack:** Go 1.24, Chi, SQLite, embedded POSIX shell/Bash, Vue 3, TypeScript, Vitest, Element Plus.

## Global Constraints

- One task selects exactly one Runtime instance/server and one `YYYY-MM-DD` server-local date.
- Never call `docker logs`; never parse, filter, redact, or rewrite source log bytes.
- Historical dates include date-tagged files only; server today additionally includes top-level active log files.
- Preserve `services/<service>/<sourcePath>` and verify captured source and archive-entry SHA256 values.
- Keep existing RBAC, worker, task, audit, local archive quota, retention, streaming, download, delete, and legacy-record behavior.
- Do not update `aifar-agent`, package offline resources, or push.

---

### Task 1: Persist and validate the server-local date

**Files:**
- Modify: `backend/internal/store/migrations.go`
- Modify: `backend/internal/store/models.go`
- Modify: `backend/internal/store/diagnostic_exports.go`
- Test: `backend/internal/store/diagnostic_exports_test.go`
- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/httpapi/aifar_runtime_diagnostics.go`
- Test: `backend/internal/httpapi/aifar_runtime_diagnostics_test.go`

**Interfaces:**
- Produces: `DiagnosticExport.LocalDate string`, `RuntimeDiagnosticRequest.LocalDate string`.
- Produces: request JSON `{ instanceId, localDate, services }` with strict date validation.

- [ ] **Step 1: Write failing Store migration and round-trip tests**

Add literal assertions that `local_date` is present after migration, a new export round-trips `2026-07-28`, and an old row without the value returns an empty string.

- [ ] **Step 2: Run the Store tests and verify RED**

Run: `go test ./internal/store -run DiagnosticExport -count=1`

Expected: FAIL because the model/column do not exist.

- [ ] **Step 3: Add the forward migration and Store plumbing**

Add one `ensureColumnTx` migration for `local_date text not null default ''`; include the field in `diagnosticExportColumns`, insert/update arguments, scan order, and normalization. Accept empty only for legacy records; otherwise validate `^\\d{4}-\\d{2}-\\d{2}$` when present.

- [ ] **Step 4: Run Store tests and verify GREEN**

Run: `go test ./internal/store -run DiagnosticExport -count=1`

Expected: PASS.

- [ ] **Step 5: Write failing HTTP request tests**

Change test helpers to send `localDate`. Assert valid `2026-07-28` reaches the module, malformed dates and missing dates return `RUNTIME_DIAGNOSTIC_REQUEST_INVALID`, and each accepted task still has exactly one target equal to the resolved server ID.

- [ ] **Step 6: Run HTTP tests and verify RED**

Run: `go test ./internal/httpapi -run RuntimeDiagnostic -count=1`

Expected: FAIL because the handler still requires `sinceAt/untilAt`.

- [ ] **Step 7: Implement minimal HTTP and registry contract**

Replace request payload range fields with `LocalDate`. Extend the estimate result with `LocalDate`, `DayStartAt`, `DayEndAt`, and `CurrentDate`; after estimate succeeds, save those exact boundaries and the selected date. Reconstruct `LocalDate` in the worker request from the persisted record.

- [ ] **Step 8: Run focused HTTP and Store tests**

Run: `go test ./internal/store ./internal/httpapi -run RuntimeDiagnostic -count=1`

Expected: PASS.

### Task 2: Select one server-local day and snapshot raw bytes

**Files:**
- Modify: `backend/internal/apps/aifar/runtime_diagnostics_protocol.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics.go`
- Modify: `backend/internal/apps/aifar/templates/runtime-diagnostics-estimate.sh`
- Modify: `backend/internal/apps/aifar/templates/runtime-diagnostics-export.sh`
- Delete: `backend/internal/apps/aifar/templates/runtime-diagnostics-filter.awk`
- Test: `backend/internal/apps/aifar/runtime_diagnostics_test.go`

**Interfaces:**
- Consumes: `RuntimeDiagnosticRequest.LocalDate`.
- Produces: estimate V3 total record with local date, UTC day bounds, timezone, current-date flag, and block reason.
- Produces: raw snapshot manifest with source and archive-entry SHA256 values.

- [ ] **Step 1: Write failing V3 estimate protocol tests**

Use literal records such as `AIFAR_DIAG_TOTAL_V3\t3\t120\tAsia/Shanghai\t2026-07-28\t1785168000\t1785254400\t1\t-`. Assert strict date, epoch ordering, current flag, totals, and expected-service validation.

- [ ] **Step 2: Run protocol tests and verify RED**

Run: `go test ./internal/apps/aifar -run 'RuntimeDiagnostic.*(Estimate|Protocol)' -count=1`

Expected: FAIL because V3 is unknown.

- [ ] **Step 3: Implement V3 parsing and request validation**

Accept only strict `YYYY-MM-DD`, reject missing dates, and use V3 day boundaries returned by the target. Keep V1/V2 parsing only for compatibility tests; new estimate rendering uses the date contract.

- [ ] **Step 4: Write failing Linux behavior tests for candidate selection**

Build a controlled service tree containing `2026-07-27/log_info.0.log`, `2026-07-28/log_info.0.log`, top-level `log_info.log`, another-day log, `.idx`, a symlink, and a sensitive path. Assert history selects only the tagged history file; today selects the tagged today file plus top-level log; no candidate returns a block.

- [ ] **Step 5: Run shell behavior tests and verify RED**

Run: `go test ./internal/apps/aifar -run 'RuntimeDiagnostic.*(Estimate|Export).*Linux' -count=1`

Expected: FAIL because current scripts scan every log and filter records.

- [ ] **Step 6: Implement daily candidate discovery**

In both scripts, accept `LOCAL_DATE`; resolve server today and UTC day bounds on the target. Select `.log`/`.log.*` files whose safe relative path contains the date; when current date, also select paths without `/`. Emit `no-candidate-files` as an estimate block and fail export if discovery becomes empty.

- [ ] **Step 7: Write failing raw snapshot mutation tests**

Assert archived bytes exactly equal a fixture containing secrets and unrecognized timestamps; SHA256 fields match literal hashes; same-inode append succeeds for today without including new bytes; truncation, path inode replacement, or captured-prefix mutation fails without emitting a final stream.

- [ ] **Step 8: Run mutation tests and verify RED**

Run: `go test ./internal/apps/aifar -run 'RuntimeDiagnostic.*RawSnapshot' -count=1`

Expected: FAIL because the current helper filters and redacts.

- [ ] **Step 9: Implement the fixed-length descriptor snapshot**

Stream exactly `initial_size` bytes from the open descriptor through `tee` and `sha256sum`, verify staged size/hash, re-hash the descriptor prefix, verify path device/inode, enforce final size equality for history and non-shrink for today, then move the staged file. Remove gawk/filter/redaction/private-key scans and record both hashes in `manifest.json`.

- [ ] **Step 10: Rename task steps and update README text**

Replace `filter-and-redact` with `snapshot-raw-log-files`; state that logs are original, unredacted bytes and today's active logs are fixed-length task-start snapshots.

- [ ] **Step 11: Run the AIFAR package tests**

Run: `go test ./internal/apps/aifar -count=1`

Expected: PASS.

### Task 3: Replace the frontend range picker with one date

**Files:**
- Modify: `web/src/containers/runtime/types.ts`
- Modify: `web/src/containers/runtime/runtimeDiagnostics.ts`
- Test: `web/src/containers/runtime/runtimeDiagnostics.test.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue`
- Test: `web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.test.ts`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes/produces: `RuntimeDiagnosticRequest { instanceId, localDate, services }`.
- Produces: a single-date form and an explicit raw/unredacted snapshot warning.

- [ ] **Step 1: Write failing frontend contract tests**

Assert `defaultRuntimeDiagnosticDate(new Date('2026-07-28T08:00:00Z')) === '2026-07-28'`, fingerprints change with `localDate`, and source tests contain a single `type="date"` picker with no time-range radio or `datetimerange`.

- [ ] **Step 2: Run focused frontend tests and verify RED**

Run: `pnpm test:web`

Expected: FAIL because the old two-hour/custom-range contract remains.

- [ ] **Step 3: Implement the date-only form**

Use one Element Plus date picker, format the selected browser date as local `YYYY-MM-DD` without `toISOString()` date truncation, invalidate estimates when it changes, and send `localDate`. Display `row.localDate` with a legacy range fallback.

- [ ] **Step 4: Update Chinese and English copy**

Add labels for server-local date, raw host files, unredacted sensitive-data warning, today's fixed-length snapshot, and no-candidate guidance. Remove claims about exact record filtering and redaction from the active UI.

- [ ] **Step 5: Run frontend tests and Web build**

Run: `pnpm test:web`

Run: `pnpm web:build`

Expected: both PASS.

### Task 4: Regression and safety verification

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: all implementation changes.
- Produces: verified handoff with no target-server mutation.

- [ ] **Step 1: Run backend regression**

Run with a repository-local writable `GOCACHE`: `pnpm test`

Expected: PASS.

- [ ] **Step 2: Run frontend and builds**

Run: `pnpm test:web`

Run: `pnpm web:build`

Run: `pnpm backend:build`

Expected: PASS.

- [ ] **Step 3: Inspect the final diff**

Run: `git diff --check`, `git status --short`, and `git diff --stat`. Confirm no credential, archive, database, generated build output, Docker-log call, agent update, offline package, or unrelated user file is included.

- [ ] **Step 4: Append the reusable conclusion to memory**

Record only the date-selection contract, raw snapshot semantics, verification evidence, and any remaining live-server verification gap. Never record credentials or full logs.
