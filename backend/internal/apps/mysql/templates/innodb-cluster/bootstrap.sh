#!/usr/bin/env sh
set -eu

CLUSTER_NAME={{shq .ClusterName}}
INSTALL_ROOT={{shq .InstallRoot}}
CREDENTIAL_CONTEXT={{shq .CredentialContextPath}}
JS_FILE="${CREDENTIAL_CONTEXT%/*}/bootstrap-cluster.js"
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
  echo "mysqlsh is required to bootstrap MySQL InnoDB Cluster; expected $INSTALL_ROOT/mysql-shell/bin/mysqlsh"
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

for (const node of nodes) {
	const target = connection(node);
  print('configuring MySQL instance ' + node.host + ':' + node.port);
  const configureOptions = {
	clusterAdmin: target.user,
    restart: true,
    interactive: false
  };
  try {
	dba.configureInstance(target, Object.assign({}, configureOptions, {
	  clusterAdminPassword: target.password
    }));
  } catch (e) {
    const message = String(e && e.message ? e.message : e);
    if (message.indexOf('already exists') >= 0 || message.indexOf('clusterAdminPassword is not allowed') >= 0) {
      print('cluster admin account already exists on ' + node.host + ':' + node.port + ', reusing it');
	  dba.configureInstance(target, configureOptions);
    } else {
      throw e;
    }
  }
}

shell.connect(connection(nodes[0]));
let cluster;
try {
  cluster = dba.getCluster(clusterName);
  print('existing InnoDB Cluster detected: ' + clusterName);
} catch (e) {
  cluster = dba.createCluster(clusterName);
  print('created InnoDB Cluster: ' + clusterName);
}

for (let i = 1; i < nodes.length; i++) {
	const instanceConnection = connection(nodes[i]);
  try {
	cluster.addInstance(instanceConnection, { recoveryMethod: 'clone', interactive: false });
    print('added MySQL instance: ' + nodes[i].host + ':' + nodes[i].port);
  } catch (e) {
    print('add instance skipped or failed for ' + nodes[i].host + ':' + nodes[i].port + ': ' + e.message);
  }
}

cluster.status();
JS
)

"$MYSQLSH" --js --file "$JS_FILE"
echo "MySQL InnoDB Cluster bootstrap completed: $CLUSTER_NAME"
