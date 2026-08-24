# AIFAR 基础服务密码轮换操作手册

本文用于在计划维护窗口内轮换 AIFAR 环境中的 MySQL、MinIO、Redis Sentinel、Nacos 以及 AIFAR Runtime 连接密码。

> **执行结论：** 这不是单个组件的独立改密。必须先停止或下线业务 Runtime，按依赖顺序修改服务端与所有消费者配置，重新建立连接并完成验收，最后才能吊销 MySQL 旧密码。

> **安全警告：** 不要把真实密码写入本文、工单、聊天记录、Shell 历史、命令参数截图或 Git。本文中的 `<...>` 均为占位符，执行时从受控密码管理工具读取实际值。

## 1. 适用范围与拓扑假设

本手册基于当前 AIFAR 安装目录和服务名：

| 组件 | 默认路径或服务 |
| --- | --- |
| MySQL Shell | `/aifar/apps/mysql/mysql-shell/bin/mysqlsh` |
| MinIO | `/aifar/apps/minio`、`aifar-minio.service` |
| Redis | `/aifar/apps/redis`、`aifar-redis.service` |
| Redis Sentinel | `aifar-redis-sentinel.service` |
| Nacos | `/aifar/apps/nacos/nacos`、`aifar-nacos.service` |
| AIFAR Runtime 密钥文件 | `/aifar/apps/admin/runtime/env/java-secrets.env` |

MinIO 章节假设 41、42 是两套独立的 standalone MinIO，并通过 `aifar` 桶做双向桶复制。这与一个 distributed MinIO 集群不同。若 `MINIO_VOLUMES` 中包含多个节点地址，说明现场可能是 distributed 拓扑，应停止执行本章，改用分布式集群改密方案。

本文不假设以下信息一定固定，执行前必须从现场确认：

- MySQL 当前 PRIMARY 地址；
- MySQL 实际存在的 `root@Host` 账号行；
- Redis 当前 master、replica、Sentinel 节点和 master name；
- MinIO 41、42 的实际 IP、复制桶和两条复制规则 ID；
- Nacos 的 namespace、group、Data ID 以及各配置中的真实密码字段；
- 所有 AIFAR Runtime 节点和改密前各业务服务的期望副本数。

## 2. 变量与密码要求

| 占位符 | 含义 |
| --- | --- |
| `<MYSQL_PRIMARY_IP>` | MySQL 当前 PRIMARY IP |
| `<MYSQL_NEW_PASSWORD>` | MySQL 新密码 |
| `<MINIO_NODE_41_IP>` | MinIO 41 节点 IP |
| `<MINIO_NODE_42_IP>` | MinIO 42 节点 IP |
| `<MINIO_NEW_PASSWORD>` | MinIO 新 root 密码 |
| `<RULE_ID_41_TO_42>` | 41 到 42 的 `aifar` 桶复制规则 ID |
| `<RULE_ID_42_TO_41>` | 42 到 41 的 `aifar` 桶复制规则 ID |
| `<REDIS_NEW_PASSWORD>` | Redis 数据节点和 Sentinel 共用的新密码 |
| `<REDIS_MASTER_NAME>` | Sentinel 监控的实际 master name，默认安装值通常为 `aifar-master` |
| `<NACOS_NEW_PASSWORD>` | Nacos 管理账号新密码 |

密码要求：

- MySQL、MinIO、Redis、Nacos 应使用不同密码；只有当前 Redis 数据节点和 Sentinel 按既有拓扑使用同一个 Redis 密码。
- 每个密码建议至少 16 位，由密码管理工具随机生成。
- 当前配置文件采用 SQL 字符串、Shell env 和 Redis 配置语法。为降低人工转义错误，建议本次使用字母、数字及 `._@%+=:-` 组成的密码，不使用空格、单双引号、反斜杠、反引号、`#`、`$` 或换行。
- 所有旧密码在验收完成前保留在密码管理工具中，不写入本地明文文件。

## 3. 总体执行顺序

