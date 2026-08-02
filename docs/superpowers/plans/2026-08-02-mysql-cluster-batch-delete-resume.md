# MySQL Cluster Batch Delete Resume Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make password-confirmed MySQL InnoDB Cluster batch deletion resumable after a member has already been removed, while preserving maintenance/reconciliation safety and removing empty cluster records.

**Architecture:** Add an optional registry-level batch-delete preflight contract. The MySQL module validates and freezes all remaining selected members before the worker mutates any node; per-item deletion then performs a narrow identity/marker recheck. `DeleteAppInstance` transactionally prunes only cluster rows that become empty.

**Tech Stack:** Go 1.24, Chi, SQLite, existing worker/task/audit and application registry contracts.

## Constraints

- Preserve the `.132` standalone MySQL instance.
- Do not bypass server-password confirmation, operation locks, worker tasks, target steps, or audit.
- Preserve unrelated dirty-worktree changes and do not commit or push.
- Fail closed on missing selection, marker parse errors, or ownership drift.

### Task 1: Add regression tests

**Files:**
- Modify: `backend/internal/apps/mysql/service_test.go`
- Modify: `backend/internal/store/store_test.go`
- Modify: `backend/internal/httpapi/apps_handlers_test.go`

- [x] Add a MySQL service test that preflights two remaining authoritative members and deletes both without a second three-member check.
- [x] Add negative tests for partial selection and maintenance/reconciliation markers.
- [x] Add a store test proving the cluster row survives while one member remains and is removed after the last member.
- [x] Add an HTTP test proving batch preflight runs before task execution and its frozen scope reaches each delete call.
- [x] Run focused tests and record the expected RED failures.

### Task 2: Add the batch-delete contract and MySQL preflight

**Files:**
- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/httpapi/apps_handlers.go`
- Modify: `backend/internal/apps/mysql/module.go`
- Add: `backend/internal/apps/mysql/delete_batch.go`
- Modify: `backend/internal/apps/mysql/service.go`

- [x] Define internal batch scope and optional `BatchDeleteModule` preflight interface.
- [x] Move batch MySQL lifecycle validation to the preflight phase and attach the scope only after success.
- [x] Validate all current remaining members, selection closure, ownership, maintenance markers, and reconciliation markers.
- [x] Use the frozen scope during each MySQL delete while still rechecking fresh instance identity and marker absence.

### Task 3: Prune empty cluster records

**Files:**
- Modify: `backend/internal/store/app_instances.go`

- [x] Capture affected cluster IDs inside `DeleteAppInstance`'s transaction.
- [x] Delete only cluster rows with no remaining members after the instance deletion.

### Task 4: Verify

- [x] Run focused MySQL, HTTP API, and store tests.
- [x] Run the complete backend suite with a writable worktree-local `GOCACHE` (`pnpm test` itself was blocked by the configured read-only cache).
- [x] Run `git diff --check` and inspect only the scoped files.
- [ ] Append the reusable conclusion to `memory.md`.
- [ ] Hand off the panel retry: select only the remaining `.134` and `.133` cluster records and enter each server's SSH confirmation password in the panel.
