# AIFAR Runtime Diagnostic Export Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不修改 `aifar-agent` 和业务微服务的前提下，为当前 AIFAR Runtime 实例提供任务化、可审计、可取消、24 小时自动清理的一键故障诊断包导出。

**Architecture:** `aifar-server` 通过 worker 和 SSH 执行 `go:embed` 的受信脚本，在目标服务器受控目录完成预估、日志采集、脱敏、清单生成和 `tar.gz` 压缩。SQLite 保存导出生命周期，下载使用二进制安全 SSH 流直接转发给浏览器；前端在 Runtime 日志页提供预估、创建、状态、下载和删除入口。

**Tech Stack:** Go 1.24、Chi、SQLite (`modernc.org/sqlite`)、`golang.org/x/crypto/ssh`、Vue 3、TypeScript、Element Plus、Pinia、Vitest、现有 worker/SSE/i18n 基础设施。

## Global Constraints

- 不修改或升级 `aifar-agent`，不修改 Java/Web 微服务。
- API 前缀保持 `/api/v2`，错误响应保持 `{ code, message, details }`。
- 客户端不得提交服务器路径、容器名、Shell 或命令片段；路径和命令只能由服务端从实例数据与固定模板生成。
- 默认时间为最近 2 小时，支持自定义起止时间；只允许选择期望副本数大于 0 的服务。
- 未压缩采集量硬上限为 `3 GiB`，压缩归档硬上限为 `1 GiB`，保留期固定为 `24h`。
- 单服务、单容器或单诊断项失败时继续生成；归档、清单、上限或 SHA256 失败时整个导出失败且不保留半成品。
- worker 只使用现有稳定状态 `pending/running/success/failed/cancelled`；“有告警”由导出记录 `warningCount` 表达。
- 创建、查询、下载和删除要求 `apps.manage`；创建、下载、删除和自动清理必须写审计。
- 所有新增用户可见文案必须同时加入后端和前端 zh/en i18n。
- 远端测试必须使用 fake remote，不连接真实 SSH/Docker。
- 修改 `web/src` 前先完整阅读 `design/ant-design-system-portable202606.md`。
- 保留工作区现有无关修改；每次提交只暂存本任务列出的文件。
- 用户已要求不要离线打包；本计划不得运行 `pnpm package`、`pnpm test:local` 或 `pnpm release:verify`。

---

## File Map

### Backend persistence

- `backend/internal/store/models.go`：新增诊断导出领域模型和分页响应。
- `backend/internal/store/migrations.go`：新增版本化 `diagnostic_exports` 表和索引。
- `backend/internal/store/diagnostic_exports.go`：导出记录 CRUD、分页和过期清理查询。
- `backend/internal/store/diagnostic_exports_test.go`：迁移、状态、过期和重启恢复测试。

### SSH transport

- `backend/internal/adapter/ssh.go`：新增取消感知的二进制文件流读取。
- `backend/internal/adapter/ssh_test.go`：二进制完整性、写入失败和取消测试。

### AIFAR diagnostic domain

- `backend/internal/apps/registry/contract.go`：新增 Runtime diagnostics 可选模块契约。
- `backend/internal/apps/aifar/service.go`：嵌入诊断脚本模板并声明流式 remote 能力。
- `backend/internal/apps/aifar/module.go`：把 registry 请求委托给诊断 service。
- `backend/internal/apps/aifar/runtime_diagnostics.go`：校验、预估、导出、取消清理、下载和删除编排。
- `backend/internal/apps/aifar/runtime_diagnostics_protocol.go`：固定行协议类型、渲染数据和解析器。
- `backend/internal/apps/aifar/runtime_diagnostics_test.go`：fake remote 驱动的领域测试。
- `backend/internal/apps/aifar/templates/runtime-diagnostics-estimate.sh`：只读候选大小和磁盘预估。
- `backend/internal/apps/aifar/templates/runtime-diagnostics-export.sh`：采集、脱敏、manifest、硬上限和归档。
- `backend/internal/apps/aifar/templates/runtime-diagnostics-cleanup.sh`：按 export ID 清理进程和受控目录。

### HTTP and housekeeping

- `backend/internal/httpapi/aifar_runtime_controller.go`：挂载 diagnostics 路由。
- `backend/internal/httpapi/aifar_runtime_diagnostics.go`：五组 HTTP handler、任务计划、审计和下载流。
- `backend/internal/httpapi/aifar_runtime_diagnostics_test.go`：权限、请求、任务、下载和删除测试。
- `backend/internal/apps/aifar/runtime_diagnostic_cleaner.go`：启动时及每小时执行合并过期清理任务。
- `backend/internal/apps/aifar/runtime_diagnostic_cleaner_test.go`：到期、离线重试和去重测试。
- `backend/cmd/aifar-server/main.go`：启动 cleaner。
- `backend/internal/i18n/messages.go`：后端步骤、日志和 API 中英文文案。

### Frontend

- `web/src/containers/runtime/types.ts`：预估、记录、分页和请求类型。
- `web/src/api/client.ts`：识别诊断包 SHA256 响应头。
- `web/src/containers/runtime/api.ts`：estimate/create/list/download/delete API。
- `web/src/containers/runtime/api.test.ts`：URL、请求体和下载参数契约。
- `web/src/containers/runtime/context.ts`：向诊断组件暴露目标查询函数。
- `web/src/views/ContainersView.vue`：把现有 `targetQuery()` 注入 Runtime context。
- `web/src/containers/runtime/runtimeDiagnostics.ts`：默认时间、服务范围、提交门禁和终态刷新纯逻辑。
- `web/src/containers/runtime/runtimeDiagnostics.test.ts`：诊断交互逻辑测试。
- `web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue`：诊断弹窗、记录表、任务联动和动作。
- `web/src/containers/runtime/AifarRuntimeLogsTab.vue`：挂载诊断面板。
- `web/src/containers/runtime/runtime.css`：诊断区响应式样式。
- `web/src/i18n/messages.ts`：前端中英文文案。

---

### Task 1: Persist Diagnostic Export Lifecycle

**Files:**
- Modify: `backend/internal/store/models.go`
- Modify: `backend/internal/store/migrations.go`
- Create: `backend/internal/store/diagnostic_exports.go`
- Create: `backend/internal/store/diagnostic_exports_test.go`

**Interfaces:**
- Produces: `store.DiagnosticExport`, `store.DiagnosticExportPage`。
- Produces: `SaveDiagnosticExport`, `GetDiagnosticExport`, `ListDiagnosticExports`, `ListDiagnosticExportsDueForCleanup`。
- Consumed by: Tasks 3–6 HTTP、domain service 和 cleaner。

