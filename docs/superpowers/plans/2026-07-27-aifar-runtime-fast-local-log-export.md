# AIFAR Runtime Fast Local Log Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the existing Docker-plus-file remote diagnostic archive flow with a fast host-mounted-log-only flow that filters by record time on the target server and streams the final archive directly into controlled local storage on `aifar-server`.

**Architecture:** The target server discovers safe files below `<installRoot>/runtime/logs/<service>`, performs one-pass record parsing, time filtering and redaction into a controlled temporary directory, then emits a bounded protocol header followed by `tar.gz` bytes on SSH stdout. `aifar-server` reserves quota in SQLite, streams into a local `0600` partial file while hashing and enforcing the 256 MiB limit, atomically promotes it, and serves subsequent download/delete/expiry operations locally. Legacy records with `storage_kind=remote` retain their existing remote download and cleanup behavior until deletion or expiry.

**Tech Stack:** Go 1.24, Chi, SQLite, `golang.org/x/crypto/ssh`, POSIX shell plus GNU awk on openEuler, Vue 3, TypeScript, Element Plus, Pinia task SSE, Vitest.

## Global Constraints

- New exports use only host-mounted logs under `<installRoot>/runtime/logs/<service>`; never call `docker logs`, inspect Docker log paths, or create `container-logs/`.
- Preserve non-log diagnostics: runtime summary, deployments, pods, containers, health checks, agent status, host resources and release summary.
- Interpret explicit `Z`/offset timestamps as written and naive timestamps in the target server timezone captured at task start; filter record blocks with `[since, until)`.
- Skip unrecognized timestamps and orphan continuations fail-closed, aggregate stable warning codes into the archive, and never place skipped raw text or sensitive paths in task/audit output.
- Support Spring/Java, ISO 8601, JSON `timestamp`/`time`/`@timestamp`/`ts`, Nginx access and Nginx error timestamps, including documented multiline Java continuations.
- Enforce 1 GiB per source file, 2 GiB total candidate scan, 500 MiB filtered/uncompressed content, 256 MiB final archive, 15-minute task timeout and 24-hour request window.
- Store new archives under `AIFAR_DIAGNOSTIC_EXPORT_DIR=data/diagnostic-exports`, retain for `AIFAR_DIAGNOSTIC_EXPORT_RETENTION_HOURS=24`, and enforce `AIFAR_DIAGNOSTIC_EXPORT_QUOTA_BYTES=5368709120`.
- Local directory permissions are `0700`; partial and final archive permissions are `0600`; paths must be server-generated relative paths constrained below the configured root.
- Do not update `aifar-agent`; the agent may be offline. Continue to require an `agent-runtime-v2` instance and use saved SSH access.
- All mutations continue through worker tasks and audit. Task progress remains SSE-driven; do not add browser polling.
- Keep `/api/v2` endpoints and `{ code, message, details }` errors backward compatible.
- Preserve legacy remote export records until they expire or are deleted; never create another remote export after this change.
- All backend and frontend user-visible text must have Chinese and English translations.
- Do not run `pnpm package`, `pnpm release:verify`, push, or include offline resources in any commit.

---

## File and Interface Map

The implementation uses these responsibility boundaries:

- `backend/internal/config/config.go` and `config/defaults.env`: local export root, retention and quota configuration.
- `backend/internal/store/{models,diagnostic_exports,migrations}.go`: durable storage kind/path, quota reservation and atomic lifecycle transitions.
- `backend/internal/apps/aifar/runtime_diagnostic_storage.go`: safe local filesystem repository, partial writer, hash/size verification and reconciliation.
- `backend/internal/adapter/ssh.go`: bounded stdout command streaming with cancellation and bounded stderr.
- `backend/internal/apps/aifar/templates/runtime-diagnostics-{estimate,export,filter,cleanup}.*`: target-side metadata estimate, record filtering, archive stream and temporary cleanup.
- `backend/internal/apps/aifar/runtime_diagnostics{,_protocol}.go`: request validation, estimate composition, streaming orchestration, local/legacy dispatch and worker steps.
- `backend/internal/apps/aifar/runtime_diagnostic_cleaner.go`: local expiry plus legacy remote cleanup and startup reconciliation.
- `backend/internal/httpapi/aifar_runtime_diagnostics.go`: unchanged routes with updated estimate fields, steps and stable errors.
- `web/src/containers/runtime/{types,api,runtimeDiagnostics,AifarRuntimeDiagnosticsPanel}.ts/.vue`: host-log wording, limit/capacity estimate and local-record actions.

The new cross-task interfaces are defined once and reused exactly:

```go
type RuntimeDiagnosticArchiveStorage interface {
    Stats(context.Context) (RuntimeDiagnosticStorageStats, error)
    Begin(exportID, archiveName string) (RuntimeDiagnosticArchiveSink, error)
    Open(relativePath string) (*os.File, error)
    Remove(relativePath string) error
    RemovePartial(exportID string) error
    Reconcile(context.Context, time.Time) (RuntimeDiagnosticReconcileResult, error)
}

type RuntimeDiagnosticArchiveSink interface {
    io.Writer
    Commit() (RuntimeDiagnosticLocalArtifact, error)
    Abort() error
}

type RuntimeDiagnosticLocalArtifact struct {
    RelativePath string
    ArchiveName  string
    Size         int64
    SHA256       string
}

type RuntimeDiagnosticCommandStreamer interface {
    StreamCommand(context.Context, store.Server, string, io.Writer) (adapter.CommandStreamResult, error)
}
```

---

### Task 1: Add Diagnostic Export Configuration

**Files:**
- Modify: `backend/internal/config/config.go`
- Create: `backend/internal/config/diagnostic_exports_test.go`
- Modify: `config/defaults.env`
- Create: `scripts/release-defaults.test.mjs`

**Interfaces:**
- Consumes: existing `getenv`, `getenvInt` and `getenvInt64` helpers.
- Produces: `Config.DiagnosticExportDir string`, `Config.DiagnosticExportRetentionHours int`, and `Config.DiagnosticExportQuotaBytes int64` for Tasks 3, 6 and 7.

- [ ] **Step 1: Write failing configuration tests**

```go
func TestLoadDiagnosticExportDefaultsFollowDatabaseDirectory(t *testing.T) {
    t.Setenv("AIFAR_DATABASE_PATH", filepath.Join(t.TempDir(), "control", "aifar.db"))
    t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_DIR", "")
    t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_RETENTION_HOURS", "")
    t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_QUOTA_BYTES", "")

    cfg := Load()
    if want := filepath.Join(filepath.Dir(cfg.DatabasePath), "diagnostic-exports"); cfg.DiagnosticExportDir != want {
        t.Fatalf("DiagnosticExportDir=%q want %q", cfg.DiagnosticExportDir, want)
    }
    if cfg.DiagnosticExportRetentionHours != 24 || cfg.DiagnosticExportQuotaBytes != int64(5*1024*1024*1024) {
        t.Fatalf("unexpected diagnostic defaults: retention=%d quota=%d", cfg.DiagnosticExportRetentionHours, cfg.DiagnosticExportQuotaBytes)
    }
}

func TestLoadDiagnosticExportOverrides(t *testing.T) {
    t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_DIR", filepath.Join(t.TempDir(), "exports"))
    t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_RETENTION_HOURS", "12")
    t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_QUOTA_BYTES", "1073741824")
    cfg := Load()
    if cfg.DiagnosticExportRetentionHours != 12 || cfg.DiagnosticExportQuotaBytes != int64(1073741824) {
        t.Fatalf("unexpected diagnostic overrides: retention=%d quota=%d", cfg.DiagnosticExportRetentionHours, cfg.DiagnosticExportQuotaBytes)
    }
}
```

