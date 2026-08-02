# MySQL Backup Schema Selection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add safe manual business-Schema selection for standalone and InnoDB Cluster backups, correct MySQL Shell output arguments, and expose masked stderr in task logs.

**Architecture:** A read-only registry capability discovers live schemas from the standalone instance or the verified cluster PRIMARY. The backup request carries selected schema names, and the worker re-inspects and validates them before rendering both dry-run and real `dumpInstance(includeSchemas)` calls. The UI displays a typed three-category catalog while keeping system and cluster metadata schemas disabled.

**Tech Stack:** Go 1.24, Chi, worker task logging, SSH adapter, MySQL Shell 8.0.36, Vue 3, TypeScript, Element Plus, Vitest.

## Global Constraints

- Keep API prefix `/api/v2`, task/audit behavior, operation locks, and stable English error codes.
- Do not expose or persist credentials, remote work paths, or raw stderr.
- System and cluster metadata schemas are display-only and must never be accepted as selected business schemas.
- InnoDB Cluster discovery and execution must resolve the current healthy PRIMARY at execution time.
- All new user-visible text must exist in Chinese and English.
- Preserve existing standalone, cluster, pre-restore, manifest-v2, and restore compatibility behavior.

---

### Task 1: Backend schema classification and request contracts

**Files:**
- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/apps/mysql/backup_types.go`
- Modify: `backend/internal/apps/mysql/backup_manifest.go`
- Test: `backend/internal/apps/mysql/backup_manifest_test.go`

**Interfaces:**
- Produces `registry.BackupSchema`, `registry.BackupSchemaCatalog`, and `registry.BackupSchemaModule`.
- Produces MySQL classification/selection normalization used by discovery and backup execution.

- [ ] Write failing tests for fixed system schemas, `mysql_innodb_cluster_metadata*`, business schemas, duplicate/unknown/empty selections, and canonical exclusions.
- [ ] Run `go test ./internal/apps/mysql -run 'Schema|Manifest'` and confirm the new tests fail for missing behavior.
- [ ] Implement classification and selection normalization with case-insensitive validation and canonical live names.
- [ ] Re-run the focused tests and confirm they pass.

### Task 2: Live discovery, MySQL Shell compatibility, and masked diagnostics

**Files:**
- Modify: `backend/internal/apps/mysql/backup.go`
- Modify: `backend/internal/apps/mysql/cluster_restore.go`
- Modify: `backend/internal/apps/mysql/credential_transport.go`
- Modify: `backend/internal/apps/mysql/reconcile.go`
- Modify: `backend/internal/apps/mysql/templates/backup/disaster-rebuild.sh`
- Test: `backend/internal/apps/mysql/backup_test.go`
- Test: `backend/internal/apps/mysql/cluster_restore_test.go`
- Test: `backend/internal/apps/mysql/credential_transport_test.go`

**Interfaces:**
- Consumes `registry.BackupSchemaCatalog` and selected `schemas` request parameter.
- Produces live discovery for standalone/current PRIMARY and safe stderr task evidence.

- [ ] Write failing tests asserting all SQL-mode mysqlsh commands use `--result-format=tabbed`, discovery categorizes live output, and failed inspection logs useful stderr without explicit or key-shaped secrets.
- [ ] Run focused MySQL tests and confirm failures.
- [ ] Implement live inspection/discovery, task-log stderr sanitization, and replace MySQL Shell SQL output arguments.
- [ ] Re-run focused tests and confirm they pass.

### Task 3: Selected-schema dump and manifest propagation

**Files:**
- Modify: `backend/internal/apps/mysql/backup_scripts.go`
- Modify: `backend/internal/apps/mysql/templates/backup/logical-backup.sh`
- Modify: `backend/internal/apps/mysql/backup.go`
- Test: `backend/internal/apps/mysql/backup_scripts_test.go`
- Test: `backend/internal/apps/mysql/backup_scripts_linux_test.go`
- Test: `backend/internal/apps/mysql/backup_test.go`

**Interfaces:**
- `LogicalBackupScriptOptions.Schemas []string` is the only schema input to the controlled renderer.
- Both dry-run and actual dump use identical canonical selected schemas via `includeSchemas`.

- [ ] Write failing tests for safe schema rendering, invalid schema rejection, dry-run/real selection parity, and selected manifest contents.
- [ ] Run focused script/backup tests and confirm failures.
- [ ] Render canonical JSON arrays into controlled Python/JavaScript and update execution/manifests to use selected schemas and exclusions.
- [ ] Re-run focused tests and confirm they pass.

### Task 4: HTTP discovery and backup request validation

**Files:**
- Modify: `backend/internal/httpapi/api.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers.go`
- Test: `backend/internal/httpapi/apps_handlers_test.go`
- Test: `backend/internal/httpapi/mysql_backup_handlers_test.go`

**Interfaces:**
- Produces `GET /apps/instances/{id}/backup-schemas`.
- Extends backup POST body with required `schemas: string[]` and forwards it to `registry.BackupRequest.Parameters`.

- [ ] Write failing decode/handler tests for valid selection, empty/malformed selection, discovery response, cluster target snapshot, and stable error mapping.
- [ ] Run focused HTTP API tests and confirm failures.
- [ ] Register the route, implement the handler, and forward a defensive copy of selected schemas.
- [ ] Re-run focused HTTP API tests and confirm they pass.

### Task 5: Frontend typed catalog and selectable UI

**Files:**
- Modify: `web/src/database/mysqlBackup.ts`
- Modify: `web/src/database/MySQLBackupDialog.vue`
- Modify: `web/src/i18n/messages.ts`
- Test: `web/src/database/mysqlBackup.test.ts`
- Test: `web/src/database/mysqlBackup.components.test.ts`

**Interfaces:**
- Produces `discoverMySQLBackupSchemas(instanceId)` and typed categories.
- `startMySQLBackup` requires `schemas: string[]` and submits a defensive validated copy.

- [ ] Write failing logic and component tests for strict catalog parsing, three groups, disabled system/internal entries, default-all business selection, empty-selection blocking, reload/error states, and exact request body.
- [ ] Run `pnpm test:web -- mysqlBackup` and confirm failures.
- [ ] Implement typed parsing, dialog loading/group layout, selection state, localized copy, and submit payload.
- [ ] Re-run focused web tests and confirm they pass.

### Task 6: Verification and handoff

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Produces verified current-branch implementation ready for the user's live test.

- [ ] Run `pnpm test`.
- [ ] Run `pnpm test:web`.
- [ ] Run `pnpm web:build`.
- [ ] Run `pnpm test:scripts` if command/template changes affect script contracts.
- [ ] Run `git diff --check` and inspect `git status --short` for unrelated changes.
- [ ] Append the problem and reusable conclusion to `memory.md` without secrets or long logs.