1. 申请维护窗口，停止外部写入并下线全部 AIFAR 业务 Runtime。
2. 记录拓扑、服务状态、Runtime 期望副本数和复制规则 ID。
3. 备份 Redis、MinIO、Nacos 和 Runtime 配置文件。
4. 在 MySQL PRIMARY 上设置新密码并暂时保留旧密码。
5. 若 Nacos 外部数据库使用本次修改的 MySQL 账号，立即更新所有 Nacos 节点的 `db.password.0` 并重启验证。
6. 修改并验证 Redis 数据节点和 Sentinel 密码。
7. 修改并验证两套 MinIO root 密码及双向桶复制目标。
8. 在 Nacos 控制台修改 Nacos 管理员密码，并修改所有相关业务 Data ID 中的 MySQL、Redis、MinIO 密码。
9. 更新每个 Runtime 节点的 `java-secrets.env`，在 AIFAR 管理平台更新对应凭据记录。
10. 通过管理平台执行“全部重启（读取新配置）”，完成端到端验收。
11. 确认所有消费者均已使用新密码后，删除 MySQL secondary password。

任一阶段失败时停止后续步骤，保持业务 Runtime 下线，进入第 11 章的回滚流程。

## 4. 维护前检查与备份

### 4.1 下线业务 Runtime

在 AIFAR 管理平台记录每个业务服务当前期望副本数，然后将全部业务服务下线或将期望副本数调整为 `0`。确认业务容器已经退出，避免改密期间仍产生写入和旧密码重连。

不要仅执行 `docker stop`。如果控制面的期望副本数仍大于 `0`，后续 reconcile 可能重新拉起服务。

### 4.2 记录服务状态

分别在对应节点执行并保存脱敏结果：

```bash
systemctl is-active aifar-mysql
systemctl is-active aifar-minio
systemctl is-active aifar-redis
systemctl is-active aifar-redis-sentinel
systemctl is-active aifar-nacos
```

不存在于该节点的服务可以显示 `inactive` 或 `unknown`，但必须与管理平台登记的拓扑一致。

### 4.3 备份配置

在每个相关节点创建仅 root 可读的维护备份目录：

```bash
BACKUP_TAG="$(date +%Y%m%d-%H%M%S)"
BACKUP_DIR="/root/aifar-password-rotation-$BACKUP_TAG"
install -d -m 0700 "$BACKUP_DIR"
```

根据节点角色备份实际存在的文件：

```bash
cp -a /aifar/apps/minio/conf/minio.env "$BACKUP_DIR/" 2>/dev/null || true
cp -a /aifar/apps/minio/conf/mc "$BACKUP_DIR/mc" 2>/dev/null || true
cp -a /aifar/apps/redis/conf/redis.conf "$BACKUP_DIR/" 2>/dev/null || true
cp -a /aifar/apps/redis/conf/sentinel.conf "$BACKUP_DIR/" 2>/dev/null || true
cp -a /aifar/apps/nacos/nacos/conf/application.properties "$BACKUP_DIR/" 2>/dev/null || true
cp -a /aifar/apps/admin/runtime/env/java-secrets.env "$BACKUP_DIR/" 2>/dev/null || true
```

确认备份文件存在且权限未被放宽：

```bash
find "$BACKUP_DIR" -maxdepth 2 -type f -printf '%m %p\n'
```

Nacos 业务配置还必须从控制台按 namespace 导出，或逐个确认 Data ID 存在可回滚的历史版本。配置导出文件同样按敏感文件管理。

## 5. MySQL 密码轮换

### 5.1 确认 PRIMARY 和账号范围

先在 AIFAR 管理平台查看当前 MySQL PRIMARY，再进入任意一台安装了 MySQL Shell 的服务器：

```bash
cd /aifar/apps/mysql/mysql-shell/bin
```

使用旧密码连接当前 PRIMARY。`--password` 后不填写值，让 MySQL Shell 安全提示输入；不要把密码拼在命令行中。

```bash
./mysqlsh \
  --credential-store-helper='<disabled>' \
  --save-passwords=never \
  --sql \
  --host=<MYSQL_PRIMARY_IP> \
  --port=3306 \
  --user=root \
  --password
```

