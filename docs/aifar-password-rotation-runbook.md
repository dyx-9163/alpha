# AIFAR 基础服务密码修改操作手册（现场验证版）

本文整理 MySQL、MinIO、Redis、Nacos 及 AIFAR Runtime 的密码修改步骤。

> 本文中的执行命令以现场已经完整验证通过的命令为准，只做 Markdown 排版和占位符统一，不改变命令语义与执行顺序。

> `<...>` 表示必须替换的现场参数。不要把真实密码提交到 Git、工单或聊天记录中。

## 1. 部署拓扑

| 节点 | 部署内容 | 本手册涉及的操作 |
| --- | --- | --- |
| 31、32 | 应用服务、AIFAR Runtime | 修改 Runtime 使用的 Nacos 密码，并同步应用配置 |
| 41、42 | MySQL、Redis、Nacos 集群节点；MinIO 节点 | 修改 MySQL、Redis、Nacos 集群密码；修改 MinIO 密码及双向复制配置 |
| 仲裁节点 | MySQL、Redis、Nacos 集群节点 | 修改 MySQL、Redis、Nacos 集群密码 |

执行范围：

- MySQL、Redis、Nacos：41、42及仲裁节点。
- MinIO：41、42节点。
- 应用服务和 AIFAR Runtime：31、32节点。

## 2. 参数说明

| 占位符 | 说明 |
| --- | --- |
| `<PRIMARY_IP>` | 管理平台显示的当前 MySQL PRIMARY IP |
| `<MYSQL_PASSWORD>` | MySQL 新密码 |
| `<MINIO_41_IP>` | 41 节点 IP |
| `<MINIO_42_IP>` | 42 节点 IP |
| `<MINIO_PASSWORD>` | MinIO `admin` 账号新密码 |
| `<41_RULE_ID>` | 41 节点上 `aifar-local/aifar` 的复制规则 ID |
| `<42_RULE_ID>` | 42 节点上 `aifar-local/aifar` 的复制规则 ID |
| `<REDIS_PASSWORD>` | Redis 与 Redis Sentinel 共用的新密码 |
| `<NACOS_PASSWORD>` | Nacos 新密码 |

## 3. MySQL

MySQL 集群部署在 41、42及仲裁节点。账号密码在当前 PRIMARY 上修改，MySQL Shell 可以从任意一台正在运行 MySQL 的集群节点进入。

### 3.1 查看当前 PRIMARY

进入 AIFAR 管理平台，查看当前 MySQL PRIMARY 节点并记录其 IP。

### 3.2 进入任意一台正在运行 MySQL 的集群节点

在 41、42或仲裁节点中任选一台正在运行 MySQL 的服务器执行：

```bash
cd /aifar/apps/mysql/mysql-shell/bin
```

### 3.3 使用 MySQL Shell 连接 PRIMARY

```bash
./mysqlsh root@<PRIMARY_IP>:3306 --sql
```

根据提示输入当前 MySQL root 密码。

#### 3.3.1 确认当前节点可读写

```sql
SELECT CURRENT_USER(), @@hostname, @@read_only, @@super_read_only;
```

#### 3.3.2 查看 root 账号信息

```sql
SELECT User, Host FROM mysql.user WHERE User = 'root';
```

#### 3.3.3 修改密码

根据上一步查询结果，对实际存在的 `root@Host` 账号执行对应语句：

```sql
ALTER USER 'root'@'%' IDENTIFIED BY '<MYSQL_PASSWORD>'
RETAIN CURRENT PASSWORD;

ALTER USER 'root'@'127.0.0.1' IDENTIFIED BY '<MYSQL_PASSWORD>'
RETAIN CURRENT PASSWORD;

ALTER USER 'root'@'localhost' IDENTIFIED BY '<MYSQL_PASSWORD>'
RETAIN CURRENT PASSWORD;
```

### 3.4 验证新密码是否生效

退出当前 MySQL Shell，然后重新执行：

```bash
./mysqlsh --credential-store-helper='<disabled>' \
  --save-passwords=never \
  --sql \
  --host=<PRIMARY_IP> \
  --port=3306 \
  --user=root \
  --password
```

根据提示输入 MySQL 新密码，确认可以正常登录。

## 4. MinIO

修改 41、42 两个节点的 MinIO 配置，并更新 `aifar` 桶的双向复制配置。

### 4.1 修改两个节点的 MinIO 密码

分别在 41、42 节点执行：

```bash
vi /aifar/apps/minio/conf/minio.env
```

修改：

```dotenv
MINIO_ROOT_PASSWORD="<MINIO_PASSWORD>"
```

### 4.2 在 41 节点执行

进入 MinIO 命令目录：

```bash
cd /aifar/apps/minio/bin
```

更新本地 alias：

