# 企业级架构优化任务记录

本文件用于沉淀 P0-P2 架构优化任务，避免把设计待办混入运行时任务中心。运行时任务仍只记录真实安装、卸载、检测、备份、回滚等可执行操作。

## 执行顺序

- 当前批次：P0 + 部分 P1
- 下一批次：剩余 P1
- 后续批次：P2

## P0 必做

- [ ] 运行时领域拆分：把 `httpapi/containers_aifar_runtime.go` 中的 AIFAR Runtime 控制面、状态转换、元数据解析、Docker 适配拆到独立领域包。
- [ ] Typed metadata：统一应用实例 metadata 的解析、读写和序列化，减少每个 handler/module 自己写 `map[string]any` 辅助函数。
- [ ] 数据库迁移：引入 `schema_migrations`，后续结构变更按版本记录，避免继续堆在单个 `migrate()` 中。
- [ ] 应用集群模型：引入 `app_clusters`、`app_cluster_members`，让 MySQL/Redis/Nacos/MinIO/AIFAR 不再只靠 `clusterId` 字符串散落关联。
- [ ] 通用操作锁：引入 `operation_locks`，把安装、卸载、升级、回滚、伸缩等互斥操作从应用私有锁逐步统一。
- [ ] 任务租约字段：为任务表预留 lease/idempotency/correlation 字段，后续支持幂等提交、重试恢复和多 worker 运行。

## P1 应做

- [ ] 发布制品与快照：引入 `app_release_artifacts`、`app_release_snapshots`，把升级/回滚需要的包、配置、manifest、基线链从 metadata 中抽离。
- [ ] 备份记录：引入 `app_backups`，把数据库备份、配置备份、应用发布前备份纳入统一审计与回滚链路。
- [ ] 凭证引用：引入 `credential_references`，删除凭证前能判断被哪些服务、集群、实例或发布引用。
- [ ] 状态历史：引入 `status_snapshot_history`，后端统一推送最新状态的同时保留变化轨迹，支持审计和告警回看。
- [ ] Store 索引与查询边界：补充关键表索引，把 dashboard/status/apps/database/storage 的查询路径固定下来。
- [ ] 后端推送 payload 标准化：统一 app/server/container/database/storage 的状态码、时间戳、版本号和 lastError 结构。

## P2 后续

- [ ] 应用生命周期标准化：把 registry 的安装、检测、卸载、升级、回滚、备份能力拆成显式接口，并补齐 capability 描述。
- [ ] 前端运行时拆分：把容器页中的 AIFAR Runtime 工作台拆成独立 feature 模块，容器页只保留 Docker runtime 管理。
- [ ] Docker 控制面升级：从 CLI/SSH 命令逐步收敛到稳定 adapter 边界，后续可替换为 Docker API client。
- [ ] MinIO 真实控制面：Bucket、User、AccessKey、Replication 从控制面记录升级为真实 MinIO/mc 操作并与任务系统对齐。
- [ ] 应用资源标准步骤：沉淀 manifest、preflight、plan、apply、record、check、uninstall 的标准文件结构和安装步骤模板。
- [ ] 企业级权限与审计：按模块/操作拆权限点，关键变更记录 request id、correlation id、任务 id 和资源快照。

## 本轮记录

- [ ] P0：落地版本化迁移框架与新增结构迁移。
- [ ] P0：落地通用 metadata 工具包。
- [ ] P0：落地通用操作锁、应用集群/成员模型。
- [ ] P0：为任务表补充 lease/idempotency/correlation 字段。
- [ ] P1：落地凭证引用、发布制品/快照、备份记录、状态历史的数据库与 Store 底座。