在 SQL 模式执行：

```sql
SELECT CURRENT_USER(), @@hostname, @@read_only, @@super_read_only;

SELECT MEMBER_HOST, MEMBER_PORT, MEMBER_STATE, MEMBER_ROLE
FROM performance_schema.replication_group_members
ORDER BY MEMBER_HOST, MEMBER_PORT;

SELECT User, Host, plugin, account_locked
FROM mysql.user
WHERE User = 'root'
ORDER BY Host;
```

继续执行的条件：

- 当前连接节点 `@@read_only = 0` 且 `@@super_read_only = 0`；
- 集群只有一个 `ONLINE/PRIMARY`；
- 预期成员全部为 `ONLINE`；
- 已确认哪些 `root@Host` 行真实存在；
- 目标账号使用预期的密码认证插件。若 `plugin` 不是现场已批准的 `caching_sha2_password` 或 `mysql_native_password`，应先确认认证设计，避免 `IDENTIFIED BY` 意外改变账号认证方式。

如果条件不满足，停止改密并先恢复 MySQL 集群健康。

### 5.2 设置新密码并保留旧密码

只对查询结果中真实存在的账号执行以下语句。每条语句单独执行并确认成功；不存在的 Host 行不要执行。

```sql
ALTER USER 'root'@'%'
  IDENTIFIED BY '<MYSQL_NEW_PASSWORD>'
  RETAIN CURRENT PASSWORD;

ALTER USER 'root'@'127.0.0.1'
  IDENTIFIED BY '<MYSQL_NEW_PASSWORD>'
  RETAIN CURRENT PASSWORD;

ALTER USER 'root'@'localhost'
  IDENTIFIED BY '<MYSQL_NEW_PASSWORD>'
  RETAIN CURRENT PASSWORD;
```

`RETAIN CURRENT PASSWORD` 会将原密码作为 secondary password 暂时保留。已有会话通常不会因 `ALTER USER` 立即断开；新连接、重连和连接池补充连接必须使用新密码或尚未吊销的 secondary password。

退出前清除本次 MySQL Shell 会话历史：

```text
\history clear
\quit
```

### 5.3 使用新密码验证

重新启动一个 MySQL Shell 会话并在提示中输入新密码：

```bash
./mysqlsh \
  --credential-store-helper='<disabled>' \
  --save-passwords=never \
  --sql \
  --host=<MYSQL_PRIMARY_IP> \
  --port=3306 \
  --user=root \
  --password
```

```sql
SELECT CURRENT_USER(), @@hostname, @@read_only, @@super_read_only;

SELECT MEMBER_HOST, MEMBER_PORT, MEMBER_STATE, MEMBER_ROLE
FROM performance_schema.replication_group_members
ORDER BY MEMBER_HOST, MEMBER_PORT;
```

此步骤不需要重启 MySQL，也不需要重新执行 `configureInstance()`、`createCluster()`、`addInstance()` 或 `rejoinInstance()`。

### 5.4 同步 Nacos 外部 MySQL 密码

只有当 Nacos `db.user.0` 使用的正是本次修改的 MySQL 账号时才执行本节。若 Nacos 使用独立账号，不得误改。

在每个 Nacos 节点修改：

```bash
vi /aifar/apps/nacos/nacos/conf/application.properties
```

将实际配置更新为：

```properties
db.password.0=<MYSQL_NEW_PASSWORD>
```

集群模式下逐节点重启并验证，一个节点健康后再处理下一个节点：

```bash
systemctl restart aifar-nacos
systemctl is-active aifar-nacos
curl -fsS http://127.0.0.1:8848/nacos/v1/console/health/readiness
```

若现场 Nacos context path 或 readiness 地址不同，应以当前 systemd 参数和已验证健康地址为准。任何节点无法重新连接 MySQL 时，立即停止后续步骤；此时 MySQL 旧密码仍作为 secondary password 可用。

### 5.5 更新 AIFAR 中的 MySQL 凭据记录

