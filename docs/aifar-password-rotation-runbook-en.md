# AIFAR Base Service Password Rotation Runbook (Field-Verified)

This runbook describes how to rotate passwords for MySQL, MinIO, Redis, Nacos, and AIFAR Runtime.

> The commands in this document follow the field-verified procedure. The Markdown formatting and placeholders are normalized, but the command semantics and execution order are preserved.

> Values in `<...>` must be replaced with environment-specific values. Do not commit real passwords to Git, tickets, or chat records.

## 1. Deployment Topology

| Node | Deployed components | Operations covered by this runbook |
| --- | --- | --- |
| 31, 32 | Application services, AIFAR Runtime | Update the Nacos password used by Runtime and synchronize application configuration |
| 41, 42 | MySQL, Redis, and Nacos cluster nodes; MinIO nodes | Rotate MySQL, Redis, and Nacos cluster passwords; rotate MinIO password and update bidirectional replication |
| Arbiter node | MySQL, Redis, and Nacos cluster node | Rotate MySQL, Redis, and Nacos cluster passwords |

## 2. Parameters

| Placeholder | Description |
| --- | --- |
| `<PRIMARY_IP>` | Current MySQL PRIMARY IP shown in the management platform |
| `<MYSQL_PASSWORD>` | New MySQL password |
| `<MINIO_41_IP>` | IP address of node 41 |
| `<MINIO_42_IP>` | IP address of node 42 |
| `<MINIO_PASSWORD>` | New password for the MinIO `admin` account |
| `<41_RULE_ID>` | Replication rule ID for `aifar-local/aifar` on node 41 |
| `<42_RULE_ID>` | Replication rule ID for `aifar-local/aifar` on node 42 |
| `<REDIS_PASSWORD>` | New password shared by Redis and Redis Sentinel |
| `<NACOS_PASSWORD>` | New Nacos password |

## 3. MySQL

The MySQL cluster is deployed on node 41, node 42, and the arbiter node. Account passwords are changed on the current PRIMARY. MySQL Shell can be started from any running MySQL cluster node.

### 3.1 Check the Current PRIMARY

Open the AIFAR management platform, find the current MySQL PRIMARY node, and record its IP address.

### 3.2 Enter Any Running MySQL Cluster Node

Run the following command on any running MySQL server among node 41, node 42, and the arbiter node:

```bash
cd /aifar/apps/mysql/mysql-shell/bin
```

### 3.3 Connect to the PRIMARY with MySQL Shell

```bash
./mysqlsh root@<PRIMARY_IP>:3306 --sql
```

Enter the current MySQL root password when prompted.

#### 3.3.1 Confirm That the Current Node Is Writable

```sql
SELECT CURRENT_USER(), @@hostname, @@read_only, @@super_read_only;
```

#### 3.3.2 Check root Account Entries

```sql
SELECT User, Host FROM mysql.user WHERE User = 'root';
```

#### 3.3.3 Change the Password

Based on the previous query result, run the corresponding statements only for the `root@Host` entries that actually exist:

```sql
ALTER USER 'root'@'%' IDENTIFIED BY '<MYSQL_PASSWORD>'
RETAIN CURRENT PASSWORD;

ALTER USER 'root'@'127.0.0.1' IDENTIFIED BY '<MYSQL_PASSWORD>'
RETAIN CURRENT PASSWORD;

ALTER USER 'root'@'localhost' IDENTIFIED BY '<MYSQL_PASSWORD>'
RETAIN CURRENT PASSWORD;
```

### 3.4 Verify the New Password

Exit the current MySQL Shell session, then run:

```bash
./mysqlsh --credential-store-helper='<disabled>' \
  --save-passwords=never \
  --sql \
  --host=<PRIMARY_IP> \
  --port=3306 \
  --user=root \
  --password
```

Enter the new MySQL password when prompted and confirm that login succeeds.

## 4. MinIO

Update the MinIO configuration on node 41 and node 42, then update bidirectional replication for the `aifar` bucket.

### 4.1 Update the MinIO Password on Both Nodes

Run the following command separately on node 41 and node 42:

