# MySQL 集群权威拓扑登记设计

## 目标

修复新安装的三节点 MySQL InnoDB Cluster 因缺少 `app_clusters` 与 `app_cluster_members` 权威拓扑而被维护安全门禁误判为不可用的问题，同时安全补登记当前已安装集群。

## 设计

- 新安装使用 `cluster_<24hex>` 作为集群 ID。
- 三条 MySQL `app_instances`、一条 `app_clusters` 和三条 `app_cluster_members` 在同一 SQLite 事务中写入；任一写入失败时全部回滚。
- 成员记录使用安装目标的实例 ID 与服务器 ID，初始状态为 `ONLINE`；首个安装目标登记为 `PRIMARY`，其余为 `SECONDARY`。后续实时检查会以 MySQL 实际角色覆盖该初值。
- 启动迁移只处理严格匹配 `mysql_cluster_<24hex>`、恰好三条 MySQL 集群实例、三个不同服务器、同一非空集群名且不存在维护或协调标记的旧记录。迁移生成新的受控集群 ID，并同步更新同组 MySQL Router 元数据。
- 不放宽维护门禁；不连接或修改远端 MySQL 数据。

## 验收

- 新安装完成后，权威集群与三条成员记录完整，维护门禁允许常规状态检查。
- 现有合规旧集群在重启迁移后完成补登记，三个节点可进入实际运行状态探测。
- 不完整、重复服务器、带维护标记或格式异常的旧数据保持原状并继续被安全门禁阻止。
