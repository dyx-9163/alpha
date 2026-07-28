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

`backup-manifest.json` 使用显式版本。历史上没有 `manifestVersion` 的记录和显式值 `1` 都按 v1 解释；其它未知版本失败关闭。新备份只生成 v2。v2 结构固定为：

```json
{
  "manifestVersion": 2,
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
  "schemas": ["aifar_business"],
  "excludedSchemas": [
    "mysql",
    "sys",
    "performance_schema",
    "information_schema",
    "mysql_innodb_cluster_metadata"
  ],
  "consistent": true,
  "gtidExecuted": "...",
  "verification": {
    "source": "mysql-shell-8.0.36-dump",
    "inventoryAlgorithm": "sha256-nul-records-v1",
    "inventorySha256": "64-lowercase-hex",
    "files": [
      {"path": "@.json", "size": 1024, "sha256": "64-lowercase-hex"}
    ],
    "schemaCount": 1,
    "tableCount": 2,
    "schemas": [
      {
        "name": "aifar_business",
        "tableCount": 2,
        "tables": ["orders", "settings"]
      }
    ]
  },
  "createdAt": "2026-07-27T00:00:00Z",
  "taskId": "task_xxx"
}
```

集群备份还需记录 `clusterName`、备份时 PRIMARY、所有 MySQL 成员 endpoint/role/status 和 Router 摘要。manifest、metadata、日志和审计均不得包含密码、私钥、恢复账号或完整敏感连接串。

### 5.1 manifest v2 校验数据来源与规范化

`verification` 只能在 `util.dumpInstance()` 成功并产生 MySQL Shell 8.0.36 完成元数据后构建。`@.done.json` 必须符合 8.0.36 的真实完成标记契约；`@.json`、每个 schema metadata 和每个 base table metadata 必须形成一个完整、闭合且无歧义的目录图。schema 与 base table 期望只来自该次 dump 元数据；不得查询仍可能变化的在线源库来补齐或替代期望值。缺少完成标记、元数据文件缺失、JSON 畸形、字段类型错误、未知/重复名称、basename 映射缺失、目录引用悬空或元数据之间不一致时，备份失败且不生成 success 记录。

MySQL Shell 8.0.36 metadata 的服务端解析契约固定为：

- `@.done.json` 必须是单个 JSON object 且同时包含 `end`、`dataBytes`、`tableDataBytes`、`chunkFileBytes`。`end` 必须是非空合法时间；三个统计字段必须使用 8.0.36 写入端的类型，其中所有字节数是 `uint64` 范围内的 JSON integer，允许为 `0`，拒绝负数、小数和溢出。`tableDataBytes` 按原始 schema/table 名两层映射；`chunkFileBytes` 的键是实际相对数据文件名。AIFAR 不采用 loader 对旧格式的宽松兼容规则，空 object 不是有效完成标记。
- 顶层 `@.json` 必须声明 `version="2.0.1"`、`origin="dumpInstance"`、`consistent=true`，并提供 `schemas` 与一一对应的 `basenames`。AIFAR 备份必须至少包含一个通过业务 schema allowlist 的 schema；schema 名唯一，basename 唯一且只能定位 dump 根下对应的 schema metadata JSON。8.0.36 固定字段和已知条件字段按其真实类型解析，拒绝 trailing JSON、`null` 和名称/映射冲突。
- 每个 schema metadata 的 `schema` 必须回指顶层原始名称，且 `includesDdl=true`、`includesViewsDdl=true`、`includesData=true`。`tables` 是完整 base-table 权威目录，`views` 是独立视图目录；两者名称不得重叠。`basenames` 必须恰好覆盖 tables 与 views 的并集。base table 目录允许为空。
- 每个 `tables` 项必须经 schema basename 映射定位到且仅定位到一份 table metadata。该文件必须满足根级 `includesData=true`、`includesDdl=true`、`extension="tsv.zst"`、`compression="zstd"`，并由 `options.schema`、`options.table` 精确回指原始目录项；`options.columns` 和根级 `primaryIndex` 必须是字符串数组。目录外、重复或回指其它 schema/table 的 table metadata 一律拒绝。view 不要求 table metadata。
- 对本方案固定的 `includesData=true`，`tableDataBytes` 的 schema/table 键必须与完整 base-table 目录一致，值允许为 `0`；`chunkFileBytes` 每个键必须是安全规范化相对路径，并对应文件清单中的真实 data file，文件清单中的每个 data file 也必须被其覆盖。统计值是未压缩字节数，不与压缩后文件大小作相等比较。