- [ ] **Step 2: Run the focused test and confirm the missing fields fail compilation**

Run from `backend/`:

```text
go test ./internal/config -run DiagnosticExport -count=1
```

Expected: FAIL because the three `Config` fields do not exist.

- [ ] **Step 3: Add fields and defaults**

Add to `Config`:

```go
DiagnosticExportDir            string `json:"diagnosticExportDir"`
DiagnosticExportRetentionHours int    `json:"diagnosticExportRetentionHours"`
DiagnosticExportQuotaBytes     int64  `json:"diagnosticExportQuotaBytes"`
```

Load them with:

```go
DiagnosticExportDir: getenv(
    "AIFAR_DIAGNOSTIC_EXPORT_DIR",
    filepath.Join(filepath.Dir(databasePath), "diagnostic-exports"),
),
DiagnosticExportRetentionHours: getenvInt("AIFAR_DIAGNOSTIC_EXPORT_RETENTION_HOURS", 24),
DiagnosticExportQuotaBytes:     getenvInt64("AIFAR_DIAGNOSTIC_EXPORT_QUOTA_BYTES", 5*1024*1024*1024),
```

Add the three documented defaults to the maintenance section of `config/defaults.env`. Extend `scripts/release-defaults.test.mjs` so release defaults must contain exact non-secret values `data/diagnostic-exports`, `24`, and `5368709120`.

- [ ] **Step 4: Run configuration and release-default tests**

Run:

```text
go test ./internal/config -run DiagnosticExport -count=1
pnpm test:scripts
```

Expected: PASS, with the release configuration validator accepting the new non-secret defaults.

- [ ] **Step 5: Commit the configuration slice**

```text
git add backend/internal/config/config.go backend/internal/config/diagnostic_exports_test.go config/defaults.env scripts/release-defaults.test.mjs
git commit -m "feat: configure local diagnostic exports"
```

---

### Task 2: Migrate Diagnostic Export Storage Metadata and Reservations

**Files:**
- Modify: `backend/internal/store/models.go`
- Modify: `backend/internal/store/migrations.go`
- Modify: `backend/internal/store/diagnostic_exports.go`
- Modify: `backend/internal/store/diagnostic_exports_test.go`
- Modify: `backend/internal/store/store_test.go`

**Interfaces:**
- Consumes: existing `diagnostic_exports` table and lifecycle states.
- Produces: `StorageKind`, `StorageRelativePath`, `ReservedBytes`, transactional reservation methods and atomic local-ready/failure methods for Tasks 3, 6 and 7.

- [ ] **Step 1: Write failing migration and legacy compatibility tests**

Add tests that open a database at migration `2026072701`, seed a remote row, reopen with current migrations, and assert:

```go
if got.StorageKind != "remote" || got.StorageRelativePath != "" || got.ReservedBytes != 0 || got.RemoteRelativePath != "diag-1/archive.tar.gz" {
    t.Fatalf("legacy migration mismatch: %+v", got)
}
```

Add a fresh local record test that persists `StorageKind: "local"` and a generated relative path without changing `RemoteRelativePath`.

- [ ] **Step 2: Run migration tests and confirm they fail**

Run from `backend/`:

```text
go test ./internal/store -run 'DiagnosticExport.*(Migration|Storage|Legacy)' -count=1
```

Expected: FAIL because the model and migration fields do not exist.

- [ ] **Step 3: Add the forward migration and model fields**

Add migration version `2026072702`:

```sql
alter table diagnostic_exports add column storage_kind text not null default 'remote';
alter table diagnostic_exports add column storage_relative_path text not null default '';
alter table diagnostic_exports add column reserved_bytes integer not null default 0;
create index if not exists diagnostic_exports_storage_kind_status
  on diagnostic_exports(storage_kind, status, expires_at);
```

Extend `DiagnosticExport`:

```go
StorageKind         string `json:"storageKind"`
StorageRelativePath string `json:"-"`
ReservedBytes       int64  `json:"-"`
```

Keep `RemoteRelativePath` unchanged for legacy rows. Normalize storage kind to `remote` or `local`; reject negative archive/reserved sizes and reject a local `ready` record without a storage-relative path.

- [ ] **Step 4: Write failing transactional lifecycle tests**

Cover these exact transitions:

```go
usage, err := s.ReserveDiagnosticExportBytes("diag-1", 256<<20, 5<<30)
if err != nil || usage.ReservedBytes != int64(256<<20) {
    t.Fatalf("reserve: usage=%+v err=%v", usage, err)
}

_, err = s.ReserveDiagnosticExportBytes("diag-2", 256<<20, 300<<20)
if !errors.Is(err, store.ErrDiagnosticExportQuotaExceeded) {
    t.Fatalf("reserve over quota error=%v", err)
}

ready, err := s.CommitLocalDiagnosticExport(store.LocalDiagnosticExportCommit{
    ID: "diag-1", StorageRelativePath: "diag-1/aifar-diagnostics-i-1.tar.gz",
    ArchiveName: "aifar-diagnostics-i-1.tar.gz", ArchiveBytes: 1024,
    UncompressedBytes: 4096, SHA256: strings.Repeat("a", 64),
    WarningCount: 2, Warnings: []string{"timestamp-unrecognized"},
    ReadyAt: now, ExpiresAt: now.Add(24*time.Hour),
})
if err != nil || ready.ReservedBytes != 0 || ready.Status != "ready" {
    t.Fatalf("commit local export: ready=%+v err=%v", ready, err)
}
```

Also prove that concurrent reservation transactions cannot exceed the quota, and `MarkDiagnosticExportFailed`/`MarkDiagnosticExportDeleted` release reservations.

- [ ] **Step 5: Implement atomic store methods**

Add exact types and methods:

```go
var ErrDiagnosticExportQuotaExceeded = errors.New("diagnostic export quota exceeded")

type DiagnosticExportStorageUsage struct {
    ReadyBytes    int64
    ReservedBytes int64
    QuotaBytes    int64
}

type LocalDiagnosticExportCommit struct {
    ID, StorageRelativePath, ArchiveName, SHA256 string
    ArchiveBytes, UncompressedBytes              int64
    WarningCount                                 int
    Warnings                                     []string
    ReadyAt, ExpiresAt                           time.Time
}

func (s *Store) ReserveDiagnosticExportBytes(id string, bytes, quota int64) (DiagnosticExportStorageUsage, error)
func (s *Store) ReleaseDiagnosticExportReservation(id string) (bool, error)
func (s *Store) CommitLocalDiagnosticExport(v LocalDiagnosticExportCommit) (DiagnosticExport, error)
func (s *Store) MarkDiagnosticExportFailed(id, errorText string, failedAt time.Time) (bool, error)
func (s *Store) ListDiagnosticExportsForReconcile() ([]DiagnosticExport, error)
```

`ReserveDiagnosticExportBytes` must use one SQLite transaction, sum non-deleted local `archive_bytes` plus all local `reserved_bytes`, and conditionally reserve only a `pending`/`building` row. `CommitLocalDiagnosticExport` updates only a local `building` row, clears its reservation and writes all final metadata in one transaction.