```bash
MC_CONFIG_DIR=/aifar/apps/minio/conf/mc \
  ./mc alias set \
  aifar-local \
  http://127.0.0.1:9000 \
  "admin" \
  "<MINIO_PASSWORD>" \
  --api S3v4
```

更新 peer alias。41 节点的 peer 指向 42：

```bash
MC_CONFIG_DIR=/aifar/apps/minio/conf/mc \
  ./mc alias set \
  aifar-peer \
  http://<MINIO_42_IP>:9000 \
  "admin" \
  "<MINIO_PASSWORD>" \
  --api S3v4
```

重启 MinIO：

```bash
systemctl restart aifar-minio
```

> 现场验证注意：`systemctl is-active aifar-minio` 返回 `active` 后，9000 端口可能还需要短暂时间才真正可访问。如果下面执行 `mc replicate ls` 出现 `127.0.0.1:9000 connection refused`，先等待端口就绪后重试，例如确认 `ss -ltn | grep ':9000'` 已有监听。

设置 `mc` 路径：

```bash
MC=/aifar/apps/minio/bin/mc
CFG=/aifar/apps/minio/conf/mc
```

查看 41 节点上的复制规则并记录规则 ID：

```bash
"$MC" --config-dir "$CFG" replicate ls aifar-local/aifar
```

更新 41 到 42 的复制规则：

```bash
"$MC" --config-dir "$CFG" replicate update \
  aifar-local/aifar \
  --id "<41_RULE_ID>" \
  --remote-bucket \
  "http://admin:<MINIO_PASSWORD>@<MINIO_42_IP>:9000/aifar"
```

### 4.3 在 42 节点执行

进入 MinIO 命令目录：

```bash
cd /aifar/apps/minio/bin
```

更新本地 alias：

```bash
MC_CONFIG_DIR=/aifar/apps/minio/conf/mc \
  ./mc alias set \
  aifar-local \
  http://127.0.0.1:9000 \
  "admin" \
  "<MINIO_PASSWORD>" \
  --api S3v4
```

更新 peer alias。42 节点的 peer 指向 41：

```bash
MC_CONFIG_DIR=/aifar/apps/minio/conf/mc \
  ./mc alias set \
  aifar-peer \
  http://<MINIO_41_IP>:9000 \
  "admin" \
  "<MINIO_PASSWORD>" \
  --api S3v4
```

重启 MinIO：

```bash
systemctl restart aifar-minio
```

> 现场验证注意：`systemctl is-active aifar-minio` 返回 `active` 后，9000 端口可能还需要短暂时间才真正可访问。如果下面执行 `mc replicate ls` 出现 `127.0.0.1:9000 connection refused`，先等待端口就绪后重试，例如确认 `ss -ltn | grep ':9000'` 已有监听。

设置 `mc` 路径：

```bash
MC=/aifar/apps/minio/bin/mc
CFG=/aifar/apps/minio/conf/mc
```

查看 42 节点上的复制规则并记录规则 ID：

```bash
"$MC" --config-dir "$CFG" replicate ls aifar-local/aifar
```

更新 42 到 41 的复制规则：

```bash
"$MC" --config-dir "$CFG" replicate update \
  aifar-local/aifar \
  --id "<42_RULE_ID>" \
  --remote-bucket \
  "http://admin:<MINIO_PASSWORD>@<MINIO_41_IP>:9000/aifar"
```

### 4.4 验证双向同步

进入 MinIO 页面上传文件，分别查看 41、42 两个节点的同步结果。

## 5. Redis

Redis 集群部署在 41、42及仲裁节点。三个节点的 Redis 数据服务与 Redis Sentinel 使用相同的新密码。

### 5.1 停止 Redis 和 Sentinel

在 41、42及仲裁节点分别执行：

```bash
systemctl stop aifar-redis
systemctl stop aifar-redis-sentinel
```

### 5.2 修改 Redis 配置

在 41、42及仲裁节点分别执行：

```bash
cd /aifar/apps/redis/conf
vi redis.conf
```

修改：

```conf
requirepass <REDIS_PASSWORD>
masterauth <REDIS_PASSWORD>
```

### 5.3 修改 Sentinel 配置

在 41、42及仲裁节点分别执行：

```bash
vi sentinel.conf
```

修改：

```conf
user default on sanitize-payload ><REDIS_PASSWORD> ~* &* +@all

sentinel auth-pass aifar-master <REDIS_PASSWORD>
sentinel sentinel-pass <REDIS_PASSWORD>
```

`user default` 行中的 `>` 是 Redis ACL 密码规则的一部分。替换后应为 `>实际密码`，`>` 与密码之间没有空格。

## 6. Nacos

Nacos 集群部署在 41、42及仲裁节点。

