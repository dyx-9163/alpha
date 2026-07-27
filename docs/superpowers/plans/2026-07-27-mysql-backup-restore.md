# MySQL Backup and Restore Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver task-driven MySQL Shell logical full backup, verification, retention, standalone restore, healthy InnoDB Cluster restore, and explicit disaster rebuild for AIFAR-managed MySQL 8.0.36 instances.

**Architecture:** Reuse `app_backups`, `app_clusters`, `app_cluster_members`, credential bindings, operation locks, worker steps, target logs, and audit records. Add a controlled panel-side backup repository and SSH download path; expose optional registry backup/restore lifecycles; keep topology-specific decisions inside the MySQL module. Standalone and cluster use one manifest and archive format, while restore mode selects standalone, healthy cluster PRIMARY, complete-outage recovery, or clean-seed disaster rebuild.

**Tech Stack:** Go 1.24, Chi, SQLite, existing worker/task framework, SSH/SFTP adapter, MySQL Shell `util.dumpInstance()` / `util.loadDump()`, Vue 3, TypeScript, Element Plus, Pinia, Vitest.

## Global Constraints

- Implement the approved design in `docs/superpowers/specs/2026-07-27-mysql-backup-restore-design.md`; if implementation pressure conflicts with it, stop and amend the design before changing behavior.
- Do not add a migrations directory or a second backup table. Extend the existing centralized store and `app_backups` record lifecycle.
- Every backup, verify, and restore request creates a worker task, persists target/step plans, streams SSE logs, and writes an audit event. Local backup deletion is synchronous only after controlled-path validation and still writes audit.
- Use bound active MySQL credentials with purpose `admin`; never use `AIFAR_DEFAULT_PASSWORD` as a backup or restore fallback.
- Never place a MySQL password in a command line, manifest, backup metadata, task log, error details, or audit message. Pass it through a `0600` remote secret context and remove that context on success, failure, and cancellation.
- Backup may run online. Restore requires an explicit maintenance confirmation and leaves maintenance active when schema deletion or loading has begun but completion fails.
- Standalone locks use `app-instance/<instanceId>`; cluster operations use `app-cluster/<clusterId>`. Use the existing mutation operation so check/delete/start/backup/restore cannot overlap.
- `local_infile` must be restored in a finally path. An unreachable target records `app_instances.metadata.mysqlReconciliation` as `{version:1,kind:"local_infile",originalValue:"ON|OFF",recordedAt:<UTC RFC3339>,taskId:<current task ID>}`, fails the task with `MYSQL_LOCAL_INFILE_RESTORE_FAILED`, and blocks later MySQL lifecycle actions until reconciliation succeeds. Unknown or malformed markers fail closed with `MYSQL_RECONCILIATION_REQUIRED`; only a verified successful reconciliation removes the whole marker. The legacy name `MYSQL_RESTORE_LOCAL_INFILE_RESTORE_FAILED` remains a read/translation compatibility alias only and is never emitted by new restore work.
- Cluster backup and healthy restore require every MySQL member `ONLINE`; backup runs once on the current PRIMARY; healthy restore loads only on the current PRIMARY with `skipBinlog:false`.
- Complete outage with intact data remains the existing `dba.rebootClusterFromCompleteOutage()` operation and is not represented as backup restore.
- Disaster rebuild is explicit and owner-only: quarantine old data, restore a clean seed, call `dba.createCluster()`, clone remaining members, and re-bootstrap Router. No automatic destructive fallback is allowed.
- User-visible backend and frontend text must exist in both Chinese and English.
- All remote/MySQL tests use fakes. Real three-node acceptance is an opt-in/manual gate and must not run in ordinary CI.
- `AIFAR_MYSQL_BACKUP_KEEP_LAST` is the default retention count. A backup request may provide a positive `keepLast` override; the handler validates it and falls back to configuration when omitted. The repository directory is always server-owned and cannot be overridden by request JSON.
- Synchronous backup deletion preserves the original creation `task_id`; deletion identity and actor belong in audit rather than replacing backup provenance.
- The backup repository uses a trusted-single-writer threat model. Its root is owned exclusively by the panel service account (`0700` on Linux); every repository lifecycle operation holds an in-process mutex and an exclusive cross-process `.aifar-repository.lock` (`0600`). Failure to acquire the lock fails closed. API path escape, symlink, non-regular-file, checksum, and wrong-target protections remain required, but malicious `root` or same-UID writers that bypass the repository API are out of scope.

## File and Responsibility Map

