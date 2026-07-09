#!/usr/bin/env sh
set -eu

CLUSTER_NAME={{shq .ClusterName}}
INSTALL_ROOT={{shq .InstallRoot}}
ROOT_USER={{shq .RootUser}}
ROOT_PASSWORD={{shq .RootPassword}}

echo "checking MySQL Shell"
MYSQLSH="$INSTALL_ROOT/mysql-shell/bin/mysqlsh"
if [ ! -x "$MYSQLSH" ]; then
  MYSQLSH="$(command -v mysqlsh || true)"
fi
if [ -z "$MYSQLSH" ] || [ ! -x "$MYSQLSH" ]; then
  echo "mysqlsh is required to start MySQL InnoDB Cluster; expected $INSTALL_ROOT/mysql-shell/bin/mysqlsh"
  exit 1
fi

JS_FILE="$(mktemp /tmp/aifar-mysql-innodb-start-XXXXXX.js)"
cat > "$JS_FILE" <<'JS'
const clusterName = {{printf "%q" .ClusterName}};
const rootUser = {{printf "%q" .RootUser}};
const rootPassword = {{printf "%q" .RootPassword}};
const nodes = [
{{range .Nodes}}  { host: {{printf "%q" .Host}}, port: {{.Port}} },
{{end}}];

function uri(node) {
  return rootUser + ':' + rootPassword + '@' + node.host + ':' + node.port;
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

shell.connect(uri(nodes[0]));

function fetchFirstColumn(sql) {
  const rows = [];
  const result = session.runSql(sql);
  let row;
  while ((row = result.fetchOne())) {
    rows.push(String(row[0]));
  }
  return rows;
}

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

let cluster;
try {
  print('attempting InnoDB Cluster reboot from complete outage: ' + clusterName);
  cluster = dba.rebootClusterFromCompleteOutage(clusterName);
  print('InnoDB Cluster complete-outage reboot completed: ' + clusterName);
} catch (e) {
  const rebootMessage = messageOf(e);
  print('complete-outage reboot was not applied: ' + rebootMessage);
  try {
    cluster = dba.getCluster(clusterName);
    print('existing InnoDB Cluster metadata loaded: ' + clusterName);
  } catch (inner) {
    throw e;
  }
}

const failures = [];
for (const node of nodes) {
  const instanceUri = uri(node);
  try {
    cluster.rejoinInstance(instanceUri, { interactive: false });
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

"$MYSQLSH" --js --file "$JS_FILE"
rm -f "$JS_FILE"
echo "MySQL InnoDB Cluster start completed: $CLUSTER_NAME"
