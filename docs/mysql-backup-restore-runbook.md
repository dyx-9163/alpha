# MySQL backup and restore operator runbook

This runbook covers AIFAR logical full backup, verification, restore, complete-outage start, and disaster rebuild for MySQL 8.0.36 standalone instances and three-member InnoDB Clusters. It is written for panel owners, application operators, database administrators, and storage administrators.

## 1. Safety boundary and responsibilities

- A backup uses MySQL Shell `util.dumpInstance()` with `consistent:true`. It contains business schemas, routines, events, and triggers, but excludes MySQL system schemas, InnoDB Cluster metadata, and users.
- This release's cluster backup, restore, maintenance-clear, reconciliation, complete-outage-start, and disaster-rebuild workflows support exactly three authoritative InnoDB Cluster members. MySQL installation still accepts three or more data nodes; a larger installed cluster is deliberately rejected by these recovery workflows until its full authoritative membership can be handled end to end.
- A restore is destructive: AIFAR drops only the business schemas named by a verified manifest and then loads the dump. It is not a merge and it is not package rollback.
- Backup may run online. Restore, maintenance recovery, and disaster rebuild require an approved maintenance window with all application writes stopped.
- The AIFAR maintenance marker blocks AIFAR lifecycle requests only. It does not stop Java services, Router clients, direct MySQL sessions, scheduled jobs, or other external writers. The operator owns external traffic isolation for the entire window.
- Only an owner may start restore, disaster rebuild, maintenance clear, or reconciliation. Backup and verification require `apps.manage`; complete-outage start requires `database.manage`.
- After installation, MySQL status checks, PRIMARY detection, cluster start, backup, restore, maintenance clear, and reconciliation require the current unique active credential bound to the instance for `purpose=admin`, with a complete username and password. They do not fall back to `AIFAR_DEFAULT_PASSWORD`.
- MySQL credentials travel only through task-scoped `0600` regular-file contexts. The target validates path containment, owner, type, mode, and non-symlink status before use. A state-changing task independently attempts bounded cleanup on success, failure, and cancellation; cleanup failure makes the task fail. A backup archive already committed before a final cleanup failure remains a usable `success` recovery-point record, but its worker task is failed and the residue must be remediated before the result is accepted.
- Do not put a real access token, SSH password, MySQL password, private key, full connection string, or sensitive task output in a ticket, screenshot, shell history, or acceptance record.

The persistent backup repository must not exist only on a MySQL node. A node-level failure must not be able to remove both the database and its recovery point.

## 2. Configure the backup repository

The MySQL repository is separate from the panel SQLite backup directory:

```env
AIFAR_MYSQL_BACKUP_DIR=<mysqlBackupRepository>
AIFAR_MYSQL_BACKUP_KEEP_LAST=5
```

`AIFAR_MYSQL_BACKUP_DIR` may be a dedicated locally mounted disk or a NAS mount. It must provide exclusive file locking and same-filesystem atomic rename semantics. Do not use object-storage mounts, non-atomic synchronization folders, or a share whose locking behavior has not been validated.

On Linux, the repository root must be a real directory, owned by the exact AIFAR service account, and have mode `0700`. AIFAR creates `.aifar-repository.lock` as a regular `0600` file. Other processes, scripts, and tools running as the same service user must not write in the repository.

Replace every angle-bracket placeholder before running this example:

```bash
export AIFAR_SERVICE_USER='<serviceUser>'
export AIFAR_SERVICE_GROUP='<serviceGroup>'
export AIFAR_MYSQL_BACKUP_DIR='<mysqlBackupRepository>'

sudo install -d -o "$AIFAR_SERVICE_USER" -g "$AIFAR_SERVICE_GROUP" -m 0700 "$AIFAR_MYSQL_BACKUP_DIR"
sudo -u "$AIFAR_SERVICE_USER" test -d "$AIFAR_MYSQL_BACKUP_DIR"
sudo -u "$AIFAR_SERVICE_USER" test -r "$AIFAR_MYSQL_BACKUP_DIR"
sudo -u "$AIFAR_SERVICE_USER" test -w "$AIFAR_MYSQL_BACKUP_DIR"
stat -c '%U:%G %a %F' "$AIFAR_MYSQL_BACKUP_DIR"
df -Pk "$AIFAR_MYSQL_BACKUP_DIR"
```