新密码连接和集群状态验证通过后，在 AIFAR 凭据中心更新与该 MySQL 实例实际绑定的账号记录。仅保存凭据记录不会修改 MySQL 服务端密码，因此必须在服务端改密成功之后执行。

暂时不要执行 `DISCARD OLD PASSWORD`，待第 10 章全部验收通过后再吊销旧密码。

## 6. MinIO 密码与双向桶复制更新

### 6.1 确认是两套 standalone MinIO

在 41、42 节点分别检查：

```bash
systemctl cat aifar-minio
grep -E '^(MINIO_ROOT_USER|MINIO_VOLUMES)=' /aifar/apps/minio/conf/minio.env
```

不要输出 `MINIO_ROOT_PASSWORD`。只有确认两端是独立 MinIO、桶 `aifar` 已启用版本控制且存在双向桶复制规则时，才继续本章。

完成 alias 更新后还应在两端确认版本控制状态：

```bash
/aifar/apps/minio/bin/mc --config-dir /aifar/apps/minio/conf/mc version info aifar-local/aifar
/aifar/apps/minio/bin/mc --config-dir /aifar/apps/minio/conf/mc version info aifar-peer/aifar
```

两端都必须显示版本控制已启用。若 alias 当前仍保存旧密码，可以在第 6.3、6.4 节更新 alias 后再执行本检查，但恢复业务写入前必须完成。

### 6.2 修改两个节点的 root 密码

先在 41 节点修改：

```bash
vi /aifar/apps/minio/conf/minio.env
```

```dotenv
MINIO_ROOT_PASSWORD="<MINIO_NEW_PASSWORD>"
```

保存后执行：

```bash
chmod 600 /aifar/apps/minio/conf/minio.env
systemctl restart aifar-minio
systemctl is-active aifar-minio
curl -fsS http://127.0.0.1:9000/minio/health/live
```

然后在 42 节点执行相同修改和验证。41 已使用新密码而 42 尚未完成时，复制认证可能暂时失败，这是维护窗口中的预期中间态；不要恢复业务写入。

### 6.3 在 41 节点更新本地和 peer alias

以下方式让密码只在隐藏输入中录入，Shell 历史中只保留变量名。`mc alias set` 本身仍会短暂把 secret 作为进程参数，因此应在受控管理员会话中执行并立即 `unset`。

```bash
cd /aifar/apps/minio/bin
umask 077
export MC_CONFIG_DIR=/aifar/apps/minio/conf/mc
read -r -s -p 'MinIO new password: ' MINIO_NEW_PASSWORD
printf '\n'

./mc alias set aifar-local \
  http://127.0.0.1:9000 \
  admin \
  "$MINIO_NEW_PASSWORD" \
  --api S3v4

./mc alias set aifar-peer \
  http://<MINIO_NODE_42_IP>:9000 \
  admin \
  "$MINIO_NEW_PASSWORD" \
  --api S3v4

unset MINIO_NEW_PASSWORD
./mc admin info aifar-local
./mc admin info aifar-peer
```

列出 41 到 42 的复制规则并记录正确规则 ID：

```bash
./mc replicate ls aifar-local/aifar
```

使用 peer alias 更新远端目标，避免把密码嵌入 `http://user:password@host/...`：

```bash
./mc replicate update aifar-local/aifar \
  --id '<RULE_ID_41_TO_42>' \
  --remote-bucket aifar-peer/aifar

./mc replicate ls aifar-local/aifar
./mc replicate status aifar-local/aifar --nodes --targets
```

### 6.4 在 42 节点更新本地和 peer alias

注意：42 节点的 `aifar-peer` 必须指向 **41 的 IP**，不能再次指向 42 自己。