- [ ] **Step 6: Run focused and full store tests**

Run from `backend/`:

```text
go test ./internal/store -run DiagnosticExport -count=1
go test ./internal/store -count=1
```

Expected: PASS, including concurrent quota reservation and old-row migration.

- [ ] **Step 7: Commit the durable lifecycle slice**

```text
git add backend/internal/store/models.go backend/internal/store/migrations.go backend/internal/store/diagnostic_exports.go backend/internal/store/diagnostic_exports_test.go backend/internal/store/store_test.go
git commit -m "feat: persist local diagnostic export lifecycle"
```

---

### Task 3: Build the Safe Local Archive Repository

**Files:**
- Create: `backend/internal/apps/aifar/runtime_diagnostic_storage.go`
- Create: `backend/internal/apps/aifar/runtime_diagnostic_storage_test.go`
- Create: `backend/internal/apps/aifar/runtime_diagnostic_disk_unix.go`
- Create: `backend/internal/apps/aifar/runtime_diagnostic_disk_windows.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`

**Interfaces:**
- Consumes: Task 2 local export records and reservations.
- Produces: `RuntimeDiagnosticArchiveStorage`, `RuntimeDiagnosticArchiveSink`, `RuntimeDiagnosticStorageStats` and safe local artifacts used by Tasks 6–8.

- [ ] **Step 1: Write failing path, permission and atomicity tests**

Create table-driven tests for:

```go
func TestLocalDiagnosticStorageRejectsUnsafeIdentity(t *testing.T)
func TestLocalDiagnosticStorageRejectsSymlinkRootOrEntry(t *testing.T)
func TestLocalDiagnosticStorageCreates0700RootAnd0600Files(t *testing.T)
func TestLocalDiagnosticSinkCommitsByAtomicRenameAndSHA256(t *testing.T)
func TestLocalDiagnosticSinkAbortRemovesPartial(t *testing.T)
func TestLocalDiagnosticStorageRejectsArchiveAbove256MiB(t *testing.T)
func TestLocalDiagnosticSinkRejectsInvalidOrUnsafeTarGzip(t *testing.T)
```

Use small injected limits in tests instead of allocating 256 MiB. Build a tiny valid tar.gz in memory for the success test. Assert the final path is exactly `<root>/<exportID>/<archiveName>`, no `.partial` remains after commit/abort, and `Open` refuses `..`, absolute paths and symlinks. Invalid gzip, absolute tar entries, `../` entries, symlink/hardlink/device entries, multiple top-level roots, or missing `README.txt`, `manifest.json`, and `collection-errors.txt` must fail before promotion.

- [ ] **Step 2: Run the focused test and confirm the package is missing**

Run from `backend/`:

```text
go test ./internal/apps/aifar -run LocalDiagnosticStorage -count=1
```

Expected: FAIL because the storage types do not exist.

- [ ] **Step 3: Implement the storage interfaces and bounded sink**

Use these exact public-within-package types:

```go
type RuntimeDiagnosticStorageStats struct {
    RootAvailableBytes int64 `json:"rootAvailableBytes"`
    ReadyBytes         int64 `json:"readyBytes"`
    ExpiredReadyBytes  int64 `json:"expiredReadyBytes"`
    ReservedBytes      int64 `json:"reservedBytes"`
    QuotaBytes         int64 `json:"quotaBytes"`
    ReservationBytes   int64 `json:"reservationBytes"`
}

type RuntimeDiagnosticLocalArtifact struct {
    RelativePath string
    ArchiveName  string
    Size         int64
    SHA256       string
}

type runtimeDiagnosticRecordLister interface {
    ListDiagnosticExportsForReconcile() ([]store.DiagnosticExport, error)
}

func NewRuntimeDiagnosticArchiveStorage(
    root string,
    quotaBytes int64,
    retention time.Duration,
    records runtimeDiagnosticRecordLister,
) RuntimeDiagnosticArchiveStorage
```

The sink must create `<exportID>/<archiveName>.partial` with `O_CREATE|O_EXCL|O_WRONLY` and mode `0600`, write through `io.MultiWriter(file, sha256.New())`, reject any write that would cross its injected `maxArchiveBytes`, call `Sync`, and close. Before rename, reopen the partial with `compress/gzip` and `archive/tar`, scan every header, enforce one expected top-level directory, regular-file/directory entry types only, safe relative names, and the three required control files. Then rename to the same name without `.partial` and sync the containing directory on Unix. A failed validation/commit calls `Abort` and never leaves a final file. Capacity checks require the 256 MiB reservation plus a named `64<<20` filesystem safety margin.

Use `golang.org/x/sys/unix.Statfs` and `golang.org/x/sys/windows.GetDiskFreeSpaceEx` in build-tagged helpers. Promote `golang.org/x/sys` from indirect to direct dependency without changing its version.

- [ ] **Step 4: Write failing reconciliation tests**

Seed a temporary root with:

- one referenced local ready archive;
- one stale `.partial` older than one hour;
- one young `.partial`;
- one orphan final archive older than the 24-hour retention;
- one path represented by a symlink.

Assert `Reconcile(ctx, now)` deletes only the stale partial and old validated orphan, leaves the referenced and young files, refuses the symlink, and returns structured counts/warnings without absolute paths.

- [ ] **Step 5: Implement reconciliation and local removal**

Implement:

```go
type RuntimeDiagnosticReconcileResult struct {
    RemovedPartials int
    RemovedOrphans  int
    MissingReadyIDs []string
    WarningCodes    []string
}
```

The repository receives the current record set through `runtimeDiagnosticRecordLister`. It validates every generated name, uses `Lstat`, never follows a symlink, and only removes an unreferenced final archive after it is older than the configured retention. `Remove` is idempotent only for a validated path below root.

- [ ] **Step 6: Run storage tests**

Run from `backend/`:

```text
go test ./internal/apps/aifar -run 'LocalDiagnostic(Storage|Sink)|DiagnosticReconcile' -count=1
```

Expected: PASS on Windows and Linux; permission assertions use platform-appropriate checks.

- [ ] **Step 7: Commit the local repository slice**

```text
git add backend/internal/apps/aifar/runtime_diagnostic_storage.go backend/internal/apps/aifar/runtime_diagnostic_storage_test.go backend/internal/apps/aifar/runtime_diagnostic_disk_unix.go backend/internal/apps/aifar/runtime_diagnostic_disk_windows.go backend/go.mod backend/go.sum
git commit -m "feat: add safe local diagnostic archive storage"
```

---

### Task 4: Add Controlled SSH Command Streaming

**Files:**
- Modify: `backend/internal/adapter/ssh.go`
- Modify: `backend/internal/adapter/ssh_test.go`

**Interfaces:**
- Consumes: existing `streamSSHOutputWithContext`, bounded stderr and SSH cancellation helpers.
- Produces: `adapter.CommandStreamResult` and `SSHRemote.StreamCommand`, consumed by Task 6 through a narrow type assertion.

- [ ] **Step 1: Write failing command-stream tests**

Add focused helper tests that do not require a live SSH server:

