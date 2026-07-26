# AIFAR Runtime 发布历史整理与安全删除设计

## 目标

精简“入口与发现”服务表格，将“发布历史”移动到 Runtime 资源页签末尾，并为发布历史增加安全的删除动作。

## 页面调整

- “入口与发现”服务表格移除 `Nacos` 和“最近错误”两列，保留服务、应用名、发现地址和 Endpoint。
- Runtime 资源页签顺序调整为：Deployments、服务、Pods、日志、入口与发现、发布历史。
- 发布历史操作列保留“回滚”，并在其后增加“删除”。删除前必须二次确认。

## 删除语义

删除仅作用于面板控制面：删除指定 `app_releases` 记录以及同一实例、同一发布 ID 下的 `app_release_artifacts`、`app_release_snapshots` 关联索引。

删除不执行 SSH，不删除目标服务器 `releases/<releaseId>` 目录，不停止或重建容器，也不改变当前 Runtime 期望状态。

以下记录禁止删除并返回 409：

- 实例元数据 `currentRevision` 或 `releaseId` 指向的当前生效版本。
- 状态为 `pending` 或 `running` 的执行中版本。
- 被其他保留发布记录的 `baseReleaseId` 或 `rollbackTo` 引用的版本。

其他成功或失败记录允许删除。不存在的实例或发布记录返回 404；非 AIFAR 实例返回 400。

## API 与审计

- 新增 `DELETE /api/v2/apps/instances/{id}/aifar/releases/{releaseId}`。
- 继续要求 `apps.manage` 权限。
- 删除成功返回被删除的 `releaseId`，并写入 `aifar.release.delete` 审计记录。
- 删除为本地短事务，不创建远程 worker 任务。

## 前端交互

- 删除按钮受现有应用管理权限控制。
- 点击后展示发布 ID 和“只删除面板记录、不删除远端制品”的确认说明。
- 删除期间仅禁用当前行，成功后清除发布缓存并立即刷新列表。
- 后端拒绝原因沿用统一 API 错误展示，不在前端复制安全判断。

## 验证

- Store 测试覆盖事务删除主记录、制品索引和快照索引。
- HTTP 测试覆盖普通记录删除，以及当前版本、执行中版本、被引用版本的拒绝。
- 前端 API 测试覆盖 DELETE URL 编码。
- 前端规则测试覆盖页签顺序和入口表格可见列。
- 运行 `pnpm test`、`pnpm test:web`、`pnpm web:build` 和 `git diff --check`。