```bash
cd /aifar/apps/minio/bin
umask 077
export MC_CONFIG_DIR=/aifar/apps/minio/conf/mc
read -r -s -p 'MinIO new password: ' MINIO_NEW_PASSWORD
printf '\n'

./mc alias set aifar-local \
  http://127.0.0.1:9000 \
  admin \
  "$MINIO_NEW_PASSWORD" \
  --api S3v4

./mc alias set aifar-peer \
  http://<MINIO_NODE_41_IP>:9000 \
  admin \
  "$MINIO_NEW_PASSWORD" \
  --api S3v4

unset MINIO_NEW_PASSWORD
./mc admin info aifar-local
./mc admin info aifar-peer
./mc replicate ls aifar-local/aifar

./mc replicate update aifar-local/aifar \
  --id '<RULE_ID_42_TO_41>' \
  --remote-bucket aifar-peer/aifar

./mc replicate ls aifar-local/aifar
./mc replicate status aifar-local/aifar --nodes --targets
```

### 6.5 验证双向复制

分别从 41、42 上传不同名称的小测试文件，并在对端确认对象出现。测试对象名必须唯一，避免把历史对象误判为本次同步结果。

41 节点：

```bash
TEST_OBJECT_41="password-rotation/41-$(date +%Y%m%d-%H%M%S).txt"
printf 'source=41 time=%s\n' "$(date -Is)" | ./mc pipe "aifar-local/aifar/$TEST_OBJECT_41"
echo "$TEST_OBJECT_41"
```

在 42 节点使用本地 alias 验证同一个对象路径：

```bash
./mc stat "aifar-local/aifar/<TEST_OBJECT_41>"
```

再从 42 上传另一个对象，并在 41 验证。最后两端都执行：

```bash
./mc replicate status aifar-local/aifar --nodes --targets
```

确认没有新的认证失败和持续 backlog 后，在 AIFAR 凭据中心更新两个 MinIO 实例实际绑定的凭据记录。

## 7. Redis Sentinel 密码轮换

### 7.1 记录当前拓扑

改密前从任一可用 Sentinel 查询当前 master。使用 `--askpass`，不要通过 `-a <密码>` 暴露旧密码：

```bash
cd /aifar/apps/redis/bin

./redis-cli \
  -p 26379 \
  --user default \
  --askpass \
  SENTINEL GET-MASTER-ADDR-BY-NAME <REDIS_MASTER_NAME>

./redis-cli \
  -p 26379 \
  --user default \
  --askpass \
  SENTINEL CKQUORUM <REDIS_MASTER_NAME>
```

记录当前 master IP、所有 replica、所有 Sentinel 节点以及 master name。不要仅凭安装时的固定节点顺序判断 master。

### 7.2 按全局顺序停止服务

先在 **所有 Sentinel 节点** 停止 Sentinel，全部确认停止后，再在 **所有 Redis 数据节点** 停止 Redis。不要逐节点同时停 Sentinel 和 Redis，否则剩余 Sentinel 可能在维护过程中触发不必要的故障转移。

所有 Sentinel 节点：

```bash
systemctl stop aifar-redis-sentinel
systemctl is-active aifar-redis-sentinel
```

所有 Redis 数据节点：

```bash
systemctl stop aifar-redis
systemctl is-active aifar-redis
```

如果服务没有变为 `inactive`，停止操作并检查 `systemctl status` 和日志，不要直接 `kill -9`。

### 7.3 修改所有 Redis 数据节点

在每个 Redis 数据节点编辑：

```bash
vi /aifar/apps/redis/conf/redis.conf
```

确保以下配置各只有一行并使用同一个新密码：

```conf
requirepass <REDIS_NEW_PASSWORD>
masterauth <REDIS_NEW_PASSWORD>
```

然后收紧文件权限：

```bash
chmod 600 /aifar/apps/redis/conf/redis.conf
```

### 7.4 修改所有 Sentinel 节点

在每个 Sentinel 节点编辑：

```bash
vi /aifar/apps/redis/conf/sentinel.conf
```

当前 AIFAR 模板对应的关键配置应类似：

```conf
user default on ><REDIS_NEW_PASSWORD> allcommands allkeys allchannels
sentinel auth-pass <REDIS_MASTER_NAME> <REDIS_NEW_PASSWORD>
sentinel sentinel-pass <REDIS_NEW_PASSWORD>
```