| Area | Files | Responsibility |
|---|---|---|
| Configuration | `backend/internal/config/config.go`, `config/defaults.env`, config tests | Separate MySQL repository root and keep-last policy |
| Backup records | `backend/internal/store/app_release_assets.go`, `backend/internal/store/credentials.go`, store tests | Backup lookup/status/delete and bound credential lookup |
| Repository | `backend/internal/backuprepo/*.go` | Controlled path creation, partial promotion, checksum, upload source validation, deletion, retention |
| SSH transfer | `backend/internal/adapter/ssh_download.go`, adapter tests | Stream one remote archive into a panel-side `.partial` file with cancellation and size/hash reporting |
| Registry | `backend/internal/apps/registry/contract.go`, registry tests | Optional backup and restore lifecycle contracts |
| MySQL common | `backend/internal/apps/mysql/backup_types.go`, `backup_manifest.go`, `backup_scripts.go`, templates and tests | Requests, manifest, safe mysqlsh scripts, stable errors, reconciliation marker |
| MySQL backup | `backend/internal/apps/mysql/backup.go`, tests | Standalone and cluster source selection, dump, package, download, record, retention |
| MySQL restore | `backend/internal/apps/mysql/restore.go`, `cluster_restore.go`, tests | Pre-restore backup, local-infile guard, standalone/healthy cluster restore, disaster rebuild |
| HTTP | `backend/internal/httpapi/mysql_backup_handlers.go`, `api.go`, handler/authz tests | Resolve instances/groups, create tasks/plans/locks, audit, list/verify/delete endpoints |
| Frontend | `web/src/database/mysqlBackup.ts`, backup components, `DatabaseView.vue`, `messages.ts`, tests | Backup records, dialogs, disaster confirmation, task tracking |
| Operations | `docs/mysql-backup-restore-runbook.md` | Environment configuration and real-target acceptance/runbook |

---

### Task 1: Add MySQL backup configuration and store contracts

**Files:**

- Modify: `backend/internal/config/config.go`
- Create: `backend/internal/config/config_test.go`
- Modify: `config/defaults.env`
- Modify: `backend/internal/httpapi/settings_users_handlers.go`
- Modify: `backend/internal/httpapi/authz_test.go`
- Modify: `backend/internal/store/app_release_assets.go`
- Modify: `backend/internal/store/credentials.go`
- Modify: `backend/internal/store/enterprise_foundations_test.go`
- Modify: `backend/internal/store/credentials_test.go`

**Interfaces:**

```go
type Config struct {
    MySQLBackupDir      string `json:"mysqlBackupDir"`
    MySQLBackupKeepLast int    `json:"mysqlBackupKeepLast"`
}

func (s *Store) GetAppBackup(id string) (AppBackup, error)
func (s *Store) ListAppBackupsForInstances(instanceIDs []string, includeDeleted bool) ([]AppBackup, error)
func (s *Store) MarkAppBackupDeleted(id string, completedAt time.Time) (AppBackup, error)
func (s *Store) GetBoundCredential(appInstanceID, purpose string, includeSecret bool) (Credential, error)
```

- [ ] Add failing config tests proving `AIFAR_MYSQL_BACKUP_DIR` defaults to `<workdir>/data/mysql-backups`, `AIFAR_MYSQL_BACKUP_KEEP_LAST` defaults to `5`, explicit values are honored, and invalid keep-last values fall back to `5`.
- [ ] Run `cd backend; go test ./internal/config -run 'TestLoad.*MySQLBackup'` and confirm the new tests fail because the fields do not exist.
- [ ] Add the two config fields, load them independently of `AIFAR_DATABASE_BACKUP_DIR`, expose only their non-secret values from settings, and document safe production examples in `config/defaults.env`.
- [ ] Add failing store tests for exact backup lookup, multi-instance cluster listing, exclusion/inclusion of `deleted`, monotonic status updates, and deletion marking that preserves path/checksum metadata for audit.
- [ ] Implement parameterized `IN (...)` construction without interpolating instance IDs; normalize IDs, de-duplicate them, and return an empty slice for an empty input.
- [ ] Add failing credential tests with two bound credentials proving only an active `purpose=admin` binding for the requested instance is returned with a decrypted `password` when `includeSecret=true`; inactive, wrong-purpose, missing-secret, and ambiguous bindings must return stable errors.
- [ ] Implement `GetBoundCredential` as a join from `credential_bindings` to `credentials`, ordered deterministically and rejecting multiple active matches rather than silently selecting one.
- [ ] Run `cd backend; go test ./internal/config ./internal/store` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/config/config.go backend/internal/config/config_test.go config/defaults.env backend/internal/httpapi/settings_users_handlers.go backend/internal/httpapi/authz_test.go backend/internal/store/app_release_assets.go backend/internal/store/credentials.go backend/internal/store/enterprise_foundations_test.go backend/internal/store/credentials_test.go && git commit -m "feat: add MySQL backup configuration and records"`.

### Task 2: Build the controlled panel backup repository

**Files:**

- Create: `backend/internal/backuprepo/repository.go`
- Create: `backend/internal/backuprepo/repository_test.go`
- Create: `backend/internal/backuprepo/checksum.go`
- Create: `backend/internal/backuprepo/checksum_test.go`

**Interfaces:**

```go
type Repository struct {
    root string
}

type BackupPaths struct {
    Directory       string
    PartialArchive  string
    Archive         string
    Manifest        string
    Checksums       string
}