```bash
vi /aifar/apps/minio/conf/minio.env
```

Change:

```dotenv
MINIO_ROOT_PASSWORD="<MINIO_PASSWORD>"
```

### 4.2 Run on Node 41

Enter the MinIO command directory:

```bash
cd /aifar/apps/minio/bin
```

Update the local alias:

```bash
MC_CONFIG_DIR=/aifar/apps/minio/conf/mc \
  ./mc alias set \
  aifar-local \
  http://127.0.0.1:9000 \
  "admin" \
  "<MINIO_PASSWORD>" \
  --api S3v4
```

Update the peer alias. On node 41, the peer points to node 42:

```bash
MC_CONFIG_DIR=/aifar/apps/minio/conf/mc \
  ./mc alias set \
  aifar-peer \
  http://<MINIO_42_IP>:9000 \
  "admin" \
  "<MINIO_PASSWORD>" \
  --api S3v4
```

Restart MinIO:

```bash
systemctl restart aifar-minio
```

> Field note: After `systemctl is-active aifar-minio` returns `active`, port 9000 may still need a short time before it accepts requests. If `mc replicate ls` below reports `127.0.0.1:9000 connection refused`, wait for the port to become ready and retry. For example, confirm that `ss -ltn | grep ':9000'` shows a listener.

Set the `mc` path:

```bash
MC=/aifar/apps/minio/bin/mc
CFG=/aifar/apps/minio/conf/mc
```

List the replication rules on node 41 and record the rule ID:

```bash
"$MC" --config-dir "$CFG" replicate ls aifar-local/aifar
```

Update the replication rule from node 41 to node 42:

```bash
"$MC" --config-dir "$CFG" replicate update \
  aifar-local/aifar \
  --id "<41_RULE_ID>" \
  --remote-bucket \
  "http://admin:<MINIO_PASSWORD>@<MINIO_42_IP>:9000/aifar"
```

### 4.3 Run on Node 42

Enter the MinIO command directory:

```bash
cd /aifar/apps/minio/bin
```

Update the local alias:

```bash
MC_CONFIG_DIR=/aifar/apps/minio/conf/mc \
  ./mc alias set \
  aifar-local \
  http://127.0.0.1:9000 \
  "admin" \
  "<MINIO_PASSWORD>" \
  --api S3v4
```

Update the peer alias. On node 42, the peer points to node 41:

```bash
MC_CONFIG_DIR=/aifar/apps/minio/conf/mc \
  ./mc alias set \
  aifar-peer \
  http://<MINIO_41_IP>:9000 \
  "admin" \
  "<MINIO_PASSWORD>" \
  --api S3v4
```

Restart MinIO:

```bash
systemctl restart aifar-minio
```

> Field note: After `systemctl is-active aifar-minio` returns `active`, port 9000 may still need a short time before it accepts requests. If `mc replicate ls` below reports `127.0.0.1:9000 connection refused`, wait for the port to become ready and retry. For example, confirm that `ss -ltn | grep ':9000'` shows a listener.

Set the `mc` path:

```bash
MC=/aifar/apps/minio/bin/mc
CFG=/aifar/apps/minio/conf/mc
```

List the replication rules on node 42 and record the rule ID:

```bash
"$MC" --config-dir "$CFG" replicate ls aifar-local/aifar
```

Update the replication rule from node 42 to node 41:

```bash
"$MC" --config-dir "$CFG" replicate update \
  aifar-local/aifar \
  --id "<42_RULE_ID>" \
  --remote-bucket \
  "http://admin:<MINIO_PASSWORD>@<MINIO_41_IP>:9000/aifar"
```

### 4.4 Verify Bidirectional Synchronization

Upload a file from the MinIO page, then check synchronization results on both node 41 and node 42.

## 5. Redis

The Redis cluster is deployed on node 41, node 42, and the arbiter node. Redis data service and Redis Sentinel must use the same new password on all three nodes.

### 5.1 Stop Redis and Sentinel

Run the following commands separately on node 41, node 42, and the arbiter node:

```bash
systemctl stop aifar-redis
systemctl stop aifar-redis-sentinel
```