The final `stat` result must identify the configured service owner, mode `700`, and a directory. Configure the environment, restart `<aifarService>`, and recheck the service log. Do not weaken permissions to make startup pass. If an administrator must inspect repository files, stop AIFAR first and use a read-only, audited procedure; direct online editing is unsupported.

For NAS use, validate these behaviors before production:

1. Two AIFAR processes cannot both acquire the repository lock.
2. Rename within the repository is atomic.
3. Ownership and `0700`/`0600` modes survive remount and failover.
4. A transient disconnect fails operations closed rather than publishing a partial backup.
5. The mount is available before the AIFAR service starts.

## 3. UI workflow

Open **Database**, then locate the standalone MySQL card or the InnoDB Cluster group card.

1. Select **Back up now**. Enter a non-sensitive name, threads, optional per-thread rate limit, and retention count. Submit and follow the task in Task Center.
2. Select **Backup records** to review status, topology, version, schemas, size, checksum, and task identity.
3. Select **Verify backup** before every planned restore and after copying or storage maintenance. Verification is a worker task; wait for terminal success.
4. Select **Restore data** only after external writers are stopped. Review the target, checksum, schema impact, topology, version, and pre-restore-backup option. Keep the window in force until the task and post-restore checks finish.
5. For a valid reconciliation marker, the page shows the affected instance, original `local_infile` value, source task, and record time. An owner selects **Run reconciliation**. This does not clear the maintenance banner; wait for a fresh page state confirming reconciliation is absent before maintenance clear becomes available.
6. For a valid incomplete-restore marker, the page shows a non-dismissible maintenance banner and disables ordinary check, cluster start, instance delete/uninstall, backup, and restore actions. Owners may use the recovery actions described below.
7. **Disaster rebuild** appears only for an eligible cluster maintenance state. It is never a shortcut for an ordinary healthy-cluster restore.

The UI submits a task and tracks it. A submit acknowledgement is not proof that backup or restore succeeded; use Task Center and the backup record's terminal state.

The **Verify backup** action is available only for successful `logical-full` records. A protective `pre-restore` record is retained as recovery evidence for its originating restore workflow, but it is not selectable through the ordinary Verify or Restore UI/API entry points.

## 4. API conventions and task evidence

The examples below are non-secret templates. Replace placeholders locally. Keep the access token in a protected process environment and do not paste it into an acceptance report.

```bash
export PANEL_URL='<panelBaseUrl>'
IFS= read -r -s -p 'AIFAR access token: ' ACCESS_TOKEN
printf '\n'
AUTH_HEADER_FILE="$(mktemp)"
chmod 0600 "$AUTH_HEADER_FILE"
printf 'Authorization: Bearer %s\n' "$ACCESS_TOKEN" >"$AUTH_HEADER_FILE"
unset ACCESS_TOKEN
trap 'rm -f -- "$AUTH_HEADER_FILE"' EXIT
trap 'exit 130' HUP INT TERM
```

Do not replace an access-token placeholder in a recorded command. The silent prompt keeps the token out of shell history; the protected header file keeps it out of the `curl` argument vector. Confirm `mktemp` created a file owned by the current operator with mode `0600`, keep the trap active for the session, and remove the file immediately if the shell does not exit normally.

Every state-changing request returns a task ID except controlled backup deletion, which returns the updated backup record. For worker actions, record the returned task ID and inspect:

```bash
curl -fsS "$PANEL_URL/api/v2/tasks/<taskId>" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'X-AIFAR-Language: en'
```

Expected task types are:

| Operation | Task or audit type |
| --- | --- |
| Backup | `apps.mysql.backup` |
| Verify | `apps.mysql.backup.verify` |
| Restore or disaster rebuild | `apps.mysql.restore` |
| Reconcile `local_infile` | `apps.mysql.reconciliation.run` |
| Maintenance clear | `apps.mysql.maintenance.clear` |
| Complete-outage start | `database.mysql.cluster.start` |
| Controlled delete | audit action `apps.mysql.backup.delete` |

Valid backup record states are `pending`, `running`, `success`, `failed`, and `deleted`. Backup types are `logical-full` and the automatically created protective `pre-restore` backup. A restore record phase may become `restore_incomplete`; do not treat that as a usable success signal.

Task Center persists these ordered machine step names. Use them when comparing a task plan with runtime evidence; translated titles may differ:

| Operation | Ordered machine step names |
| --- | --- |
| Backup, standalone or cluster | `load-instance`, `acquire-instance-lock`, `resolve-credential`, `inspect-mysql`, `check-backup-space`, `prepare-workdir`, `dry-run-dump`, `dump-instance`, `build-manifest`, `package-backup`, `transfer-backup`, `verify-checksum`, `record-backup`, `apply-retention`, `cleanup-workdir` |
| Verify | `load-backup`, `verify-manifest`, `verify-checksum`, `record-verification` |
| Standalone or healthy-cluster restore | `load-backup`, `acquire-instance-lock`, `verify-maintenance-confirmation`, `verify-manifest`, `verify-checksum`, `verify-version`, `create-pre-restore-backup`, `upload-backup`, `extract-backup`, `dry-run-load`, `capture-local-infile`, `enable-local-infile`, `drop-target-schemas`, `load-dump`, `restore-local-infile`, `verify-schemas`, `verify-data`, `cleanup-workdir`, `record-restore`, `release-lock` |
| Disaster rebuild | `stop-router`, `stop-group-replication`, `quarantine-old-data`, `initialize-clean-seed`, `restore-seed`, `verify-seed`, `create-cluster`, `clone-members`, `wait-members-online`, `bootstrap-router`, `verify-router-6446`, `record-completion` |
| Complete-outage start | `load-cluster`, `start-cluster`, `detect-primary`, `update-instance` |
| Reconcile `local_infile` | `reconcile-local-infile` |
| Maintenance clear | `clear-maintenance` |

## 5. Standalone backup, verify, and restore

### Create a logical full backup

```bash
curl -fsS -X POST "$PANEL_URL/api/v2/apps/instances/<instanceId>/backup" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'Content-Type: application/json' \
  -H 'X-AIFAR-Language: en' \
  -d '{"name":"scheduled-full","threads":4,"maxRateMBps":0,"keepLast":5}'
```

Wait for `apps.mysql.backup` to finish. A successful backup is published only after the archive is transferred to the panel repository, size and SHA-256 are verified, and the final files are committed. The MySQL node's task work directory is temporary and is not a recovery repository.

### List and verify backups

```bash
curl -fsS "$PANEL_URL/api/v2/apps/instances/<instanceId>/backups" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'X-AIFAR-Language: en'

curl -fsS -X POST "$PANEL_URL/api/v2/apps/backups/<backupId>/verify" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'Content-Type: application/json' \
  -H 'X-AIFAR-Language: en' \
  -d '{}'
```

Record the verification task ID, terminal status, backup ID, archive checksum, manifest version, and verification timestamp. Do not record repository paths if an acceptance artifact may leave the operations team.

### Restore standalone data

Preconditions:

- The selected backup is a verified `logical-full` manifest v2 owned by the same standalone instance and server.
- Source and target MySQL versions are compatible; this release expects the same full version.
- External application writes are stopped and monitored.
- The target is readable if `createPreRestoreBackup` is `true`. Keep this protective default unless the approved recovery procedure explicitly allows skipping it.

```bash
curl -fsS -X POST "$PANEL_URL/api/v2/apps/instances/<instanceId>/restore" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'Content-Type: application/json' \
  -H 'X-AIFAR-Language: en' \
  -d '{"backupId":"<backupId>","mode":"standalone","maintenanceConfirmed":true,"createPreRestoreBackup":true,"disasterConfirmed":false,"threads":4}'
```

Keep external writes stopped until all of these are true:

- the restore task is terminal `success`;
- the backup restore phase reached `verified`;
- MySQL ping succeeded;
- the exact business schema/base-table catalog matches manifest v2;
- the current task ID and canonical manifest SHA-256 match the restore record;
- `local_infile` equals its captured pre-restore value;
- no maintenance or reconciliation marker remains;
- application smoke tests pass through the normal connection path.

## 6. Healthy InnoDB Cluster backup and restore

For backup, call the same backup endpoint with any current authoritative cluster-member instance ID. AIFAR expands the three-member cluster, requires exactly three `ONLINE` members and one `PRIMARY`, connects directly to the current PRIMARY on MySQL port 3306, and generates one cluster-level backup. It does not dump each member and does not dump through Router.

Cluster backup verification takes the raw `clusterId` mutation lock and then rereads the backup record, representative instance, `app_clusters`, all `app_cluster_members`, all referenced instances, and all referenced server rows. It publishes verification success only when the authoritative closure still contains exactly three unique MySQL instances on three existing servers with non-empty hosts and the same non-empty `clusterId`, including the backup owner. A membership or server-record change after request admission therefore fails verification instead of validating a stale cluster snapshot.

