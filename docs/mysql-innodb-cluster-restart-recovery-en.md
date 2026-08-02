# MySQL InnoDB Cluster Three-Node Restart Recovery

This guide is written for an operator who is not familiar with MySQL InnoDB Cluster. Follow only the section that matches the current cluster condition.

> **Important:** Commands containing `force`, `forceQuorumUsingPartitionOf()`, or `recoveryMethod: 'clone'` can cause data loss or split brain if they are used in the wrong situation. Do not use them unless the conditions in the relevant section have been confirmed.

## 1. Parameters and Placeholders

Replace every uppercase placeholder in the commands with the actual value. Do not type the placeholder name itself.

| Placeholder | Meaning | Example |
| --- | --- | --- |
| `CLUSTER_ADMIN` | The MySQL account used to administer the InnoDB Cluster. This is a MySQL account, not the Linux SSH account. | `root` or `icadmin` |
| `CLUSTER_NAME` | The InnoDB Cluster name stored in MySQL metadata. | `aifarCluster` |
| `NODE1_IP` | IP address of the first MySQL member. It is only a node label; it does not always mean PRIMARY. | `192.168.74.133` |
| `NODE2_IP` | IP address of the second MySQL member. | `192.168.74.134` |
| `NODE3_IP` | IP address of the third MySQL member. | `192.168.74.137` |
| `MYSQL_PORT` | MySQL Classic Protocol port used by MySQL Shell. | `3306` |
| `SEED_IP` | The node selected to restart the cluster after complete outage validation. Use the node reported by `dryRun`; if no different seed is reported, use the node to which MySQL Shell is currently connected. | `192.168.74.133` |
| `TRUSTED_NODE_IP` | The ONLINE node in the only surviving, authoritative partition when the cluster has lost quorum. It is used only for quorum-loss recovery. | `192.168.74.133` |
| `FAILED_NODE_IP` | The member that cannot rejoin the already recovered cluster. Its local data will be overwritten when Clone recovery is used. | `192.168.74.137` |

Values known for the current three-node environment are:

```text
CLUSTER_NAME = aifarCluster
NODE1_IP     = 192.168.74.133
NODE2_IP     = 192.168.74.134
NODE3_IP     = 192.168.74.137
MYSQL_PORT   = 3306
```

Confirm the `CLUSTER_ADMIN` account with the system administrator. The password is entered interactively and must not be written into this document or added to the command line.

The bundled MySQL Shell executable is located at:

```text
/aifar/apps/mysql/mysql-shell/bin/mysqlsh
```

### Key Terms in Plain Language

| Term | Meaning |
| --- | --- |
| PRIMARY | The member that accepts write operations. A single-primary cluster must have exactly one PRIMARY. |
| SECONDARY | A read-only member that receives replicated data from the cluster. This three-node cluster should have two SECONDARY members. |
| ONLINE | The member is active in Group Replication and participating in the cluster. |
| Quorum | The majority of members required to make safe cluster decisions. A three-node cluster normally needs at least two communicating members. |
| GTID | A transaction identifier used to compare which transactions exist on each node. Divergent GTID sets can indicate conflicting data histories. |
| Seed | The validated member used to restart Group Replication after a complete outage. |
| Metadata | Internal information used by MySQL Shell to remember the cluster name, members, and topology. |
| Clone recovery | A recovery method that deletes the target member's existing MySQL data and replaces it with a physical copy from a healthy donor. |
| Split brain | A dangerous condition where separate partitions both operate as if they are the valid cluster. |

## 2. Select the Correct Recovery Scenario

If the current condition is unknown, run the companion read-only script [mysql-cluster-status.sh](mysql-cluster-status.sh) first. The script does not start, stop, or modify the cluster.

| Current condition | Section to use |
| --- | --- |
| All three MySQL servers are reachable, but no cluster member is `ONLINE` | Section 3: Complete outage recovery |
| At least one member is `ONLINE`, but the cluster cannot obtain a majority quorum | Section 4: Quorum-loss recovery |
| The cluster has already recovered, but one member cannot rejoin | Section 5: Replace one failed member using Clone |
| You cannot determine which condition applies | Stop and contact the DBA; do not try force commands |

## 3. Complete Outage Recovery

Use this section when Group Replication has stopped on all three members. All three MySQL servers must be started and reachable before running the recovery command.

### Step 1: Start MySQL Shell and Connect to Any Node

**Purpose:** Open MySQL Shell in JavaScript mode and connect to one cluster member.

Run this command in the Linux terminal. The example connects to `NODE1_IP`:

```bash
/aifar/apps/mysql/mysql-shell/bin/mysqlsh --js --uri CLUSTER_ADMIN@NODE1_IP:MYSQL_PORT
```

Example using the current node address and the account `root`:

```bash
/aifar/apps/mysql/mysql-shell/bin/mysqlsh --js --uri root@192.168.74.133:3306
```