文件清单只覆盖待打包 dump 根目录下的普通文件，不包含仓库侧 `backup-manifest.json`、`checksums.txt` 或归档本身，避免自引用。每项记录相对 dump 根的 UTF-8 POSIX 路径、字节大小和文件内容 SHA-256；路径必须规范化、唯一、按 UTF-8 字节序升序，不得为绝对路径、包含 `.`/`..` 段、反斜杠、NUL、符号链接或其它特殊文件。`sha256-nul-records-v1` 对每个排序后的条目连续写入 `lowerHex(fileSha256) NUL decimalSize NUL path NUL` 的 UTF-8 字节并计算 SHA-256，结果作为 `inventorySha256` 小写十六进制值。打包前和面板仓库验证时都必须重算文件级摘要与清单摘要。

`schemas` 和每个 schema 的 `tables` 保存 dump 中完整业务 schema 与 base table 集合，按名称 UTF-8 字节序升序且不重复；`schemaCount`、顶层 `tableCount` 和各 schema 的 `tableCount` 必须与数组精确一致。空业务 schema 允许 `tableCount=0`。view、trigger、routine 和 event 不计入这里的 base table 集合；它们仍随 dump 恢复。每个目录中的 base table 必须有且只有一份匹配的 table metadata，目录外 table metadata 一律拒绝。

顶层 `schemas` 必须与 `verification.schemas[].name` 的排序结果完全相同。`verification.source=mysql-shell-8.0.36-dump` 和 `inventoryAlgorithm=sha256-nul-records-v1` 是 v2 固定常量；未知或缺失值不得兼容猜测。

MySQL Shell 8.0.36 的 dump 元数据不保存逐表 `rowsWritten`，因此 v2 不保存逐表行数，也不执行抽样 `COUNT(*)`。AIFAR 不扫描压缩数据文件推算行数，也不使用在线源库生成期望值。此版本的 restore success 表示：受控归档和所有 dump 文件未被篡改、MySQL Shell 确认 dump 已完成、完整 schema/base table 目录已恢复、`util.loadDump()` 成功、MySQL 可连接且 task/manifest 身份一致；它不声明业务行数逐表相等。UI、任务日志和运维文档不得把该门禁表述为行级数据一致性校验。

历史 v1 manifest 可继续列表、仓库完整性校验和受控删除，但任何会修改 MySQL 的 restore 或 disaster rebuild 必须在远端连接和 schema 变更前以 `MYSQL_RESTORE_MANIFEST_INVALID` 拒绝。v1 不允许通过在线查询补写成 v2。新建的普通备份和 pre-restore 备份均生成 v2。

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

待恢复标记是 `app_instances.metadata` 中服务端管理的版本化对象，字段名和首版结构固定为：

```json
{
  "mysqlReconciliation": {
    "version": 1,
    "kind": "local_infile",
    "originalValue": "ON",
    "recordedAt": "2026-07-28T00:00:00Z",
    "taskId": "task_xxx"
  }
}
```

`originalValue` 只能是 `ON` 或 `OFF`，`recordedAt` 使用 UTC RFC3339，`taskId` 必须是当前受控 restore task ID。对象不得包含账号、密码、连接串或远端命令。调和成功后只有在重新读取并确认 `@@GLOBAL.local_infile` 等于记录原值时才能删除整个 `mysqlReconciliation` 字段；未知 `version`、未知 `kind`、畸形或不完整 marker 均失败关闭并返回 `MYSQL_RECONCILIATION_REQUIRED`。

