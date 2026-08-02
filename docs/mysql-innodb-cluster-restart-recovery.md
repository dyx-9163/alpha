# MySQL InnoDB Cluster 三节点重启恢复操作手册

本文面向不熟悉 MySQL InnoDB Cluster 的操作人员。请先判断当前集群属于哪种情况，然后只执行对应章节的步骤。

> **重要警告：** 包含 `force`、`forceQuorumUsingPartitionOf()` 或 `recoveryMethod: 'clone'` 的命令，如果使用场景错误，可能造成数据丢失或脑裂。未确认对应章节中的执行条件前，不要运行这些命令。

## 1. 参数和占位符说明

命令中的大写英文都是占位符，执行前必须替换成现场实际值。不要直接输入占位符名称。

| 占位符 | 含义 | 示例 |
| --- | --- | --- |
| `CLUSTER_ADMIN` | 用于管理 InnoDB Cluster 的 MySQL 账号。它是 MySQL 账号，不是 Linux SSH 账号。 | `root` 或 `icadmin` |
| `CLUSTER_NAME` | 保存在 MySQL 元数据中的 InnoDB Cluster 名称。 | `aifarCluster` |
| `NODE1_IP` | 第一个 MySQL 成员的 IP 地址。它只是节点标识，不代表该节点一定是 PRIMARY。 | `192.168.74.133` |
| `NODE2_IP` | 第二个 MySQL 成员的 IP 地址。 | `192.168.74.134` |
| `NODE3_IP` | 第三个 MySQL 成员的 IP 地址。 | `192.168.74.137` |
| `MYSQL_PORT` | MySQL Shell 使用的 MySQL Classic Protocol 端口。 | `3306` |
| `SEED_IP` | 完整停机校验后，被选中用于重新启动集群的节点。优先使用 `dryRun` 输出中报告的节点；如果没有报告另一个 Seed，则使用 MySQL Shell 当前连接的节点。 | `192.168.74.133` |
| `TRUSTED_NODE_IP` | 集群丢失 quorum 时，唯一存活且权威分区中的 `ONLINE` 节点。只在丢失 quorum 的恢复场景使用。 | `192.168.74.133` |
| `FAILED_NODE_IP` | 集群已经恢复后仍无法重新加入的异常成员。使用 Clone 恢复时，该节点本地数据会被覆盖。 | `192.168.74.137` |

当前三节点环境已知值如下：

```text
CLUSTER_NAME = aifarCluster
NODE1_IP     = 192.168.74.133
NODE2_IP     = 192.168.74.134
NODE3_IP     = 192.168.74.137
MYSQL_PORT   = 3306
```

请向系统管理员确认 `CLUSTER_ADMIN` 账号。密码在 MySQL Shell 中交互输入，不要把密码写入本文档，也不要把密码直接放在命令行中。

系统自带的 MySQL Shell 路径为：

```text
/aifar/apps/mysql/mysql-shell/bin/mysqlsh
```

### 常用术语说明

| 术语 | 含义 |
| --- | --- |
| PRIMARY | 接受写操作的成员。单主模式集群必须只有一个 PRIMARY。 |
| SECONDARY | 从集群同步数据的只读成员。当前三节点集群应有两个 SECONDARY。 |
| ONLINE | 成员已经加入 Group Replication，并且正在正常参与集群。 |
| Quorum | 集群安全作出决策所需的多数成员。三节点集群通常至少需要两个节点能够互相通信。 |
| GTID | 用于比较每个节点已经执行了哪些事务的事务标识。GTID 分叉可能表示节点存在互相冲突的数据历史。 |
| Seed | 完整停机后，用于重新启动 Group Replication 的已校验成员。 |
| Metadata | MySQL Shell 用来记录集群名称、成员和拓扑的内部元数据。 |
| Clone 恢复 | 删除目标成员现有 MySQL 数据，并用健康供体节点的物理副本完整覆盖目标节点。 |
| 脑裂 | 不同网络分区都认为自己是有效集群并同时运行的危险状态。 |

## 2. 选择正确的恢复场景