func New(root string) (*Repository, error)
func (r *Repository) Prepare(backupID string) (BackupPaths, error)
func (r *Repository) Commit(paths BackupPaths, manifest []byte, expectedSHA256 string, expectedSize int64) error
func (r *Repository) Verify(backup store.AppBackup) (Verification, error)
func (r *Repository) Delete(backup store.AppBackup) error
func (r *Repository) RetentionCandidates(backups []store.AppBackup, keepLast int) []store.AppBackup
```

- [ ] Write failing table tests rejecting an empty root, traversal IDs, absolute IDs, symlinked root/backup directories, non-regular archives, a record path outside the configured root, checksum mismatch, manifest/record ID mismatch, and unsupported extensions.
- [ ] Add success tests for the exact on-disk layout `root/<backupId>/{backup-manifest.json,dump.tar,checksums.txt}`, a `.partial` archive before commit, and atomic promotion only after size and SHA256 match.
- [ ] Run `cd backend; go test ./internal/backuprepo` and confirm failure because the package is not implemented.
- [ ] Implement root canonicalization, directory creation with owner-only permissions where supported, `Lstat` checks at every managed path boundary, streaming SHA256, `fsync`, and same-directory rename from `dump.tar.partial` to `dump.tar`.
- [ ] Serialize `Prepare`, `Commit`, `Verify`, and `Delete` with a repository-root keyed in-process mutex plus an exclusive `.aifar-repository.lock` file. Reject insecure Linux root permissions/ownership and fail closed when the configured filesystem cannot provide the required lock and atomic rename semantics.
- [ ] Implement manifest and checksums writes through same-directory temporary files followed by `fsync` and rename. Ensure a failed commit leaves no final archive and removes panel-side partial files.
- [ ] Implement `Verify` so the record ID, record path, manifest backup ID, archive size, archive SHA256, and `checksums.txt` all agree before success.
- [ ] Implement deletion so only the exact verified managed backup directory is removed; do not follow symlinks and do not allow deletion of the root itself.
- [ ] Implement retention selection over successful records only, newest first, with `max(keepLast,1)` so the latest successful backup is never selected.
- [ ] Run `cd backend; go test ./internal/backuprepo` and confirm all repository tests pass.
- [ ] Commit: `git add backend/internal/backuprepo && git commit -m "feat: add controlled MySQL backup repository"`.

### Task 3: Add cancellable SSH download

**Files:**

- Create: `backend/internal/adapter/ssh_download.go`
- Create: `backend/internal/adapter/ssh_download_test.go`
- Modify: `backend/internal/adapter/ssh.go`
- Modify: `backend/internal/installer/installerkit/installerkit.go`

**Interfaces:**

```go
type DownloadResult struct {
    Size   int64
    SHA256 string
}

type FileDownloader interface {
    DownloadFile(ctx context.Context, server store.Server, remotePath, localPath string, mode os.FileMode) (DownloadResult, error)
}

func (SSHRemote) DownloadFile(ctx context.Context, server store.Server, remotePath, localPath string, mode os.FileMode) (DownloadResult, error)
```

- [ ] Add unit tests around an internal `copyDownload(ctx, dst, src)` helper for byte count, SHA256, short write, source error, destination error, and cancellation during streaming.
- [ ] Add path tests proving the remote command accepts only an internally generated absolute path under `/aifar/apps/mysql/_backup/<taskId>/`, rejects newline/NUL/traversal, verifies a regular non-symlink file, and never accepts a user-provided shell fragment.
- [ ] Run `cd backend; go test ./internal/adapter -run 'Test.*Download'` and confirm failure before implementation.
- [ ] Implement download over one SSH session, stream stdout directly to an already-created local `.partial` file opened with exclusive creation, set the requested mode, calculate SHA256 while copying, and remove the local partial on error or cancellation.
- [ ] Reuse existing SSH client/auth/context cancellation helpers. Capture only bounded stderr and return sanitized errors that contain server ID and operation, not credentials or command text.
- [ ] Add `installerkit.FileDownloader` as a separate optional interface; do not widen `installerkit.Remote`, so existing installer fakes remain valid.
- [ ] Run `cd backend; go test ./internal/adapter ./internal/installer/installerkit` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/adapter/ssh_download.go backend/internal/adapter/ssh_download_test.go backend/internal/adapter/ssh.go backend/internal/installer/installerkit/installerkit.go && git commit -m "feat: support controlled SSH downloads"`.

### Task 4: Add registry backup and restore lifecycles

**Files:**

- Modify: `backend/internal/apps/registry/contract.go`
- Create: `backend/internal/apps/registry/registry_test.go`
- Modify: `backend/internal/apps/mysql/module.go`
- Modify: `backend/internal/apps/mysql/service_test.go`
- Modify: `backend/internal/httpapi/api.go`

**Interfaces:**

```go
type BackupRequest struct {
    Instance      store.AppInstance
    Instances     []store.AppInstance
    Servers       []store.Server
    Language      string
    Actor         string
    RepositoryDir string
    KeepLast      int
    Parameters    map[string]any
}

type RestoreRequest struct {
    Instance      store.AppInstance
    Instances     []store.AppInstance
    Servers       []store.Server
    Backup        store.AppBackup
    Language      string
    Actor         string
    RepositoryDir string
    Parameters    map[string]any
}

type BackupModule interface {
    PlanBackup(context.Context, BackupRequest) ([]InstallStepPlan, error)
    Backup(context.Context, BackupRequest, RunContext) error
}

type RestoreModule interface {
    PlanRestore(context.Context, RestoreRequest) ([]InstallStepPlan, error)
    Restore(context.Context, RestoreRequest, RunContext) error
}
```

