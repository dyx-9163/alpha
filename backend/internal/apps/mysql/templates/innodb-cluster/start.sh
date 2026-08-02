#!/usr/bin/env sh
set -eu

CLUSTER_NAME={{shq .ClusterName}}
INSTALL_ROOT={{shq .InstallRoot}}
CREDENTIAL_CONTEXT={{shq .CredentialContextPath}}
JS_FILE="${CREDENTIAL_CONTEXT%/*}/start-cluster.js"
umask 077

cleanup_secret_artifacts() {
  cleanup_status=0
  rm -f -- "$JS_FILE" || cleanup_status=1
  rm -f -- "$CREDENTIAL_CONTEXT" || cleanup_status=1
  return "$cleanup_status"
}

finish() {
  status=$?
  trap - EXIT HUP INT TERM
  if ! cleanup_secret_artifacts; then
    status=1
  fi
  exit "$status"
}
trap finish EXIT
trap 'exit 1' HUP INT TERM

case "$CREDENTIAL_CONTEXT" in
  */_work/mysql-credential-*/*) ;;
  *) echo "invalid MySQL credential context"; exit 1 ;;
esac
[ -f "$CREDENTIAL_CONTEXT" ] && [ ! -L "$CREDENTIAL_CONTEXT" ] || { echo "invalid MySQL credential context"; exit 1; }
[ "$(stat -c '%u' "$CREDENTIAL_CONTEXT")" = "$(id -u)" ] || { echo "invalid MySQL credential context owner"; exit 1; }
[ "$(stat -c '%a' "$CREDENTIAL_CONTEXT")" = "600" ] || { echo "invalid MySQL credential context mode"; exit 1; }

echo "checking MySQL Shell"
MYSQLSH="$INSTALL_ROOT/mysql-shell/bin/mysqlsh"
if [ ! -x "$MYSQLSH" ]; then
  MYSQLSH="$(command -v mysqlsh || true)"
fi
if [ -z "$MYSQLSH" ] || [ ! -x "$MYSQLSH" ]; then
  echo "mysqlsh is required to start MySQL InnoDB Cluster; expected $INSTALL_ROOT/mysql-shell/bin/mysqlsh"
  exit 1
fi

rm -f -- "$JS_FILE"
(umask 077; cat > "$JS_FILE" <<'JS'
const clusterName = {{printf "%q" .ClusterName}};
const credentialPath = {{printf "%q" .CredentialContextPath}};
const nodes = [
{{range .Nodes}}  { host: {{printf "%q" .Host}}, port: {{.Port}} },
{{end}}];

const credentialContext = JSON.parse(os.loadTextFile(credentialPath));
if (credentialContext.version !== 1 || !Array.isArray(credentialContext.connections)) {
  throw new Error('invalid MySQL credential context');
}

function connection(node) {
  const matches = credentialContext.connections.filter((candidate) =>
	candidate.host === node.host && Number(candidate.port) === Number(node.port));
  if (matches.length !== 1 || !matches[0].user || !matches[0].password) {
	throw new Error('missing MySQL member credential');
  }
  return {scheme: 'mysql', host: node.host, port: Number(node.port), user: matches[0].user, password: matches[0].password};
}

function messageOf(error) {
  return String(error && error.message ? error.message : error);
}

function isAlreadyReady(message) {
  const value = String(message || '').toLowerCase();
  return value.indexOf('already') >= 0 ||
    value.indexOf('online') >= 0 ||
    value.indexOf('part of the cluster') >= 0 ||
    value.indexOf('active member') >= 0 ||
    value.indexOf('current instance') >= 0;
}

function fetchFirstColumn(sql, args) {
  const rows = [];
  const result = session.runSql(sql, args || []);
  let row;
  while ((row = result.fetchOne())) {
    rows.push(String(row[0]));
  }
  return rows;
}

const gtidSnapshots = nodes.map((node) => {
  shell.connect(connection(node));
  const rows = fetchFirstColumn('SELECT @@GLOBAL.gtid_executed');
  if (rows.length !== 1 || !rows[0]) {
    throw new Error('unable to read a non-empty GTID set from every cluster member');
  }
  return {node: node, gtid: rows[0]};
});

const candidates = [];
for (const candidate of gtidSnapshots) {
  shell.connect(connection(candidate.node));
  let coversEveryMember = true;
  for (const observed of gtidSnapshots) {
    const subset = fetchFirstColumn('SELECT GTID_SUBSET(?, ?)', [observed.gtid, candidate.gtid]);
    if (subset.length !== 1 || subset[0] !== '1') {
      coversEveryMember = false;
      break;
    }
  }
  if (coversEveryMember) {
    candidates.push(candidate);
  }
}
if (candidates.length === 0) {
  throw new Error('complete-outage recovery could not identify a GTID-superset member; transaction sets may have diverged');
}
const seed = candidates[0].node;
shell.connect(connection(seed));

function assertGroupReplicationTableKeys() {
  const missingKeys = fetchFirstColumn(`
SELECT CONCAT(t.table_schema, '.', t.table_name) AS table_name
FROM information_schema.tables t
WHERE t.table_type = 'BASE TABLE'
  AND t.engine = 'InnoDB'
  AND t.table_schema NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
  AND NOT EXISTS (
    SELECT 1
    FROM information_schema.table_constraints tc
    WHERE tc.table_schema = t.table_schema
      AND tc.table_name = t.table_name
      AND tc.constraint_type = 'PRIMARY KEY'
  )
  AND NOT EXISTS (
    SELECT 1
    FROM information_schema.statistics s
    JOIN information_schema.columns c
      ON c.table_schema = s.table_schema
     AND c.table_name = s.table_name
     AND c.column_name = s.column_name
    WHERE s.table_schema = t.table_schema
      AND s.table_name = t.table_name
      AND s.non_unique = 0
    GROUP BY s.table_schema, s.table_name, s.index_name
    HAVING SUM(CASE WHEN c.is_nullable = 'YES' THEN 1 ELSE 0 END) = 0
  )
ORDER BY t.table_schema, t.table_name`);
  if (missingKeys.length === 0) {
    return;
  }
  print('ERROR: Group Replication requires every writable InnoDB table to have a PRIMARY KEY or a NOT NULL UNIQUE key.');
  print('Tables missing a compatible key:');
  for (const tableName of missingKeys) {
    print('  - ' + tableName);
  }
  print('Add a primary key or non-null unique key to these tables, then retry MySQL InnoDB Cluster start.');
  print('For MySQL 8.0.23+, an invisible auto-increment primary key can be used when the application schema cannot expose a new column.');
  throw new Error('Group Replication table key validation failed: ' + missingKeys.join(', '));
}

assertGroupReplicationTableKeys();

print('validating InnoDB Cluster complete-outage reboot without mutation: ' + clusterName);
dba.rebootClusterFromCompleteOutage(clusterName, {dryRun: true});
print('attempting InnoDB Cluster reboot from the selected GTID-superset member: ' + clusterName);
const cluster = dba.rebootClusterFromCompleteOutage(clusterName);
print('InnoDB Cluster complete-outage reboot completed: ' + clusterName);

const failures = [];
for (const node of nodes) {
	const instanceConnection = connection(node);
  try {
	cluster.rejoinInstance(instanceConnection, { interactive: false });
    print('rejoined MySQL instance: ' + node.host + ':' + node.port);
  } catch (e) {
    const message = messageOf(e);
    if (isAlreadyReady(message)) {
      print('MySQL instance already active: ' + node.host + ':' + node.port);
    } else {
      failures.push(node.host + ':' + node.port + ' ' + message);
      print('failed to rejoin MySQL instance: ' + node.host + ':' + node.port + ': ' + message);
    }
  }
}

if (failures.length > 0) {
  throw new Error('some MySQL instances did not rejoin: ' + failures.join('; '));
}

cluster.status({ extended: 1 });
JS
)

"$MYSQLSH" --js --file "$JS_FILE"
if ! cleanup_secret_artifacts; then
  exit 1
fi
trap - EXIT HUP INT TERM
echo "MySQL InnoDB Cluster start completed: $CLUSTER_NAME"
