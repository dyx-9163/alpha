#!/usr/bin/env bash
set -euo pipefail

INSTALL_ROOT="/aifar/apps/mysql"
WORK_DIR="{{.WorkDir}}"
DUMP_DIR="{{.DumpDir}}"
SECRET_CONTEXT="$WORK_DIR/secret-context.cnf"
JS_FILE="$WORK_DIR/logical-backup.js"
MYSQLSH="$INSTALL_ROOT/mysql-shell/bin/mysqlsh"

require_secret_context() {
  [ ! -L "$SECRET_CONTEXT" ] || { echo "secret context must not be a symlink" >&2; exit 1; }
  [ -f "$SECRET_CONTEXT" ] || { echo "secret context is unavailable" >&2; exit 1; }
  [ "$(stat -c '%a' "$SECRET_CONTEXT")" = "600" ] || { echo "secret context must have mode 0600" >&2; exit 1; }
}

require_secret_context
[ -x "$MYSQLSH" ] || { echo "mysqlsh is unavailable" >&2; exit 1; }
mkdir -p "$DUMP_DIR"
cat > "$JS_FILE" <<'MYSQLSH_JS'
const options = {
  consistent: true,
  threads: {{.Threads}},
  compression: "zstd",
  users: false,
  showProgress: false,
  excludeSchemas: ["information_schema", "mysql", "mysql_innodb_cluster_metadata", "performance_schema", "sys"]
};
if ({{.MaxRateMBps}} > 0) {
  options.maxRate = "{{.MaxRateMBps}}M";
}
util.dumpInstance("{{.DumpDir}}", options);
MYSQLSH_JS
"$MYSQLSH" --defaults-file="$SECRET_CONTEXT" --js --file "$JS_FILE"