- [ ] Add compile-time registry tests with a minimal fake module proving backup and restore remain optional and do not alter the required `Module` interface.
- [ ] Add tests for request immutability and plan target/name/order preservation.
- [ ] Run `cd backend; go test ./internal/apps/registry` and confirm the contract tests fail before adding the interfaces.
- [ ] Add the request types and optional interfaces exactly as above. Keep `RepositoryDir` server-owned and reject it in request JSON. The decoder may accept a positive `keepLast` policy override; the handler validates it, falls back to `cfg.MySQLBackupKeepLast` when omitted, and only then assigns `BackupRequest.KeepLast`.
- [ ] Wire `cfg.MySQLBackupDir` and `cfg.MySQLBackupKeepLast` into requests when handlers are implemented, not into user parameters.
- [ ] Extend MySQL manifest capabilities with `apps.mysql.backup`, `apps.mysql.restore`, `apps.mysql.backup.verify`, and `apps.mysql.disaster-rebuild` only after its module implements the contracts.
- [ ] Update MySQL fakes only as required by later dedicated backup dependencies; do not make install/check depend on backup repository availability.
- [ ] Run `cd backend; go test ./internal/apps/registry ./internal/apps/mysql` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/apps/registry/contract.go backend/internal/apps/registry/registry_test.go backend/internal/apps/mysql/module.go backend/internal/apps/mysql/service_test.go backend/internal/httpapi/api.go && git commit -m "feat: define application backup restore lifecycles"`.

### Task 5: Implement MySQL backup manifest and safe script primitives

**Files:**

- Create: `backend/internal/apps/mysql/backup_types.go`
- Create: `backend/internal/apps/mysql/backup_manifest.go`
- Create: `backend/internal/apps/mysql/backup_manifest_test.go`
- Create: `backend/internal/apps/mysql/backup_scripts.go`
- Create: `backend/internal/apps/mysql/backup_scripts_test.go`
- Create: `backend/internal/apps/mysql/templates/backup/logical-backup.sh`
- Create: `backend/internal/apps/mysql/templates/backup/logical-restore.sh`
- Create: `backend/internal/apps/mysql/templates/backup/inspect.sql`
- Modify: `backend/internal/apps/mysql/i18n.go`
- Modify: `backend/internal/i18n/messages.go`

**Core models:**

```go
type BackupManifest struct {
    BackupID         string             `json:"backupId"`
    App              string             `json:"app"`
    Topology         string             `json:"topology"`
    InstanceID       string             `json:"instanceId"`
    ClusterID        string             `json:"clusterId,omitempty"`
    SourceServerID   string             `json:"sourceServerId"`
    SourceEndpoint   string             `json:"sourceEndpoint"`
    SourceServerUUID string             `json:"sourceServerUuid"`
    MySQLVersion     string             `json:"mysqlVersion"`
    MySQLShellVersion string            `json:"mysqlShellVersion"`
    Schemas          []string           `json:"schemas"`
    ExcludedSchemas  []string           `json:"excludedSchemas"`
    Consistent       bool               `json:"consistent"`
    GTIDExecuted     string             `json:"gtidExecuted"`
    Members          []ClusterMemberRef `json:"members,omitempty"`
    Routers          []RouterRef        `json:"routers,omitempty"`
    CreatedAt        time.Time          `json:"createdAt"`
    TaskID           string             `json:"taskId"`
}
```

- [ ] Write manifest tests that require `app=mysql`, one allowed topology, exact version fields, non-empty business schemas, fixed system-schema exclusion, `consistent=true`, and no secret-shaped keys or values in recursive JSON.
- [ ] Add compatibility tests: topology must match, MySQL full version must match in v1, backup type must be `logical-full` or `pre-restore`, and a cluster manifest requires cluster ID, source PRIMARY, three unique members, and all members `ONLINE`.
- [ ] Add snapshot tests for rendered backup script containing `consistent:true`, `users:false`, `showProgress:false`, zstd compression, explicit `mysql_innodb_cluster_metadata` exclusion, and no `rootPassword` interpolation.
- [ ] Add snapshot tests for rendered restore script containing `loadUsers:false`, `ignoreExistingObjects:false`, `skipBinlog:false`, `showProgress:false`, and a secret context file read rather than a command-line password.
- [ ] Run `cd backend; go test ./internal/apps/mysql -run 'Test(BackupManifest|RenderLogical)'` and confirm failure.
- [ ] Implement strict schema-name validation and reject system schemas before any dump/drop work. Sort schemas and members for deterministic manifests.
- [ ] Embed and render fixed templates. Accept only validated numeric threads/rate values and internally derived paths; do not render arbitrary strings into shell or JavaScript source.
- [ ] Add stable Chinese/English messages and error codes for every code in design section 15, including `MYSQL_CREDENTIAL_UNAVAILABLE` for any backup or restore target that cannot resolve exactly one active bound `purpose=admin` credential with a usable decrypted secret, plus topology, PRIMARY, checksum, version, maintenance, local-infile, restore-incomplete, and disaster-rebuild failures. Keep the credential response generic for missing, inactive, ambiguous, missing-secret, and decryption failures.
- [ ] Run `cd backend; go test ./internal/apps/mysql` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/apps/mysql/backup_types.go backend/internal/apps/mysql/backup_manifest.go backend/internal/apps/mysql/backup_manifest_test.go backend/internal/apps/mysql/backup_scripts.go backend/internal/apps/mysql/backup_scripts_test.go backend/internal/apps/mysql/templates/backup backend/internal/apps/mysql/i18n.go backend/internal/i18n/messages.go && git commit -m "feat: add MySQL backup manifest and scripts"`.

### Task 6: Deliver standalone backup through worker, API, and audit