- [ ] **Step 1: Write the failing migration and CRUD tests**

```go
func TestDiagnosticExportLifecycle(t *testing.T) {
    db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
    if err != nil { t.Fatal(err) }
    defer db.Close()
    now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
    saved, err := db.SaveDiagnosticExport(DiagnosticExport{
        ID: "diag-1", TaskID: "task-1", InstanceID: "instance-1", ServerID: "server-1",
        Status: "pending", ServicesJSON: `["gateway","oauth"]`, SinceAt: now.Add(-2*time.Hour),
        UntilAt: now, CreatedBy: "owner", CreatedAt: now, ExpiresAt: now.Add(24*time.Hour),
        CleanupStatus: "none",
    })
    if err != nil { t.Fatal(err) }
    saved.Status = "ready"
    saved.ArchiveName = "aifar-diagnostics.tar.gz"
    saved.RemoteRelativePath = "diag-1/aifar-diagnostics.tar.gz"
    saved.ArchiveBytes = 1024
    saved.SHA256 = strings.Repeat("a", 64)
    saved.ReadyAt = now
    if _, err := db.SaveDiagnosticExport(saved); err != nil { t.Fatal(err) }

    got, err := db.GetDiagnosticExport("diag-1")
    if err != nil { t.Fatal(err) }
    if got.Status != "ready" || got.ArchiveBytes != 1024 || got.ReadyAt.IsZero() {
        t.Fatalf("unexpected export: %+v", got)
    }
}

func TestListDiagnosticExportsDueForCleanup(t *testing.T) {
    db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
    if err != nil { t.Fatal(err) }
    defer db.Close()
    now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
    rows := []DiagnosticExport{
        {ID: "expired", InstanceID: "i1", ServerID: "s1", Status: "ready", SinceAt: now.Add(-3*time.Hour), UntilAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second)},
        {ID: "future", InstanceID: "i1", ServerID: "s1", Status: "ready", SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(time.Hour)},
        {ID: "failed", InstanceID: "i1", ServerID: "s1", Status: "failed", SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(-time.Second)},
        {ID: "deleted", InstanceID: "i1", ServerID: "s1", Status: "deleted", SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(-time.Second), CleanupStatus: "complete"},
    }
    for _, row := range rows {
        if _, err := db.SaveDiagnosticExport(row); err != nil { t.Fatal(err) }
    }
    due, err := db.ListDiagnosticExportsDueForCleanup(now, 20)
    if err != nil { t.Fatal(err) }
    if len(due) != 1 || due[0].ID != "expired" {
        t.Fatalf("unexpected due exports: %+v", due)
    }
}
```

- [ ] **Step 2: Run the focused test and confirm it fails**

Run: `go test ./internal/store -run DiagnosticExport -count=1`

Expected: FAIL because the model and store methods do not exist.

- [ ] **Step 3: Add the model and versioned migration**

```go
type DiagnosticExport struct {
    ID                 string    `json:"id"`
    TaskID             string    `json:"taskId,omitempty"`
    InstanceID         string    `json:"instanceId"`
    ServerID           string    `json:"serverId"`
    Status             string    `json:"status"`
    ServicesJSON       string    `json:"-"`
    Services           []string  `json:"services"`
    SinceAt            time.Time `json:"sinceAt"`
    UntilAt            time.Time `json:"untilAt"`
    RemoteRelativePath string    `json:"-"`
    ArchiveName        string    `json:"archiveName,omitempty"`
    ArchiveBytes       int64     `json:"archiveBytes"`
    UncompressedBytes  int64     `json:"uncompressedBytes"`
    SHA256             string    `json:"sha256,omitempty"`
    WarningCount       int       `json:"warningCount"`
    WarningsJSON       string    `json:"-"`
    Warnings           []string  `json:"warnings,omitempty"`
    ErrorText          string    `json:"error,omitempty"`
    CreatedBy          string    `json:"createdBy"`
    CreatedAt          time.Time `json:"createdAt"`
    ReadyAt            time.Time `json:"readyAt,omitempty"`
    ExpiresAt          time.Time `json:"expiresAt"`
    DownloadedAt       time.Time `json:"downloadedAt,omitempty"`
    DeletedAt          time.Time `json:"deletedAt,omitempty"`
    CleanupStatus      string    `json:"cleanupStatus"`
    CleanupError       string    `json:"cleanupError,omitempty"`
    CleanupAttemptedAt time.Time `json:"cleanupAttemptedAt,omitempty"`
}

type DiagnosticExportPage struct {
    Items    []DiagnosticExport `json:"items"`
    Total    int                `json:"total"`
    Page     int                `json:"page"`
    PageSize int                `json:"pageSize"`
}
```

Add migration version `2026072701` with the columns above and indexes on `(instance_id, created_at)`, `(status, expires_at)`, and `task_id`.

- [ ] **Step 4: Implement normalized save, scan, paging, and due-cleanup queries**

```go
func (s *Store) SaveDiagnosticExport(v DiagnosticExport) (DiagnosticExport, error)
func (s *Store) GetDiagnosticExport(id string) (DiagnosticExport, error)
func (s *Store) ListDiagnosticExports(instanceID string, page, pageSize int) (DiagnosticExportPage, error)
func (s *Store) ListDiagnosticExportsDueForCleanup(now time.Time, limit int) ([]DiagnosticExport, error)
```

Normalize status to the allowed set, sort/deduplicate service names, serialize `ServicesJSON` and `WarningsJSON`, cap page size at 100, and use `nullableTime`/`nullTime` for optional timestamps. `ListDiagnosticExportsDueForCleanup` must select `ready` rows with `expires_at <= now` plus `expired` rows whose cleanup is not complete.

- [ ] **Step 5: Run store tests**

Run: `go test ./internal/store -run DiagnosticExport -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/store/models.go backend/internal/store/migrations.go backend/internal/store/diagnostic_exports.go backend/internal/store/diagnostic_exports_test.go
git commit -m "feat: persist runtime diagnostic exports"
```

---

### Task 2: Add Binary-Safe SSH File Streaming

**Files:**
- Modify: `backend/internal/adapter/ssh.go`
- Modify: `backend/internal/adapter/ssh_test.go`

**Interfaces:**
- Produces: `SSHRemote.StreamFile(ctx, server, remotePath, dst) (int64, error)`。
- Produces: package function `StreamSSHFile` with the same signature。
- Consumed by: Task 4 domain download and Task 5 HTTP download。

- [ ] **Step 1: Write failing helper tests**