### 7.1 显式调和恢复任务

普通 check、backup 和 restore 不能承担调和入口：它们会被持久化维护门禁在任务创建前拒绝，而且把变量修复隐藏在普通生命周期动作中会削弱审计边界。AIFAR 必须提供独立的 owner-only worker task：

```text
POST /api/v2/apps/instances/{id}/mysql/reconciliation/run
task type: apps.mysql.reconciliation.run
lock: app-instance/<instanceId>/mutate or app-cluster/<clusterId>/mutate
request: {"reconciliationConfirmed":true}
```

接口只接受携带严格有效 `mysqlReconciliation` marker 的 MySQL 实例；无 marker 返回 `MYSQL_RECONCILIATION_NOT_REQUIRED`，缺少明确确认返回 `MYSQL_RECONCILIATION_CONFIRMATION_REQUIRED`，畸形 marker 继续失败关闭并返回 `MYSQL_RECONCILIATION_REQUIRED`。任务允许在同一实例或集群仍有有效 `mysqlMaintenance` 时执行，这是维护门禁的受控例外；它不得删除、改写或放宽维护 marker。

handler 创建 task、target、step 和 audit 后，worker 取得与其它 MySQL mutation 相同的 raw `mutate` 锁，并在锁内重读实例、拓扑和 marker。集群锁必须由 marker 所在实例的权威 `clusterId` 推导，不得信任请求提供的拓扑。任务只使用唯一、启用、绑定 `purpose=admin` 且同时含用户名和密码的凭据；随后重新连接目标，恢复 marker 记录的 `@@GLOBAL.local_infile` 原值，再次读取并验证相等，最后以比较并交换方式仅删除该实例的整个 `mysqlReconciliation` 字段。连接失败、设置失败、复验失败、凭据不完整、marker 或拓扑漂移、metadata 清除失败均保持 marker 并使任务失败；日志、错误 details 和 audit 不得泄露凭据。

调和成功只证明 `local_infile` 已恢复到记录原值，不证明 restore 数据完整，也不解除维护状态。若 `mysqlMaintenance` 同时存在，owner 仍须完成外部修复并另行执行第 9.1 节维护清除任务。

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
- 仅 manifest v2 可进入还原。任务在任何远端 mutation 前计算 `SHA256(CanonicalBackupManifestJSON(manifest))`；把小写十六进制结果作为本次 restore 的不可变 `restoreExpectedManifestSha256`，连同 `restoreTaskId` 写入 `app_backups.metadata`。最终校验重新从受控仓库读取并规范化 manifest，任务 ID 或摘要不一致即失败。
- 最终校验必须同时满足：任务已经持久化进入 `load_complete`，证明受控 `util.loadDump()` 正常返回；MySQL ping 成功；业务 schema 与 base table 名称集合及各级数量和 manifest v2 完全一致；重新计算的 canonical manifest SHA-256 等于当前任务记录的 `restoreExpectedManifestSha256`。任一条件失败都写入 `restore_incomplete` 并返回 `MYSQL_RESTORE_INCOMPLETE`，不得报告 restore success。

### 9.1 持久化维护门禁

`maintenanceConfirmed` 只证明用户在任务提交时声明已经停止业务写入，不是持久化状态。standalone 或健康集群 restore 完成全部 preflight 后，必须先把 backup restore phase 持久化为 `schema_mutation_started`，再在 `app_instances.metadata.mysqlMaintenance` 写入以下唯一合法对象；marker 写入成功后才能执行第一条 schema mutation：

```json
{
  "version": 1,
  "state": "required",
  "reason": "restore_incomplete",
  "scope": "standalone",
  "backupId": "backup_xxx",
  "taskId": "tsk_xxx",
  "restorePhase": "schema_mutation_started",
  "recordedAt": "2026-07-27T12:00:00Z"
}
```