**Files:**

- Create: `backend/internal/apps/mysql/backup.go`
- Create: `backend/internal/apps/mysql/backup_test.go`
- Create: `backend/internal/httpapi/mysql_backup_handlers.go`
- Create: `backend/internal/httpapi/mysql_backup_handlers_test.go`
- Modify: `backend/internal/httpapi/api.go`
- Modify: `backend/internal/httpapi/operation_lock_helpers.go`
- Modify: `backend/internal/httpapi/authz_test.go`

**Routes and task:**

```text
GET  /api/v2/apps/instances/{id}/backups
POST /api/v2/apps/instances/{id}/backup
task type: apps.mysql.backup
lock: app-instance/<instanceId>/mutate
```

- [ ] Add MySQL service tests for the exact standalone step sequence from design section 8 and a successful flow: load bound admin credential, inspect version/schemas/space, create `app_backups` pending/running, dump once, package once, download to `.partial`, commit repository files, mark success, run retention, and clean remote workdir.
- [ ] Add failure tests for missing/inactive/ambiguous credential, absent `FileDownloader`, mysqlsh failure, system schema discovery, insufficient source/panel space, transfer cancellation, checksum mismatch, and cleanup failure. Assert no final archive and a retained failed record.
- [ ] Run `cd backend; go test ./internal/apps/mysql -run 'TestBackupStandalone'` and confirm the tests fail.
- [ ] Implement `PlanBackup` and `Backup` on `mysql.Module`; resolve credential at execution time, derive `/aifar/apps/mysql/_backup/<taskId>/`, create no command from request text, and avoid logging secret material.
- [ ] Add handler tests for instance/app/topology validation, server-owned repository settings, `apps.manage`, task creation, persisted steps/target, operation-lock conflict `409`, audit `running`, and task SSE compatibility.
- [ ] Add `GET` list behavior: a standalone ID returns its records; a cluster member ID resolves all group member IDs and returns the cluster-level representative record once. Default excludes `deleted`.
- [ ] Mount both routes. Decode only `name`, `threads`, `maxRateMBps`, and `keepLast`; clamp validated values in the MySQL module and use config defaults when omitted.
- [ ] Start the worker with `RunContext{TaskID, Log, TargetLog, Concurrency}`; release locks on pre-start failure and rely on worker manager task-ID lock release after execution.
- [ ] Run `cd backend; go test ./internal/apps/mysql ./internal/httpapi -run 'Test.*MySQL.*Backup|TestBackupStandalone'` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/apps/mysql/backup.go backend/internal/apps/mysql/backup_test.go backend/internal/httpapi/mysql_backup_handlers.go backend/internal/httpapi/mysql_backup_handlers_test.go backend/internal/httpapi/api.go backend/internal/httpapi/operation_lock_helpers.go backend/internal/httpapi/authz_test.go && git commit -m "feat: back up standalone MySQL instances"`.

### Task 7: Add verification, controlled deletion, and retention completion

**Files:**

- Modify: `backend/internal/backuprepo/repository.go`
- Modify: `backend/internal/backuprepo/repository_test.go`
- Modify: `backend/internal/apps/mysql/backup.go`
- Modify: `backend/internal/apps/mysql/backup_test.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers_test.go`
- Modify: `backend/internal/httpapi/api.go`

**Routes:**

```text
POST   /api/v2/apps/backups/{backupId}/verify
DELETE /api/v2/apps/backups/{backupId}
task type: apps.mysql.backup.verify
```

- [ ] Add handler tests proving verify creates a worker task, saves `load-backup`, `verify-manifest`, `verify-checksum`, and `record-verification` steps, requires `apps.manage`, and audits task start.
- [ ] Add repository/service tests proving verify fails before MySQL contact for a missing file, path escape, manifest mismatch, altered archive, or altered checksums file.
- [ ] Add delete tests proving pending/running backups cannot be deleted, the latest remaining successful backup is protected unless another successful backup exists, files are deleted before the record becomes `deleted`, and file-deletion failure preserves the original record status.
- [ ] Run targeted tests and confirm failure: `cd backend; go test ./internal/backuprepo ./internal/httpapi -run 'Test.*Backup(Verify|Delete|Retention)'`.
- [ ] Implement verify inside the worker using `backuprepo.Repository.Verify`; update non-secret verification time/result in `app_backups.metadata` without changing archive identity.
- [ ] Implement synchronous DELETE with exact managed-path validation, operation-lock conflict protection, audit success/failure, and `MarkAppBackupDeleted` only after filesystem deletion succeeds.
- [ ] Finish post-success retention: verify each candidate still belongs to the repository, delete its files, mark it deleted, and log a warning without failing the newly completed backup when old cleanup fails.
- [ ] Run `cd backend; go test ./internal/backuprepo ./internal/apps/mysql ./internal/httpapi` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/backuprepo backend/internal/apps/mysql/backup.go backend/internal/apps/mysql/backup_test.go backend/internal/httpapi/mysql_backup_handlers.go backend/internal/httpapi/mysql_backup_handlers_test.go backend/internal/httpapi/api.go && git commit -m "feat: verify and retain MySQL backups"`.

### Task 8: Restore standalone MySQL with a local-infile safety guard

**Files:**