```go
func TestStreamSSHOutputCopiesBinaryBytes(t *testing.T) {
    payload := []byte{0x00, 0x01, 0xff, '\n', 'A'}
    var dst bytes.Buffer
    copied, err := streamSSHOutputWithContext(context.Background(), &dst, bytes.NewReader(payload),
        func() error { return nil }, func() {}, func() string { return "" })
    if err != nil { t.Fatal(err) }
    if copied != int64(len(payload)) || !bytes.Equal(dst.Bytes(), payload) {
        t.Fatalf("binary payload changed: %x", dst.Bytes())
    }
}

func TestStreamSSHOutputCancelsSessionWhenWriterFails(t *testing.T) {
    // Use a writer that returns io.ErrClosedPipe and assert cancelCommand is called.
}
```

- [ ] **Step 2: Run the focused test and confirm it fails**

Run: `go test ./internal/adapter -run StreamSSH -count=1`

Expected: FAIL because the stream helper is undefined.

- [ ] **Step 3: Implement the stream helper and SSH method**

```go
func (SSHRemote) StreamFile(ctx context.Context, server store.Server, remotePath string, dst io.Writer) (int64, error) {
    return StreamSSHFile(ctx, server, remotePath, dst)
}

func StreamSSHFile(ctx context.Context, server store.Server, remotePath string, dst io.Writer) (int64, error) {
    // Dial, create a session, bind stdout to a pipe, capture bounded stderr,
    // start `cat <shell-quoted-path>`, copy raw bytes, and close on ctx cancellation.
}
```

`remotePath` must be non-empty; build the command internally with `shellQuote`. Do not expose a generic streaming command API. Bound captured stderr to 8 KiB and return a masked summary. A destination write error or context cancellation must signal/close the SSH session and client.

- [ ] **Step 4: Run adapter tests**

Run: `go test ./internal/adapter -run 'StreamSSH|RunSSH|UploadSSH' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/adapter/ssh.go backend/internal/adapter/ssh_test.go
git commit -m "feat: stream remote files over ssh"
```

---

### Task 3: Implement Request Validation and Size Estimation

**Files:**
- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/module.go`
- Create: `backend/internal/apps/aifar/runtime_diagnostics.go`
- Create: `backend/internal/apps/aifar/runtime_diagnostics_protocol.go`
- Create: `backend/internal/apps/aifar/runtime_diagnostics_test.go`
- Create: `backend/internal/apps/aifar/templates/runtime-diagnostics-estimate.sh`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Consumes: Task 1 `DiagnosticExport`; existing `ListAIFARDeployments` and instance metadata。
- Produces: registry request/result structs and `RuntimeDiagnosticsModule`。
- Produces: `EstimateRuntimeDiagnostics` and strict line-protocol parser。
- Consumed by: Tasks 4–6。

- [ ] **Step 1: Write failing validation and parser tests**

```go
func TestEstimateRuntimeDiagnosticsRejectsDisabledAndUnknownServices(t *testing.T) {
    // Seed gateway desired=1 and oauth desired=0.
    // Requests for oauth or arbitrary "../../etc" must fail before remote.Run.
}

func TestParseRuntimeDiagnosticEstimate(t *testing.T) {
    raw := strings.Join([]string{
        "AIFAR_DIAG_SERVICE\tgateway\t100\t200",
        "AIFAR_DIAG_SERVICE\toauth\t50\t0",
        "AIFAR_DIAG_TOTAL\t150\t200\t350\t9000000000\t1073742524",
        "AIFAR_DIAG_WARNING\tdocker-log-conservative\tgateway",
    }, "\n")
    got, err := parseRuntimeDiagnosticEstimate(raw)
    if err != nil { t.Fatal(err) }
    if got.TotalBytes != 350 || got.AvailableBytes != 9000000000 || len(got.Services) != 2 {
        t.Fatalf("unexpected estimate: %+v", got)
    }
}
```

- [ ] **Step 2: Run the focused test and confirm it fails**

Run: `go test ./internal/apps/aifar -run RuntimeDiagnosticEstimate -count=1`

Expected: FAIL because diagnostics contracts and parser do not exist.

- [ ] **Step 3: Add registry contracts with exact signatures**

```go
type RuntimeDiagnosticRequest struct {
    ExportID  string
    Instance  store.AppInstance
    Server    store.Server
    Language  string
    Actor     string
    Services  []string
    SinceAt   time.Time
    UntilAt   time.Time
}

type RuntimeDiagnosticEstimateResult struct {
    Services       []RuntimeDiagnosticServiceEstimate `json:"services"`
    FileBytes      int64 `json:"fileBytes"`
    ContainerBytes int64 `json:"containerBytes"`
    TotalBytes     int64 `json:"totalBytes"`
    RequiredBytes  int64 `json:"requiredBytes"`
    AvailableBytes int64 `json:"availableBytes"`
    Allowed        bool `json:"allowed"`
    Warnings       []string `json:"warnings,omitempty"`
}

type RuntimeDiagnosticServiceEstimate struct {
    Service        string `json:"service"`
    FileBytes      int64  `json:"fileBytes"`
    ContainerBytes int64  `json:"containerBytes"`
}

type RuntimeDiagnosticsModule interface {
    EstimateRuntimeDiagnostics(context.Context, RuntimeDiagnosticRequest, RunContext) (RuntimeDiagnosticEstimateResult, error)
    ExportRuntimeDiagnostics(context.Context, RuntimeDiagnosticRequest, RunContext) error
    DeleteRuntimeDiagnosticExport(context.Context, RuntimeDiagnosticDeleteRequest, RunContext) error
    StreamRuntimeDiagnosticExport(context.Context, RuntimeDiagnosticStreamRequest, io.Writer) (int64, error)
}
```

Add `RuntimeDiagnosticServiceEstimate`, `RuntimeDiagnosticDeleteRequest`, and `RuntimeDiagnosticStreamRequest` with explicit instance/server/export fields. Add imports for `io` and `time`.

In the AIFAR package, define aliases so service and module signatures cannot drift:

```go
type RuntimeDiagnosticRequest = registry.RuntimeDiagnosticRequest
type RuntimeDiagnosticDeleteRequest = registry.RuntimeDiagnosticDeleteRequest
type RuntimeDiagnosticStreamRequest = registry.RuntimeDiagnosticStreamRequest
```

Do not expand the base `aifar.Store` interface and break unrelated fake stores. Add a diagnostics-only asserted interface containing `SaveDiagnosticExport`, `GetDiagnosticExport`, `ListAIFARDeployments`, `ListAIFARPods`, and `ListAppReleases`; return a clear internal capability error when the assertion fails.

- [ ] **Step 4: Implement trusted estimate rendering and parsing**

The template must emit only the following machine protocol on stdout:

```text
AIFAR_DIAG_SERVICE<TAB><service><TAB><fileBytes><TAB><containerBytes>
AIFAR_DIAG_TOTAL<TAB><fileBytes><TAB><containerBytes><TAB><totalBytes><TAB><availableBytes><TAB><requiredBytes>
AIFAR_DIAG_WARNING<TAB><stable-code><TAB><service-or-dash>
```

The script must:

```sh
set -eu
for required in docker find tar gzip sha256sum df stat setsid; do
  command -v "$required" >/dev/null 2>&1 || exit 20
