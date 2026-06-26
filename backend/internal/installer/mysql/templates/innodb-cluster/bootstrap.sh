#!/usr/bin/env sh
set -eu

CLUSTER_NAME={{shq .ClusterName}}
ROOT_USER={{shq .RootUser}}
ROOT_PASSWORD={{shq .RootPassword}}

echo "checking MySQL Shell"
command -v mysqlsh >/dev/null 2>&1 || { echo "mysqlsh is required to bootstrap MySQL InnoDB Cluster"; exit 1; }

JS_FILE="$(mktemp /tmp/aifar-mysql-innodb-cluster-XXXXXX.js)"
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

for (const node of nodes) {
  print('configuring MySQL instance ' + node.host + ':' + node.port);
  dba.configureInstance(uri(node), {
    clusterAdmin: rootUser,
    clusterAdminPassword: rootPassword,
    restart: true,
    interactive: false
  });
}

shell.connect(uri(nodes[0]));
let cluster;
try {
  cluster = dba.getCluster(clusterName);
  print('existing InnoDB Cluster detected: ' + clusterName);
} catch (e) {
  cluster = dba.createCluster(clusterName);
  print('created InnoDB Cluster: ' + clusterName);
}

for (let i = 1; i < nodes.length; i++) {
  const instanceUri = uri(nodes[i]);
  try {
    cluster.addInstance(instanceUri, { recoveryMethod: 'clone', interactive: false });
    print('added MySQL instance: ' + nodes[i].host + ':' + nodes[i].port);
  } catch (e) {
    print('add instance skipped or failed for ' + nodes[i].host + ':' + nodes[i].port + ': ' + e.message);
  }
}

cluster.status();
JS

mysqlsh --js --file "$JS_FILE"
rm -f "$JS_FILE"
echo "MySQL InnoDB Cluster bootstrap completed: $CLUSTER_NAME"