```go
func TestStreamSSHCommandOutputCopiesBinaryAndReturnsBoundedStderr(t *testing.T)
func TestStreamSSHCommandOutputCancelsWaitAndWriter(t *testing.T)
func TestStreamSSHCommandOutputPropagatesDestinationWriteFailure(t *testing.T)
func TestStreamSSHCommandOutputRejectsEmptyCommandOrNilWriter(t *testing.T)
```

Use an `io.Pipe`, a fake wait function and a writer that fails after N bytes. Confirm cancellation invokes the cancel callback once and stderr never exceeds `sshStreamStderrLimit`.

- [ ] **Step 2: Run the focused test and confirm the API is missing**

Run from `backend/`:

```text
go test ./internal/adapter -run 'SSHCommandOutput|StreamSSHCommand' -count=1
```

Expected: FAIL because `CommandStreamResult` and `StreamCommand` do not exist.

- [ ] **Step 3: Implement the narrow adapter method**

Add:

```go
type CommandStreamResult struct {
    Bytes  int64
    Stderr string
}

func (SSHRemote) StreamCommand(ctx context.Context, server store.Server, command string, dst io.Writer) (CommandStreamResult, error) {
    return StreamSSHCommand(ctx, server, command, dst)
}
```

`StreamSSHCommand` opens one session, attaches `StdoutPipe`, attaches `newBoundedSSHStderr(8*1024)`, calls `session.Start(command)`, and delegates copying/cancellation to the existing streaming helper. Wrap errors with `streamSSHError` so stderr is log-masked. Do not expose this through HTTP and do not accept a user-provided command.

- [ ] **Step 4: Run adapter tests**

Run from `backend/`:

```text
go test ./internal/adapter -run 'SSH(Command|Output|Stream)' -count=1
go test ./internal/adapter -count=1
```

Expected: PASS with binary bytes preserved, bounded errors and context cancellation.

- [ ] **Step 5: Commit the SSH transport slice**

```text
git add backend/internal/adapter/ssh.go backend/internal/adapter/ssh_test.go
git commit -m "feat: stream controlled ssh command output"
```

---

### Task 5: Replace Remote Collection with Host-Log Record Filtering

**Files:**
- Modify: `backend/internal/apps/aifar/templates/runtime-diagnostics-estimate.sh`
- Modify: `backend/internal/apps/aifar/templates/runtime-diagnostics-export.sh`
- Create: `backend/internal/apps/aifar/templates/runtime-diagnostics-filter.awk`
- Modify: `backend/internal/apps/aifar/templates/runtime-diagnostics-cleanup.sh`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics_protocol.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics_test.go`
- Create: `backend/internal/apps/aifar/testdata/runtime-diagnostics/spring.log`
- Create: `backend/internal/apps/aifar/testdata/runtime-diagnostics/iso-json.log`
- Create: `backend/internal/apps/aifar/testdata/runtime-diagnostics/nginx-access.log`
- Create: `backend/internal/apps/aifar/testdata/runtime-diagnostics/nginx-error.log`
- Create: `backend/internal/apps/aifar/testdata/runtime-diagnostics/unknown.log`

**Interfaces:**
- Consumes: target filesystem and fixed request parameters.
- Produces: metadata estimate records and a binary stream beginning with one bounded `AIFAR_DIAG_STREAM_V1` header, consumed by Task 6.

- [ ] **Step 1: Write failing source-selection and no-Docker tests**

Update script rendering tests to require:

```go
for _, forbidden := range []string{"docker logs", ".LogPath", "container-logs", "docker-log-conservative"} {
    if strings.Contains(estimateScript+exportScript, forbidden) {
        t.Fatalf("rendered scripts contain forbidden source %q", forbidden)
    }
}
for _, required := range []string{
    `LOG_ROOT="$INSTALL_ROOT/runtime/logs"`,
    "MAX_FILE_SCAN=1073741824",
    "MAX_TOTAL_SCAN=2147483648",
    "MAX_FILTERED=524288000",
    "AIFAR_DIAG_STREAM_V1",
} {
    if !strings.Contains(estimateScript+exportScript, required) {
        t.Fatalf("rendered scripts missing %q", required)
    }
}
```

Assert the file allowlist accepts `.log`, `.log.1` and `.log.2026-07-27.1`, but rejects `.idx`, hidden files, config/database/secret names and symlinks.

- [ ] **Step 2: Write failing timestamp fixture tests**

Add a Linux-capable fixture harness that renders the embedded awk program and runs the available GNU awk. Skip only when GNU awk is genuinely absent; Linux CI must run it. Verify exact output for:

- Spring comma and dot fractions;
- ISO `Z` and `+08:00` offsets;
- JSON fields `timestamp`, `time`, `@timestamp`, `ts`;
- Nginx access and error formats;
- target timezone `Asia/Shanghai` for naive timestamps;
- `[since, until)` boundary inclusion/exclusion;
- Java `Caused by`, `Suppressed`, `at ...`, `... N more` and indented continuations;
- ordinary unknown lines and orphan continuations skipped with warning counts;
- an unterminated active-file tail deferred with `active-tail-deferred`.

The fixture runner compares filtered content and a tab-separated summary:

```text
AIFAR_DIAG_FILTER_V1	<parser>	<scanned-bytes>	<filtered-bytes>	<records>	<warnings>
```

- [ ] **Step 3: Run the focused script tests and confirm old behavior fails**

Run from `backend/`:

```text
go test ./internal/apps/aifar -run 'RuntimeDiagnostic.*(HostLog|Timestamp|Filter|Rotation|NoDocker)' -count=1
```

Expected: FAIL because the current scripts use mtime upper bounds, copy whole files and call Docker logs.

- [ ] **Step 4: Implement the fast metadata-only estimate protocol**

The estimate script must inspect only safe file metadata and print:

```text
AIFAR_DIAG_SERVICE_V2	<service>	<candidate-files>	<candidate-scan-bytes>
AIFAR_DIAG_TOTAL_V2	<files>	<candidate-scan-bytes>	<server-timezone>	<block-code-or-dash>
```

It must not inspect Docker log paths or read file contents. A source file above 1 GiB sets `file-scan-limit-exceeded`; a total above 2 GiB sets `total-scan-limit-exceeded`. Return these as structured block codes rather than truncating or performing a content scan. Capture a reliable timezone from `timedatectl show -p Timezone --value`, falling back to the canonical `/etc/localtime` zone path; fail if neither yields a stable zone.

- [ ] **Step 5: Implement the GNU awk record filter**

Embed `runtime-diagnostics-filter.awk` through `templateFS`. Its core contract is:

```awk
function flush_record() {
  if (record_epoch >= since_epoch && record_epoch < until_epoch) {
    printf "%s", record_text
    filtered_records++
  }
  record_text = ""
}