- Create: `backend/internal/apps/mysql/restore.go`
- Create: `backend/internal/apps/mysql/restore_test.go`
- Create: `backend/internal/apps/mysql/reconcile.go`
- Create: `backend/internal/apps/mysql/reconcile_test.go`
- Modify: `backend/internal/apps/mysql/module.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers_test.go`
- Modify: `backend/internal/httpapi/api.go`
- Modify: `backend/internal/httpapi/apps_handlers.go`

**Request and task:**

```json
{
  "backupId": "backup_xxx",
  "mode": "standalone",
  "maintenanceConfirmed": true,
  "createPreRestoreBackup": true,
  "disasterConfirmed": false,
  "threads": 4
}
```

```text
POST /api/v2/apps/instances/{id}/restore
task type: apps.mysql.restore
lock: app-instance/<instanceId>/mutate
```

- [ ] Add service tests for the exact section 9 plan, including repository verification before remote mutation, matching standalone/full version, mandatory pre-restore backup for readable targets, upload/extract, exact schema drop, `ignoreExistingObjects:false`, verification, and cleanup.
- [ ] Add `local_infile` tests for original OFF/ON values across success, dump-load failure, cancellation, and verification failure. Every reachable path must restore the original value.
- [ ] Add an unreachable-finally test proving the task returns `MYSQL_LOCAL_INFILE_RESTORE_FAILED`, writes exact non-secret `metadata.mysqlReconciliation={version:1,kind:"local_infile",originalValue:"ON|OFF",recordedAt:<UTC RFC3339>,taskId:<current task ID>}`, and does not report restore success. Unknown/malformed markers fail closed with `MYSQL_RECONCILIATION_REQUIRED`; successful reconciliation clears the whole marker only after verifying the recorded value. New restore work never emits the legacy alias `MYSQL_RESTORE_LOCAL_INFILE_RESTORE_FAILED`.
- [ ] Add reconciliation tests proving backup/restore/check first reads the marker, reconnects, restores the recorded original value, clears the marker only after verification, and otherwise returns `MYSQL_RECONCILIATION_REQUIRED` before lifecycle work.
- [ ] Run `cd backend; go test ./internal/apps/mysql -run 'Test(RestoreStandalone|LocalInfile|Reconcile)'` and confirm failure.
- [ ] Implement a `localInfileGuard` with explicit `Capture`, `Enable`, and idempotent `Restore` methods. Ensure deferred restoration uses a cleanup context with a bounded timeout rather than the already-cancelled task context.
- [ ] Implement restore state transitions in non-secret `app_backups.metadata`: `preflight`, `pre_restore_complete`, `schema_mutation_started`, `load_complete`, `verified`, or `restore_incomplete`. Do not auto-restore the pre-restore backup.
- [ ] Add handler tests for owner-only restore, missing maintenance confirmation, topology mismatch, task/plan/lock/audit creation, and rejection before mutation for a failed verification.
- [ ] Mount the restore route under owner authorization by adding a small `requireOwner` middleware using `rbac.NormalizeRole(currentUser(r).Role) == "owner"`; keep backup/list/verify under their documented permissions.
- [ ] Invoke reconciliation at the beginning of MySQL `Check`, `Backup`, and `Restore`. Add a focused regression test that existing check behavior remains unchanged when no marker exists.
- [ ] Run `cd backend; go test ./internal/apps/mysql ./internal/httpapi` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/apps/mysql/restore.go backend/internal/apps/mysql/restore_test.go backend/internal/apps/mysql/reconcile.go backend/internal/apps/mysql/reconcile_test.go backend/internal/apps/mysql/module.go backend/internal/httpapi/mysql_backup_handlers.go backend/internal/httpapi/mysql_backup_handlers_test.go backend/internal/httpapi/api.go backend/internal/httpapi/apps_handlers.go && git commit -m "feat: restore standalone MySQL safely"`.

### Task 9: Add InnoDB Cluster backup and healthy PRIMARY restore

**Files:**

- Modify: `backend/internal/apps/mysql/backup.go`
- Modify: `backend/internal/apps/mysql/backup_test.go`
- Create: `backend/internal/apps/mysql/cluster_restore.go`
- Create: `backend/internal/apps/mysql/cluster_restore_test.go`
- Modify: `backend/internal/apps/mysql/restore.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers_test.go`
- Modify: `backend/internal/httpapi/operation_lock_helpers.go`

**Cluster invariants:**

```text
backup source: current ONLINE PRIMARY, exactly one dump
restore target: current ONLINE PRIMARY, exactly one load
replication: skipBinlog=false
lock: app-cluster/<clusterId>/mutate
```

- [ ] Add cluster backup tests with three recorded members and runtime inspection proving all members must exist in metadata and report `ONLINE`, exactly one PRIMARY must exist, only PRIMARY receives dump commands, and manifest records members/roles/status plus Router summary without secrets.
- [ ] Add refusal tests for `RECOVERING`, `OFFLINE`, `ERROR`, split/missing PRIMARY, incomplete cluster membership, duplicate server UUID, or a representative instance outside the resolved cluster.
- [ ] Add healthy restore tests proving pre-restore creates one cluster-level backup, application writes are declared stopped, restore runs only on current PRIMARY, `skipBinlog:false` is rendered, all SECONDARY members return ONLINE after load, and Router read/write verification succeeds.
- [ ] Add failure tests proving member/Router verification failure results in `restore_incomplete`, keeps maintenance active, and never automatically loads the pre-restore backup.
- [ ] Run `cd backend; go test ./internal/apps/mysql -run 'Test(BackupInnoDBCluster|RestoreHealthyCluster)'` and confirm failure.
- [ ] Implement cluster resolution from `app_clusters` and `app_cluster_members`, then cross-check against MySQL `performance_schema.replication_group_members`; do not trust persisted roles for source selection.
- [ ] Use one representative `app_backups.instance_id`, source PRIMARY `server_id`, and cluster ID in metadata/manifest. Make list/retention operate over all member IDs but de-duplicate by backup ID.
- [ ] Build the section 10 and 11 target plans, including current PRIMARY discovery at execution time so a failover between request and task start does not target the stale node.
- [ ] Change cluster operation-lock helpers to use one cluster lock for check/start/delete/backup/restore. Add regression tests for lock conflicts across action names using the shared `mutate` operation.
- [ ] Run `cd backend; go test ./internal/apps/mysql ./internal/httpapi` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/apps/mysql/backup.go backend/internal/apps/mysql/backup_test.go backend/internal/apps/mysql/cluster_restore.go backend/internal/apps/mysql/cluster_restore_test.go backend/internal/apps/mysql/restore.go backend/internal/httpapi/mysql_backup_handlers.go backend/internal/httpapi/mysql_backup_handlers_test.go backend/internal/httpapi/operation_lock_helpers.go && git commit -m "feat: back up and restore healthy MySQL clusters"`.

