# AIFAR Runtime 批量下线、Pod 指标与日志渲染修复设计

日期：2026-07-28  
状态：已确认范围，待实施

## 1. 背景

当前 AIFAR Runtime 工作台存在四个相互独立但都集中在同一页面的问题：

1. Deployments 只能逐行下线，连续操作会重复确认、重复创建任务，并与实例级变更互斥产生冲突。
2. 服务页仍显示 CPU 和内存列，但这些聚合值不是该页面的必要信息。
3. Pods 首次进入时只获取 Pod 列表，没有请求 Docker stats，CPU 和内存需要再次点击“刷新指标”才出现。
4. 实时日志的 SSE 已收到日志，页面统计也显示行数，但虚拟列表在滚动状态出现非有限值时会计算出非法窗口，最终不渲染任何日志行。

## 2. 目标

1. Deployments 支持勾选多个在线服务，并通过一个任务批量下线。
2. 服务页不再展示 CPU 和内存列；后端字段保持兼容。
3. 进入 Pods、切换 Runtime 实例和刷新 Pods 时自动获取 CPU 与内存指标。
4. 实时日志即使收到异常滚动位置，也能从合法窗口继续渲染，不再出现“有日志计数但内容区空白”。
5. 所有新增变更操作继续使用 worker、任务步骤、目标日志和审计，用户可见文案提供中英文。

## 3. 非目标

- 本次不修改制品更新、健康检查、发布提交或回滚逻辑。
- 本次不处理坏更新包导致的持续重启、失败隔离或后续更新阻塞问题。
- 不新增 Runtime 数据库表或字段。
- 不删除服务响应中的 `cpuPercent`、`memoryPercent`，也不改变 autoscaler 使用的指标。
- 不改变 Runtime 日志 SSE 协议、Docker 日志采集方式或日志解析规则。
- 不增加新的浏览器轮询器；继续复用现有全局 SSE 触发的运行时刷新。

## 4. 选定方案

采用“单个批量下线任务 + 前端指标加载语义修正 + 虚拟列表输入防御”的组合方案。

未采用的方案：

- 前端依次调用现有单服务下线 API：会被实例级互斥锁拒绝，或者形成难以解释的部分成功状态。
- 并行调用单服务下线 API：多个操作会同时修改同一份 Runtime 期望状态，不符合实例级单写者约束。
- 删除后端服务指标字段：会扩大兼容风险，也会影响 Pods、诊断和自动扩缩容的复用空间。
- 关闭日志虚拟化：可以绕过当前问题，但在高行数日志下会显著增加 DOM 和浏览器开销。

## 5. 批量下线设计

### 5.1 前端交互

`AifarRuntimeDeploymentsTab.vue` 增加 Element Plus selection 列，并在表格上方增加“批量下线（N）”危险操作按钮。

- 只有期望副本大于 0 且具备服务名的 Deployment 可勾选。
- 已下线、数据不完整或当前操作被禁用的行不可勾选，并沿用现有禁用原因。
- 未选择服务时按钮禁用。
- 点击按钮后弹出一次确认框，展示服务数量和服务名，并说明所有选中服务的期望副本会设为 0、对应 Nacos 代理会被摘除。
- 提交期间禁用选择与批量按钮；任务创建成功后清空选择并使用现有任务跟踪入口展示进度。
- 保留逐行“下线”按钮，满足单服务快捷操作和向后兼容。

前端上下文增加一个批量动作，不在组件内直接调用 API：

```ts
offlineAifarServices(rows: AifarRuntimeService[]): Promise<void>
```

API helper 使用固定集合路径，避免与单服务 `{service}` 路径冲突：

```http
POST /api/v2/containers/aifar/services/batch-offline?serverId=<server-id>
Content-Type: application/json

{
  "instanceId": "<runtime-instance-id>",
  "services": ["file", "gateway"]
}
```

响应继续使用现有任务响应：

```json
{ "taskId": "<task-id>" }
```

### 5.2 后端边界

HTTP handler 只负责：

1. 读取服务器、Runtime 实例和操作者。
2. 规范化服务名：去空白、去重、拒绝空集合和未知服务。
3. 在创建任务前取得现有实例级 Runtime mutation lock；冲突继续返回 `409`。
4. 创建一个 `aifar.scale.batch-offline` worker 任务，写入任务步骤、目标和审计。
5. 调用 AIFAR module 的批量下线业务接口。

Registry 增加可选批量能力，不改变现有单服务 `ScaleService`：

```go
type ServiceBatchScaleRequest struct {
    Server          store.Server
    Instance        store.AppInstance
    DesiredReplicas map[string]int
    Actor           string
    Reason          string
    TaskID          string
}

type ServiceBatchScaleModule interface {
    ScaleServices(context.Context, ServiceBatchScaleRequest, RunContext) error
}
```

AIFAR module 实现该能力，并在服务层执行一次实例状态读取、一次期望副本合并和一次远端 staged spec 提交。不能在业务层循环调用 `ScaleService`，否则仍会重复取得实例锁和重复改写正式 spec。

批量任务步骤固定为：

1. `load-runtime`
2. `validate-services`
3. `apply-batch-offline`
4. `record-runtime-state`

所有服务在远端提交前一次性校验。远端提交成功后，控制面一次性把所选服务的 `desiredReplicas` 记录为 0，并更新 Deployment、Pod、Endpoint 和 Nacos 代理状态。若远端提交失败，正式 spec 和控制面期望状态保持不变；若远端成功而控制面落库失败，沿用现有 `AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED` 处置，不执行反向拉起。

