# MySQL 备份 Schema 选择与诊断增强设计

## 目标

在现有 standalone 与 InnoDB Cluster 逻辑全量备份上增加实时 Schema 分类和业务库手动选择，同时修复 MySQL Shell 8.0.36 不支持 `--raw` 导致的检查失败，并把经过脱敏的远端 stderr 写入任务日志。

## 已确认的交互与安全边界

- 打开备份弹窗时读取实时 Schema。standalone 读取该实例；InnoDB Cluster 先校验三成员健康并解析当前 PRIMARY，再从 PRIMARY 读取。
- Schema 分为 `server-system`、`cluster-metadata`、`business` 三类。
- MySQL Server 固有系统库包括 `information_schema`、`mysql`、`ndbinfo`、`performance_schema`、`sys`（仅展示实际存在的库）。
- 名称以 `mysql_innodb_cluster_metadata` 开头的 Schema 归为 MySQL Shell/InnoDB Cluster 内部管理元数据库，包括备份或 previous 变体。
- 三类均展示；只有业务库可勾选，默认全选。系统库和集群元数据库禁选并说明不会进入业务数据恢复。
- 提交时必须至少选择一个业务库。后端在真正导出前重新检查实时 Schema，拒绝空选择、重复项、未知库、系统库和集群元数据库。
- 实际导出继续使用 MySQL Shell 8.0.36 的 `util.dumpInstance()`，通过 `includeSchemas` 精确限定选择，保留现有 dumpInstance v2 元数据解析和恢复契约。账号仍通过 `users:false` 排除。
- 清单 `schemas` 只记录实际选择并验证的业务库；`excludedSchemas` 记录强制系统排除项、内部元数据库及未选择业务库。
- 恢复仍只依据备份清单恢复被选择的业务库，不新增系统库或集群元数据库恢复入口。

## API 与数据流

- 新增 `GET /api/v2/apps/instances/{id}/backup-schemas`，返回非敏感来源摘要和按名称排序的 Schema 项：`name`、`category`、`selectable`、`selectedByDefault`。
- registry 增加只读 `BackupSchemaModule` 能力，HTTP handler 只负责鉴权、实例快照、模块调用和错误映射。
- `POST /api/v2/apps/instances/{id}/backup` 请求增加必填 `schemas: string[]`。
- worker 开始后重新解析当前拓扑和 Schema；HTTP 返回的发现结果不是授权数据，只用于交互展示。

## 诊断

- 所有 MySQL Shell SQL 模式命令从 `--raw` 改为 `--result-format=tabbed`，保留 `--skip-column-names` 以维持机器解析格式。
- 备份检查命令失败时只把 stderr 写入任务错误日志，不写 stdout。
- stderr 先经过通用键值脱敏，再按当前已解密凭据做精确替换；API 错误和 target 错误仍只返回稳定码/本地化文案。

## 验收

- 单体和集群弹窗均能显示三类库，业务库默认全选并可逐项取消；禁选项不能进入请求。
- 空业务选择不能提交，服务端绕过前端同样拒绝。
- 导出脚本和 dry-run 都包含相同的 `includeSchemas`。
- manifest/verification 只包含所选业务库。
- MySQL Shell 命令中没有 `--raw`，失败日志包含可诊断 stderr，但不包含密码、token 或 secret 值。
- Go 针对性测试、Web 组件/逻辑测试、前端构建和仓库回归测试通过。