done
# File candidates: find "$LOG_ROOT/$service" -xdev -type f with mtime bounds; never use -L.
# Container candidates: docker ps -a filtered by aifar.instance and aifar.service labels.
# Docker estimate: stat each matching json-file LogPath as a conservative upper bound.
# requiredBytes = estimated generated container logs + 1 GiB + max(512 MiB, generated*20%).
```

Use template fields `InstallRoot`, `InstanceID`, `Services`, `SinceUnix`, `UntilUnix`; render all values with `installerkit.ShellQuote`. The Go parser must reject duplicate totals, malformed integers, negative bytes, unknown service output, extra tab fields, and missing totals.

- [ ] **Step 5: Implement domain validation and estimation**

```go
const (
    runtimeDiagnosticMaxUncompressed = int64(3 * 1024 * 1024 * 1024)
    runtimeDiagnosticMaxArchive      = int64(1 * 1024 * 1024 * 1024)
    runtimeDiagnosticRetention       = 24 * time.Hour
)

func (s Service) EstimateRuntimeDiagnostics(ctx context.Context, req RuntimeDiagnosticRequest, log Logger) (registry.RuntimeDiagnosticEstimateResult, error)
```

Validate non-legacy instance, server ownership, `SinceAt < UntilAt`, a bounded window not in the future, normalized `[a-z0-9][a-z0-9-]*` service names, and membership in deployments with `DesiredReplicas > 0`. Reject empty selection. Compute `Allowed` from the 3 GiB limit and required/free space; return stable localized errors instead of raw stderr.

- [ ] **Step 6: Run focused tests**

Run: `go test ./internal/apps/aifar -run 'RuntimeDiagnosticEstimate|ParseRuntimeDiagnostic' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/apps/registry/contract.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/module.go backend/internal/apps/aifar/runtime_diagnostics.go backend/internal/apps/aifar/runtime_diagnostics_protocol.go backend/internal/apps/aifar/runtime_diagnostics_test.go backend/internal/apps/aifar/templates/runtime-diagnostics-estimate.sh backend/internal/i18n/messages.go
git commit -m "feat: estimate runtime diagnostic exports"
```

---

### Task 4: Generate, Cancel, Stream, and Delete Remote Archives

**Files:**
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/module.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics_protocol.go`
- Modify: `backend/internal/apps/aifar/runtime_diagnostics_test.go`
- Create: `backend/internal/apps/aifar/templates/runtime-diagnostics-export.sh`
- Create: `backend/internal/apps/aifar/templates/runtime-diagnostics-cleanup.sh`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Consumes: Tasks 1–3 store, estimate, registry contracts, and `SSHRemote.StreamFile`。
- Produces: complete nine-step export, cancellation cleanup, download stream, and controlled deletion。
- Consumed by: Task 5 handlers and Task 6 cleaner。

- [ ] **Step 1: Write failing export tests using a scripted fake remote**

```go
func TestExportRuntimeDiagnosticsPersistsReadyWithWarnings(t *testing.T) {
    // Fake estimate output is allowed; fake export output contains one warning and valid result.
    // Assert nine task steps finish success, store row becomes ready, warningCount=1,
    // remoteRelativePath is under diag ID, ExpiresAt=ReadyAt+24h, and worker returns nil.
}

func TestExportRuntimeDiagnosticsCriticalFailureDeletesPartial(t *testing.T) {
    // Fake archive command fails; assert cleanup script runs only for the same export ID,
    // row becomes failed, and no ready path is recorded.
}

func TestExportRuntimeDiagnosticsCancellationCleansOnlyOwnPartial(t *testing.T) {
    // Cancel context during export; assert cleanup receives a numeric PID check and exact export root,
    // original runtime/logs path is never an rm target, and returned error is context.Canceled.
}
```

- [ ] **Step 2: Run focused tests and confirm they fail**

Run: `go test ./internal/apps/aifar -run 'ExportRuntimeDiagnostics|DiagnosticStream|DiagnosticDelete' -count=1`

Expected: FAIL because export, stream and delete are not implemented.

- [ ] **Step 3: Implement the export script contract**

The script receives only server-generated fields and must create this exact structure:

```text
aifar-diagnostics-<instance>-<timestamp>/
  README.txt
  manifest.json
  collection-errors.txt
  services/<service>/file-logs/...
  services/<service>/container-logs/<container>.log
  diagnostics/runtime-summary.json
  diagnostics/deployments.json
  diagnostics/pods.json
  diagnostics/containers.txt
  diagnostics/health-checks.txt
  diagnostics/agent-status.txt
  diagnostics/host-resources.txt
  diagnostics/release-summary.json
```

Critical shell rules:

```sh
umask 077
PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"
FINAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID"
printf '%s\n' "$$" > "$PARTIAL_ROOT/.collector.pid"
trap 'touch "$PARTIAL_ROOT/.cancelled" 2>/dev/null || true' INT TERM

# File logs: use a generated NUL-delimited candidate list from find -xdev -type f.
# Recheck every resolved path starts with "$LOG_ROOT/$service/" before reading.
# Container logs: docker ps -a with both instance and service labels, then
# docker logs --since "$SINCE" --until "$UNTIL" --timestamps "$container".
# Before every append, enforce cumulative uncompressed <= 3 GiB.
# Redact into staged files; never modify originals.
# Generate manifest and collection-errors even when individual items fail.
# Run `tar -czf` in a subshell with `ulimit -f 2097152` so .tar.gz.partial
# cannot grow beyond 1 GiB; any limit signal or tar failure is critical.
# Verify archive, calculate SHA256, then mv the complete directory atomically.
```

Implement a fixed shell `json_escape` helper for manifest fields and validate its output in Go tests; do not add `jq`, Python, Node or another runtime dependency to the target server.

Emit one final line only after successful promotion:

```text
AIFAR_DIAG_RESULT<TAB><relativePath><TAB><archiveName><TAB><archiveBytes><TAB><uncompressedBytes><TAB><sha256><TAB><warningCount>
```