function warn(code) {
  warning_count++
  warning_codes[code]++
}
```

Parser functions return epoch plus a parser name. Use GNU awk `mktime(..., 1)` for explicit UTC/offset conversion, local `mktime(...)` under the captured server `TZ` for naive timestamps, and a fixed English month map for Nginx access logs. Only documented continuation patterns join the current record. At EOF, flush only when the initial-size snapshot ended in newline; otherwise defer the last buffered record and increment `active-tail-deferred`.

- [ ] **Step 6: Rewrite the export script around filtered temporary files**

The export script must:

1. Create only `<installRoot>/runtime/diagnostics/<exportID>.partial/` with `umask 077`.
2. Discover allowed regular files below each selected service without following symlinks.
3. Capture device, inode and initial size before reading; enforce 1 GiB per file and 2 GiB total.
4. Feed exactly the first initial-size bytes into the awk filter without persisting a raw copy.
5. Recheck device/inode after reading and record a warning if the source changed.
6. Pipe awk output directly through the redactor into `services/<service>/<relative-path>` so the server-relative log tree is preserved without a synthetic directory and no unredacted filtered log is persisted.
7. Enforce a cumulative 500 MiB filtered-content limit as a fatal error.
8. Preserve the non-log diagnostic files from the approved archive layout.
9. Build `manifest.json`, `README.txt` and `collection-errors.txt` without absolute paths or raw skipped content. A time window with zero matching log records is successful and still produces these three files plus available non-log diagnostics.
10. Scan the staged bundle with the versioned secret patterns before compression; a detected credential is fatal and its value is never logged.
11. Print one header and stream the archive:

```text
AIFAR_DIAG_STREAM_V1\t<archive-name>\t<uncompressed-bytes>\t<warning-count>\t<timezone>\n
<binary tar.gz bytes>
```

12. Use an `EXIT` trap to remove the remote partial directory. The independent cleanup script remains available for cancellation recovery and removes only `<exportID>.partial`, never a final remote archive for new exports.

The generated tree is exact:

```text
aifar-diagnostics-<instance>-<timestamp>/
  README.txt
  manifest.json
  collection-errors.txt
  services/<service>/<relative-log-path>
  diagnostics/runtime-summary.json
  diagnostics/deployments.json
  diagnostics/pods.json
  diagnostics/containers.txt
  diagnostics/health-checks.txt
  diagnostics/agent-status.txt
  diagnostics/host-resources.txt
  diagnostics/release-summary.json
```

Each manifest source record contains the service, safe relative source path, device/inode snapshot identity, initial bytes, scanned bytes, filtered bytes, SHA256, selected parser and warning codes. The manifest root contains the format version, `[since, until)` values, captured server timezone, selected services, configured hard limits, redaction-rule version and generation time. It never contains final archive SHA256 because that value is computed after the archive is closed and stored in SQLite.

- [ ] **Step 7: Implement bounded protocol parsers**

Replace the old remote-final-result parser with:

```go
type runtimeDiagnosticStreamHeader struct {
    ArchiveName      string
    UncompressedBytes int64
    WarningCount     int
    ServerTimezone   string
}

func parseRuntimeDiagnosticStreamHeader(line string) (runtimeDiagnosticStreamHeader, error)
```

The service will read at most 4096 bytes through `bufio.Reader.ReadString('\n')`; reject missing LF, extra fields, unsafe archive names, negative counts, a timezone outside `^[A-Za-z0-9_+.-]+(?:/[A-Za-z0-9_+.-]+)*$`, and unsupported protocol versions. Update estimate parsing to V2 fields with `CandidateFiles`, `CandidateScanBytes` and `ServerTimezone`.

- [ ] **Step 8: Run script/protocol tests**

Run from `backend/`:

```text
go test ./internal/apps/aifar -run 'RuntimeDiagnostic.*(Protocol|Estimate|HostLog|Timestamp|Filter|Rotation|NoDocker)' -count=1
```

Expected: PASS. On Linux the GNU awk fixtures must execute; on Windows rendering/protocol tests pass and the environment-dependent fixture test reports a skip only if GNU awk is unavailable.

- [ ] **Step 9: Commit the target-side collection slice**

```text
git add backend/internal/apps/aifar/templates/runtime-diagnostics-estimate.sh backend/internal/apps/aifar/templates/runtime-diagnostics-export.sh backend/internal/apps/aifar/templates/runtime-diagnostics-filter.awk backend/internal/apps/aifar/templates/runtime-diagnostics-cleanup.sh backend/internal/apps/aifar/service.go backend/internal/apps/aifar/runtime_diagnostics.go backend/internal/apps/aifar/runtime_diagnostics_protocol.go backend/internal/apps/aifar/runtime_diagnostics_test.go backend/internal/apps/aifar/testdata/runtime-diagnostics
git commit -m "feat: filter host runtime logs by record time"
```

---

### Task 6: Stream Exports into aifar-server Local Storage

**Files:**
- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/apps/aifar/module.go`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics_test.go`
- Modify: `backend/internal/httpapi/api.go`
- Modify: `backend/cmd/aifar-server/main.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Consumes: Tasks 1–5 configuration, storage, store transitions, SSH command stream and stream header.
- Produces: local export creation/download/delete behavior while keeping the existing `RuntimeDiagnosticsModule` HTTP-facing methods.

- [ ] **Step 1: Write failing service orchestration tests**

Use a fake command streamer that writes `AIFAR_DIAG_STREAM_V1` plus gzip bytes in chunks and a fake archive storage. Cover:

```go
func TestExportRuntimeDiagnosticsStreamsIntoLocalStorage(t *testing.T)
func TestExportRuntimeDiagnosticsRejectsArchiveAbove256MiBAndCleansBothSides(t *testing.T)
func TestExportRuntimeDiagnosticsCancellationAbortsLocalPartialAndRemoteWork(t *testing.T)
func TestExportRuntimeDiagnosticsDeletesFinalFileWhenReadyCommitFails(t *testing.T)
func TestEstimateRuntimeDiagnosticsCombinesRemoteMetadataAndLocalCapacity(t *testing.T)
func TestStreamRuntimeDiagnosticExportReadsLocalArchiveWithoutSSH(t *testing.T)
func TestDeleteRuntimeDiagnosticExportRemovesLocalArchiveWithoutSSH(t *testing.T)
func TestLegacyRemoteDiagnosticExportStillStreamsAndDeletesRemotely(t *testing.T)
```

Assert new-export fake commands never call `StreamFile`, `docker logs` or remote-final-path cleanup.

- [ ] **Step 2: Run focused service tests and confirm the old remote flow fails**

Run from `backend/`:

```text
go test ./internal/apps/aifar -run 'RuntimeDiagnostic.*(Local|Stream|Capacity|Legacy|Cancellation)' -count=1
```

Expected: FAIL because `Service` has no local archive storage and still promotes the archive remotely.

- [ ] **Step 3: Inject configured archive storage through registry dependencies**

Extend `registry.Dependencies` with configuration values, not a concrete application import:

```go
DiagnosticExportDir            string
DiagnosticExportRetentionHours int
DiagnosticExportQuotaBytes     int64
```

Pass them from both `httpapi.NewWithRealtime` and the collector registry construction in `main.go`. In the AIFAR factory, construct the repository exactly as follows, then pass it to the module:

```go
archives := NewRuntimeDiagnosticArchiveStorage(
    deps.DiagnosticExportDir,
    deps.DiagnosticExportQuotaBytes,
    time.Duration(deps.DiagnosticExportRetentionHours)*time.Hour,
    deps.Store,
)
```

Use these constructors:

```go
func NewModuleWithDiagnosticStorage(s Store, remote Remote, archives RuntimeDiagnosticArchiveStorage) Module
func NewServiceWithDiagnosticStorage(s Store, remote Remote, archives RuntimeDiagnosticArchiveStorage) Service
```

Keep `NewModule` and `NewService` as test/backward-compatible wrappers that create no archive storage; diagnostic methods must return the existing storage-missing localized error when it is absent.

