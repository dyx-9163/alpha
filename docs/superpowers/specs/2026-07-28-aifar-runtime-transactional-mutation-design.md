# AIFAR Runtime 事务化变更与可靠下线设计

日期：2026-07-28
状态：已确认，待实施

## 1. 背景

现场 `file` 服务下线任务最终成功，但任务耗时约 114 秒。主要时间消耗在 `aifar-agent` 的全局串行调和：交互式下线等待正在执行的周期性全量 resync，该轮还重建了无关的 `web-vue3` Pod。随后一次手工扩容在远端规范已改写、agent 尚未完成持久化时被取消，留下 `compose.env`、工作区 `runtime-spec.json` 与 agent 持久态不一致；后续其他服务的全量规范操作又把 `file` 副本数带回 1。

当前实现有三个相关问题：

1. 周期性全量调和与交互式变更共用 `reconcileMu`，但交互式操作没有优先级。
2. 不同服务的操作锁允许并发，而这些操作共同写入同一份 `compose.env` 和 `runtime-spec.json`。
3. 远端脚本先覆盖正式文件再调用 agent，任务取消或 SSH 中断可留下半提交状态。

## 2. 目标

1. 同一 AIFAR Runtime 实例任意时刻只允许一个状态变更。
2. 下线不等待无关服务的健康检查或重建，正常场景在数秒内完成。
3. 提交前允许取消；进入远端提交后不可取消，必须完成或明确失败。
4. 正式规范只在 agent 成功后更新，失败不得留下半提交文件。
5. 非目标服务的期望副本数严格保持原值，包括 `0`。
6. SSH 回包丢失时通过 agent 持久态判断真实结果，避免把已成功提交误判为失败。
7. 保持现有 API 路径、任务体系、审计模型和数据库结构兼容。

## 3. 非目标

- 不建设通用 generation/CAS 调度平台或新的持久化任务队列。
- 不改变 AIFAR Runtime 的单机 agent-proxy 架构。
- 不允许多个服务并发修改同一 Runtime 实例。
- 不在本次改造中重做完整发布、回滚或服务安装协议。
- 不自动替换现场 agent；真实服务器升级与写入验收需单独确认。

## 4. 选定方案

采用“实例级单写者 + worker 提交边界 + 暂存规范 + agent 交互式增量调和”。

未采用的方案：

- 仅增加实例锁：不能解决周期 resync 阻塞和取消半提交。
- 完整 generation/CAS 控制器：长期边界更完整，但协议、持久化和兼容改动超出本次问题所需范围。

## 5. 实例级 Runtime 单写者

所有会修改 Runtime 期望态、业务 Pod、入口或正式规范的操作统一竞争实例级操作锁。锁键为 Runtime 实例 ID，不再按服务名拆分并发。

纳入互斥范围：

- 服务扩容、缩容和下线；
- 单制品更新、批量更新和回滚；
- Runtime 参数更新；
- 服务安装；
- Runtime reconcile 与全部重启；
- Runtime/AIFAR 卸载、agent 卸载和残留清理；
- 自动扩容。

只读状态、日志、诊断估算与诊断导出不竞争该锁。Autoscaler 获取不到实例锁时跳过本轮，不创建排队任务。API 在创建人工变更任务前获取锁；已有变更时返回明确的 `409` 冲突和当前操作信息。

继续复用现有操作锁表和任务关联，不增加数据库迁移。服务级历史锁在启动恢复时按现有 TTL/任务恢复规则清理；新代码只创建实例级锁。

## 6. Worker 提交边界

worker 增加原子提交阶段能力，建议接口语义为 `TryEnterCommit(taskID) bool`，由 `worker.Logger` 暴露给 Runtime 服务层。

状态规则：

1. 准备阶段保留现有可取消 context。
2. `TryEnterCommit` 与 `Cancel` 在 worker manager 同一互斥区内竞争。
3. 若取消先发生，进入提交失败，任务按 `cancelled` 收口且不得执行远端变更。
4. 若提交先发生，worker 移除该任务的取消函数并记录提交阶段；后续取消返回 `cancelled=false`。
5. 提交阶段结束后，任务只能是 `success` 或 `failed`，不能再因原 context 被取消而改写为 `cancelled`。