注意事项：

- `<REDIS_MASTER_NAME>` 必须与同一文件中 `sentinel monitor` 的名称完全一致。
- `user default` 可能已被 Redis 重写为 `#<SHA256>` 密码摘要。修改时应移除旧的 `>` 或 `#` 密码令牌，只保留一个新密码令牌，避免旧密码继续有效。
- 如果现场 `user default` 行包含 `sanitize-payload`、`~*`、`&*`、`+@all` 等等价权限写法，保留原权限范围，只替换密码令牌。
- 不要把 Markdown 中用于占位的尖括号输入配置；实际格式是 `>新密码`，`>` 与密码之间没有空格。

完成后执行：

```bash
chmod 600 /aifar/apps/redis/conf/sentinel.conf
```

### 7.5 按拓扑启动并验证

1. 先启动改密前记录的 Redis master。
2. 使用新密码确认 master 可用。
3. 再逐台启动 replica，确认复制链路为 `up`。
4. 最后启动全部 Sentinel。

Redis master：

```bash
systemctl start aifar-redis
/aifar/apps/redis/bin/redis-cli -p 6379 --user default --askpass ROLE
```

每个 replica：

```bash
systemctl start aifar-redis
/aifar/apps/redis/bin/redis-cli -p 6379 --user default --askpass ROLE
/aifar/apps/redis/bin/redis-cli -p 6379 --user default --askpass INFO replication
```

只有 replica 输出的 `master_link_status:up` 后，才启动 Sentinel。所有 Sentinel 节点：

```bash
systemctl start aifar-redis-sentinel
systemctl is-active aifar-redis-sentinel
```

从至少两个 Sentinel 节点验证：

```bash
/aifar/apps/redis/bin/redis-cli \
  -p 26379 \
  --user default \
  --askpass \
  SENTINEL GET-MASTER-ADDR-BY-NAME <REDIS_MASTER_NAME>

/aifar/apps/redis/bin/redis-cli \
  -p 26379 \
  --user default \
  --askpass \
  SENTINEL CKQUORUM <REDIS_MASTER_NAME>
```

确认所有 Sentinel 返回同一个 master，quorum 检查为 `OK`，再在 AIFAR 凭据中心更新该 Redis 拓扑实际绑定的凭据记录。

## 8. Nacos 管理密码与业务配置更新

### 8.1 修改 Nacos 管理员密码

使用当前管理员账号登录 Nacos Web 控制台，在用户管理页面修改该账号密码。Nacos 集群使用共享外部数据库时，只需修改一次，不需要逐节点修改用户表。

修改后必须退出当前会话，并使用无痕窗口或新的浏览器会话验证新密码登录。不要仅依赖当前页面仍可操作来判断成功，因为已有 token 可能在过期前继续有效。

如果现场不是 Nacos 默认本地用户认证，而是 LDAP、OIDC 或自定义认证插件，应停止本节并按实际身份源执行密码轮换。

### 8.2 修改 Nacos 中的业务连接配置

进入 Nacos 配置管理，按 **namespace → group → Data ID** 逐项检查。若现场确实存在 `datasource.yaml`，修改该 Data ID；同时搜索并检查其他服务专属 YAML，不要假设密码只存在一个文件。

根据实际消费者修改：

- MySQL：仅修改使用本次轮换账号的数据源密码；
- Redis：修改 Sentinel 模式下的数据密码、Sentinel 密码或客户端对应字段；
- MinIO：修改业务使用的 access key/secret key。若业务暂时直接使用 root 账号，则更新为本次 root 密码，并计划后续迁移到独立最小权限 access key；
- 保留 URL、端口、database、master name、namespace、group 和其他业务参数不变。

每次发布前：

1. 核对 namespace、group、Data ID；
2. 确认已导出旧配置或可从历史版本回滚；
3. 使用 YAML 校验工具检查缩进和引号；
4. 只修改目标密码字段，不做无关格式化；
5. 发布后记录 Data ID 和新版本时间，不记录密码。