- [ ] **Step 4: Replace estimate fields and enforce local preflight**

Change registry result types to:

```go
type RuntimeDiagnosticEstimateResult struct {
    Services             []RuntimeDiagnosticServiceEstimate `json:"services"`
    LogSource            string                             `json:"logSource"`
    CandidateFiles       int                                `json:"candidateFiles"`
    CandidateScanBytes   int64                              `json:"candidateScanBytes"`
    EstimatedSecondsMin  int                                `json:"estimatedSecondsMin"`
    EstimatedSecondsMax  int                                `json:"estimatedSecondsMax"`
    MaxFileScanBytes     int64                              `json:"maxFileScanBytes"`
    MaxTotalScanBytes    int64                              `json:"maxTotalScanBytes"`
    MaxFilteredBytes     int64                              `json:"maxFilteredBytes"`
    MaxArchiveBytes      int64                              `json:"maxArchiveBytes"`
    TimeoutSeconds       int                                `json:"timeoutSeconds"`
    ServerTimezone       string                             `json:"serverTimezone"`
    LocalAvailableBytes  int64                              `json:"localAvailableBytes"`
    LocalReadyBytes      int64                              `json:"localReadyBytes"`
    LocalReservedBytes   int64                              `json:"localReservedBytes"`
    LocalQuotaBytes      int64                              `json:"localQuotaBytes"`
    ExpiresAt            time.Time                          `json:"expiresAt"`
    Allowed              bool                               `json:"allowed"`
    BlockReason          string                             `json:"blockReason,omitempty"`
    Warnings             []string                           `json:"warnings,omitempty"`
}
```

Per-service estimates contain `Service`, `CandidateFiles` and `CandidateScanBytes` only. Set `LogSource` to `host-mounted`. Calculate a conservative duration range from candidate bytes using named constants and clamp the upper bound to 15 minutes; never present it as an exact promise.

For admission, compute projected quota as `ReadyBytes - ExpiredReadyBytes + ReservedBytes + 256 MiB`; compute projected filesystem headroom as `RootAvailableBytes + ExpiredReadyBytes - 256 MiB`. Allow only when projected quota is at most the configured quota and projected headroom remains at least the `64 MiB` safety margin. Expired bytes are reclaimable only for this projection; the export worker must successfully delete them before reserving or streaming.

- [ ] **Step 5: Implement the seven-step local export transaction**

Replace `runtimeDiagnosticSteps` with:

```go
var runtimeDiagnosticSteps = []string{
    "validate-local-storage",
    "discover-log-files",
    "filter-and-redact",
    "build-manifest",
    "stream-local-archive",
    "verify-local-archive",
    "cleanup-remote",
}
```

Keep `runtimeDiagnosticEstimateTimeout=30*time.Second`. At export execution:

1. Wrap the job context with `context.WithTimeout(ctx, 15*time.Minute)`.
2. Under the export worker and audit trail, delete expired local archives and stale partials, but never an unexpired ready archive; then recompute capacity.
3. Transition the row to local `building` and reserve `256<<20` bytes transactionally. Require real filesystem free space of at least the reservation plus `64<<20` safety bytes.
4. Render the approved remote export command and call `StreamCommand` into an `io.Pipe`.
5. Read/validate the 4096-byte header before beginning the local sink.
6. Copy remaining binary bytes to the bounded sink; the sink computes final size/SHA256.
7. Commit the file, then call `CommitLocalDiagnosticExport` with a 24-hour expiry.
8. If the database commit fails, remove the final file before returning failure.
9. In every terminal path, use an independent short cleanup context for the remote partial, abort local partials and release the reservation.

Log only stable localized messages and warning counts. Do not include the remote command, absolute paths, header bytes or log content.

- [ ] **Step 6: Branch download/delete by storage kind**

For `storage_kind=local`:

- `StreamRuntimeDiagnosticExport` validates the relative path, opens the local file, copies exactly `ArchiveBytes`, checks the stored SHA256 while copying, and marks `DownloadedAt`.
- `DeleteRuntimeDiagnosticExport` marks cleanup pending, removes the local file idempotently, then marks deleted.

For `storage_kind=remote`, retain the current `StreamFile` and remote cleanup implementation. No code path may convert an old remote row into local storage implicitly.

- [ ] **Step 7: Add stable localized errors and step titles**

Add zh/en text for scan limit, filtered limit, archive limit, local quota, local disk, stream header, timeout, local commit, local checksum and remote cleanup failures. The suggested stable API-facing codes are:

```text
RUNTIME_DIAGNOSTIC_SCAN_LIMIT_EXCEEDED
RUNTIME_DIAGNOSTIC_FILTERED_LIMIT_EXCEEDED
RUNTIME_DIAGNOSTIC_ARCHIVE_LIMIT_EXCEEDED
RUNTIME_DIAGNOSTIC_LOCAL_QUOTA_EXCEEDED
RUNTIME_DIAGNOSTIC_LOCAL_DISK_INSUFFICIENT
RUNTIME_DIAGNOSTIC_STREAM_FAILED
RUNTIME_DIAGNOSTIC_TIMEOUT
RUNTIME_DIAGNOSTIC_LOCAL_COMMIT_FAILED
```

- [ ] **Step 8: Run service and registry tests**

Run from `backend/`:

```text
go test ./internal/apps/aifar ./internal/apps/registry -run RuntimeDiagnostic -count=1
```

Expected: PASS for local flow, failure cleanup, legacy compatibility and the seven task steps.

- [ ] **Step 9: Commit the orchestration slice**

```text
git add backend/internal/apps/registry/contract.go backend/internal/apps/aifar/module.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/runtime_diagnostics.go backend/internal/apps/aifar/runtime_diagnostics_test.go backend/internal/httpapi/api.go backend/cmd/aifar-server/main.go backend/internal/i18n/messages.go
git commit -m "feat: store runtime diagnostics on aifar server"
```

---

### Task 7: Convert Cleanup to Local-First Retention and Startup Reconciliation