### 6.1 修改 Nacos 使用的 MySQL 密码

在 41、42及仲裁节点分别执行：

```bash
vi /aifar/apps/nacos/nacos/conf/application.properties
```

修改：

```properties
db.password.0=<MYSQL_PASSWORD>
```

重启 Nacos：

```bash
systemctl restart aifar-nacos
```

### 6.2 修改 Nacos 登录密码

进入 Nacos Web 页面修改 Nacos 登录密码。

修改完成后，还需要同步修改应用使用的 Nacos 账号和密码配置。

如果无法进入 Nacos Web 页面，但可以使用 MySQL root 账号连接 Nacos 后端库，可使用 Nacos 自带的 `PasswordEncoderUtil` 生成兼容密码摘要后更新 `users` 表。以下为管理员兜底方式，执行前先确认 Nacos 后端库名和用户表；本环境库名为 `aifar_nacos`。

在任意一台 Nacos/MySQL 节点上准备临时工具：

```bash
rm -rf /tmp/aifar-nacos-auth-jars
mkdir -p /tmp/aifar-nacos-auth-jars
cd /tmp/aifar-nacos-auth-jars

/aifar/apps/nacos/jdk/bin/jar xf \
  /aifar/apps/nacos/nacos/target/nacos-server.jar \
  BOOT-INF/lib

cat > GenNacosPassword.java <<'EOF'
public class GenNacosPassword {
  public static void main(String[] args) {
    if (args.length != 1) throw new IllegalArgumentException("password required");
    System.out.print(com.alibaba.nacos.plugin.auth.impl.utils.PasswordEncoderUtil.encode(args[0]));
  }
}
EOF

/aifar/apps/nacos/jdk/bin/javac \
  -cp "BOOT-INF/lib/*" \
  GenNacosPassword.java
```

生成新密码摘要并更新 Nacos 用户密码：

```bash
NACOS_HASH=$(/aifar/apps/nacos/jdk/bin/java \
  -cp "/tmp/aifar-nacos-auth-jars:/tmp/aifar-nacos-auth-jars/BOOT-INF/lib/*" \
  GenNacosPassword "<NACOS_PASSWORD>")

cd /aifar/apps/mysql/mysql-shell/bin

./mysqlsh --credential-store-helper='<disabled>' \
  --save-passwords=never \
  --sql \
  --host=<PRIMARY_IP> \
  --port=3306 \
  --user=root \
  --password \
  --execute "UPDATE aifar_nacos.users SET password = '${NACOS_HASH}' WHERE username = 'nacos';"
```

更新后重启三台 Nacos，并重新登录 Web 页面验证：

```bash
systemctl restart aifar-nacos
```

### 6.3 修改 Nacos 中的业务配置

进入 Nacos Web 页面，在对应 namespace、group 和 Data ID 下修改：

- `datasource.yaml`
- `resources.yaml`

同步修改其中 MySQL 集群和 Redis 集群对应的密码。

> 现场验证注意：按实际配置内容修改即可，不要强行新增不存在的字段。如果 `resources.yaml` 中没有 MySQL 或 Redis 密码字段，则无需强行补密码。

## 7. AIFAR Runtime 服务器

应用服务和 AIFAR Runtime 部署在 31、32节点。在 31、32两台服务器分别修改：

```bash
vi /aifar/apps/admin/runtime/env/java-secrets.env
```

修改：

```dotenv
NACOS_PASSWORD=<NACOS_PASSWORD>
```

> 现场验证注意：修改 `java-secrets.env` 只会更新后续创建容器使用的配置文件，已经运行的 Docker 容器不会自动获得新的环境变量。修改后需要通过 AIFAR Runtime/Agent 重新发布或重建应用容器；仅修改文件或执行 `docker restart` 不能改变已创建容器的环境变量。

## 8. 完成确认

- [ ] MySQL 使用新密码可以重新登录。
- [ ] MySQL、Redis、Nacos 的操作范围已覆盖 41、42及仲裁节点。
- [ ] 41 节点的 `aifar-peer` 指向 42。
- [ ] 42 节点的 `aifar-peer` 指向 41。
- [ ] MinIO 页面上传文件后，双向同步结果正常。
- [ ] 41、42及仲裁节点的 Redis 与 Sentinel 配置已使用同一个新密码。
- [ ] 41、42及仲裁节点的 Nacos `db.password.0` 已更新并完成重启。
- [ ] Nacos 登录密码已经修改。
- [ ] `datasource.yaml`、`resources.yaml` 中的 MySQL、Redis 密码已经更新。
- [ ] 31、32应用节点的 `java-secrets.env` 已更新。
- [ ] 应用容器已通过 AIFAR Runtime/Agent 重新发布或重建，并确认运行中容器已使用新的 Nacos 密码。
