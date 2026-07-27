# MySQL 单体与 InnoDB Cluster 备份还原设计

## 1. 状态

- 设计日期：2026-07-27
- 用户确认：已确认
- 实施状态：未实施
- 目标版本：首版逻辑全量备份与维护窗口还原

## 2. 目标与边界

本设计在现有 AIFAR Deployment 中为 MySQL standalone 和 InnoDB Cluster 增加一等公民的 backup/restore 生命周期，并继续复用 worker、task、step、target、log、audit、`app_backups`、`app_clusters`、`operation_locks`、SSH adapter 和应用 registry。

首版边界：

- 备份使用随 MySQL 安装包部署的 MySQL Shell `util.dumpInstance()`。
- 备份允许在线执行，使用 `consistent: true` 创建一致性逻辑快照。
- 还原必须进入维护窗口，业务写入停止后执行。
- standalone 与 InnoDB Cluster 使用同一备份格式。
- 集群只生成一份 cluster-level 备份，不在每个成员上重复 dump。
- 只备份业务 schema、事件、存储过程和触发器；不备份系统 schema、InnoDB Cluster metadata 和账号。
- 首版不支持增量备份、binlog 时间点恢复、跨主版本还原、跨拓扑还原、XtraBackup 或存储快照。
- 数据还原与包级回滚保持独立，不能由升级回滚隐式触发数据还原。

## 3. 方案选择

### 3.1 推荐：MySQL Shell 逻辑备份

优点：现有节点已经安装 mysqlsh，standalone 和 InnoDB Cluster 可以共用一套脚本，支持并行、压缩、一致性检查、dry run 和 progress file，最适合先形成平台闭环。

代价：大库恢复时间长于物理备份，不能提供首版 PITR。

### 3.2 暂不采用：XtraBackup 物理备份

优点是大数据量下备份与恢复更快；缺点是需要新增离线资源、版本兼容矩阵、物理目录权限、prepare/copy-back、集群成员重建与更复杂的失败恢复。待逻辑备份闭环稳定并有明确大库 RTO 后再引入。

### 3.3 暂不采用：LVM 或存储快照

快照速度快，但依赖目标机器的卷管理、文件系统和存储平台，不适合作为通用离线部署能力。

## 4. 备份仓库

新增独立配置：

```env
AIFAR_MYSQL_BACKUP_DIR=data/mysql-backups
AIFAR_MYSQL_BACKUP_KEEP_LAST=5
```

不得复用 `AIFAR_DATABASE_BACKUP_DIR`，该配置继续专用于面板 SQLite 控制面备份。

数据库节点只承担临时 dump：

```text
/aifar/apps/mysql/_backup/<taskId>/
```

持久产物保存在面板备份仓库：

```text
data/mysql-backups/<backupId>/
  backup-manifest.json
  dump.tar
  checksums.txt
```

生产环境应将 `AIFAR_MYSQL_BACKUP_DIR` 放在独立挂载盘或 NAS。备份不能只留在 MySQL 数据节点，否则节点级故障会同时丢失源数据和备份。

备份仓库采用**受信单写者**威胁模型：仓库目录由面板服务账号独占，Linux 上目录权限固定为 `0700`，不得由其它面板进程、脚本或同 UID 工具直接读写。`Prepare`、`Commit`、`Verify`、`Delete` 等仓库生命周期操作必须持有同一仓库根的进程内互斥锁和根目录 `.aifar-repository.lock` 跨进程排他锁；锁文件权限为 `0600`。未取得锁时操作失败关闭，不继续读写文件。

该边界继续防御 API 输入造成的路径逃逸、符号链接、非普通文件、校验和不一致和误删，但不承诺抵御 `root` 或能够以面板服务 UID 绕过仓库 API 的恶意并发进程。运维侧直接修改仓库文件属于不受支持操作；需要人工介入时必须先停止面板服务并完成离线校验。NAS 必须提供与本地 Linux 文件系统等价的排他锁和原子 rename 语义，否则不得作为此版本的仓库。

现有 SSH adapter 只有本地到远端上传能力；实施时增加受控 SFTP Download，将数据库节点上的单一归档流式下载到面板 `.partial` 文件。SHA256 和大小复验通过后原子提升到最终路径，随后清理源节点临时目录。恢复时复用现有 Upload 将归档上传到目标 MySQL 节点。

## 5. 备份数据模型

继续使用现有 `app_backups` 表，不新增备份表。字段语义：