**Files:**
- Modify: `backend/internal/apps/aifar/runtime_diagnostic_cleaner.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostic_cleaner_test.go`
- Modify: `backend/cmd/aifar-server/main.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Consumes: Tasks 2, 3 and 6 storage kind/path, local repository and legacy branch.
- Produces: startup reconciliation plus hourly expiry that deletes local files without SSH and remote legacy files with SSH.

- [ ] **Step 1: Write failing local cleanup and recovery tests**

Add:

```go
func TestRuntimeDiagnosticCleanerDeletesExpiredLocalWithoutSSH(t *testing.T)
func TestRuntimeDiagnosticCleanerKeepsNonExpiredLocalWhenQuotaIsFull(t *testing.T)
func TestRuntimeDiagnosticCleanerUsesSSHOnlyForLegacyRemote(t *testing.T)
func TestRuntimeDiagnosticCleanerReconcilesStalePartialsAndMissingReadyFiles(t *testing.T)
func TestRuntimeDiagnosticCleanerReleasesInterruptedReservations(t *testing.T)
```

Assert the cleaner never silently removes an unexpired ready archive to make quota space. A missing local ready file becomes non-downloadable with a stable cleanup error and no arbitrary filesystem access.

- [ ] **Step 2: Run cleaner tests and confirm remote-only logic fails**

Run from `backend/`:

```text
go test ./internal/apps/aifar -run RuntimeDiagnosticCleaner -count=1
```

Expected: FAIL because the current cleaner always loads a server and runs remote cleanup.

- [ ] **Step 3: Inject archive storage into the cleaner**

Change the constructor to:

```go
func NewRuntimeDiagnosticCleaner(
    s *store.Store,
    tasks *worker.Manager,
    remote Remote,
    archives RuntimeDiagnosticArchiveStorage,
) *RuntimeDiagnosticCleaner
```

Construct the same configured local repository in `main.go`. Run `Reconcile` once after interrupted task/lock recovery and before starting the hourly cleaner. Startup reconciliation errors are warnings and must not log absolute sensitive paths.

- [ ] **Step 4: Implement local-first cleanup branching**

In `cleanupOne`:

```go
switch export.StorageKind {
case "local":
    err = c.archives.Remove(export.StorageRelativePath)
case "remote":
    err = c.cleanupLegacyRemote(ctx, export)
default:
    err = errors.New(i18n.Text("", "aifar.diag.storageKindInvalid"))
}
```

Retain operation locks, cleanup pending/failed/complete states and audit. Mark an expired ready row before deletion. Release stale reservations belonging to terminal or missing tasks during reconciliation. Cleaner task steps become `mark-expired`, `delete-local-or-legacy-artifacts`, `record-cleanup`.

- [ ] **Step 5: Run cleaner and server tests**

Run from `backend/`:

```text
go test ./internal/apps/aifar ./cmd/aifar-server -run 'RuntimeDiagnosticCleaner|DiagnosticReconcile' -count=1
```

Expected: PASS, including no-SSH local deletion and SSH-only legacy deletion.

- [ ] **Step 6: Commit cleanup and recovery**

```text
git add backend/internal/apps/aifar/runtime_diagnostic_cleaner.go backend/internal/apps/aifar/runtime_diagnostic_cleaner_test.go backend/cmd/aifar-server/main.go backend/internal/i18n/messages.go
git commit -m "feat: reconcile and expire local diagnostics"
```

---

### Task 8: Update HTTP Contracts, Steps and Error Mapping

**Files:**
- Modify: `backend/internal/httpapi/aifar_runtime_diagnostics.go`
- Modify: `backend/internal/httpapi/aifar_runtime_diagnostics_test.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime_test.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Consumes: Task 6 registry estimate/result types and local-aware module behavior.
- Produces: backward-compatible endpoints with new JSON fields, local archive validation and stable errors for Task 9.

- [ ] **Step 1: Write failing estimate and task-plan HTTP tests**

Assert `POST /diagnostics/estimate` returns:

```json
{
  "logSource": "host-mounted",
  "candidateScanBytes": 1048576,
  "maxFileScanBytes": 1073741824,
  "maxTotalScanBytes": 2147483648,
  "maxFilteredBytes": 524288000,
  "maxArchiveBytes": 268435456,
  "timeoutSeconds": 900,
  "localQuotaBytes": 5368709120,
  "allowed": true
}
```

Assert create stores `StorageKind: "local"` with no remote path and saves exactly the seven step names from Task 6. In the fake worker execution, assert `ReserveDiagnosticExportBytes` occurs before `StreamCommand`. When preflight rejects, `details.blockReason` and the relevant capacity/limit fields are present.

- [ ] **Step 2: Write failing local download/delete tests**

Cover:

- local ready download streams from the module, includes Content-Length and `X-AIFAR-Diagnostic-SHA256`, and never requires an online agent;
- local path mismatch and missing file fail before headers;
- interrupted response does not mark downloaded or enqueue delete-after-download;
- full response with `deleteAfterDownload=true` enqueues the existing audited delete worker;
- manual local delete has steps `validate-export`, `delete-local-or-legacy-archive`, `record-deletion`;
- legacy remote record remains downloadable/deletable.

- [ ] **Step 3: Run HTTP tests and confirm current fields/steps fail**

Run from `backend/`:

```text
go test ./internal/httpapi -run 'RuntimeDiagnostic|AIFARRuntime' -count=1
```

Expected: FAIL because current responses expose Docker byte estimates and remote task steps.

- [ ] **Step 4: Update creation, validation and error mapping**

Replace old `fileBytes/containerBytes/requiredBytes/availableBytes` rejection details with the Task 6 estimate. Keep status codes:

- `400` for invalid time/service/protocol input;
- `409` for hard-limit, quota, disk, operation-lock and state conflicts;
- `410` for expired archives;
- `500` for local commit/storage failures.

Map typed service/store errors to the stable codes from Task 6 while preserving `{ code, message, details }`. Do not return raw `os.PathError`, SSH stderr or remote commands.

- [ ] **Step 5: Update download validation**

Validation branches by `StorageKind`:

```go
case "local":
    require non-empty StorageRelativePath, safe ArchiveName, positive size, valid SHA256
case "remote":
    retain validateRuntimeDiagnosticRelativePath(export.ID, export.RemoteRelativePath, export.ArchiveName)
```

Headers remain unchanged. Once headers are sent, only audit incomplete byte counts; never attempt a JSON error body.

- [ ] **Step 6: Run HTTP tests**

Run from `backend/`:

```text
go test ./internal/httpapi -run 'RuntimeDiagnostic|AIFARRuntime' -count=1
```

Expected: PASS with local and legacy branches covered.

- [ ] **Step 7: Commit the HTTP contract slice**

```text
git add backend/internal/httpapi/aifar_runtime_diagnostics.go backend/internal/httpapi/aifar_runtime_diagnostics_test.go backend/internal/httpapi/containers_aifar_runtime_test.go backend/internal/i18n/messages.go
git commit -m "feat: expose local runtime diagnostic exports"
```

---

### Task 9: Update the Runtime Diagnostic UI for Host Logs and Local Storage

**Files:**
- Read first: `design/ant-design-system-portable202606.md`
- Modify: `web/src/containers/runtime/types.ts`
- Modify: `web/src/containers/runtime/api.ts`
- Modify: `web/src/containers/runtime/api.test.ts`
- Modify: `web/src/containers/runtime/runtimeDiagnostics.ts`
- Modify: `web/src/containers/runtime/runtimeDiagnostics.test.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue`
- Modify: `web/src/containers/runtime/runtime.css`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: Task 8 JSON fields and unchanged API routes.
- Produces: a single-instance host-log export workflow with metadata estimate, local-record actions and SSE progress.

- [ ] **Step 1: Read the design guide completely before editing Vue/CSS**

Run:

```text
Get-Content -LiteralPath 'design/ant-design-system-portable202606.md' -Encoding UTF8
```

Expected: spacing, panel, table, alert, form, button and responsive conventions are understood before any `web/src` modification.

- [ ] **Step 2: Write failing TypeScript contract tests**

Replace Docker estimate expectations with:

```ts
const estimate: RuntimeDiagnosticEstimate = {
  services: [{ service: 'gateway', candidateFiles: 2, candidateScanBytes: 1024 }],
  logSource: 'host-mounted',
  candidateFiles: 2,
  candidateScanBytes: 1024,
  estimatedSecondsMin: 5,
  estimatedSecondsMax: 20,
  maxFileScanBytes: 1073741824,
  maxTotalScanBytes: 2147483648,
  maxFilteredBytes: 524288000,
  maxArchiveBytes: 268435456,
  timeoutSeconds: 900,
  serverTimezone: 'Asia/Shanghai',
  localAvailableBytes: 10_000_000_000,
  localReadyBytes: 1024,
  localReservedBytes: 0,
  localQuotaBytes: 5368709120,
  expiresAt: '2026-07-28T08:00:00Z',
  allowed: true
}
```