如果不清楚当前集群属于哪种情况，请先运行只读脚本 [mysql-cluster-status.sh](mysql-cluster-status.sh)。该脚本不会启动、停止或修改集群。

| 当前情况 | 应使用的章节 |
| --- | --- |
| 三台 MySQL 服务都可以连接，但没有任何集群成员处于 `ONLINE` | 第 3 章：完整停机恢复 |
| 至少有一个成员仍为 `ONLINE`，但集群无法获得多数 quorum | 第 4 章：丢失 quorum 恢复 |
| 集群已经恢复，但其中一个成员无法重新加入 | 第 5 章：使用 Clone 替换一个异常成员 |
| 无法判断属于哪种情况 | 停止操作并联系 DBA，不要尝试 force 命令 |

## 3. 完整停机恢复

当三个成员的 Group Replication 都已停止时，使用本章节。运行恢复命令前，三台 MySQL 服务必须已经启动并且可以连接。

### 第 1 步：启动 MySQL Shell 并连接任意节点

**目的：** 以 JavaScript 模式打开 MySQL Shell，并连接一个集群成员。

在 Linux 终端执行以下命令。下面的命令连接 `NODE1_IP`：

```bash
/aifar/apps/mysql/mysql-shell/bin/mysqlsh --js --uri CLUSTER_ADMIN@NODE1_IP:MYSQL_PORT
```

使用当前节点地址和 `root` 账号的示例：

```bash
/aifar/apps/mysql/mysql-shell/bin/mysqlsh --js --uri root@192.168.74.133:3306
```

MySQL Shell 会提示输入 MySQL 密码。输入密码后按 Enter。输入过程中密码不会显示。

**成功结果：** 命令提示符变为 `mysql-js>`，并且没有连接错误。

**出现以下情况必须停止：** 输出包含 `Access denied`、`Can't connect`、连接超时、SSL 错误或认证错误。先解决连接或账号问题，再继续操作。

### 第 2 步：执行只读 dryRun

**目的：** 在不修改集群的情况下，验证完整停机恢复是否安全，并确定 Seed 节点。

在 `mysql-js>` 提示符下执行：

```javascript
dba.rebootClusterFromCompleteOutage('aifarCluster', {dryRun: true})
```

如果实际集群名称不是 `aifarCluster`，请将其替换为 `CLUSTER_NAME` 的实际值。

**如何确定 `SEED_IP`：**

- 如果输出包含 `Switching over to instance 'IP:PORT' to be used as seed`，将其中的 IP 作为 `SEED_IP`。
- 如果 dryRun 成功结束，并且没有报告另一个 Seed，则使用 MySQL Shell 当前连接节点的 IP。
- `SEED_IP` 不一定是原来的 PRIMARY。它是经过校验后用于重新启动集群的节点。

**成功结果：** 输出表示 `dryRun` 校验已经完成，或包含 `dryRun finished`，并且没有异常。

**出现以下情况必须停止：** dryRun 报告成员不可达、GTID 冲突或分叉、元数据缺失，或者抛出任何异常。不要改用 `force:true` 重试。

### 第 3 步：连接校验通过的 Seed 节点

**目的：** 确保正式恢复命令在 dryRun 认可的节点上运行。

在 MySQL Shell 中执行以下命令，并替换 `SEED_IP`、`CLUSTER_ADMIN` 和 `MYSQL_PORT`：

```javascript
\connect CLUSTER_ADMIN@SEED_IP:MYSQL_PORT
```

示例：

```javascript
\connect root@192.168.74.133:3306
```

根据提示输入 MySQL 密码。

**成功结果：** MySQL Shell 报告当前会话已连接到 `SEED_IP:MYSQL_PORT`。

**出现以下情况必须停止：** 连接失败，或者实际连接地址不是经过校验的 Seed。

### 第 4 步：重新启动集群

**目的：** 执行真正的完整停机恢复。该命令会修改集群状态。

执行：

```javascript
var cluster = dba.rebootClusterFromCompleteOutage('aifarCluster')
```

**成功结果：** MySQL Shell 报告集群已经成功重启，并返回一个 `cluster` 对象。