### 5.2 Update Redis Configuration

Run the following commands separately on node 41, node 42, and the arbiter node:

```bash
cd /aifar/apps/redis/conf
vi redis.conf
```

Change:

```conf
requirepass <REDIS_PASSWORD>
masterauth <REDIS_PASSWORD>
```

### 5.3 Update Sentinel Configuration

Run the following command separately on node 41, node 42, and the arbiter node:

```bash
vi sentinel.conf
```

Change:

```conf
user default on sanitize-payload ><REDIS_PASSWORD> ~* &* +@all

sentinel auth-pass aifar-master <REDIS_PASSWORD>
sentinel sentinel-pass <REDIS_PASSWORD>
```

The `>` in the `user default` line is part of the Redis ACL password rule. After replacement, the value must be `>actual-password`, with no space between `>` and the password.

## 6. Nacos

The Nacos cluster is deployed on node 41, node 42, and the arbiter node.

### 6.1 Update the MySQL Password Used by Nacos

Run the following command separately on node 41, node 42, and the arbiter node:

```bash
vi /aifar/apps/nacos/nacos/conf/application.properties
```

Change:

```properties
db.password.0=<MYSQL_PASSWORD>
```

Restart Nacos:

```bash
systemctl restart aifar-nacos
```

### 6.2 Change the Nacos Login Password

Open the Nacos Web page and change the Nacos login password.

After the password is changed, also update the Nacos username and password configuration used by the applications.

If you cannot log in to the Nacos Web page but can connect to the Nacos backend database with the MySQL root account, use Nacos's own `PasswordEncoderUtil` to generate a compatible password hash and update the `users` table. The following is an administrator fallback procedure. Before running it, confirm the actual Nacos backend database name and user table. In this environment, the database name is `aifar_nacos`.

Prepare a temporary tool on any Nacos/MySQL node:

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

Generate the new password hash and update the Nacos user password:

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

Restart Nacos on all three nodes, then log in to the Web page again to verify:

```bash
systemctl restart aifar-nacos
```

### 6.3 Update Business Configuration in Nacos

Open the Nacos Web page and update the following Data IDs under the corresponding namespace and group:

- `datasource.yaml`
- `resources.yaml`

Synchronize the MySQL cluster and Redis cluster passwords in those files.

> Field note: Update only the password fields that actually exist in the configuration. Do not add missing fields just for this rotation. If `resources.yaml` does not contain MySQL or Redis password fields, do not add new password fields just for this procedure.

## 7. AIFAR Runtime Servers

Application services and AIFAR Runtime are deployed on node 31 and node 32. Update the following file separately on both application servers:

```bash
vi /aifar/apps/admin/runtime/env/java-secrets.env
```

Change:

```dotenv
NACOS_PASSWORD=<NACOS_PASSWORD>
```

> Field note: Updating `java-secrets.env` only changes the configuration file used by newly created containers. Existing Docker containers do not automatically receive new environment variables. After this file is updated, redeploy or rebuild the application containers through AIFAR Runtime/Agent. Changing the file or running `docker restart` alone will not change the environment variables of existing containers.

## 8. Completion Checklist

- [ ] MySQL can log in with the new password.
- [ ] MySQL, Redis, and Nacos operations have covered node 41, node 42, and the arbiter node.
- [ ] On node 41, `aifar-peer` points to node 42.
- [ ] On node 42, `aifar-peer` points to node 41.
- [ ] After uploading a file from the MinIO page, bidirectional synchronization works correctly.
- [ ] Redis and Sentinel on node 41, node 42, and the arbiter node use the same new password.
- [ ] Nacos `db.password.0` has been updated and Nacos has been restarted on node 41, node 42, and the arbiter node.
- [ ] The Nacos login password has been changed.
- [ ] MySQL and Redis passwords have been updated in `datasource.yaml` and `resources.yaml`.
- [ ] `java-secrets.env` has been updated on application nodes 31 and 32.
- [ ] Application containers have been redeployed or rebuilt through AIFAR Runtime/Agent, and the running containers have been confirmed to use the new Nacos password.