`manifest.json` contains per-file source, relative path, original/staged size and SHA256. `README.txt` states `file-mtime` versus exact Docker time semantics and warns that business text may still be sensitive. Do not archive `.env`, configuration or database files.

- [ ] **Step 4: Implement nine-step service orchestration**

```go
func (s Service) ExportRuntimeDiagnostics(ctx context.Context, req RuntimeDiagnosticRequest, log Logger, targetLog targetLogger) (err error)
```

Use the exact steps `load-instance`, `validate-request`, `estimate-size`, `collect-file-logs`, `collect-container-logs`, `collect-diagnostics`, `redact-and-manifest`, `create-archive`, `record-export`. The HTTP task creator in Task 5 acquires generic operation lock `{scope:"runtime-diagnostics", resourceID:instance.ID, operation:"export"}` with the worker task ID; the domain service must not acquire a second copy of the same lock.

Before rendering, build `RuntimeSummaryJSON`, `DeploymentsJSON`, `PodsJSON`, and `ReleaseSummaryJSON` from allowlisted store fields only. Pass those sanitized JSON strings as quoted template data; never pass instance metadata, Runtime spec bodies, environment values, credential fields, or arbitrary JSON from the client. The script writes these payloads into `diagnostics/` and obtains container, health, Agent and host-resource facts from fixed remote commands.

The remote command must start the embedded script with `setsid sh -s`. On context cancellation or critical error, create a 15-second background cleanup context and execute only the embedded cleanup template. Once archive promotion and SHA256 complete, ignore cancellation only inside the short `record-export` critical section so the database cannot disagree with an already-ready file.

- [ ] **Step 5: Implement stream and deletion methods**

```go
type diagnosticFileStreamer interface {
    StreamFile(context.Context, store.Server, string, io.Writer) (int64, error)
}

func (s Service) StreamRuntimeDiagnosticExport(ctx context.Context, req RuntimeDiagnosticStreamRequest, dst io.Writer) (int64, error)
func (s Service) DeleteRuntimeDiagnosticExport(ctx context.Context, req RuntimeDiagnosticDeleteRequest, log Logger) error
```

Both methods re-read the export and instance, verify server ownership, state, expiry and normalized relative path. Download builds the absolute path from controlled `installRoot/runtime/diagnostics`; delete invokes the cleanup template and only marks `deleted/complete` after remote absence is confirmed.

- [ ] **Step 6: Run domain tests**

Run: `go test ./internal/apps/aifar -run 'RuntimeDiagnostic|ExportRuntimeDiagnostics' -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/apps/aifar/service.go backend/internal/apps/aifar/module.go backend/internal/apps/aifar/runtime_diagnostics.go backend/internal/apps/aifar/runtime_diagnostics_protocol.go backend/internal/apps/aifar/runtime_diagnostics_test.go backend/internal/apps/aifar/templates/runtime-diagnostics-export.sh backend/internal/apps/aifar/templates/runtime-diagnostics-cleanup.sh backend/internal/i18n/messages.go
git commit -m "feat: build runtime diagnostic archives"
```

---

### Task 5: Expose Authenticated HTTP Workflow

**Files:**
- Modify: `backend/internal/httpapi/aifar_runtime_controller.go`
- Create: `backend/internal/httpapi/aifar_runtime_diagnostics.go`
- Create: `backend/internal/httpapi/aifar_runtime_diagnostics_test.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime_test.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Consumes: Tasks 1–4 store and `RuntimeDiagnosticsModule`。
- Produces: estimate, create, list, download and delete endpoints。
- Consumed by: Task 7 frontend API。

- [ ] **Step 1: Write failing route, permission, and task tests**

```go
func TestRuntimeDiagnosticsCreateReturnsTaskAndExportIDs(t *testing.T) {
    // POST /api/v2/containers/aifar/runtime/diagnostics/exports?serverId=...
    // body: instanceId, sinceAt, untilAt, services.
    // Expect 202, taskId, exportId, nine planned steps, one target, pending record,
    // and audit action containers.aifar.runtime.diagnostics.export.
}

func TestRuntimeDiagnosticsRoutesRequireAppsManage(t *testing.T) {
    // Viewer receives 403 for estimate/list/create/download/delete.
}

func TestRuntimeDiagnosticDownloadDoesNotDeleteOnShortWrite(t *testing.T) {
    // Fake module returns io.ErrClosedPipe; export remains ready and no delete task is created.
}
```

- [ ] **Step 2: Run focused HTTP tests and confirm they fail**

Run: `go test ./internal/httpapi -run RuntimeDiagnostic -count=1`

Expected: FAIL with missing routes/handlers.

- [ ] **Step 3: Mount exact routes**

```go
r.Post("/containers/aifar/runtime/diagnostics/estimate", a.requirePermission(rbac.AppsManage, a.estimateDiagnostics))
r.Post("/containers/aifar/runtime/diagnostics/exports", a.requirePermission(rbac.AppsManage, a.createDiagnosticExport))
r.Get("/containers/aifar/runtime/diagnostics/exports", a.requirePermission(rbac.AppsManage, a.listDiagnosticExports))
r.Get("/containers/aifar/runtime/diagnostics/exports/{id}/download", a.requirePermission(rbac.AppsManage, a.downloadDiagnosticExport))
r.Delete("/containers/aifar/runtime/diagnostics/exports/{id}", a.requirePermission(rbac.AppsManage, a.deleteDiagnosticExport))
```

- [ ] **Step 4: Implement request parsing and create/list behavior**

```go
type runtimeDiagnosticRequestPayload struct {
    InstanceID string   `json:"instanceId"`
    SinceAt    string   `json:"sinceAt"`
    UntilAt    string   `json:"untilAt"`
    Services   []string `json:"services"`
}

