# AIFAR 模块状态与维护优化建议（P1）

## 背景

近期在仪表盘、应用商店、服务器工作台和面板设置中连续发现三类体验与运维问题：

1. 同一服务在不同页面显示为“失败”“服务不可用”“已安装”等，安装生命周期和运行健康状态混在一起。
2. 统一日志维护已经覆盖审计、任务、状态历史和告警事件，但底层删除仍应避免单次大事务。
3. 日志清理会创建后台任务，但页面没有把任务加入右上角任务进度，用户只能看到“已受理”，无法跟踪执行结果。

## 优先级建议

### P1：本轮立即修复

- 建立统一前端状态语义：安装生命周期只回答“是否安装成功”；运行健康只回答“服务是否可用”；服务器探测只回答“SSH/主机是否可达”。
- SQLite 日志清理改为批量 keyset 删除：审计日志、任务记录、状态历史、告警事件都按短事务分批清理，保留现有统一保留天数入口和数据库备份清理功能。
- 日志维护页执行清理后，把返回的 `taskId` 纳入全局任务进度。

### P2：后续独立修复

- 设置页多项配置保存改为同事务提交，避免部分成功。
- 复核各页面“刷新”按钮：只保留真正会重新加载页面当前数据的按钮；纯依赖 SSE 的页面删除无效刷新。
- Runtime/Agent 的 Condition reason 增加面向运维的中文处置建议。

### P3：质量补强

- 前端建立跨模块状态显示快照测试，覆盖 Dashboard、Apps、Database、Storage、Nacos。
- 维护任务增加控制面操作锁，避免多个清理任务并发抢 SQLite 写锁。

## P1 设计

### 1. 状态语义统一

新增共享前端状态语义模块，集中提供：

- `installLifecycleDisplayStatus(record)`：只根据安装生命周期字段判断 `installed` 或 `failed`，不把运行探测失败当成安装失败。
- `runtimeHealthDisplayStatus(status)`：把 `failed/error/unavailable/offline/no-endpoints` 等运行态统一显示为 `unavailable`。
- `serverReachabilityDisplayStatus(status)`：把服务器探测失败统一为 `unavailable`，成功统一为 `available`。

现有 Dashboard 与 Apps 的状态函数改为复用该模块，避免后续页面各写一套映射。

### 2. SQLite 日志清理批处理

Store 增加 batch 级删除方法：

- `DeleteAuditLogsBeforeBatch(cutoff, limit)`
- `DeleteFinishedTasksBeforeBatch(cutoff, limit)`
- `DeleteStatusSnapshotHistoryBeforeBatch(cutoff, limit)`
- `DeleteAlertEventsBeforeBatch(cutoff, limit)`

原有 `Delete*Before` 方法保留作为兼容入口，但内部循环调用 batch 方法。每批先按主键选出有限行，再在短事务内删除这些行。这样页面和 HTTP API 不变，已有环境升级无新增配置要求。

### 3. 日志清理任务进度

`LogMaintenancePanel` 调用 `/maintenance/retention/run` 后读取响应里的 `taskId`，调用 `useTaskProgressStore().track(...)`。按钮仍只负责受理任务，实际执行结果由全局任务进度/SSE 告知。

## 验收

- Dashboard 数据库/对象存储/运行态不再混用“失败”和“服务不可用”。
- 应用商店“已安装”页只用安装生命周期显示；服务监控失败不再污染安装状态。
- 日志维护可设置统一日志保留天数，例如 1 天；执行清理会覆盖审计、任务、状态历史、告警事件。
- 数据库备份清理仍保留在“数据维护”中，不被日志维护替代。
- 清理任务出现在右上角任务进度。
- 相关 Go/Vitest 测试与构建通过。

## P2 执行记录

### 1. 设置保存事务化

`/settings` 的多项设置保存改为先规范化请求字段，再通过 Store 的 `SetSettings` 在同一个 SQLite 事务中提交。任一 key 非法或写入失败时整批回滚，避免语言、部署并发、日志保留天数出现部分成功。

### 2. 实时页面刷新入口收敛

Database、Nacos、Storage 三个依赖后台实时推送/当前状态的页面，不再在页头显示误导性的“刷新”按钮。相关测试改为直接验证 realtime-backed 页面只展示连接状态；需要重新拉取状态的内部流程仍可调用页面 `load()`。

### 3. Runtime / Agent Condition 操作建议

Runtime Deployment 的 Condition reason 增加面向运维的分组建议：Agent 通道、服务健康、制品版本、下线状态、调和中、运行诊断。表格中保留机器 reason，同时展示中文/英文分组与下一步建议，避免只暴露英文机器码而无法判断处理方向。

### P2 验收

- Settings 多 key 保存具备事务回滚测试。
- Database/Nacos/Storage 页头无无效刷新按钮，旧测试不再依赖按钮。
- Runtime Condition reason 能展示分组与操作建议。
- Go 受影响包、全量前端测试、前端构建和 diff check 通过。

## P3 执行记录

### 1. 跨模块状态快照

新增前端状态快照测试，覆盖 Dashboard、Apps、Database、Storage、Nacos 的共享展示口径：

- 安装生命周期只表示“安装是否成功”，运行监控失败不再把“已安装”染成“失败”。
- 运行健康失败统一归一为 `unavailable`，展示为“服务不可用”，避免同一类服务监控失败在不同模块显示为“失败/服务不可用”两套口径。
- 服务器探测继续独立表示 SSH/主机可达性，不和应用运行健康混用。

Database、Nacos、Storage 残留的本地安装失败/运行失败判断改为复用共享 `status/semantics`，后续新增模块必须优先复用该语义模块。

### 2. 维护清理控制面锁

`/maintenance/retention/run` 执行统一日志清理前，现在会获取 `maintenance/retention-cleanup/mutate` operation lock。已有清理任务运行或锁未释放时，新请求直接返回 `409 OPERATION_LOCKED`，不会创建额外任务，避免多个日志清理任务并发抢 SQLite 写锁。

### P3 验收

- `go test ./internal/httpapi -run 'TestRetentionCleanup(RequiresControlPlaneOperationLock|StartsTask)$' -count=1` 通过。
- `go test ./internal/httpapi -count=1` 通过。
- `pnpm test:web -- --run src/status/moduleStatusSnapshot.test.ts src/status/semantics.test.ts` 通过，实际跑全 Web 测试 56 files / 441 tests。
- `pnpm web:build` 通过，仅保留既有 Rollup PURE/chunk-size 警告。
- `git diff --check` 通过。