任务日志新增中英文消息，明确显示“正在完成不可中断的 Runtime 提交”。该能力只在本次纳入的 Runtime 状态变更中启用，普通 worker 任务继续使用原取消语义。

## 7. 事务化远端规范

### 7.1 准备阶段

在持有实例锁后重新读取 `app_instances.metadata`，以其中的 `desiredReplicas` 作为用户期望态。只修改目标服务，其余服务的 `0/1/N` 原样保留，不再使用当前容器数量覆盖非目标期望值。

远端脚本在与正式文件相同的文件系统中创建任务唯一的暂存文件：

- `runtime/agent/runtime-spec.json.<task>.staged`
- `runtime/env/compose.env.<task>.staged`

`compose.env` 暂存文件以正式文件为基底，仅替换 `AIFAR_DESIRED_REPLICAS`；其他键保持不变。准备失败或提交前取消时删除暂存文件，正式文件不变。

### 7.2 提交阶段

进入 worker 不可取消阶段后，使用带独立上限的提交 context 执行远端命令。agent 使用暂存 spec 做交互式调和；成功后脚本才提升暂存文件为正式文件。

每次提升使用同目录临时文件和原子 rename。由于两个文件无法跨文件原子替换，agent 持久态是提交后的权威来源：任一镜像文件提升失败时，脚本必须读取 agent 持久 spec 修复正式 `runtime-spec.json`，并据此重建 `AIFAR_DESIRED_REPLICAS`，两份镜像一致后才允许返回成功。

脚本用独立 cleanup trap 删除暂存文件，不把凭据、env 内容或完整 spec 写入任务日志。

## 8. Agent 交互式增量调和

### 8.1 交互式优先级

区分启动加载/周期 resync 与交互式 Apply。Manager 记录当前周期 resync 的 cancel function；交互式 Apply 到达时先取消正在运行的周期 resync，再获取 `reconcileMu`。周期 resync 因此取消属于正常抢占，不记录为运行时故障；下一次 ticker 使用最新持久态继续。

交互式 Apply 不取消另一条交互式 Apply。面板实例锁是第一层保护，agent 的 `reconcileMu` 是节点内第二层保护。

### 8.2 变更集合

agent 比较当前持久 spec 和新 spec，以 `serviceName` 为键。Deployment 的副本数、镜像、revision、端口、env 文件、volume、资源、健康检查、命令、环境或标签任一变化均视为变更。

只对新增或变化的 Deployment 执行 `ensureDeployment`；未变化服务只刷新必要的状态快照，不执行启动、重启、替换或健康等待。副本数变为 `0` 的 Deployment 优先处理，删除 Pod、清空 Endpoint 并标记 offline。移除整个服务仍沿用现有卸载生命周期，不在增量 Apply 中隐式删除。

变更调和成功后，agent 更新路由、Endpoint、Deployment 状态和本地持久 spec，再执行 Nacos proxy 同步。目标服务副本为 `0` 时必须注销对应 Nacos proxy。

### 8.3 Feature 与升级

agent `status.features` 新增稳定 feature，例如：

- `interactive-reconcile-priority`
- `runtime-delta-apply`

所有 Runtime 变更在提交前调用现有 agent 能力检测；缺少任一 feature 时先通过 `ensureRuntimeAgent` 升级并验证。旧 API 路径和现有 `reconcile-runtime --spec` 命令保持可用。

## 9. 结果验收与异常恢复

### 9.1 正常验收

下线提交后只验收目标服务：

- agent `desiredReplicas=0`；
- `currentReplicas=0`、`readyReplicas=0`；
- 无目标服务 Pod 容器；
- Endpoint 为空；
- Nacos proxy 为未注册/offline。

之后再写入面板 metadata、Deployment、ReplicaSet、Pod 和 Endpoint 控制面记录并释放实例锁。