type runtimeDiagnosticTaskResponse struct {
    TaskID   string `json:"taskId"`
    ExportID string `json:"exportId"`
    Status   string `json:"status"`
}
```

Resolve the server through existing `serverId` query handling and call `resolveAIFARRuntimeActionTargetForInstanceWithAgent(..., false)`. Create the pending export and task atomically from the handler perspective: if task plan or start fails, delete/mark failed record and delete the unused task. Acquire the per-instance diagnostics operation lock before starting. List uses `instanceId`, `page`, `pageSize` and returns `DiagnosticExportPage`.

- [ ] **Step 5: Implement safe streaming and optional post-download deletion**

Before headers, verify record readiness, `expiresAt > now`, archive name, size, SHA256, instance and server. Set:

```go
w.Header().Set("Content-Type", "application/gzip")
w.Header().Set("Content-Disposition", contentDispositionAttachment(export.ArchiveName))
w.Header().Set("Content-Length", strconv.FormatInt(export.ArchiveBytes, 10))
w.Header().Set("X-AIFAR-Diagnostic-SHA256", export.SHA256)
```

After headers, never attempt a JSON error response. Audit success only when copied bytes equal `ArchiveBytes`; audit truncated/failed otherwise. Set `DownloadedAt` on success. If `deleteAfterDownload=true`, enqueue the same audited delete worker only after full success; default is false.

- [ ] **Step 6: Implement async manual deletion**

`DELETE` creates `aifar.runtime.diagnostics.delete`, target `<instanceId>:<exportId>`, steps `validate-export`, `delete-remote-archive`, `record-deletion`, returns `202 {taskId,status}` and audits `containers.aifar.runtime.diagnostics.delete`.

- [ ] **Step 7: Run HTTP tests**

Run: `go test ./internal/httpapi -run 'RuntimeDiagnostic|AIFARRuntime' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/httpapi/aifar_runtime_controller.go backend/internal/httpapi/aifar_runtime_diagnostics.go backend/internal/httpapi/aifar_runtime_diagnostics_test.go backend/internal/httpapi/containers_aifar_runtime_test.go backend/internal/i18n/messages.go
git commit -m "feat: expose runtime diagnostic export api"
```

---

### Task 6: Add Restart-Safe 24-Hour Cleanup

**Files:**
- Create: `backend/internal/apps/aifar/runtime_diagnostic_cleaner.go`
- Create: `backend/internal/apps/aifar/runtime_diagnostic_cleaner_test.go`
- Modify: `backend/cmd/aifar-server/main.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Consumes: Task 1 due-cleanup query and Task 4 controlled deletion。
- Produces: `NewRuntimeDiagnosticCleaner(...).Start(ctx)`。

- [ ] **Step 1: Write failing cleaner tests**

```go
func TestRuntimeDiagnosticCleanerMarksExpiredAndDeletesReachableArchive(t *testing.T) {
    // Seed one expired ready row and one future ready row.
    // Tick must create one merged cleanup task, delete only expired row,
    // set status=deleted cleanupStatus=complete, and write system audit.
}

func TestRuntimeDiagnosticCleanerRetriesOfflineServer(t *testing.T) {
    // First tick remote fails: status=expired cleanupStatus=failed.
    // Second tick succeeds: status=deleted cleanupStatus=complete.
}

func TestRuntimeDiagnosticCleanerCoalescesOverlappingTicks(t *testing.T) {
    // Block the first job and call tick again; only one task may start.
}
```

- [ ] **Step 2: Run focused cleaner tests and confirm they fail**

Run: `go test ./internal/apps/aifar -run RuntimeDiagnosticCleaner -count=1`

Expected: FAIL because the cleaner does not exist.

- [ ] **Step 3: Implement coalesced startup/hourly scheduling**

```go
const runtimeDiagnosticCleanupInterval = time.Hour

type RuntimeDiagnosticCleaner struct {
    store    *store.Store
    tasks    *worker.Manager
    remote   Remote
    interval time.Duration
    running  atomic.Bool
}

func (c *RuntimeDiagnosticCleaner) Start(ctx context.Context) {
    go func() {
        c.tick(ctx, time.Now().UTC())
        ticker := time.NewTicker(c.interval)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done(): return
            case now := <-ticker.C: c.tick(ctx, now.UTC())
            }
        }
    }()
}
```

Each tick starts one `aifar.runtime.diagnostics.cleanup` task only when due rows exist and `running.CompareAndSwap(false, true)` succeeds. Plan steps are `mark-expired`, `delete-remote-artifacts`, `record-cleanup`; actor is `system`, target is `runtime-diagnostics`.

- [ ] **Step 4: Implement per-record retry semantics**

Before remote delete, persist `status=expired`, `cleanupStatus=pending`, and `cleanupAttemptedAt`. A remote failure sets `cleanupStatus=failed` with masked error and continues to the next row; the merged worker succeeds if it completed the scan, while individual failures remain explicit in records and logs. Confirmed absence sets `status=deleted`, `cleanupStatus=complete`, `DeletedAt=now`. Audit every attempt with action `containers.aifar.runtime.diagnostics.cleanup`.

- [ ] **Step 5: Start cleaner from the server**

```go
aifar.NewRuntimeDiagnosticCleaner(db, tasks, adapter.SSHRemote{}).Start(context.Background())
```

Place it beside autoscaler startup after interrupted task/lock recovery.

- [ ] **Step 6: Run cleaner and main-package tests**

Run: `go test ./internal/apps/aifar ./cmd/aifar-server -run RuntimeDiagnosticCleaner -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/apps/aifar/runtime_diagnostic_cleaner.go backend/internal/apps/aifar/runtime_diagnostic_cleaner_test.go backend/cmd/aifar-server/main.go backend/internal/i18n/messages.go
git commit -m "feat: clean expired runtime diagnostics"
```

---

### Task 7: Add Frontend Diagnostic API Contracts

**Files:**
- Modify: `web/src/api/client.ts`
- Modify: `web/src/containers/runtime/types.ts`
- Modify: `web/src/containers/runtime/api.ts`
- Modify: `web/src/containers/runtime/api.test.ts`

**Interfaces:**
- Consumes: Task 5 HTTP routes and JSON fields。
- Produces: typed estimate/create/list/download/delete functions for Task 8。

- [ ] **Step 1: Write failing API contract tests**

```ts
it('estimates and creates a runtime diagnostic export', async () => {
  const payload = {
    instanceId: 'instance-1',
    sinceAt: '2026-07-27T06:00:00Z',
    untilAt: '2026-07-27T08:00:00Z',
    services: ['gateway', 'oauth']
  }
  await estimateRuntimeDiagnostics('serverId=server-1', payload)
  expect(apiPostMock).toHaveBeenCalledWith('/containers/aifar/runtime/diagnostics/estimate?serverId=server-1', payload)
  await createRuntimeDiagnosticExport('serverId=server-1', payload)
  expect(apiPostMock).toHaveBeenLastCalledWith('/containers/aifar/runtime/diagnostics/exports?serverId=server-1', payload)
})

it('downloads with delete-after-download disabled by default', async () => {
  await downloadRuntimeDiagnosticExport('serverId=server-1', 'diag-1', false)
  expect(apiDownloadMock).toHaveBeenCalledWith('/containers/aifar/runtime/diagnostics/exports/diag-1/download?serverId=server-1&deleteAfterDownload=false')
})

it('reads the runtime diagnostic sha256 response header', async () => {
  // Mock fetch with X-AIFAR-Diagnostic-SHA256 and assert apiDownload returns it.
})
```