集群对象的 `scope` 固定为 `cluster`，并必须额外包含非空 `clusterId`；standalone 对象不得包含 `clusterId`。`restorePhase` 只能是 `schema_mutation_started` 或 `load_complete`，`recordedAt` 必须是 UTC RFC3339。对象只允许上述固定字段、枚举、受控 ID 和时间，不得包含账号、密码、连接串、远端命令或自由文本。解析使用严格单对象契约：未知字段、未知版本、未知枚举、缺字段或 scope/clusterId 不匹配均失败关闭。

standalone 在本实例写入一个 marker；cluster 必须在同一 SQLite 事务中为控制面记录的三个权威 MySQL 成员写入完全相同的 marker。写入使用事务内重读和比较，成员归属、实例 ID 集合、clusterId 或原 metadata 已变化时整笔回滚，绝不留下部分标记。preflight、pre-restore backup、上传、dry run 或 mutation 前 PRIMARY 切换失败不得设置。marker 在整个 mutation/load/verify 窗口内保持有效；只有 restore 到达 `verified` 后才能原子清除。load 完成后可把 marker 的 `restorePhase` 原子推进到 `load_complete`，推进失败仍保留原 `schema_mutation_started` marker 并使任务失败。

若初始 marker 无法持久化，任务必须在第一条 schema mutation 前返回 `MYSQL_MAINTENANCE_STATE_PERSIST_FAILED`，保持 backup restore phase 为 `restore_incomplete`，并记录不含 secret 的失败证据。若 `verified` 后清除失败，任务同样返回该码且原 marker 保持有效；不得报告 restore success。

有效 marker 存在时，AIFAR 必须在两层失败关闭：HTTP handler 在创建普通任务前拒绝；MySQL module/service 在取得实例或集群 `mutate` 锁并重读权威状态后再次拒绝。被阻断的普通生命周期操作包括 check、start、delete、backup 和 ordinary restore。有效 marker 返回 `MYSQL_MAINTENANCE_REQUIRED`；畸形 marker 或同一集群三成员 marker 缺失/不一致返回 `MYSQL_MAINTENANCE_STATE_INVALID`。唯一允许继续的是 owner-only 维护状态读取/清除任务、第 7.1 节显式调和恢复任务，以及第 12.2 节显式 disaster rebuild。

维护清除接口固定为：

```text
POST /api/v2/apps/instances/{id}/mysql/maintenance/clear
task type: apps.mysql.maintenance.clear
lock: app-instance/<instanceId>/mutate or app-cluster/<clusterId>/mutate
request: {"recoveryConfirmed":true}
```

清除任务必须是 owner-only worker task，并记录 target、step、日志和 audit。取得同一 raw mutate 锁后重读 marker 身份与拓扑，要求没有 `mysqlReconciliation` marker；standalone 必须通过 MySQL ping，cluster 必须恰有三个 `ONLINE` 成员、一个 PRIMARY，且 Router 6446 可真实读写。随后 standalone 清除一个 marker，cluster 在一个 SQLite 事务中清除三个完全一致的 marker。`recoveryConfirmed` 表示 owner 在外部修复后接受继续运行的数据风险；健康检查不证明业务数据逐行相等，任务和 UI 不得如此宣称。

该 marker 只保证 AIFAR 面板控制面的生命周期门禁，不会自动阻止 Java、直连 MySQL 或其它外部客户端写入。运维方仍必须在 restore、故障修复和 marker 清除期间维持外部业务维护窗口。

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
- 与 standalone 相同，集群新备份在 dump 完成后只从 PRIMARY 上该次 MySQL Shell dump 产物构建 manifest v2 verification 数据。

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
- 成功门禁包括：三个成员 ONLINE、SECONDARY applier 队列归零、完整 schema/base table 目录一致、Router 6446 真实读写成功。
- 集群健康还原和 disaster rebuild 同样只接受 manifest v2，并复用第 9 节的受控 load 完成、task/manifest 摘要和完整 schema/base table 集合门禁。

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
- disaster rebuild 是持久化维护 marker 存在时允许执行的唯一破坏性恢复路径；成功完成 seed、成员、Router 与最终数据目录验证后，必须在更新控制面的同一事务中清除三个成员的 `mysqlMaintenance`。任一阶段失败都保留 marker。