```bash
curl -fsS -X POST "$PANEL_URL/api/v2/apps/instances/<representativeInstanceId>/backup" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'Content-Type: application/json' \
  -H 'X-AIFAR-Language: en' \
  -d '{"name":"cluster-full","threads":4,"maxRateMBps":0,"keepLast":5}'
```

For a healthy restore, stop external writes but leave Group Replication running. Verify the backup, then use API mode `innodb-cluster`:

```bash
curl -fsS -X POST "$PANEL_URL/api/v2/apps/instances/<representativeInstanceId>/restore" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'Content-Type: application/json' \
  -H 'X-AIFAR-Language: en' \
  -d '{"backupId":"<backupId>","mode":"innodb-cluster","maintenanceConfirmed":true,"createPreRestoreBackup":true,"disasterConfirmed":false,"threads":4}'
```

AIFAR loads only the current PRIMARY with `skipBinlog:false`; Group Replication distributes the restore. A PRIMARY change or connection loss during mutation causes failure and retention of the maintenance marker. AIFAR does not continue writing to a newly elected PRIMARY. Success additionally requires three `ONLINE` members, one PRIMARY, exact schema/base-table catalog agreement, at least one authoritative Router record, and a real read/write transaction through every recorded Router endpoint. This release does not inspect an applier-queue metric; record catch-up evidence separately before reopening traffic.

## 7. Choose the correct cluster recovery path

| Situation | Correct operation | Data behavior |
| --- | --- | --- |
| Cluster healthy; business data must be rolled back | Healthy cluster restore | Drop/load business schemas once on current PRIMARY; Group Replication remains active. |
| All Group Replication members stopped; data directories remain intact | Complete-outage start | Use `dba.rebootClusterFromCompleteOutage()` from the GTID-superset member. No dump load, schema drop, quarantine, or rebuild. |
| Data directories are damaged and complete-outage start is not viable | Disaster rebuild | Restore a clean seed, recreate cluster, clone two secondaries, and bootstrap Router. Destructive and owner-only. |

### Complete-outage start

This endpoint accepts the three authoritative MySQL instance IDs. It is not a backup-restore endpoint:

```bash
curl -fsS -X POST "$PANEL_URL/api/v2/database/mysql/clusters/start" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'Content-Type: application/json' \
  -H 'X-AIFAR-Language: en' \
  -d '{"instanceIds":["<instanceIdA>","<instanceIdB>","<instanceIdC>"]}'
```

All three members must be running and reachable. AIFAR reads each member's non-empty `@@GLOBAL.gtid_executed`, evaluates `GTID_SUBSET`, and proceeds only when exactly one member is a superset of both others. It connects to that seed, executes `dba.rebootClusterFromCompleteOutage(clusterName, {dryRun:true})`, and invokes the mutating reboot only after the dry run succeeds. Equal sets with no unique seed, divergent histories, an empty GTID set, an unreachable member, or dry-run rejection are stop-and-escalate conditions; the workflow never uses `force:true`.

Do not use complete-outage start when a valid maintenance marker indicates an incomplete destructive restore. Ordinary start is intentionally blocked by that marker.

### Disaster rebuild

Disaster rebuild is permitted only for an InnoDB Cluster whose three authoritative members have the same valid cluster maintenance marker and whose marker names the selected verified manifest v2 backup. Keep applications and Router stopped. Confirm the exact instance-to-server mapping and enter each target server's saved SSH password only in the panel's owner-only **Disaster rebuild** dialog. This runbook intentionally provides no copyable CLI request for this operation because its transient `serverPasswords` payload must not enter shell history or the process argument vector. If API automation is required, use an organization-approved secret runner that sends a protected `0600` request file over stdin and deterministically removes it; never substitute secrets into `curl -d`.

The workflow quarantines old data directories by task identity, restores a clean seed, calls `dba.createCluster()`, adds B and C with clone recovery, bootstraps Router, verifies Router 6446, and atomically updates the control plane. A failure preserves quarantine and the maintenance marker, keeps Router stopped, and must not be reported as success.

## 8. Manifest v2: repository verification versus restore verification

New ordinary and pre-restore backups use manifest v2. The ordinary **Verify backup** worker accepts only a successful `logical-full` record. Without connecting to MySQL, it proves:

- the MySQL Shell 8.0.36 `@.done.json` completion contract;
- a closed, unambiguous graph of `@.json`, schema metadata, and base-table metadata;
- the complete sorted inventory of regular dump files, each file's size and SHA-256, and the `sha256-nul-records-v1` inventory digest;
- exact business schema and base-table names and counts;
- identity between the managed backup record, repository path, archive, manifest, checksum, and size;
- for a cluster backup, a still-authoritative locked closure of the backup owner, exact three members, instances, and server rows.

A successful restore additionally proves:

- successful controlled `util.loadDump()` return persisted as `load_complete`;
- MySQL ping after load;
- exact restored business schema/base-table catalog agreement with manifest v2;
- identity between the current restore task and the canonical manifest SHA-256.

Restore work-directory and task-scoped credential cleanup completes before the `record-restore` step clears reconciliation and maintenance markers. A cleanup failure therefore fails the task, records `restore_incomplete`, and retains the earlier maintenance marker instead of publishing a clean restore.

MySQL Shell 8.0.36 does not provide the per-table `rowsWritten` evidence required for a trustworthy row-count gate. This release does not persist or verify per-table row counts, does not run sampling `COUNT(*)`, and does not claim row-level equality. Operators must perform application-specific data validation before reopening traffic.

A historical v1 backup remains listable, repository-verifiable, and eligible for controlled deletion. It is rejected with `MYSQL_RESTORE_MANIFEST_INVALID` before any destructive restore or disaster-rebuild mutation. Do not convert v1 to v2 using live-source queries.

## 9. Persistent maintenance marker

`maintenanceConfirmed:true` is only the submitter's declaration that external writes are stopped. The persistent control-plane gate is `app_instances.metadata.mysqlMaintenance`.

The legal standalone marker is:

```json
{
  "version": 1,
  "state": "required",
  "reason": "restore_incomplete",
  "scope": "standalone",
  "backupId": "<backupId>",
  "taskId": "<taskId>",
  "restorePhase": "schema_mutation_started",
  "recordedAt": "<utcRfc3339>"
}
```

The legal cluster marker has `"scope":"cluster"` and one additional non-empty `"clusterId":"<clusterId>"`. Exactly the same marker must exist on all three authoritative members in one SQLite transaction.

The only legal phase transitions are:

```text
absent
  -> schema_mutation_started (persisted before the first schema mutation)
  -> load_complete (controlled load returned successfully)
  -> cleared (only after all verification reaches verified)
```

An initial marker-write failure returns `MYSQL_MAINTENANCE_STATE_PERSIST_FAILED` and must occur before schema mutation. If phase advancement, verification, cleanup, publication, or final clear fails, the earlier valid marker remains and the task does not report success. A final-clear persistence failure returns the same stable code even if data load already completed.

While a valid marker exists, AIFAR rejects ordinary check, cluster start, instance delete/uninstall, backup, and restore both before task creation and again after the mutation lock is acquired. Controlled deletion of a backup record is a separate recovery-point policy operation. `MYSQL_MAINTENANCE_REQUIRED` means the marker is valid; keep external traffic stopped and investigate the named backup, task, phase, and timestamp. `MYSQL_MAINTENANCE_STATE_INVALID` means the marker is malformed, missing from a cluster member, or divergent; AIFAR fails closed. Do not edit SQLite metadata by hand.

After approved external remediation, an owner may request clear:

```bash
curl -fsS -X POST "$PANEL_URL/api/v2/apps/instances/<instanceId>/mysql/maintenance/clear" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'Content-Type: application/json' \
  -H 'X-AIFAR-Language: en' \
  -d '{"recoveryConfirmed":true}'
```

Clear obtains the same mutation lock, rereads marker identity and topology, rejects any pending `mysqlReconciliation`, and applies these health gates:

- standalone: the uniquely bound active MySQL admin credential works and MySQL ping succeeds;
- cluster: exactly three members are `ONLINE`, exactly one is PRIMARY, and Router 6446 completes a real read/write transaction;
- cluster clear removes all three identical markers atomically.

`recoveryConfirmed` means the owner independently remediated and validated the database and accepts the residual data risk. Ping, `ONLINE`, catalog agreement, or Router health does not prove row-level equality.

## 10. `local_infile` reconciliation

`util.loadDump()` temporarily requires `@@GLOBAL.local_infile=ON`. AIFAR captures the original `ON` or `OFF` value, records a non-secret marker before enabling it, and restores and rereads the value on successful, failed, and cancelled paths while the target remains reachable.

The marker shape is:

```json
{
  "version": 1,
  "kind": "local_infile",
  "originalValue": "OFF",
  "recordedAt": "<utcRfc3339>",
  "taskId": "<taskId>"
}
```

It is stored under `app_instances.metadata.mysqlReconciliation`. Unknown versions, kinds, values, fields, or invalid timestamps/task IDs fail closed with `MYSQL_RECONCILIATION_REQUIRED`. New restore work emits `MYSQL_LOCAL_INFILE_RESTORE_FAILED` when the finally-path restore cannot complete; the older `MYSQL_RESTORE_LOCAL_INFILE_RESTORE_FAILED` name is only a historical translation alias.

Safe operator response:

1. Keep external writes stopped and retain the failed task ID and non-secret marker fields.
2. Restore SSH/MySQL reachability and the unique active admin credential binding. Do not clear the marker or change `local_infile` metadata manually.
3. As an owner, submit the dedicated reconciliation task against the exact affected instance carrying the marker. For a cluster, do not substitute a different member; AIFAR derives and locks the authoritative cluster from the marked instance:

```bash
curl -fsS -X POST "$PANEL_URL/api/v2/apps/instances/<instanceId>/mysql/reconciliation/run" \
  -H "@$AUTH_HEADER_FILE" \
  -H 'Content-Type: application/json' \
  -H 'X-AIFAR-Language: en' \
  -d '{"reconciliationConfirmed":true}'
```

4. Follow task type `apps.mysql.reconciliation.run`. It takes the raw instance or authoritative cluster mutation lock, strictly rereads the marker, sets the recorded original value, rereads `@@GLOBAL.local_infile`, and compare-and-swap clears only the exact `mysqlReconciliation` marker. The `mysqlMaintenance` marker intentionally remains.
5. If maintenance clear reports `MYSQL_RECONCILIATION_REQUIRED`, complete reconciliation first; never bypass the check by editing `app_instances`.
6. Continue with maintenance clear only after reconciliation succeeds and independent data validation is complete.

Missing acknowledgement returns `MYSQL_RECONCILIATION_CONFIRMATION_REQUIRED`; absence of a marker returns `MYSQL_RECONCILIATION_NOT_REQUIRED`; a malformed, changed, unreachable, or unverifiable marker remains in place and returns `MYSQL_RECONCILIATION_REQUIRED`. Reconciliation does not validate restored business data and does not reopen external traffic.

## 11. Stable-error diagnosis

| Error code | Meaning and safe response |
| --- | --- |
| `MYSQL_MAINTENANCE_REQUIRED` | A valid incomplete-restore marker is present. Keep external traffic stopped, inspect the named backup/task/phase, complete reconciliation if present, remediate and validate data, then use owner clear. |
| `MYSQL_MAINTENANCE_STATE_INVALID` | A marker is malformed or cluster member markers/topology differ. AIFAR fails closed. Preserve all three member records, stop mutation attempts, and escalate; do not edit SQLite. |
| `MYSQL_MAINTENANCE_STATE_PERSIST_FAILED` | Initial marker persistence failed before mutation, or a later phase/final clear could not be persisted. Check the task phase and audit evidence; never assume load was rolled back. The earlier marker must remain after a post-mutation failure. |
| `MYSQL_RECONCILIATION_CONFIRMATION_REQUIRED` | The dedicated owner task was submitted without the exact risk acknowledgement. Reconfirm reachability, credential binding, and external maintenance before resubmitting. |
| `MYSQL_RECONCILIATION_NOT_REQUIRED` | No `mysqlReconciliation` marker exists. Do not create or clear metadata manually; continue with the maintenance/data checks appropriate to the current state. |
| `MYSQL_RECONCILIATION_REQUIRED` | A marker exists but is malformed, changed, unreachable, lacks a usable bound admin credential, or its original `local_infile` value cannot be restored and reread. Repair the prerequisite and rerun the dedicated owner task; the marker must stay until verified. |
| `MYSQL_LOCAL_INFILE_RESTORE_FAILED` | The restore finally path could not restore the captured value. Keep maintenance in force and use the dedicated reconciliation task. |
| `MYSQL_CREDENTIAL_UNAVAILABLE` | A unique, enabled, complete `purpose=admin` MySQL binding cannot be resolved or decrypted. Repair the binding; do not supply a default password or place a credential in an API body, script, or command. |
| `MYSQL_RESTORE_INCOMPLETE` | Mutation started but load, verification, cleanup, or publication did not complete. Keep the marker and external maintenance; do not automatically load the pre-restore backup. |
| `MYSQL_RESTORE_MANIFEST_INVALID` | The manifest, inventory, catalog, count, or task/digest identity is unsafe, or the backup is v1. Restore/rebuild stops before destructive mutation. Use another verified v2 recovery point. |
| `MYSQL_RESTORE_PRIMARY_CHANGED` | The healthy-cluster PRIMARY changed during the protected window. AIFAR does not continue on the new PRIMARY; retain maintenance and assess cluster state. |