- [ ] **Step 2: Run the focused frontend test and confirm it fails**

Run: `pnpm --dir web exec vitest run src/containers/runtime/api.test.ts`

Expected: FAIL because functions and types are missing.

- [ ] **Step 3: Add exact TypeScript types**

```ts
export type RuntimeDiagnosticRequest = {
  instanceId: string
  sinceAt: string
  untilAt: string
  services: string[]
}

export type RuntimeDiagnosticEstimate = {
  services: Array<{ service: string; fileBytes: number; containerBytes: number }>
  fileBytes: number
  containerBytes: number
  totalBytes: number
  requiredBytes: number
  availableBytes: number
  allowed: boolean
  warnings?: string[]
}

export type RuntimeDiagnosticExport = {
  id: string
  taskId?: string
  instanceId: string
  serverId: string
  status: 'pending' | 'building' | 'ready' | 'failed' | 'cancelled' | 'expired' | 'deleted'
  services: string[]
  sinceAt: string
  untilAt: string
  archiveName?: string
  archiveBytes: number
  uncompressedBytes: number
  sha256?: string
  warningCount: number
  warnings?: string[]
  error?: string
  createdAt: string
  readyAt?: string
  expiresAt: string
  downloadedAt?: string
  deletedAt?: string
  cleanupStatus: 'none' | 'pending' | 'failed' | 'complete'
  cleanupError?: string
}
```

- [ ] **Step 4: Implement API functions**

```ts
export function estimateRuntimeDiagnostics(query: string, payload: RuntimeDiagnosticRequest): Promise<RuntimeDiagnosticEstimate>
export function createRuntimeDiagnosticExport(query: string, payload: RuntimeDiagnosticRequest): Promise<{ taskId: string; exportId: string; status: string }>
export function fetchRuntimeDiagnosticExports(query: string, instanceId: string, page = 1, pageSize = 20): Promise<RuntimeDiagnosticExportPage>
export function downloadRuntimeDiagnosticExport(query: string, exportId: string, deleteAfterDownload = false): ReturnType<typeof apiDownload>
export function deleteRuntimeDiagnosticExport(query: string, exportId: string): Promise<RuntimeTaskResponse>
```

Use `URLSearchParams` for all query parameters and `encodeURIComponent` for export IDs.

Update `apiDownload` without changing backup behavior:

```ts
sha256: response.headers.get('X-AIFAR-Diagnostic-SHA256')
  ?? response.headers.get('X-AIFAR-Backup-SHA256')
  ?? ''
```

- [ ] **Step 5: Run API tests**

Run: `pnpm --dir web exec vitest run src/containers/runtime/api.test.ts`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/api/client.ts web/src/containers/runtime/types.ts web/src/containers/runtime/api.ts web/src/containers/runtime/api.test.ts
git commit -m "feat: add runtime diagnostic web api"
```

---

### Task 8: Build Runtime Diagnostic Export UI

**Files:**
- Read first: `design/ant-design-system-portable202606.md`
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/views/ContainersView.vue`
- Create: `web/src/containers/runtime/runtimeDiagnostics.ts`
- Create: `web/src/containers/runtime/runtimeDiagnostics.test.ts`
- Create: `web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue`
- Modify: `web/src/containers/runtime/AifarRuntimeLogsTab.vue`
- Modify: `web/src/containers/runtime/runtime.css`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: Task 7 typed API and existing `useTaskProgressStore`/global task SSE refresh。
- Produces: complete Runtime logs-page workflow。

- [ ] **Step 1: Read the design guide completely**

Run: `Get-Content -LiteralPath 'design/ant-design-system-portable202606.md' -Encoding UTF8`

Expected: Existing spacing, hierarchy, button and responsive conventions are understood before editing Vue/CSS.

- [ ] **Step 2: Write failing diagnostic interaction logic tests**

```ts
it('defaults to the last two hours and all enabled deployments', () => {
  const now = new Date('2026-07-27T08:00:00Z')
  expect(defaultRuntimeDiagnosticWindow(now)).toEqual({
    sinceAt: new Date('2026-07-27T06:00:00Z'),
    untilAt: now
  })
  expect(enabledRuntimeDiagnosticServices([
    { instanceId: 'instance-1', serviceName: 'gateway', desiredReplicas: 1 },
    { instanceId: 'instance-1', serviceName: 'oauth', desiredReplicas: 0 }
  ])).toEqual(['gateway'])
})

it('blocks submit until a current estimate is allowed', () => {
  expect(runtimeDiagnosticSubmitDisabledReason({ services: ['gateway'], estimate: null, estimating: false, submitting: false })).toBe('estimate-required')
  expect(runtimeDiagnosticSubmitDisabledReason({ services: ['gateway'], estimate: { allowed: false }, estimating: false, submitting: false })).toBe('estimate-blocked')
})

it('refreshes each tracked export task exactly once at terminal state', () => {
  const refreshed = new Set<string>()
  expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'running' }], new Set(['task-1']), refreshed)).toBe('')
  expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'success' }], new Set(['task-1']), refreshed)).toBe('task-1')
  refreshed.add('task-1')
  expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'success' }], new Set(['task-1']), refreshed)).toBe('')
})
```

- [ ] **Step 3: Run the focused logic test and confirm it fails**

Run: `pnpm --dir web exec vitest run src/containers/runtime/runtimeDiagnostics.test.ts`

Expected: FAIL because the pure diagnostic helpers do not exist.

- [ ] **Step 4: Expose only the required context input**

Add to `AifarRuntimeContext`:

```ts
runtimeTargetQuery: () => string
```

Provide existing `targetQuery` from `ContainersView.vue`. `AifarRuntimeLogsTab.vue` reads `selectedRuntimeInstanceId`, `selectedRuntimeDeployments`, and `runtimeTargetQuery` from context, then passes plain props to the panel:

```vue
<AifarRuntimeDiagnosticsPanel
  :instance-id="selectedRuntimeInstanceId"
  :deployments="selectedRuntimeDeployments"
  :target-query="runtimeTargetQuery()"
/>
```

The panel uses props plus its own local state; do not move diagnostic state into the already-large view.

- [ ] **Step 5: Implement the self-contained panel and dialog**

The component owns:

```ts
const dialogVisible = ref(false)
const mode = ref<'last2h' | 'custom'>('last2h')
const selectedServices = ref<string[]>([])
const sinceAt = ref<Date>()
const untilAt = ref<Date>()
const estimate = ref<RuntimeDiagnosticEstimate | null>(null)
const exportsPage = ref<RuntimeDiagnosticExportPage>({ items: [], total: 0, page: 1, pageSize: 20 })
const deleteAfterDownload = ref(false)
```