## 13. 前端交互

修改 Database 页面前必须遵循 `design/ant-design-system-portable202606.md`。

在 standalone 实例卡片和 InnoDB Cluster 组卡片提供：

- 立即备份
- 备份记录
- 校验备份
- 恢复数据
- 灾难重建集群，仅集群展示

备份弹窗首版只提供备份名称、线程数、每线程限速和保留数量；默认值由后端返回。恢复弹窗展示备份来源、版本、拓扑、schema、时间、大小、checksum 和影响范围，并要求维护确认。灾难重建额外展示目标三节点、quarantine 策略、Router 影响和 SSH 密码确认。

实例或集群存在 `mysqlMaintenance` 时，Database 页面必须显示不可忽略的维护横幅、来源 backup/task、restore phase、记录时间和“面板门禁不等于外部业务已停止”的提示。普通 check/start/delete/backup/restore 操作禁用并解释原因；owner 可发起独立的“清除维护状态”任务，确认文案必须说明健康检查不证明数据相等，只有在外部修复和风险确认后才能提交。

任一实例存在严格有效 `mysqlReconciliation` 时，页面必须显示受影响实例、原 `local_infile` 值、来源 task 和记录时间，并向 owner 提供独立的“执行调和”动作，提交 `{"reconciliationConfirmed":true}` 并跟踪返回 task。若维护 marker 同时存在，“清除维护状态”保持禁用，直到重新读取状态确认调和 marker 已消失。UI 必须明确说明调和只恢复 `local_infile`，不会校验数据、不会解除维护；畸形 marker 只显示失败关闭诊断，不提供绕过动作。

页面只提交结构化参数并跟踪 task；不拼接 shell、不持有解密密码、不复制后端安全判断。用户可见文案全部提供 zh/en。

## 14. 失败语义

- 备份失败：删除面板 `.partial`，保留失败 `app_backups` 记录；源节点临时归档可按短 TTL 保留用于重试。
- checksum、manifest、路径或版本校验失败：还原在任何 schema 变更前终止。
- v1 manifest 参与破坏性还原，或 v2 完成标记、metadata 目录图、文件清单、schema/base table 集合、计数、canonical manifest 摘要任一无效：还原在任何远端 mutation 前以 `MYSQL_RESTORE_MANIFEST_INVALID` 终止。
- 删除 schema 后 load 失败：任务标记 `restore_incomplete`，保持维护模式，不自动恢复业务。
- schema mutation 前必须按第 9.1 节先原子写入 `mysqlMaintenance`；写入失败不执行 mutation。mutation 后任一失败保留 marker；最终清除失败优先返回 `MYSQL_MAINTENANCE_STATE_PERSIST_FAILED`，不得报告 restore success。
- `local_infile` 恢复失败：任务必须失败并输出稳定错误码，即使数据导入已完成；目标不可达时保留待调和标记并阻止后续生命周期操作。
- 显式调和失败：保留完整 `mysqlReconciliation`，不修改 `mysqlMaintenance`；只有恢复、复读验证和比较并交换清除都成功，任务才可报告 success。
- 健康集群 PRIMARY 切换：任务失败，不跨 PRIMARY 续写。
- disaster rebuild 中成员 clone 失败：保留已恢复 seed，保持 Router 停止，允许修复后重试未完成成员。
- 自动 pre-restore 备份是安全回退点，但恢复它仍需显式创建新的 restore task，不能在失败路径隐式执行。

## 15. 稳定错误码

至少定义：

