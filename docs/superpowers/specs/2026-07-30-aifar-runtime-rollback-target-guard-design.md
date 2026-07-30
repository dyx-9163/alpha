# AIFAR Runtime 回滚目标保护设计

日期：2026-07-30  
状态：设计已口头确认，待书面复核

## 1. 问题

发布历史页面当前把所点行的 `releaseId` 直接作为 `targetReleaseId`。后端只根据发布成功、存在 `changedServices` 和制品判断是否可回滚，没有把目标发布与各服务当前 revision 比较，也没有排除 `kind=rollback` 的审计记录。

因此会出现两类错误行为：

1. 对当前已生效发布点击“回滚”，重复部署相同制品。
2. 对最新的 rollback 审计记录再次点击“回滚”，使用其引用的相同制品重新生成 revision，造成历史和实际版本语义混乱。

## 2. 已确认语义

继续采用“回滚到所选版本”，不改成“撤销所选发布”。

示例：服务从 A 升级到 B 后，要恢复 A，用户选择 A 并执行“回滚到此版本”；选择 B 不应创建任务。

## 3. 方案比较

### 方案 A：按服务 current revision 计算可回滚范围（采用）

发布列表返回所选发布中仍需切换的服务集合。前端只提交该集合；后端在请求校验和取得运行时编排锁后都复验当前 revision。rollback 审计记录不作为目标发布。

优点是兼容单服务升级、批量升级和各服务处于不同 revision 的情况，并能在前后端同时阻止 no-op。代价是发布列表响应需要增加少量派生字段。

### 方案 B：点击某条发布时自动读取 `serviceRevisionsBefore`

把按钮解释为“撤销这次发布”。该方案与现有确认文案、API `targetReleaseId` 和用户选择稳定目标版本的模型冲突，连续回滚时也更难理解，因此不采用。

### 方案 C：只禁用列表第一条或最新时间记录

实现简单，但 AIFAR 支持单服务独立升级，最新全局记录不等于每个服务的当前 revision；rollback 审计记录也可能排在第一条，因此不采用。

## 4. API 与后端设计

### 4.1 发布列表派生字段

`GET /api/v2/apps/instances/{id}/aifar/releases` 在现有字段外增加：

- `currentServices: string[]`：该发布 `changedServices` 中当前 revision 等于该发布 `releaseId` 的服务。
- `rollbackServices: string[]`：具备目标制品且当前 revision 不等于该发布 `releaseId` 的服务。
- `rollbackUnavailableReason: string`：稳定机器码，取值为空、`ROLLBACK_RECORD`、`ALREADY_ACTIVE` 或 `ARTIFACT_UNAVAILABLE`。

`rollbackAvailable` 仅在以下条件全部满足时为 `true`：

1. 发布状态为 `success`。
2. `kind` 不是 `rollback`。
3. 至少有一个 `rollbackServices`。
4. 每个返回的目标服务都有完整 `file`、`sha256` 和 `remotePath`。

这些字段均由实例 metadata 中的 `activeEndpoints`、`serviceRevisions`、`currentRevision`/`releaseId` 按现有 `currentRevisionForService` 优先级计算，不新增数据库字段。

派生逻辑放在 AIFAR module/service，并通过 registry 的可选只读 inspector 接口暴露给 HTTP handler。handler 只负责加载实例与发布记录、调用 inspector 和组装响应，不复制 AIFAR revision 业务规则，也不直接依赖具体 AIFAR package。

### 4.2 回滚请求校验

`ValidateArtifactRollback` 增加：

- 拒绝 `kind=rollback` 的目标记录。
- 拒绝请求服务不属于目标发布或缺少完整制品。
- 拒绝所有请求服务当前都已处于目标 revision 的 no-op 请求。

任务执行取得最新实例和编排锁后，在上传脚本前用同一规则复验。若并发状态变化使全部目标服务已经生效，任务明确失败，不上传脚本、不重启 Pod、不写成功 release。

对于部分服务已处于目标 revision 的场景，页面只提交 `rollbackServices`。直接 API 调用如果混入已处于目标 revision 的服务，后端拒绝请求，不静默缩小调用方声明的范围。

## 5. 前端设计

- 按钮文案改为“回滚到此版本”/`Roll back to this release`。
- 请求体中的 `services` 使用后端返回的 `rollbackServices`，不再直接使用 `changedServices`。
- 当前已生效发布禁用按钮，tooltip 显示“所选服务已处于此版本”。
- rollback 审计记录禁用按钮，tooltip 说明该记录仅用于审计，应选择实际制品发布。
- 制品不完整继续显示现有不可回滚原因。
- 发布 ID 旁显示轻量“当前”标记；仅当 `currentServices` 非空时展示，tooltip 列出对应服务，兼容部分服务当前的情况。
- 确认框继续显示明确目标 release，并说明业务数据不会自动回滚。

视觉沿用现有 Element Plus 小尺寸按钮、Tag 和 Tooltip，不新增页面布局或样式体系。

## 6. 错误与一致性

- 前端禁用只是体验保护，后端校验始终是最终边界。
- no-op 请求不创建远端变更；预校验发现时不创建任务，锁后发现时任务失败并记录清晰日志。
- 本次不改变 shell 回滚脚本、release 制品目录、保留策略或业务数据处理。
- 本次不新增数据库迁移，不改变既有发布记录。

## 7. 测试策略

遵循 TDD，先写失败测试并确认失败原因，再做最小实现。

后端覆盖：

1. 当前单服务发布返回 `ALREADY_ACTIVE` 且不可回滚。
2. 旧发布只返回 revision 不同且制品完整的 `rollbackServices`。
3. 部分服务已处于目标 revision 时派生集合准确。
4. rollback 审计记录返回 `ROLLBACK_RECORD`。
5. 请求当前 revision 或 rollback 审计记录时校验失败。
6. 取得锁后状态变为目标 revision 时，不上传、不执行远端脚本、不记录成功回滚。

前端覆盖：

1. 当前版本、rollback 审计记录和缺制品记录显示正确禁用原因。
2. 可回滚行提交 `rollbackServices` 而不是 `changedServices`。
3. 按钮和确认文案明确为“回滚到此版本”。
4. 当前服务标记仅在 `currentServices` 非空时显示。

验证命令：

```text
pnpm test:web
pnpm test
pnpm web:build
pnpm backend:build
git diff --check
```

## 8. 验收标准

1. 当前已生效发布不能从 UI 或 API 触发重复回滚。
2. rollback 审计记录不能作为回滚目标。
3. 单服务和批量发布按每个服务当前 revision 正确计算可回滚集合。
4. 并发状态变化后，后端锁内复验能阻止 no-op 远端变更。
5. 选择旧的实际制品发布仍能创建原有 worker 任务并完成回滚。
6. 不新增数据库迁移，不改变业务数据，不触碰当前工作区无关改动。