Add `storageKind: 'local' | 'remote'` to `RuntimeDiagnosticExport`. Assert API URLs remain identical and `deleteAfterDownload` defaults to false.

- [ ] **Step 3: Run focused frontend tests and confirm old types fail**

Run:

```text
pnpm test:web
```

Expected: FAIL because current types require Docker/file byte fields.

- [ ] **Step 4: Update types and pure UI helpers**

Update `RuntimeDiagnosticEstimate` to match Task 8 exactly. Add:

```ts
export function runtimeDiagnosticLimitRows(estimate: RuntimeDiagnosticEstimate) {
  return [
    { key: 'file', value: estimate.maxFileScanBytes },
    { key: 'scan', value: estimate.maxTotalScanBytes },
    { key: 'filtered', value: estimate.maxFilteredBytes },
    { key: 'archive', value: estimate.maxArchiveBytes }
  ]
}

export function runtimeDiagnosticCapacityBlocked(estimate: RuntimeDiagnosticEstimate) {
  return !estimate.allowed && [
    'local-quota-exceeded', 'local-disk-insufficient', 'scan-limit-exceeded'
  ].includes(estimate.blockReason || '')
}
```

Keep existing request/export fingerprints, terminal-task refresh and `{ polling: false }` tracking unchanged.

- [ ] **Step 5: Replace panel content and actions**

The panel must visibly show:

- “日志来源：宿主机挂载日志” / “Log source: host-mounted logs”.
- candidate file count and candidate scan bytes;
- 1 GiB, 2 GiB, 500 MiB, 256 MiB and 15-minute hard limits;
- estimated duration as a range, target timezone, local free bytes, used/reserved/quota bytes and expiry;
- an actionable block reason that recommends selecting fewer services or a narrower time range;
- records with storage kind, archive size, warning count, created time and expiry.

Remove all Docker conservative estimate text and all wording that says download/delete operates on a remote archive. Keep create/delete task tracking through SSE and never add timers or polling.

- [ ] **Step 6: Add exact zh/en translations**

Replace `diagnosticsContainerBytes`, `diagnosticsRequiredBytes`, Docker conservative text and remote-delete wording with keys for:

```text
diagnosticsLogSourceHost
diagnosticsCandidateFiles
diagnosticsCandidateScanBytes
diagnosticsEstimatedDuration
diagnosticsServerTimezone
diagnosticsLocalAvailable
diagnosticsLocalUsage
diagnosticsLocalReserved
diagnosticsLocalQuota
diagnosticsExpiresAt
diagnosticsFastLimits
diagnosticsMaxFileScan
diagnosticsMaxTotalScan
diagnosticsMaxFiltered
diagnosticsMaxArchive
diagnosticsTimeout
diagnosticsStorageLocal
diagnosticsStorageLegacyRemote
diagnosticsSplitSuggestion
```

Both languages must state that estimates are conservative ranges and final output is enforced during export.

- [ ] **Step 7: Run focused tests and build**

Run:

```text
pnpm test:web
pnpm web:build
```

Expected: all Vitest tests, Vue type checking and Vite build PASS.

- [ ] **Step 8: Commit the frontend slice**

```text
git add web/src/containers/runtime/types.ts web/src/containers/runtime/api.ts web/src/containers/runtime/api.test.ts web/src/containers/runtime/runtimeDiagnostics.ts web/src/containers/runtime/runtimeDiagnostics.test.ts web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue web/src/containers/runtime/runtime.css web/src/i18n/messages.ts
git commit -m "feat: show host log diagnostic exports"
```

---

### Task 10: Complete Cross-Layer Verification and Controlled Acceptance

**Files:**
- Modify only if a verification failure proves a defect: files from Tasks 1–9.
- Update after implementation: `memory.md` with a concise reusable conclusion and no credentials.

**Interfaces:**
- Consumes: all prior tasks.
- Produces: verified implementation evidence with no offline package and no push.

- [ ] **Step 1: Run focused backend suites**

Run from `backend/`:

```text
go test ./internal/config ./internal/store ./internal/adapter ./internal/apps/aifar ./internal/apps/registry ./internal/httpapi -count=1
```

Expected: PASS with no skipped GNU awk fixture on Linux CI.

- [ ] **Step 2: Run all backend tests**

Run from repository root:

```text
pnpm test
```

Expected: all Go packages PASS.

- [ ] **Step 3: Run race-sensitive packages where supported**

Run from `backend/`:

```text
go test -race ./internal/worker ./internal/store ./internal/apps/aifar
```

Expected: PASS on Linux with the race detector. If the Windows toolchain cannot run it, record the exact toolchain limitation and require the Linux CI job to pass before release.

- [ ] **Step 4: Run frontend, script and build gates**

Run from repository root:

```text
pnpm test:web
pnpm test:scripts
pnpm web:build
pnpm backend:build
```

Expected: frontend tests, script contracts, Vue/Vite production build and Linux/Windows amd64 backend builds all PASS.

- [ ] **Step 5: Verify repository and feature invariants**

Run:

```text
rg -n "docker logs|container-logs|\.LogPath|diagnosticsContainerBytes" backend/internal/apps/aifar web/src/containers/runtime web/src/i18n/messages.ts
git diff --check
git status --short
```

Expected: the first command has no production-flow matches; any retained occurrence is confined to explicit negative regression tests or legacy documentation. `git diff --check` emits nothing. Preserve unrelated user changes, especially the existing uncommitted `memory.md`, and do not run package/release commands.

- [ ] **Step 6: Perform controlled real-server acceptance after explicit authorization**

On the authorized openEuler target, use transient in-memory credentials and verify:

1. Estimate reports `host-mounted`, the target timezone, candidate scan bytes, 2 GiB total scan limit, 256 MiB archive limit and local `aifar-server` capacity.
2. A boundary fixture or known log interval proves `since` is included and `until` is excluded.
3. Spring, ISO/JSON and Nginx records are included correctly; multiline Java exceptions remain intact.
4. Unknown-timestamp lines are absent and warning codes/counts appear in `manifest.json` and `collection-errors.txt`.
5. The archive preserves each selected service's server-relative paths directly below `services/<service>/`, with no synthetic `file-logs/` or `container-logs/` directory.
6. The final archive exists only under the configured `aifar-server` directory; the target has no final archive.
7. SHA256 and byte size match the database/download headers.
8. Cancellation and forced stream failure leave no target `.partial`, local `.partial` or quota reservation.
9. Manual delete removes the local file; a seeded legacy remote record still follows its old cleanup path.
10. Archive inspection finds no credentials, private keys, `.env` bodies, tokens or absolute sensitive paths.

- [ ] **Step 7: Record evidence and commit only proven fixes**

If verification exposes a defect, first add a failing regression test, run it to observe failure, implement the minimal fix, and rerun the affected focused suite plus all gates above. Review the exact changed paths, interactively stage only the regression test and its fix, then commit:

```text
git diff --name-only
git add -p
git commit -m "fix: harden local runtime diagnostic export"
```

Do not create an empty verification commit. Do not push.