- `app=mysql`
- `instance_id`：standalone 实例 ID；集群备份使用触发操作的代表 MySQL 实例 ID
- `server_id`：实际执行 dump 的服务器 ID
- `backup_type=logical-full` 或 `pre-restore`
- `status=pending|running|success|failed|deleted`
- `path`：面板受控备份目录下的最终路径
- `checksum`、`size`：最终归档 SHA256 和大小
- `task_id`：创建该备份的 worker task
- `metadata`：`clusterId`、版本、schema 摘要、来源 PRIMARY、成员快照等非敏感信息

`backup-manifest.json` 至少包含：

```json
{
  "backupId": "backup_mysql_20260727_xxx",
  "app": "mysql",
  "topology": "standalone",
  "instanceId": "instance_xxx",
  "clusterId": "",
  "sourceServerId": "server_xxx",
  "sourceEndpoint": "192.168.1.10:3306",
  "sourceServerUuid": "uuid",
  "mysqlVersion": "8.0.36",
  "mysqlShellVersion": "8.0.x",
  "schemas": ["aifar_nacos", "aifar_business"],
  "excludedSchemas": [
    "mysql",
    "sys",
    "performance_schema",
    "information_schema",
    "mysql_innodb_cluster_metadata"
  ],
  "consistent": true,
  "gtidExecuted": "...",
  "createdAt": "2026-07-27T00:00:00Z",
  "taskId": "task_xxx"
}
```

集群备份还需记录 `clusterName`、备份时 PRIMARY、所有 MySQL 成员 endpoint/role/status 和 Router 摘要。manifest、metadata、日志和审计均不得包含密码、私钥、恢复账号或完整敏感连接串。

## 6. Registry、API 与权限

在 registry 增加可选生命周期接口：

```go
type BackupModule interface {
    PlanBackup(context.Context, BackupRequest) ([]InstallStepPlan, error)
    Backup(context.Context, BackupRequest, RunContext) error
}

type RestoreModule interface {
    PlanRestore(context.Context, RestoreRequest) ([]InstallStepPlan, error)
    Restore(context.Context, RestoreRequest, RunContext) error
}
```

API：

```text
GET    /api/v2/apps/instances/{id}/backups
POST   /api/v2/apps/instances/{id}/backup
POST   /api/v2/apps/instances/{id}/restore
POST   /api/v2/apps/backups/{backupId}/verify
DELETE /api/v2/apps/backups/{backupId}
```

规则：

- backup、restore、verify 都创建 worker task，返回 task id 并写审计。
- 删除备份为受控本地文件删除；需校验记录、路径、文件类型和 SHA256 归属，写审计。
- backup 需要 `apps.manage`；restore 和灾难重建建议仅 owner 可执行。
- standalone 使用 `app-instance/<instanceId>` operation lock。
- cluster 使用 `app-cluster/<clusterId>` operation lock，阻止同时 install/check/delete/start/backup/restore/credential rotation。
- MySQL 密码必须来自实例绑定凭据并在任务执行时解密；不得继续使用默认密码作为 restore 凭据来源。

## 7. MySQL Shell 安全执行

连接目标固定为受控实例的 `127.0.0.1:<port>`，不接受用户输入自由 endpoint 或自由 shell。密码通过 `0600` 临时 secret context 传入 mysqlsh，不出现在命令行、日志、manifest 或审计；临时文件在成功、失败和取消路径都必须清理。

备份核心选项：

```javascript
util.dumpInstance(dumpDir, {
  consistent: true,
  threads: 4,
  compression: "zstd",
  users: false,
  showProgress: false,
  excludeSchemas: ["mysql_innodb_cluster_metadata"]
})
```

MySQL Shell 自动排除 `information_schema`、`mysql`、`performance_schema`、`sys` 等系统 schema；AIFAR 显式排除 `mysql_innodb_cluster_metadata` 并设置 `users:false`，避免旧集群 metadata、root 账号和内部 recovery 账号进入备份。

还原核心选项：

```javascript
util.loadDump(dumpDir, {
  threads: 4,
  loadUsers: false,
  ignoreExistingObjects: false,
  skipBinlog: false,
  showProgress: false
})
```

首版不应用源端 `gtidExecuted` 到目标。源 GTID 只用于 manifest、诊断和兼容性判断；还原产生新的 GTID。这样 standalone 原地还原与健康 InnoDB Cluster PRIMARY 还原使用相同语义，集群 restore 事务能够经 Group Replication 同步。

`util.loadDump()` 使用 `LOAD DATA LOCAL INFILE`。任务必须读取并保存 `@@GLOBAL.local_infile` 原值，导入前临时设为 ON，并在所有仍可连接目标 MySQL 的成功、失败和取消路径恢复原值。若目标在 finally 阶段不可达，任务保持 failed，记录不含 secret 的待恢复标记；后续任何 backup/restore/check 必须先调和该标记并验证变量已回到原值。开启窗口只覆盖受控 restore 阶段。

