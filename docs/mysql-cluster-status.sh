#!/usr/bin/env sh
set -eu

# 可按需覆盖：
# MYSQL_HOSTS="10.0.0.1,10.0.0.2,10.0.0.3" MYSQL_USER=root sh mysql-cluster-status.sh
MYSQL_HOSTS="${MYSQL_HOSTS:-192.168.74.133,192.168.74.134,192.168.74.137}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_USER="${MYSQL_USER:-root}"
MYSQL_CLUSTER_NAME="${MYSQL_CLUSTER_NAME:-aifarCluster}"
MYSQLSH_BIN="${MYSQLSH_BIN:-/aifar/apps/mysql/mysql-shell/bin/mysqlsh}"

if [ ! -x "$MYSQLSH_BIN" ]; then
  MYSQLSH_BIN="$(command -v mysqlsh || true)"
fi
if [ -z "$MYSQLSH_BIN" ] || [ ! -x "$MYSQLSH_BIN" ]; then
  echo "mysqlsh not found" >&2
  exit 1
fi

JS_FILE="$(mktemp "${TMPDIR:-/tmp}/mysql-cluster-status.XXXXXX.js")"
cleanup() {
  rm -f -- "$JS_FILE"
}
trap cleanup EXIT HUP INT TERM
chmod 600 "$JS_FILE"

cat >"$JS_FILE" <<'JS'
const hosts = os.getenv('AIFAR_MYSQL_HOSTS').split(',').map((host) => host.trim()).filter(Boolean);
const port = Number(os.getenv('AIFAR_MYSQL_PORT'));
const user = os.getenv('AIFAR_MYSQL_USER');
const clusterName = os.getenv('AIFAR_MYSQL_CLUSTER_NAME');
const password = shell.prompt('MySQL password: ', {type: 'password'});
let clusterStatusPrinted = false;

function fetchRows(sql) {
  const result = session.runSql(sql);
  const rows = [];
  let row;
  while ((row = result.fetchOne())) {
    rows.push(row);
  }
  return rows;
}

for (const host of hosts) {
  print('\n========== ' + host + ':' + port + ' ==========');
  try {
    shell.connect({scheme: 'mysql', host, port, user, password});
    const state = fetchRows(`
SELECT @@hostname,
       @@server_uuid,
       @@global.read_only,
       @@global.super_read_only,
       @@global.gtid_executed`)[0];
    print(JSON.stringify({
      reachable: true,
      hostname: String(state[0]),
      serverUuid: String(state[1]),
      readOnly: Number(state[2]),
      superReadOnly: Number(state[3]),
      gtidExecuted: String(state[4])
    }, null, 2));

    const members = fetchRows(`
SELECT MEMBER_HOST, MEMBER_PORT, MEMBER_STATE, MEMBER_ROLE
FROM performance_schema.replication_group_members
ORDER BY MEMBER_HOST, MEMBER_PORT`).map((row) => ({
      host: String(row[0]),
      port: Number(row[1]),
      state: String(row[2]),
      role: String(row[3])
    }));
    print('Group Replication members:');
    print(JSON.stringify(members, null, 2));

    if (!clusterStatusPrinted) {
      try {
        const cluster = dba.getCluster(clusterName);
        print('InnoDB Cluster status (extended=1):');
        print(JSON.stringify(cluster.status({extended: 1}), null, 2));
        clusterStatusPrinted = true;
      } catch (error) {
        print('AdminAPI status unavailable from this node: ' + String(error.message || error));
      }
    }
  } catch (error) {
    print(JSON.stringify({
      reachable: false,
      error: String(error.message || error)
    }, null, 2));
  }
}

if (!clusterStatusPrinted) {
  print('\nNo node returned cluster.status(); review the per-node GTID and Group Replication output above.');
}
JS

export AIFAR_MYSQL_HOSTS="$MYSQL_HOSTS"
export AIFAR_MYSQL_PORT="$MYSQL_PORT"
export AIFAR_MYSQL_USER="$MYSQL_USER"
export AIFAR_MYSQL_CLUSTER_NAME="$MYSQL_CLUSTER_NAME"

"$MYSQLSH_BIN" --js --file "$JS_FILE"