## 12. Retention, capacity, and recovery testing

- `AIFAR_MYSQL_BACKUP_KEEP_LAST` defaults to 5. A per-backup request may set `keepLast`; use the approved policy rather than reducing it ad hoc.
- Retention runs only after the new backup succeeds and must keep at least one successful recovery point. Cleanup failure is a warning and does not reverse the new backup.
- The controlled DELETE endpoint currently supports standalone logical-full backups only and requires another verified recovery point for the same standalone owner. Cluster recovery points are removed only by cluster-aware retention after a new backup succeeds. Never delete repository directories directly.
- Keep at least one additional copy off the MySQL nodes and outside the panel host's single failure domain. A replicated NAS, backup appliance, or separately controlled copy workflow is recommended; the live AIFAR repository itself must still satisfy locking and atomic-rename requirements.
- Monitor both free bytes and free inodes on the actual mount. Alert before the planned largest backup plus staging/verification headroom can exhaust the filesystem.

```bash
df -Pk '<mysqlBackupRepository>'
df -Pi '<mysqlBackupRepository>'
du -sk '<mysqlBackupRepository>'
```

Run a scheduled verification of every retained recovery tier, and perform a restore into a disposable target at least quarterly, after MySQL/AIFAR/storage upgrades, and after any backup-repository migration. Set a stricter cadence if the business RPO/RTO requires it. Measure RPO from the selected backup creation time and RTO from the approved restore start to validated service reopening; do not substitute worker duration for full RTO.

## 13. Automated evidence versus real acceptance

Local Go, SQLite, fake-remote, and Vitest coverage validates control-plane contracts such as request validation, permissions, task/step/target recording, locking, marker transactions, manifest parsing, path safety, checksums, UI availability, and translations. It does not prove:

- real SSH behavior on the target operating system;
- the packaged MySQL Shell 8.0.36 dump/load behavior;
- real target argv, signal/cancellation, and residue behavior for task-scoped credential contexts and transient SQL/JS files;
- mount ownership, locking, atomic rename, disconnect, and capacity behavior on the production filesystem or NAS;
- SELinux policy on openEuler;
- live Group Replication PRIMARY election, clone, catch-up, or Router bootstrap;
- real Router 6446 read/write behavior;
- application data correctness, RPO, RTO, or cleanup on a real target;
- signed-in browser layout or interaction.

Those claims require the manual matrix below. Never convert an automated fake result into a real-target acceptance result.

## 14. Manual acceptance matrix

Environment status for this repository session: **NOT EXECUTED — target environment not provided**. No authorized standalone target or disposable three-node MySQL 8.0.36/openEuler/Router environment was provided. Every row remains a release blocker until an operator records desensitized evidence in a dedicated environment.

Use identifiers, digests, counts, timestamps, durations, and state names only. Never record credentials, tokens, full connection strings, private repository paths, or raw logs containing secrets.