Implement and consume these pure helpers from `runtimeDiagnostics.ts`:

```ts
export function defaultRuntimeDiagnosticWindow(now = new Date()): { sinceAt: Date; untilAt: Date }
export function enabledRuntimeDiagnosticServices(deployments: AifarRuntimeDeployment[]): string[]
export function runtimeDiagnosticSubmitDisabledReason(input: RuntimeDiagnosticSubmitState): '' | 'services-required' | 'estimate-required' | 'estimate-blocked' | 'busy'
export function terminalDiagnosticTaskToRefresh(items: Array<{ id: string; status: string }>, tracked: Set<string>, refreshed: Set<string>): string
export function runtimeDiagnosticStatusKey(row: RuntimeDiagnosticExport): string
```

Open behavior resets the range to `[now-2h, now]`, selects deployments with `desiredReplicas > 0`, and clears stale estimates whenever time or services change. The panel displays:

- “导出诊断包” primary action。
- estimate breakdown: file logs, Docker conservative estimate, required/free space, 3 GiB decision。
- records table: status, range, services, size, warnings, created/expiry times。
- actions: view task, download, optional delete after download, confirmed manual delete。

Use `taskProgress.track(taskId, t('containers.diagnosticsExportTask'))`. Watch only the tracked task IDs created by this component; when an ID first enters `success/failed/cancelled`, refresh the list once. Do not add a diagnostics timer or page polling.

- [ ] **Step 6: Implement download and deletion behavior**

Use existing `apiDownload` result to create and revoke an object URL. Compare returned `X-AIFAR-Diagnostic-SHA256` to the record SHA256 when both exist; mismatch shows an error and does not report success. Manual delete uses an Element Plus confirmation, tracks the returned task, and refreshes on terminal state.

- [ ] **Step 7: Add exact i18n keys in zh/en**

Add `containers.diagnosticsExport`, `diagnosticsExportTitle`, `diagnosticsTimeRange`, `diagnosticsLast2Hours`, `diagnosticsCustomRange`, `diagnosticsServices`, `diagnosticsEstimate`, `diagnosticsEstimating`, `diagnosticsFileBytes`, `diagnosticsContainerBytes`, `diagnosticsRequiredBytes`, `diagnosticsAvailableBytes`, `diagnosticsConservativeHint`, `diagnosticsOverLimit`, `diagnosticsInsufficientSpace`, `diagnosticsStart`, `diagnosticsExportTask`, `diagnosticsRecords`, `diagnosticsReady`, `diagnosticsReadyWithWarnings`, `diagnosticsBuilding`, `diagnosticsFailed`, `diagnosticsCancelled`, `diagnosticsExpiredCleanupPending`, `diagnosticsDeleted`, `diagnosticsDeleteAfterDownload`, `diagnosticsDeleteConfirm`, `diagnosticsDeleteTask`, `diagnosticsDownloadStarted`, `diagnosticsDownloadFailed`, `diagnosticsChecksumMismatch`, and `diagnosticsNoRecords` with complete Chinese and English text.

- [ ] **Step 8: Run component and full frontend tests**

Run: `pnpm --dir web exec vitest run src/containers/runtime/runtimeDiagnostics.test.ts src/containers/runtime/api.test.ts`

Expected: PASS.

Run: `pnpm test:web`

Expected: PASS with no regressions.

- [ ] **Step 9: Build the web frontend**

Run: `pnpm web:build`

Expected: Vue type-check and Vite production build PASS.

- [ ] **Step 10: Commit**

```bash
git add web/src/containers/runtime/context.ts web/src/containers/runtime/runtimeDiagnostics.ts web/src/containers/runtime/runtimeDiagnostics.test.ts web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue web/src/containers/runtime/AifarRuntimeLogsTab.vue web/src/containers/runtime/runtime.css web/src/i18n/messages.ts
git add -p web/src/views/ContainersView.vue
git commit -m "feat: add runtime diagnostic export ui"
```

---

### Task 9: Complete Cross-Layer Verification and Acceptance Notes

**Files:**
- Modify only if verification finds a defect: files from Tasks 1–8。
- Update: `memory.md` with concise reusable conclusion after implementation。

**Interfaces:**
- Consumes: all previous tasks。
- Produces: verified local implementation ready for user review; no offline bundle and no push。

- [ ] **Step 1: Run all backend tests**

Run from `backend/`: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Run race-sensitive packages**

Run from `backend/`: `go test -race ./internal/worker ./internal/store ./internal/apps/aifar`

Expected: PASS on an environment where the Go race detector is available. If Windows toolchain cannot run it, record the exact limitation and leave Linux CI as the required gate.

- [ ] **Step 3: Run frontend and script gates**

Run: `pnpm test:web`

Expected: all Vitest suites PASS.

Run: `pnpm test:scripts`

Expected: script/startup contract tests PASS.

Run: `pnpm web:build`

Expected: TypeScript, Vue and Vite build PASS.

Run: `pnpm backend:build`

Expected: Linux and Windows amd64 backend builds PASS without packaging resources.

- [ ] **Step 4: Verify repository hygiene**

Run: `git diff --check`

Expected: no whitespace errors.

Run: `git status --short`

Expected: identify and preserve pre-existing unrelated changes; implementation commits contain only diagnostic-export files. Do not run package, release verification, push, or destructive cleanup.

- [ ] **Step 5: Perform real openEuler acceptance when a target is available**

Use a non-production test Runtime instance and verify:

1. Multiple running services generate the documented archive structure.
2. One failed service/container still yields a downloadable package with manifest warnings.
3. Cancellation leaves original logs and business containers untouched and removes `.partial`.
4. Custom time/service scope matches archive contents.
5. Download SHA256 matches the record.
6. Interrupted download keeps the file; full download optional deletion works.
7. 24-hour expiry and offline-then-online cleanup retry work.
8. Archive inspection finds no `.env`, configuration bodies, passwords, tokens or SSH credentials.

- [ ] **Step 6: Record verification evidence and commit any test-only fixes**

If verification required changes, rerun the affected focused test first, then all gates above, and commit only those fixes:

```bash
git add backend/internal/store/diagnostic_exports.go backend/internal/apps/aifar/runtime_diagnostics.go backend/internal/httpapi/aifar_runtime_diagnostics.go web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue
git commit -m "fix: harden runtime diagnostic export"
```

Do not create an empty commit when no fixes are needed.