**出现以下情况必须停止：** 命令报告 GTID 分叉、成员不可达、元数据错误或其他校验失败。不要添加 `{force: true}`。

### 第 5 步：检查哪些成员已经 ONLINE

**目的：** 确认是否还有成员需要重新加入。

执行：

```javascript
cluster.status({extended: 2})
```

查看 `defaultReplicaSet.topology`。每个节点都有 `status` 和 `memberRole`。

- 如果节点已经是 `ONLINE`，不要再对该节点执行 `rejoinInstance()`。
- 如果节点是 `OFFLINE`、`MISSING` 或者没有出现在列表中，对该节点继续执行第 6 步。

### 第 6 步：只重新加入不是 ONLINE 的成员

**目的：** 将剩余成员重新加入已经恢复的集群。

下面给出了节点 2 和节点 3 的示例命令。只对不是 `ONLINE` 的节点执行对应命令：

```javascript
cluster.rejoinInstance('CLUSTER_ADMIN@NODE2_IP:MYSQL_PORT')
cluster.rejoinInstance('CLUSTER_ADMIN@NODE3_IP:MYSQL_PORT')
```

使用当前节点地址的示例：

```javascript
cluster.rejoinInstance('root@192.168.74.134:3306')
cluster.rejoinInstance('root@192.168.74.137:3306')
```

根据提示输入 MySQL 密码。

**成功结果：** MySQL Shell 报告实例已经成功重新加入，或者该成员状态变为 `ONLINE`。

**出现以下情况必须停止：** 输出报告 errant transaction、GTID 分叉、恢复所需事务缺失、要求使用 Clone，或者持续出现认证或网络错误。只有获得 DBA 批准后，才能继续执行第 5 章。

### 第 7 步：执行最终状态检查

执行：

```javascript
cluster.status({extended: 2})
```

只有同时满足以下全部条件，才表示恢复完成：

- 集群整体状态为 `OK`。
- 正好一个成员的 `memberRole` 为 `PRIMARY`。
- 正好两个成员的 `memberRole` 为 `SECONDARY`。
- 三个成员的 `status` 全部为 `ONLINE`。

任何一个条件不满足，都不能判定恢复完成。

## 4. 丢失 quorum 恢复

只有在至少一个成员仍为 `ONLINE`、但集群已经失去多数 quorum 时，才使用本章节。

> **脑裂警告：** `forceQuorumUsingPartitionOf()` 是最后手段。运行前必须确认网络中不存在其他仍在活动的集群分区。如果发现其他活动分区，立即停止。

### 第 1 步：确定可信节点

`TRUSTED_NODE_IP` 必须是唯一存活权威分区中的 `ONLINE` 成员。该节点必须保存集群元数据，并且 MySQL Shell 可以连接。

不要仅因为某个节点以前是 PRIMARY，或者它最先启动，就把它选为可信节点。

### 第 2 步：连接可信节点

在 Linux 终端执行：

```bash
/aifar/apps/mysql/mysql-shell/bin/mysqlsh --js --uri CLUSTER_ADMIN@TRUSTED_NODE_IP:MYSQL_PORT
```

根据提示输入 MySQL 密码。

**成功结果：** 命令提示符变为 `mysql-js>`。

### 第 3 步：加载集群对象

执行：

```javascript
var cluster = dba.getCluster('aifarCluster')
```

**成功结果：** 没有异常，并且成功创建 `cluster` 变量。

### 第 4 步：使用可信分区恢复 quorum

只有确认脑裂警告中的条件后，才能执行：

```javascript
cluster.forceQuorumUsingPartitionOf('CLUSTER_ADMIN@TRUSTED_NODE_IP:MYSQL_PORT')
```

示例：

```javascript
cluster.forceQuorumUsingPartitionOf('root@192.168.74.133:3306')
```

**成功结果：** MySQL Shell 报告已经使用可信分区成功恢复 InnoDB Cluster。

**出现以下情况必须停止：** 发现其他活动分区、可信节点不是 `ONLINE`，或者 MySQL Shell 报告元数据或 GTID 错误。

### 第 5 步：重新加入缺失成员并检查