### 9.2 SSH 回包丢失

远端命令返回连接中断或超时时，面板重新连接并读取 agent 持久 spec 与目标服务状态：

- 与本次提交一致且目标验收通过：按成功继续提升/修复镜像文件并落库。
- 与本次提交不一致：任务失败，正式文件保持或恢复为 agent 权威状态。

### 9.3 Agent 失败

agent 在持久化新期望态前失败时不提升暂存文件。周期 resync 继续依据旧持久态恢复运行。日志返回目标 Deployment 的明确诊断，不输出敏感 env。

### 9.4 控制面落库失败

远端验收成功后不通过重新启动服务进行补偿。落库先按短退避重试；仍失败时任务以 `failed` 收口，任务错误使用稳定机器码 `AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED`，并在现有审计记录中写入 task ID、Runtime 实例 ID 和 agent spec 摘要；不得写入完整 spec、环境变量或凭据。后续人工检测或 Runtime reconcile 读取 agent 权威期望态，修复 metadata 与编排记录，不得使用陈旧 metadata 把服务拉回。

## 10. 兼容与现场恢复

- 不增加数据库 schema。
- `/api/v2` 路径和任务响应结构保持兼容。
- 旧 Runtime spec 与 metadata 无需离线迁移。
- 首次新式变更在实例锁内重新读取控制面 `desiredReplicas` 并生成完整暂存规范，可清除历史取消任务遗留的工作文件不一致。
- 若最近任务错误或审计中存在 `AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED`，则 agent 持久态优先于 metadata；该标记复用现有任务和审计字段，不新增数据库字段。正常完成任务仍以 metadata 记录的用户期望态生成下一次变更。
- 本地实现通过后只重启本地开发 `aifar-server`。真实目标机 agent 的替换和写入验收需要用户再次确认。

## 11. 测试策略

所有生产代码先有失败回归，再做最小实现。

### 11.1 Worker

- 取消与进入提交阶段并发时只有一个结果获胜。
- 提交前取消保持 `cancelled`。
- 提交后取消返回 false，任务最终为 success 或 failed。
- 未启用提交边界的普通任务取消行为不变。

### 11.2 操作锁与服务层

- 同一实例不同服务的 Runtime 变更互斥。
- Autoscaler 在实例锁存在时不创建任务。
- 目标服务设为 0，非目标服务副本数包含 0 时原样保留。
- agent 成功前正式文件无写入；失败清理暂存文件。
- SSH 回包丢失后 readback 一致可按成功收口。
- 控制面写失败不反向重启已经下线的服务。

### 11.3 Agent

- 被阻塞的周期 resync 会被交互式 Apply 取消。
- 交互式 Apply 不取消另一条交互式 Apply。
- 只调和变化 Deployment；无关不健康服务不阻塞。
- zero-replica Deployment 优先删除 Pod 并清空 Endpoint。
- 成功后持久 spec、状态与 Nacos proxy 一致。
- 抢占的周期 resync 不产生错误状态或错误日志。

### 11.4 API 与门禁

- 实例已有 Runtime 变更时返回 409 和当前操作。
- 提交阶段取消返回 `cancelled=false`。
- 运行相关 Go 包测试、`pnpm test`、后端构建与 `git diff --check`。
- 真实 SSH/Docker 自动测试继续使用 fake remote/runner，不连接现场服务器。

## 12. 验收标准

1. 无其他变更时，单服务下线数秒内完成。
2. 周期 resync 正在重建其他服务时，下线会抢占并优先执行。
3. 无关不健康服务不能阻塞目标服务下线。
4. 下线后连续观察至少三个 15 秒 resync 周期，服务不得恢复。
5. 重复点击、跨服务并发和自动扩容均不能覆盖实例级期望态。
6. 提交前取消不改远端；提交后取消被拒绝且任务完成收口。
7. SSH 回包丢失、agent 失败和控制面落库失败均有确定、可审计的终态。
8. 现有 API、数据库和旧 spec 保持兼容，旧 agent 可在提交前自动升级。