MySQL Shell prompts for the MySQL password. Type the password and press Enter. The password is not displayed while typing.

**Success result:** The prompt changes to `mysql-js>` and no connection error is displayed.

**Stop if:** The output contains `Access denied`, `Can't connect`, a timeout, or an SSL/authentication error. Correct the connection or account problem before continuing.

### Step 2: Perform a Read-Only Dry Run

**Purpose:** Validate whether complete-outage recovery is safe and determine the seed node without changing the cluster.

Run this at the `mysql-js>` prompt:

```javascript
dba.rebootClusterFromCompleteOutage('aifarCluster', {dryRun: true})
```

If the actual cluster name is different, replace `aifarCluster` with `CLUSTER_NAME`.

**How to determine `SEED_IP`:**

- If the output contains `Switching over to instance 'IP:PORT' to be used as seed`, use that IP as `SEED_IP`.
- If the dry run finishes successfully and does not report a different seed, use the IP of the node to which MySQL Shell is currently connected.
- `SEED_IP` is not automatically the old PRIMARY. It is the validated node used to restart the cluster.

**Success result:** The output states that `dryRun` validations completed or contains `dryRun finished`, with no exception.

**Stop if:** The dry run reports an unreachable member, GTID conflict/divergence, missing metadata, or any exception. Do not retry with `force:true`.

### Step 3: Connect to the Validated Seed Node

**Purpose:** Ensure the real recovery command runs from the node approved by the dry run.

Run this inside MySQL Shell, replacing `SEED_IP`, `CLUSTER_ADMIN`, and `MYSQL_PORT`:

```javascript
\connect CLUSTER_ADMIN@SEED_IP:MYSQL_PORT
```

Example:

```javascript
\connect root@192.168.74.133:3306
```

Enter the MySQL password when prompted.

**Success result:** MySQL Shell reports that the session is connected to `SEED_IP:MYSQL_PORT`.

**Stop if:** The connection fails or connects to a different address than the validated seed.

### Step 4: Restart the Cluster

**Purpose:** Perform the actual complete-outage recovery. This command changes cluster state.

Run:

```javascript
var cluster = dba.rebootClusterFromCompleteOutage('aifarCluster')
```

**Success result:** MySQL Shell reports that the cluster was successfully rebooted and returns a `cluster` object.

**Stop if:** The command reports GTID divergence, an unreachable member, metadata errors, or any failed validation. Do not add `{force: true}`.

### Step 5: Check Which Members Are ONLINE

**Purpose:** Identify whether any member still needs to be rejoined.

Run:

```javascript
cluster.status({extended: 2})
```

Look under `defaultReplicaSet.topology`. Each node has a `status` and `memberRole`.

- If a node is already `ONLINE`, do not run `rejoinInstance()` for that node.
- If a node is `OFFLINE`, `MISSING`, or not listed, continue to Step 6 for that node.

### Step 6: Rejoin Only the Members That Are Not ONLINE

**Purpose:** Return the remaining members to the recovered cluster.

Example commands for Node 2 and Node 3 are shown below. Run only the command for a node that is not `ONLINE`:

```javascript
cluster.rejoinInstance('CLUSTER_ADMIN@NODE2_IP:MYSQL_PORT')
cluster.rejoinInstance('CLUSTER_ADMIN@NODE3_IP:MYSQL_PORT')
```

Example:

```javascript
cluster.rejoinInstance('root@192.168.74.134:3306')
cluster.rejoinInstance('root@192.168.74.137:3306')
```

Enter the MySQL password when prompted.

**Success result:** MySQL Shell reports that the instance was successfully rejoined or the member changes to `ONLINE`.

**Stop if:** The output reports errant transactions, GTID divergence, missing recovery transactions, Clone requirements, or repeated authentication/network errors. Continue with Section 5 only after DBA approval.

### Step 7: Perform the Final Status Check

Run:

```javascript
cluster.status({extended: 2})
```

Recovery is complete only when all of the following are true:

- The overall cluster status is `OK`.
- Exactly one member has `memberRole: PRIMARY`.
- Exactly two members have `memberRole: SECONDARY`.
- All three members have `status: ONLINE`.

If any condition is not met, the recovery is not complete.

## 4. Quorum-Loss Recovery

Use this section only when at least one member is still `ONLINE`, but the cluster has lost the majority required for quorum.

> **Split-brain warning:** `forceQuorumUsingPartitionOf()` is a last-resort command. Before running it, confirm that no other cluster partition is active anywhere else on the network. If another active partition exists, stop immediately.

### Step 1: Identify the Trusted Node

`TRUSTED_NODE_IP` must be an `ONLINE` member in the only surviving authoritative partition. It must contain the cluster metadata and must be reachable from MySQL Shell.

Do not choose a node only because it was the previous PRIMARY or because it started first.

### Step 2: Connect to the Trusted Node

Run in the Linux terminal:

```bash
/aifar/apps/mysql/mysql-shell/bin/mysqlsh --js --uri CLUSTER_ADMIN@TRUSTED_NODE_IP:MYSQL_PORT
```

Enter the MySQL password when prompted.

**Success result:** The prompt changes to `mysql-js>`.

### Step 3: Load the Cluster Object

Run:

```javascript
var cluster = dba.getCluster('aifarCluster')
```

**Success result:** No exception is displayed and the `cluster` variable is created.

### Step 4: Restore Quorum from the Trusted Partition

Run only after the split-brain warning has been checked:

```javascript
cluster.forceQuorumUsingPartitionOf('CLUSTER_ADMIN@TRUSTED_NODE_IP:MYSQL_PORT')
```

Example:

```javascript
cluster.forceQuorumUsingPartitionOf('root@192.168.74.133:3306')
```

**Success result:** MySQL Shell reports that the InnoDB Cluster was successfully restored using the trusted partition.

**Stop if:** You discover another active partition, the trusted node is not `ONLINE`, or MySQL Shell reports metadata/GTID errors.

### Step 5: Rejoin Missing Members and Verify

Run `cluster.status({extended: 2})`, then rejoin only members that are not `ONLINE`:

```javascript
cluster.status({extended: 2})
cluster.rejoinInstance('CLUSTER_ADMIN@NODE2_IP:MYSQL_PORT')
cluster.rejoinInstance('CLUSTER_ADMIN@NODE3_IP:MYSQL_PORT')
cluster.status({extended: 2})
```

The final state must contain one PRIMARY, two SECONDARY members, and all three members `ONLINE`.

## 5. Replace One Failed Member Using Clone

Use this section only when the cluster is already operational but one member cannot rejoin.

> **Data-loss warning:** Clone recovery completely overwrites the MySQL data on `FAILED_NODE_IP` with a physical snapshot from a healthy cluster member. Obtain explicit approval before continuing.

### Step 1: Confirm the Failed Node

Run:

```javascript
cluster.status({extended: 2})
```

Set `FAILED_NODE_IP` to the address of the member that is not `ONLINE`. Confirm that the healthy cluster has an ONLINE PRIMARY and that the local data on the failed node may be deleted.

### Step 2: Remove the Failed Member from Cluster Metadata

Run:

```javascript
cluster.removeInstance('CLUSTER_ADMIN@FAILED_NODE_IP:MYSQL_PORT', {force: true})
```

Example:

```javascript
cluster.removeInstance('root@192.168.74.137:3306', {force: true})
```

**Success result:** The failed member is no longer listed as a cluster member.

**Stop if:** `FAILED_NODE_IP` is the current PRIMARY, more than one member is unhealthy, or you do not have approval to discard the failed node's local data.

### Step 3: Add the Member Back Using Clone Recovery

Run:

```javascript
cluster.addInstance(
  'CLUSTER_ADMIN@FAILED_NODE_IP:MYSQL_PORT',
  {recoveryMethod: 'clone'}
)
```

Example:

```javascript
cluster.addInstance(
  'root@192.168.74.137:3306',
  {recoveryMethod: 'clone'}
)
```

Enter the password when prompted. MySQL Shell may restart the failed MySQL instance during Clone recovery.

**Success result:** Clone finishes, the instance rejoins the cluster, and its status becomes `ONLINE`.

**Stop if:** Clone fails, the donor is unhealthy, the node cannot restart, or the node remains outside the cluster.

### Step 4: Verify the Final Cluster State

Run:

```javascript
cluster.status({extended: 2})
```

The final state must show one PRIMARY, two SECONDARY members, all three members `ONLINE`, and overall status `OK`.

## 6. Stop and Escalate to the DBA

Stop immediately and do not use `force:true` if any of the following occurs:

- The GTID sets have diverged.
- MySQL Shell reports errant transactions or lost transactions.
- The authoritative or most up-to-date node cannot be identified.
- InnoDB Cluster metadata is missing or severely damaged.
- More than one partition might still be active.
- The `dryRun` validation fails.
- More than one node requires destructive recovery.
- A valid backup might be required to rebuild the cluster.

The DBA must identify the authoritative data source and decide whether the cluster must be rebuilt from a valid backup.

## 7. Official Documentation

- [Rebooting a Cluster from a Major Outage](https://dev.mysql.com/doc/mysql-shell/8.0/en/reboot-outage.html)
- [Rejoining a Cluster](https://dev.mysql.com/doc/mysql-shell/8.0/en/rejoin-cluster.html)
- [Restoring a Cluster from Quorum Loss](https://dev.mysql.com/doc/mysql-shell/8.0/en/restore-cluster-from-quorum-loss.html)
- [Adding Instances Using Clone Recovery](https://dev.mysql.com/doc/mysql-shell/8.0/en/add-instances-cluster.html)
- [Monitoring an InnoDB Cluster](https://dev.mysql.com/doc/mysql-shell/8.0/en/monitoring-innodb-cluster.html)