## 8. Standalone 备份

步骤：

```text
load-instance
acquire-instance-lock
resolve-credential
inspect-mysql
check-backup-space
prepare-workdir
dry-run-dump
dump-instance
build-manifest
package-backup
transfer-backup
verify-checksum
record-backup
apply-retention
cleanup-workdir
```

准入条件：实例存在且为 standalone、MySQL 可认证、mysqlsh 可执行、业务 schema 均为允许备份的名称、备份目录和工作目录为受控非符号链接路径、源节点和面板仓库空间足够。

只有归档在面板仓库完成 SHA256 复验并原子提升后，`app_backups.status` 才能标记 success。保留策略只在新备份成功后执行，至少保留最近一次成功备份；删除失败记录告警但不反向使新备份失败。

## 9. Standalone 还原

步骤：

```text
load-backup
acquire-instance-lock
verify-maintenance-confirmation
verify-manifest
verify-checksum
verify-version
create-pre-restore-backup
upload-backup
extract-backup
dry-run-load
capture-local-infile
enable-local-infile
drop-target-schemas
load-dump
restore-local-infile
verify-schemas
verify-data
record-restore
cleanup-workdir
release-lock
```

规则：

- 用户必须明确确认业务写入已停止。
- 备份和目标都必须为 standalone，主版本必须相同；首版默认完整版本相同。
- 健康可读的目标必须先创建 `pre-restore` 备份；目标不可读时，只能在灾难确认后跳过。
- 不支持合并式恢复。根据 manifest 精确删除目标业务 schema 后重新导入，`ignoreExistingObjects` 固定为 false。
- 删除范围严格来自已验证 manifest 中的业务 schema allowlist，永不删除系统 schema。
- 校验至少包括 schema/table 数量、关键表抽样、MySQL ping 和 task manifest 摘要。

## 10. InnoDB Cluster 备份

步骤：

```text
resolve-cluster
acquire-cluster-lock
resolve-cluster-credential
inspect-cluster-status
select-primary
verify-members
check-backup-space
dump-primary
snapshot-topology
build-manifest
package-backup
transfer-backup
verify-checksum
record-backup
cleanup-workdir
```

规则：

- 通过 `clusterId` 展开所有 MySQL 数据节点；Router 只写入拓扑摘要，不执行 dump。
- 通过 mysqlsh `cluster.status({extended: 1})` 找到 ONLINE PRIMARY。
- 直接连接 PRIMARY 3306，不经 Router。
- 只执行一次一致性 `util.dumpInstance()`。
- 首版要求三个 MySQL 成员全部 ONLINE；OFFLINE、RECOVERING、ERROR 或 metadata 不完整时拒绝备份并提示先修复集群。

## 11. 健康 InnoDB Cluster 数据还原

适用于集群仍健康、恢复误删或错误修改业务数据，不重建集群。

步骤：

```text
resolve-cluster
acquire-cluster-lock
verify-maintenance-confirmation
verify-backup
inspect-cluster-status
select-primary
create-pre-restore-backup
upload-backup-to-primary
capture-local-infile
enable-local-infile-on-primary
drop-target-schemas-on-primary
load-dump-on-primary
wait-replication-catchup
restore-local-infile
verify-all-members
verify-router
record-restore
```

规则：

- 应用停止写入，MySQL 集群保持运行。
- 只在当前 PRIMARY 上删除业务 schema 和执行 load。
- `skipBinlog:false`，所有 restore DDL/DML 经 Group Replication 同步到 SECONDARY。
- 禁止在三个成员上重复导入。
- PRIMARY 在 drop/load 阶段发生切换或连接中断时，任务立即失败并保持维护状态；不得自动改连新 PRIMARY 继续写。
- 成功门禁包括：三个成员 ONLINE、SECONDARY applier 队列归零、schema/table/抽样数据一致、Router 6446 真实读写成功。

## 12. InnoDB Cluster 完整停机与灾难重建

### 12.1 数据仍完整的 complete outage

如果三台 Group Replication 都停止但数据目录仍完整，继续使用现有 `dba.rebootClusterFromCompleteOutage()` 流程。必须从具有 GTID superset 的最新成员恢复；这不是 backup restore，不删除 schema、不导入 dump。

### 12.2 数据损坏的 disaster rebuild

适用于无法使用 complete-outage reboot 的数据损坏场景：