先执行 `cluster.status({extended: 2})`，然后只重新加入不是 `ONLINE` 的成员：

```javascript
cluster.status({extended: 2})
cluster.rejoinInstance('CLUSTER_ADMIN@NODE2_IP:MYSQL_PORT')
cluster.rejoinInstance('CLUSTER_ADMIN@NODE3_IP:MYSQL_PORT')
cluster.status({extended: 2})
```

最终状态必须包含一个 PRIMARY、两个 SECONDARY，并且三个成员全部为 `ONLINE`。

## 5. 使用 Clone 替换一个异常成员

只有在集群已经可以正常运行、但其中一个成员无法重新加入时，才使用本章节。

> **数据丢失警告：** Clone 恢复会使用健康集群成员的物理快照，完整覆盖 `FAILED_NODE_IP` 上的 MySQL 数据。继续操作前必须获得明确批准。

### 第 1 步：确认异常节点

执行：

```javascript
cluster.status({extended: 2})
```

将不是 `ONLINE` 的成员地址填写为 `FAILED_NODE_IP`。确认健康集群中存在一个 `ONLINE` 的 PRIMARY，并确认异常节点本地数据允许被删除。

### 第 2 步：从集群元数据中移除异常成员

执行：

```javascript
cluster.removeInstance('CLUSTER_ADMIN@FAILED_NODE_IP:MYSQL_PORT', {force: true})
```

示例：

```javascript
cluster.removeInstance('root@192.168.74.137:3306', {force: true})
```

**成功结果：** 异常成员不再出现在集群成员列表中。

**出现以下情况必须停止：** `FAILED_NODE_IP` 是当前 PRIMARY、存在多个异常成员，或者没有获得丢弃异常节点本地数据的批准。

### 第 3 步：使用 Clone 恢复重新加入成员

执行：

```javascript
cluster.addInstance(
  'CLUSTER_ADMIN@FAILED_NODE_IP:MYSQL_PORT',
  {recoveryMethod: 'clone'}
)
```

示例：

```javascript
cluster.addInstance(
  'root@192.168.74.137:3306',
  {recoveryMethod: 'clone'}
)
```

根据提示输入密码。Clone 恢复过程中，MySQL Shell 可能会重新启动异常节点上的 MySQL 实例。

**成功结果：** Clone 完成，实例重新加入集群，并且状态变为 `ONLINE`。

**出现以下情况必须停止：** Clone 失败、供体节点不健康、目标节点无法重启，或者目标节点仍未进入集群。

### 第 4 步：检查最终集群状态

执行：

```javascript
cluster.status({extended: 2})
```

最终状态必须显示一个 PRIMARY、两个 SECONDARY、三个成员全部 `ONLINE`，并且集群整体状态为 `OK`。

## 6. 停止操作并升级给 DBA

出现以下任一情况时，必须立即停止，不要使用 `force:true`：

- 三个节点的 GTID 集合出现分叉。
- MySQL Shell 报告 errant transaction 或 lost transaction。
- 无法确定权威节点或事务最新节点。
- InnoDB Cluster 元数据缺失或严重损坏。
- 可能仍有多个活动分区。
- `dryRun` 校验失败。
- 多于一个节点需要破坏性恢复。
- 可能需要使用有效备份重建集群。

必须由 DBA 确认权威数据源，并决定是否需要使用有效备份重建集群。

## 7. 官方文档

- [完整停机后重启集群](https://dev.mysql.com/doc/mysql-shell/8.0/en/reboot-outage.html)
- [成员重新加入集群](https://dev.mysql.com/doc/mysql-shell/8.0/en/rejoin-cluster.html)
- [丢失 quorum 后恢复集群](https://dev.mysql.com/doc/mysql-shell/8.0/en/restore-cluster-from-quorum-loss.html)
- [使用 Clone 添加成员](https://dev.mysql.com/doc/mysql-shell/8.0/en/add-instances-cluster.html)
- [查看 InnoDB Cluster 状态](https://dev.mysql.com/doc/mysql-shell/8.0/en/monitoring-innodb-cluster.html)