```text
MYSQL_CREDENTIAL_UNAVAILABLE
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
MYSQL_LOCAL_INFILE_RESTORE_FAILED
MYSQL_RESTORE_INCOMPLETE
MYSQL_MAINTENANCE_REQUIRED
MYSQL_MAINTENANCE_STATE_INVALID
MYSQL_MAINTENANCE_STATE_PERSIST_FAILED
MYSQL_RECONCILIATION_CONFIRMATION_REQUIRED
MYSQL_RECONCILIATION_NOT_REQUIRED
MYSQL_REBUILD_CONFIRMATION_REQUIRED
MYSQL_REBUILD_ROUTER_FAILED
```

`MYSQL_CREDENTIAL_UNAVAILABLE` 同时用于备份和还原：目标无法解析出唯一、启用且绑定 `purpose=admin` 的 MySQL 凭据，或其密文不可用时返回该码。缺失、停用、重复绑定、缺少密文和解密失败共用这一公开错误码；消息和 details 不得暴露具体凭据记录或失败的秘密信息。

新实现只产生 `MYSQL_LOCAL_INFILE_RESTORE_FAILED`。旧名称 `MYSQL_RESTORE_LOCAL_INFILE_RESTORE_FAILED` 仅作为兼容别名保留，用于识别历史任务、日志或调用方；其公开文案映射到同一安全消息，不得由新的 restore 流程返回。

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

- Store：`app_backups` pending/running/success/failed、列表、保留策略和删除归属；standalone marker 单实例写入/清除、cluster 三成员原子写入/清除、事务比较失败回滚且无部分更新。
- Adapter：SFTP Download 流式传输、partial、checksum、取消和路径错误。
- MySQL module：standalone/cluster PlanBackup、PlanRestore、PRIMARY 选择、集群展开和锁冲突；marker 严格解析、preflight 失败不设置、第一条 mutation 前设置、失败保留、验证成功后清除、普通生命周期两层门禁、owner 调和和 owner 清除健康检查。
- Manifest v2：使用真实 MySQL Shell 8.0.36 完成标记和 metadata 目录图构建完整文件/schema/base table 期望；覆盖 inventory 摘要、缺失/悬空/重复 metadata、v1 只读兼容和 destructive restore 拒绝。
- 脚本：dump/load 选项、账号排除、metadata 排除、`local_infile` finally 恢复、日志脱敏。
- HTTP：权限、task id、target、step、audit、错误码、危险确认、非法 manifest、维护门禁创建前拒绝、owner-only reconciliation task 和 owner-only clear task。
- 前端：按钮能力、备份列表、恢复确认、灾难向导、调和动作、任务追踪和 zh/en。
- 安全：归档 traversal、symlink、超额展开、checksum 篡改、secret 泄露扫描。

### 17.2 真实环境验收

- standalone 在线备份、空目标恢复；验证受控 load 完成、完整 schema/base table 集合、MySQL ping 和 task/manifest 摘要一致，并明确不宣称逐表行数一致。
- standalone checksum 损坏时在变更目标前拒绝。
- 三节点集群从 PRIMARY 只生成一份备份。
- 健康集群只在 PRIMARY 导入，三个成员最终一致。
- restore 中 PRIMARY 切换后安全失败并保持维护。
- standalone/cluster 在第一条 mutation 前已经持久化维护 marker，mutation 后失败会保留它；普通 check/start/delete/backup/restore 被阻断，owner clear 经健康检查与风险确认后原子清除。验证 marker 不会阻止外部直连客户端，维护窗口由运维方持续控制。
- complete outage 使用 reboot 路径，不误触 disaster rebuild。
- 从备份恢复 clean seed，clone B/C，三个成员 ONLINE。
- Router 6446 恢复真实读写。
- 目标仍可达的成功、失败和取消场景恢复 `local_infile` 原值；不可达场景产生门禁标记，并在目标恢复后通过独立 owner 调和任务完成恢复与复验。若维护 marker 同时存在，证明调和只清自己的 marker，随后才允许另行执行维护清除。
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