### Task 10: Implement explicit disaster rebuild and preserve complete-outage semantics

**Files:**

- Create: `backend/internal/apps/mysql/disaster_rebuild.go`
- Create: `backend/internal/apps/mysql/disaster_rebuild_test.go`
- Create: `backend/internal/apps/mysql/templates/backup/disaster-rebuild.sh`
- Modify: `backend/internal/apps/mysql/restore.go`
- Modify: `backend/internal/apps/mysql/script.go`
- Modify: `backend/internal/apps/mysql/service_test.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers.go`
- Modify: `backend/internal/httpapi/mysql_backup_handlers_test.go`

**Mode boundary:**

```text
complete outage + intact data -> existing start-cluster task -> rebootClusterFromCompleteOutage
damaged/lost data + explicit disaster confirmation -> restore task mode disaster-rebuild
```

- [ ] Add regression tests proving `startMySQLCluster` still calls `dba.rebootClusterFromCompleteOutage()` and never reads, drops, or loads a backup.
- [ ] Add disaster tests requiring owner, maintenance confirmation, disaster confirmation, exactly three compatible MySQL nodes, a verified cluster backup, explicit target mapping, and per-server SSH password confirmation required by destructive lifecycle rules.
- [ ] Add plan tests for: stop Router; stop Group Replication; quarantine old data; initialize clean seed; restore seed with `skipBinlog:false`; verify seed; `dba.createCluster()`; clone B/C using `recoveryMethod:"clone"`; wait all ONLINE; re-bootstrap Router; verify 6446 read/write; record completion.
- [ ] Add failure-boundary tests: before quarantine leaves nodes untouched; seed load failure preserves quarantine and maintenance; clone failure preserves verified seed and stopped Router; Router failure preserves online database cluster but leaves restore incomplete.
- [ ] Run `cd backend; go test ./internal/apps/mysql ./internal/httpapi -run 'Test(CompleteOutage|DisasterRebuild)'` and confirm failure.
- [ ] Implement quarantine as an atomic rename to a task-scoped path after validating the exact MySQL data directory and mount boundary. Never delete old data in the disaster task.
- [ ] Reuse/refactor existing cluster bootstrap renderers for `dba.createCluster()` and clone joins; keep JavaScript generated only from validated internal host/port/cluster-name values.
- [ ] Stop and re-bootstrap recorded MySQL Router instances only after the seed and members are healthy. Store non-secret progress markers so retry resumes at the first incomplete member rather than reloading the seed.
- [ ] Ensure disaster restore metadata records quarantine paths, member progress, Router progress, source backup ID, and task ID but no SSH/MySQL secret.
- [ ] Run `cd backend; go test ./internal/apps/mysql ./internal/httpapi` and confirm all tests pass.
- [ ] Commit: `git add backend/internal/apps/mysql/disaster_rebuild.go backend/internal/apps/mysql/disaster_rebuild_test.go backend/internal/apps/mysql/templates/backup/disaster-rebuild.sh backend/internal/apps/mysql/restore.go backend/internal/apps/mysql/script.go backend/internal/apps/mysql/service_test.go backend/internal/httpapi/mysql_backup_handlers.go backend/internal/httpapi/mysql_backup_handlers_test.go && git commit -m "feat: rebuild MySQL clusters from backup"`.

### Task 11: Add the Database backup and restore user experience

**Files:**

- Create: `web/src/database/mysqlBackup.ts`
- Create: `web/src/database/mysqlBackup.test.ts`
- Create: `web/src/database/MySQLBackupDialog.vue`
- Create: `web/src/database/MySQLBackupDrawer.vue`
- Create: `web/src/database/MySQLRestoreDialog.vue`
- Create: `web/src/database/MySQLDisasterRebuildDialog.vue`
- Modify: `web/src/views/DatabaseView.vue`
- Modify: `web/src/i18n/messages.ts`