| ID | Scenario and expected result | Required desensitized evidence | Result |
| --- | --- | --- | --- |
| S1 | Standalone online backup creates one successful recovery point. | task ID; backup ID; source version; archive size and SHA-256; start/end UTC; source write observation; cleanup state | NOT EXECUTED — target environment not provided |
| S2 | Standalone manifest v2 and empty-target restore pass the supported integrity gate. | valid 8.0.36 completion-marker result; closed metadata-catalog result; inventory digest; exact schema/base-table counts; controlled load result; MySQL ping; current task/canonical manifest digest match; explicit “per-table rows not verified”; RPO/RTO | NOT EXECUTED — target environment not provided |
| S3 | A v1 fixture remains listable and repository-verifiable but is rejected before remote connection/schema mutation. | backup ID; list result; verify task ID/result; restore error `MYSQL_RESTORE_MANIFEST_INVALID`; proof no target mutation | NOT EXECUTED — target environment not provided |
| S4 | Checksum corruption is rejected before target mutation. | backup ID; original and test digest references; verify/restore task ID; stable error; proof no schema mutation; fixture cleanup | NOT EXECUTED — target environment not provided |
| S5 | Standalone post-mutation fault retains a valid marker and blocks ordinary actions at API and execution gates. | failed task ID; backup ID; non-secret marker with phase; check/start/delete/backup/restore rejection codes; worker/module gate evidence; external direct-client observation; remediation and owner clear task; before/after `local_infile`; cleanup | NOT EXECUTED — target environment not provided |
| C1 | Healthy three-node cluster backup dumps the runtime PRIMARY once. | task ID; backup ID; pre-backup three-member role/state/UUID summary; selected PRIMARY; dump invocation count=1; checksum; cleanup | NOT EXECUTED — target environment not provided |
| C2 | Healthy cluster restore loads only current PRIMARY and validates the controlled topology. | task/backup/pre-restore IDs; load target; `skipBinlog:false` evidence; before/after member roles and `ONLINE` states; separately collected applier/catch-up evidence; exact catalog counts; manifest/task digest; every recorded Router read/write transaction; RPO/RTO | NOT EXECUTED — target environment not provided |
| C3 | PRIMARY switch after mutation starts fails safely and preserves maintenance. | injected-switch timestamp; task/backup IDs; old/new PRIMARY; `MYSQL_RESTORE_PRIMARY_CHANGED` or terminal incomplete evidence; marker; proof no continuation on new PRIMARY; cleanup/remediation | NOT EXECUTED — target environment not provided |
| C4 | Cluster post-mutation fault writes and clears exactly the same marker on all three members atomically. | three redacted metadata snapshots before/after; shared marker identity; API and execution-gate rejections; external direct-client observation; health/Router evidence; owner clear task; transaction result | NOT EXECUTED — target environment not provided |
| C5 | Complete outage uses reboot, not backup restore or rebuild. | start task ID; GTID-superset selection; `rebootClusterFromCompleteOutage` result; proof no schema drop/load/quarantine; final roles/states | NOT EXECUTED — target environment not provided |
| C6 | Disaster rebuild restores a clean seed, clones B/C, and reaches three `ONLINE` members. | restore task/backup IDs; mapping IDs; quarantine identifiers; seed validation; create-cluster result; B/C clone results; final member roles/states/UUIDs; cleanup status; RPO/RTO | NOT EXECUTED — target environment not provided |
| C7 | Rebuilt Router 6446 completes a real write/read transaction. | Router identity summary; bootstrap task step; test schema/table identifier; write/read match; rollback/drop cleanup; no credential output | NOT EXECUTED — target environment not provided |
| L1 | Reachable success, failure, and cancellation each restore the original `local_infile` value. | three task IDs; before/after values; terminal status; marker absent after each; cleanup status | NOT EXECUTED — target environment not provided |
| L2 | Unreachable finally path records reconciliation, blocks unsafe continuation, and later reconciles safely. | failed task ID; `MYSQL_LOCAL_INFILE_RESTORE_FAILED`; non-secret marker; blocked-action evidence; reconciliation task ID and verification; marker clear; maintenance clear; cleanup | NOT EXECUTED — target environment not provided |
| O1 | Task, step, target, log, audit, and backup records are complete and secret-free. | task IDs; expected/actual step-name lists; target count; audit actions; backup states; redaction scan result | NOT EXECUTED — target environment not provided |
| O2 | Repository and operating-system boundaries hold. | service UID/GID and modes; mount type; lock contention result; atomic rename result; disconnect behavior; SELinux labels/denials; free bytes/inodes; partial-file cleanup | NOT EXECUTED — target environment not provided |
| O3 | Install/bootstrap/start/status/PRIMARY credential transport is secret-free and uses only current bindings. | current bound credential identity without secret; rotation before/after binding reference; 0600 owner/non-symlink context checks; remote argv snapshot; script/temp-directory inventory; task/error/audit/metadata secret scan; success/failure/cancel cleanup; proof no default fallback | NOT EXECUTED — target environment not provided |
| U1 | Signed-in browser workflow is usable in both languages. | build identifier; browser/viewport; zh/en screenshots with sensitive fields redacted; backup, records, verify, restore, marker, clear, and disaster interaction results | NOT EXECUTED — signed-in browser session not performed |

Acceptance sign-off must name the environment owner, build/commit, date, operator, reviewer, unresolved deviations, and approval decision. Attach evidence through the organization's protected channel; do not commit real-environment evidence to this source repository.