```text
validated backup
  -> clean seed restore
  -> dba.createCluster()
  -> add member B with clone
  -> add member C with clone
  -> re-bootstrap Router
```

步骤：

```text
validate-disaster-confirmation
acquire-cluster-lock
verify-backup
stop-business-and-router
prepare-clean-seed
restore-dump-to-seed
verify-seed-data
create-cluster-on-seed
prepare-clean-secondary-b
clone-secondary-b
prepare-clean-secondary-c
clone-secondary-c
wait-cluster-online
bootstrap-router
verify-router
update-control-plane
record-restore
```

规则：

- 用户输入目标服务器已保存 SSH 密码进行破坏性二次确认。
- 三台目标使用与备份兼容的 MySQL 版本。
- disaster rebuild 的逻辑目标始终是原 InnoDB Cluster；seed 在装载数据时暂时以单节点运行，不属于允许 standalone 与 cluster 互相还原的跨拓扑例外。
- 旧数据目录优先原子移动到按 task ID 隔离的 quarantine 目录；空间不足或路径归属不明时 fail closed，不直接删除。
- seed 必须为干净实例；restore 不加载旧 `mysql_innodb_cluster_metadata` 和账号。
- seed 数据校验通过后执行 `dba.createCluster()`，B/C 使用 `cluster.addInstance(..., {recoveryMethod:"clone"})` 完整覆盖加入。
- Router 必须重新 bootstrap，不能假设旧 metadata 自动指向新集群。
- 控制面逻辑 `clusterId` 可保留；metadata 增加 `restoreGeneration`、`restoredFromBackupId`、`restoredAt` 和新的 PRIMARY/成员状态。
- 失败后保持业务和 Router 停止；不能把半完成重建标记为成功，也不能自动覆盖 quarantine 中的旧数据。

## 13. 前端交互

修改 Database 页面前必须遵循 `design/ant-design-system-portable202606.md`。

在 standalone 实例卡片和 InnoDB Cluster 组卡片提供：

- 立即备份
- 备份记录
- 校验备份
- 恢复数据
- 灾难重建集群，仅集群展示

备份弹窗首版只提供备份名称、线程数、每线程限速和保留数量；默认值由后端返回。恢复弹窗展示备份来源、版本、拓扑、schema、时间、大小、checksum 和影响范围，并要求维护确认。灾难重建额外展示目标三节点、quarantine 策略、Router 影响和 SSH 密码确认。

页面只提交结构化参数并跟踪 task；不拼接 shell、不持有解密密码、不复制后端安全判断。用户可见文案全部提供 zh/en。

## 14. 失败语义

- 备份失败：删除面板 `.partial`，保留失败 `app_backups` 记录；源节点临时归档可按短 TTL 保留用于重试。
- checksum、manifest、路径或版本校验失败：还原在任何 schema 变更前终止。
- 删除 schema 后 load 失败：任务标记 `restore_incomplete`，保持维护模式，不自动恢复业务。
- `local_infile` 恢复失败：任务必须失败并输出稳定错误码，即使数据导入已完成；目标不可达时保留待调和标记并阻止后续生命周期操作。
- 健康集群 PRIMARY 切换：任务失败，不跨 PRIMARY 续写。
- disaster rebuild 中成员 clone 失败：保留已恢复 seed，保持 Router 停止，允许修复后重试未完成成员。
- 自动 pre-restore 备份是安全回退点，但恢复它仍需显式创建新的 restore task，不能在失败路径隐式执行。

## 15. 稳定错误码

至少定义：

```text
MYSQL_BACKUP_UNSUPPORTED_TOPOLOGY
MYSQL_BACKUP_CLUSTER_UNHEALTHY
MYSQL_BACKUP_PRIMARY_NOT_FOUND
MYSQL_BACKUP_SPACE_INSUFFICIENT
MYSQL_BACKUP_TRANSFER_FAILED
MYSQL_BACKUP_CHECKSUM_MISMATCH
MYSQL_RESTORE_MAINTENANCE_REQUIRED
MYSQL_RESTORE_VERSION_INCOMPATIBLE
MYSQL_RESTORE_MANIFEST_INVALID
MYSQL_RESTORE_TARGET_NOT_CLEAN
MYSQL_RESTORE_PRIMARY_CHANGED
MYSQL_RESTORE_LOCAL_INFILE_RESTORE_FAILED
MYSQL_RESTORE_INCOMPLETE
MYSQL_REBUILD_CONFIRMATION_REQUIRED
MYSQL_REBUILD_ROUTER_FAILED
```

错误响应保持 `{code,message,details}`，用户可见 message 和任务日志进入 backend i18n zh/en。