### 8.3 更新 AIFAR Runtime 的 Nacos 密码

在 **每个** AIFAR Runtime 节点修改：

```bash
vi /aifar/apps/admin/runtime/env/java-secrets.env
```

```dotenv
NACOS_PASSWORD=<NACOS_NEW_PASSWORD>
```

保存后确认权限：

```bash
chmod 600 /aifar/apps/admin/runtime/env/java-secrets.env
stat -c '%a %U:%G %n' /aifar/apps/admin/runtime/env/java-secrets.env
```

只修改宿主机 env 文件不会改变正在运行容器的环境变量。普通 `docker restart`、`docker compose restart` 或重启 Docker daemon也不会重新读取 `--env-file`；必须通过管理平台触发读取新配置的重建流程。

### 8.4 更新 AIFAR 中的 Nacos 凭据记录

在 AIFAR 凭据中心更新 Nacos 管理账号对应记录，确认该记录绑定到正确的 Nacos 实例。再次强调：这一步只更新面板加密记录，不能替代 Nacos 服务端改密。

## 9. 恢复 Runtime 并强制新连接验收

在管理平台进入 AIFAR Runtime 页面，执行：

> **全部重启（读取新配置）**

该操作应重新读取 env 配置并滚动重建已启用服务。若当前部署版本没有该入口或功能被禁用，不要用普通 `docker restart` 代替；应先确认该版本支持的受控重建方式。

恢复改密前记录的期望副本数，并逐项验证：

- 所有服务恢复到预期副本数；
- 新容器 ID 或创建时间已变化，证明发生了重建而非仅重启旧容器；
- Nacos 服务注册实例数量与实际副本一致；
- Java 日志中没有新的 MySQL、Redis、MinIO、Nacos `Access denied`、`NOAUTH`、`WRONGPASS`、`InvalidAccessKeyId`、`SignatureDoesNotMatch` 或登录失败；
- 核心业务登录、查询、写入、文件上传和下载 smoke test 通过。

## 10. 最终验收与吊销 MySQL 旧密码

### 10.1 验收清单

- [ ] MySQL 新密码可建立新连接，PRIMARY 可写，预期成员全部 `ONLINE`。
- [ ] MySQL Router 如被业务使用，已通过 Router 读写端口用新密码建立连接并完成真实读写。
- [ ] Nacos 所有节点可连接外部 MySQL，readiness 正常。
- [ ] Redis master/replica 角色正确，复制链路 `up`。
- [ ] 至少两个 Sentinel 返回同一个 master，`CKQUORUM` 为 `OK`。
- [ ] MinIO 41、42 均健康，两条复制规则启用且双向测试对象同步成功。
- [ ] Nacos 新密码能在新会话登录。
- [ ] 所有相关 Nacos Data ID 已更新并有可回滚历史。
- [ ] 所有 Runtime 节点的 `java-secrets.env` 已更新且权限为 `0600`。
- [ ] 所有业务容器已重建、健康且恢复原期望副本数。
- [ ] AIFAR 凭据中心的 MySQL、Redis、MinIO、Nacos 绑定记录已更新并验证。
- [ ] 所有外部消费者、脚本、监控和备份任务已使用新密码验证。
- [ ] Redis、MinIO、Nacos 旧密码的新建认证已失败；验证过程未中断唯一的新密码管理会话。

### 10.2 吊销 MySQL secondary password

只有上述清单全部通过，并且已强制所有消费者建立新连接后，才在 MySQL PRIMARY 对实际存在的账号执行：

```sql
ALTER USER 'root'@'%' DISCARD OLD PASSWORD;
ALTER USER 'root'@'127.0.0.1' DISCARD OLD PASSWORD;
ALTER USER 'root'@'localhost' DISCARD OLD PASSWORD;
```

仍然只执行现场真实存在的 Host 行。吊销后使用旧密码发起一次新连接，应认证失败；使用新密码应成功。不要为了验证旧密码而断开当前唯一管理会话，至少保留一个已验证的新密码管理会话作为应急通道。

## 11. 失败停止与回滚