审计操作码为 `aifar.scale.batch-offline`，审计 target 使用 Runtime 实例 ID，details 只记录服务名、数量和 task ID，不记录完整 spec 或环境变量。

## 6. 服务页字段调整

`AifarRuntimeServicesTab.vue` 删除 CPU、内存两列及不再使用的格式化依赖。服务、应用名、状态、Endpoint、代理和镜像列保持不变。

`AifarRuntimeService` 类型以及后端 JSON 字段继续保留，避免改变 API 合同。此次变更只影响该表格的呈现层。

## 7. Pods 自动指标

Pods 的加载语义统一为“请求 Pods 时默认同时请求 stats”：

- 首次进入 Pods 调用 `ensureRuntimePodsLoaded(false, true)`。
- 切换服务器或 Runtime 实例后，如果当前页签为 Pods，也调用 `ensureRuntimePodsLoaded(false, true)`。
- Pods 页普通“刷新”调用 `ensureRuntimePodsLoaded(true, true)`。
- 全局 `aifar.runtime` SSE 事件触发现有合并刷新时，只要当前 Pods 指标已经加载或 Pods 正在显示，就继续携带 `includeStats=1`。
- “刷新指标”按钮保留为显式强制刷新入口，同样调用 `ensureRuntimePodsLoaded(true, true)`。

页面离开 Pods 后不启动额外定时器，也不主动持续抓取 stats。这样可保持现有事件驱动刷新模型，并避免隐藏页签产生 Docker stats 开销。

请求失败时保留最后一次可用的 Pods 数据和现有错误提示。指标从未成功加载时继续显示 `-`，不得把缺失值显示为 `0%`。

## 8. 实时日志渲染修复

根因修复集中在 `useAifarRuntimeLogViewport.ts`，不改变日志传输与解析：

- 非有限的 `scrollTop` 按 0 处理。
- `rowHeight`、`visibleCount` 和 overscan 参与计算前转为合法正数。
- 虚拟窗口起点钳制到 `[0, max(0, rows.length - 1)]`。
- top/bottom spacer 始终输出有限且不小于 0 的像素值。
- 清空日志、切换日志范围或重新连接时同步把响应式滚动位置和真实列表元素 `scrollTop` 重置为 0。
- 若自动滚动目标不存在或元素尺寸暂时不可用，保留合法窗口，不把 `NaN` 写回状态。

修复后，收到 200 行日志时至少渲染当前窗口内的日志行；关键词、级别、仅看错误、暂停和自动滚动行为保持不变。

## 9. 错误处理与兼容性

- 批量请求为空、包含未知服务或全部服务已下线时返回稳定的 `400` 错误，不创建任务。
- 同一实例已有 Runtime 状态变更时返回现有风格的 `409`，不排队、不拆分为多个任务。
- 任务中任一远端提交失败时任务整体失败，不把部分服务写成已下线。
- 单服务下线 API 和逐行操作保持兼容。
- 老 agent 缺少现有事务式 scale feature 时，继续走当前能力检查与升级提示；本次不引入新的发布或回滚 feature。
- 日志虚拟列表防御逻辑对空日志、少量日志和大于一个窗口的日志使用同一计算路径。

## 10. 测试策略

所有生产改动先增加失败测试，再做最小实现。

### 10.1 前端

- Deployments 选择逻辑：只允许选择可下线行，按钮数量正确，确认后只调用一次批量动作。
- API helper：路径、serverId、instanceId、去重后的 services 请求体正确。
- 服务页：CPU 和内存表头不再渲染，其余列仍存在。
- Pods：首次进入、实例切换、普通刷新和状态事件刷新都携带 `includeStats=1`。
- 日志 viewport：`scrollTop` 为 `NaN`、`Infinity`、负数和超大值时仍返回可见行，spacer 为有限非负值。
- 日志组件：统计有日志时 DOM 中存在 `.runtime-log-row`，清空和重新连接后滚动位置归零。

### 10.2 后端

- Handler：空集合、重复服务、未知服务、锁冲突、任务创建和审计。
- Registry/module：批量接口转发准确，单服务接口行为不变。
- Service：一次提交修改多个目标，保留所有非目标副本数，包括原本为 0 的服务。
- Service：远端失败不提升 staged spec、不写入部分控制面状态。
- Service：远端已提交但回包丢失时通过 agent readback 判定批量目标结果。
- Shell/fake remote：批量目标全部写为 0，正式文件只在 agent 成功后提升。

### 10.3 验证命令

```text
pnpm test:web
pnpm test
pnpm web:build
pnpm backend:build
git diff --check
```

最后在本地真实页面验证：多选下线确认框与任务跟踪、服务页列、Pods 首次指标、200 行实时日志正常渲染。真实服务器写入操作不属于本轮自动验收，除非用户另行授权。

## 11. 验收标准

1. 可勾选两个及以上在线 Deployment，并只创建一个批量下线任务。
2. 批量任务遵守实例级互斥，成功后所有选中服务期望副本为 0，未选服务期望副本不变。
3. 服务页不存在 CPU 和内存列，API 兼容字段未删除。
4. 首次进入 Pods 即显示可用的 CPU 和内存数据，无需额外点击“刷新指标”。
5. 日志统计显示有内容时，日志区域能渲染行；非法滚动输入不会产生空窗口或非法 spacer。
6. 中英文文案、任务步骤、目标日志和审计齐全。
7. 本次 diff 不包含制品更新、健康检查、回滚或失败隔离逻辑的修改。