## 16. 安全要求

- 所有远端路径必须位于计算得到的 MySQL workdir、backup workdir 或 restore workdir 中，并拒绝符号链接和路径逃逸。
- 归档解包前验证成员路径、数量、总大小和文件类型，拒绝绝对路径、`..`、符号链接、设备文件和超额展开。
- manifest 和 checksums 必须在受控归档内且互相一致。
- 密码只来自加密凭据；secret context 为 0600，使用后尽力安全清理。
- 不允许 API 接收自由 shell、自由远端路径或任意 schema drop SQL。
- restore 的 schema 集合只能来自已验证 manifest 与服务端 allowlist 的交集。
- backup、restore、delete、verify 和 disaster rebuild 全部写审计。
- 面板备份仓库是服务账号独占的受信单写者目录；所有仓库生命周期操作必须持有进程内互斥锁和跨进程排他锁。`root` 或恶意同 UID 进程不在本版本威胁模型内。

## 17. 测试与验收

### 17.1 单元与集成测试

- Store：`app_backups` pending/running/success/failed、列表、保留策略和删除归属。
- Adapter：SFTP Download 流式传输、partial、checksum、取消和路径错误。
- MySQL module：standalone/cluster PlanBackup、PlanRestore、PRIMARY 选择、集群展开和锁冲突。
- 脚本：dump/load 选项、账号排除、metadata 排除、`local_infile` finally 恢复、日志脱敏。
- HTTP：权限、task id、audit、错误码、危险确认和非法 manifest。
- 前端：按钮能力、备份列表、恢复确认、灾难向导、任务追踪和 zh/en。
- 安全：归档 traversal、symlink、超额展开、checksum 篡改、secret 泄露扫描。

### 17.2 真实环境验收

- standalone 在线备份、空目标恢复、关键数据一致。
- standalone checksum 损坏时在变更目标前拒绝。
- 三节点集群从 PRIMARY 只生成一份备份。
- 健康集群只在 PRIMARY 导入，三个成员最终一致。
- restore 中 PRIMARY 切换后安全失败并保持维护。
- complete outage 使用 reboot 路径，不误触 disaster rebuild。
- 从备份恢复 clean seed，clone B/C，三个成员 ONLINE。
- Router 6446 恢复真实读写。
- 目标仍可达的成功、失败和取消场景恢复 `local_infile` 原值；不可达场景产生门禁标记，并在目标恢复后完成调和。
- task、step、target、log、audit、`app_backups` 完整且无 secret。

### 17.3 本地门禁

实施完成后至少运行：

```text
pnpm test
pnpm test:web
pnpm test:scripts
pnpm web:build
git diff --check
```

收口前运行 `pnpm test:local`。真实 SSH/MySQL/InnoDB Cluster/Router 验收必须在专用测试环境执行，不放入默认单元测试。

## 18. 推荐实施分解

1. Registry request/interface、稳定错误码和 API 骨架。
2. Store 备份列表/状态/保留策略补强与配置项。
3. Adapter SFTP Download 和受控备份仓库。
4. Standalone backup 闭环。
5. Standalone restore 与 `local_infile` 安全恢复。
6. Cluster topology/PRIMARY 解析与 cluster backup。
7. 健康 cluster restore。
8. Disaster rebuild、clone 和 Router bootstrap。
9. Database 前端入口、备份记录和恢复向导。
10. 自动化门禁与三节点真实验收。

## 19. 参考

- MySQL Shell 8.0 Instance Dump Utility: <https://dev.mysql.com/doc/mysql-shell/8.0/en/mysql-shell-utilities-dump-instance-schema.html>
- MySQL Shell 8.0 Dump Loading Utility: <https://dev.mysql.com/doc/mysql-shell/8.0/en/mysql-shell-utilities-load-dump.html>
- MySQL Shell 8.0 Clone with InnoDB Cluster: <https://dev.mysql.com/doc/mysql-shell/8.0/en/mysql-innodb-cluster-working-with-clone.html>
- MySQL Shell 8.0 Rebooting a Cluster from a Major Outage: <https://dev.mysql.com/doc/mysql-shell/8.0/en/reboot-outage.html>
- 当前安装模板：`backend/internal/apps/mysql/templates/standalone/install.sh`
- 当前建群模板：`backend/internal/apps/mysql/templates/innodb-cluster/bootstrap.sh`
- 当前 complete-outage 模板：`backend/internal/apps/mysql/templates/innodb-cluster/start.sh`
- 当前备份模型：`backend/internal/store/models.go`、`backend/internal/store/app_release_assets.go`