**Frontend contract:**

```ts
export type MySQLBackupStatus = 'pending' | 'running' | 'success' | 'failed' | 'deleted'
export type MySQLRestoreMode = 'standalone' | 'healthy-cluster' | 'disaster-rebuild'

export interface MySQLBackupRecord {
  id: string
  instanceId: string
  serverId: string
  backupType: 'logical-full' | 'pre-restore'
  status: MySQLBackupStatus
  checksum: string
  size: number
  metadata: MySQLBackupMetadata
  createdAt: string
  completedAt?: string
}
```

- [ ] Write failing Vitest tests for JSON metadata parsing, cluster de-duplication, operation availability by topology/status/permission, default backup parameters, restore impact text, and task response tracking.
- [ ] Run `pnpm test:web -- mysqlBackup` and confirm the new tests fail.
- [ ] Implement typed API helpers using existing `apiGet`, `apiPost`, and `apiDelete`; never accept or display filesystem paths or secrets returned by backend records.
- [ ] Add row/group actions in `DatabaseView.vue`: **立即备份**, **备份记录**, **校验备份**, and **恢复数据**, with corresponding English messages. Use the current group model so a cluster operation is launched once for the logical cluster.
- [ ] Implement a 520px backup dialog with name, threads, per-thread rate, and keep-last; defaults come from backend settings/list response and submission tracks the returned task via the existing task progress store.
- [ ] Implement a 736px backup drawer showing source, version, topology, schemas, time, size, checksum, verification result, and task link. Deleted records are hidden by default.
- [ ] Implement a 736px restore dialog that shows source/target compatibility and impact, requires maintenance confirmation, defaults pre-restore backup to enabled, and disables submission until backend-compatible state is present.
- [ ] Implement a separate owner-only disaster dialog showing three target nodes, quarantine behavior, Router impact, backup identity/checksum, maintenance/disaster confirmations, and per-server SSH password confirmation. Do not reuse the ordinary restore submit button for disaster mode.
- [ ] Follow `design/ant-design-system-portable202606.md`: 24px content spacing, 32px standard actions, semantic status colors, modal/drawer widths above, responsive single-column fallback, keyboard focus, and no color-only status meaning.
- [ ] Run `pnpm test:web -- mysqlBackup` and `pnpm web:build`; fix all type, test, and build failures.
- [ ] Commit: `git add web/src/database web/src/views/DatabaseView.vue web/src/i18n/messages.ts && git commit -m "feat: add MySQL backup restore workspace"`.

### Task 12: Document operations and run release-quality verification

**Files:**

- Create: `docs/mysql-backup-restore-runbook.md`
- Modify: `README.md`
- Modify: `memory.md`

**Runbook contents:**

- Configuration and mount ownership for `AIFAR_MYSQL_BACKUP_DIR`.
- Backup/verify/restore API and UI workflow.
- Difference between healthy restore, complete-outage start, and disaster rebuild.
- Maintenance-window responsibilities and `restore_incomplete` handling.
- `MYSQL_RECONCILIATION_REQUIRED` diagnosis and safe reconciliation.
- Retention, off-host/NAS recommendation, capacity monitoring, and test restore cadence.
- Manual three-node acceptance matrix from design section 17.2 with evidence fields.

- [ ] Write the runbook and link it from the README operations section. Include copyable non-secret examples and explicitly state that repository storage must not exist only on a MySQL node.
- [ ] Run focused backend verification: `pnpm test`.
- [ ] Run focused frontend verification: `pnpm test:web` and `pnpm web:build`.
- [ ] Run script/release-config verification because `config/defaults.env` changed: `pnpm test:scripts`.
- [ ] Run the complete local gate once at final convergence: `pnpm test:local`.
- [ ] Inspect `git diff --check`, `git status --short`, and `git diff --stat`; confirm no credentials, generated archives, `.partial` files, database files, or unrelated user changes are staged.
- [ ] Execute the manual acceptance matrix on one standalone and one disposable three-node MySQL 8.0.36 environment. Record task IDs, backup IDs, checksums, member states, Router 6446 read/write result, `local_infile` before/after values, RPO/RTO, and cleanup status without recording secrets.
- [ ] Append the reusable implementation result and verification status to `memory.md`; keep `memory.md` out of the feature commit unless repository policy for the executing session explicitly includes it.
- [ ] Commit documentation only: `git add docs/mysql-backup-restore-runbook.md README.md && git commit -m "docs: add MySQL backup restore runbook"`.

## Final Review Gate

- [ ] Compare every requirement in design sections 2, 4-17 against at least one implementation file and one automated or manual test.
- [ ] Search for unfinished code markers: `rg -n "unfinished|stub|not implemented|pending implementation" backend web docs/mysql-backup-restore-runbook.md` and resolve every feature-related hit.
- [ ] Search for secret leakage: `rg -n "rootPassword|mysqlPassword|AIFAR_DEFAULT_PASSWORD" backend/internal/apps/mysql backend/internal/httpapi/mysql_backup_handlers.go`; every remaining match must be an input mapping/test fixture and never a command/log/manifest formatter.
- [ ] Verify route, task type, step name, backup status, restore mode, and stable error-code strings match across Go, TypeScript, i18n, tests, and the runbook.
- [ ] Confirm the branch contains only intentional commits and is not pushed unless the user explicitly requests publication.