### 11.1 通用停止条件

出现以下任一情况时，不得继续下一组件：

- MySQL PRIMARY 不唯一、成员不全为 `ONLINE` 或新密码无法连接；
- Nacos 任一节点无法连接外部 MySQL；
- Redis replica 无法恢复 `master_link_status:up` 或 Sentinel quorum 不足；
- MinIO 任一端不健康、peer alias 无法连接或复制规则更新失败；
- Nacos 新密码无法在新会话登录；
- Nacos YAML 无法确认修改范围或发布后业务解析失败；
- Runtime 容器未真正重建，或核心业务 smoke test 失败。

### 11.2 回滚原则

1. 保持业务 Runtime 下线，不要让使用混合凭据的服务反复重连。
2. 从 Nacos 历史版本恢复被修改的业务 Data ID。
3. 恢复每个 Runtime 节点的 `java-secrets.env` 备份。
4. 使用当前可登录的 Nacos 管理员会话将管理员密码恢复为旧值并重新验证。
5. 在两个 MinIO 节点恢复 `minio.env` 和 `mc` 配置备份，重启两端，并用旧凭据重新更新两条复制目标。
6. 先停止全部 Sentinel，再停止全部 Redis；恢复所有 `redis.conf`、`sentinel.conf`，按 master → replica → Sentinel 顺序启动验证。
7. 恢复所有 Nacos 节点的 `application.properties`，逐节点重启验证。
8. MySQL 旧密码仍是 secondary password 时，可以继续建立旧密码连接。确认下游已恢复后，再将旧密码恢复为 primary 并清理不需要的 secondary password。
9. 将 AIFAR 凭据中心记录恢复到与远端实际状态一致。
10. 完整重复第 10 章验收后，才能恢复业务流量。

配置备份至少保留到变更验收和观察期结束。删除前确认审计要求和外部安全存储中已有合规副本。

## 12. 操作记录模板

| 阶段 | 开始时间 | 完成时间 | 操作人 | 验证人 | 结果/任务 ID | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| Runtime 下线 |  |  |  |  |  |  |
| MySQL 改密 |  |  |  |  |  |  |
| Nacos 外部 MySQL 同步 |  |  |  |  |  |  |
| Redis/Sentinel 改密 |  |  |  |  |  |  |
| MinIO 与复制改密 |  |  |  |  |  |  |
| Nacos 管理员改密 |  |  |  |  |  |  |
| Nacos Data ID 更新 |  |  |  |  |  |  |
| Runtime 重建 |  |  |  |  |  |  |
| 端到端验收 |  |  |  |  |  |  |
| MySQL 旧密码吊销 |  |  |  |  |  |  |

## 13. 参考资料

- [MySQL 8.0 `ALTER USER`、`RETAIN CURRENT PASSWORD` 与 `DISCARD OLD PASSWORD`](https://dev.mysql.com/doc/refman/8.0/en/alter-user.html)
- [MySQL Shell 命令行参数与密码提示](https://dev.mysql.com/doc/mysql-shell/8.0/en/mysqlsh.html)
- [MinIO `mc alias set`](https://docs.min.io/aistor/reference/cli/mc-alias/mc-alias-set/)
- [MinIO `mc replicate update`](https://docs.min.io/aistor/reference/cli/mc-replicate/mc-replicate-update/)
- [MinIO `mc replicate status`](https://docs.min.io/aistor/reference/cli/mc-replicate/mc-replicate-status/)
- [Redis Sentinel 高可用与认证配置](https://redis.io/docs/latest/operate/oss_and_stack/management/sentinel/)
- [Redis CLI `--askpass`](https://redis.io/docs/latest/develop/tools/cli/)
- [Redis ACL 规则](https://redis.io/docs/latest/operate/oss_and_stack/management/security/acl/)
- [Nacos 系统参数与外部数据源配置](https://nacos.io/en/docs/latest/manual/admin/system-configurations/)
- [Nacos 控制台与配置历史](https://nacos.io/en/docs/latest/manual/admin/console/)
