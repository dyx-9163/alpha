#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
CONFIG_VERSION={{ .ConfigVersion }}
CONFIG_ROOT="$INSTALL_ROOT/runtime/config/versions"
CURRENT_STAGE=""
CURRENT_SERVICE=""

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

cleanup_stage() {
  [ -n "$CURRENT_STAGE" ] || return 0
  if [ -d "$CURRENT_STAGE" ]; then
    rm -f -- "$CURRENT_STAGE/config.meta" "$CURRENT_STAGE/resource.env" "$CURRENT_STAGE/java-jvm.options" "$CURRENT_STAGE/java-jvm.$CURRENT_SERVICE.options" "$CURRENT_STAGE/java-entrypoint.sh"
    rmdir -- "$CURRENT_STAGE" 2>/dev/null || true
  fi
  CURRENT_STAGE=""
  CURRENT_SERVICE=""
}

trap cleanup_stage EXIT HUP INT TERM

write_jvm_options() {
  file="$1"
  initial="$2"
  max="$3"
  cat > "$file" <<EOF
-XX:+UseContainerSupport
-XX:InitialRAMPercentage=${initial}
-XX:MaxRAMPercentage=${max}
-XX:+ExitOnOutOfMemoryError
EOF
  chmod 0644 "$file"
}

java_start_command() {
  cat <<'EOF'
opts_file="/opt/aifar/runtime/env/java-jvm.${AIFAR_SERVICE_NAME}.options"
[ -f "$opts_file" ] || opts_file="/opt/aifar/runtime/env/java-jvm.options"
java_opts=""
[ -f "$opts_file" ] && java_opts="$(tr '\n' ' ' < "$opts_file")"
jar="aifar-${AIFAR_SERVICE_NAME}.jar"
[ -f app.jar ] && jar="app.jar"
if [ ! -f "$jar" ]; then
  jar="$(find . -maxdepth 1 -type f -name '*.jar' 2>/dev/null | head -n 1 | sed 's#^\./##')"
fi
[ -n "$jar" ] && [ -f "$jar" ] || { echo "AIFAR jar is missing for ${AIFAR_SERVICE_NAME}" >&2; exit 1; }
exec java $java_opts --add-opens=java.base/java.lang=ALL-UNNAMED --add-opens=java.base/java.lang.reflect=ALL-UNNAMED --add-opens=java.base/java.lang.invoke=ALL-UNNAMED --add-opens=java.base/java.math=ALL-UNNAMED --add-opens=java.base/sun.net.util=ALL-UNNAMED --add-opens=java.base/java.io=ALL-UNNAMED --add-opens=java.base/java.net=ALL-UNNAMED --add-opens=java.base/java.nio=ALL-UNNAMED --add-opens=java.base/java.security=ALL-UNNAMED --add-opens=java.base/java.text=ALL-UNNAMED --add-opens=java.base/java.time=ALL-UNNAMED --add-opens=java.base/java.util=ALL-UNNAMED --add-opens=java.base/jdk.internal.module=ALL-UNNAMED --add-opens=java.base/sun.security.util=ALL-UNNAMED -Dfile.encoding=utf8 -Djava.security.egd=file:/dev/./urandom -jar "$jar"
EOF
}

prepare_service_config() {
  service="$1"
  java="$2"
  cpus="$3"
  memory="$4"
  jvm_initial="$5"
  jvm_max="$6"
  nacos_ephemeral="$7"
  config_hash="$8"
  final_dir="$9"
  parent_dir="$CONFIG_ROOT/$service"
  [ "$final_dir" = "$parent_dir/v${CONFIG_VERSION}-${config_hash}" ] || fail "runtime config destination is invalid"
  mkdir -p -- "$parent_dir"
  chmod 0755 -- "$CONFIG_ROOT" "$parent_dir"

  CURRENT_STAGE="$parent_dir/.v${CONFIG_VERSION}-${config_hash}.tmp.$$"
  CURRENT_SERVICE="$service"
  [ ! -e "$CURRENT_STAGE" ] || fail "runtime config staging directory already exists"
  mkdir -- "$CURRENT_STAGE"
  chmod 0755 -- "$CURRENT_STAGE"
  printf "service=%s\nversion=%s\nhash=%s\n" "$service" "$CONFIG_VERSION" "$config_hash" > "$CURRENT_STAGE/config.meta"
  printf "APP_CPUS=%s\nAPP_MEMORY_LIMIT=%s\nAIFAR_RUNTIME_CONFIG_VERSION=%s\nAIFAR_RUNTIME_CONFIG_HASH=%s\nAIFAR_NACOS_EPHEMERAL=%s\n" \
    "$cpus" "$memory" "$CONFIG_VERSION" "$config_hash" "$nacos_ephemeral" > "$CURRENT_STAGE/resource.env"
  chmod 0644 -- "$CURRENT_STAGE/config.meta" "$CURRENT_STAGE/resource.env"
  if [ "$java" = "true" ]; then
    write_jvm_options "$CURRENT_STAGE/java-jvm.options" "$jvm_initial" "$jvm_max"
    write_jvm_options "$CURRENT_STAGE/java-jvm.$service.options" "$jvm_initial" "$jvm_max"
    java_start_command > "$CURRENT_STAGE/java-entrypoint.sh"
    chmod 0755 -- "$CURRENT_STAGE/java-entrypoint.sh"
  fi

  if [ -e "$final_dir" ]; then
    [ -d "$final_dir" ] || fail "runtime config destination is not a directory"
    cmp -s "$CURRENT_STAGE/config.meta" "$final_dir/config.meta" || fail "immutable runtime config metadata mismatch"
    cmp -s "$CURRENT_STAGE/resource.env" "$final_dir/resource.env" || fail "immutable runtime resource config mismatch"
    if [ "$java" = "true" ]; then
      cmp -s "$CURRENT_STAGE/java-jvm.options" "$final_dir/java-jvm.options" || fail "immutable runtime JVM config mismatch"
      cmp -s "$CURRENT_STAGE/java-jvm.$service.options" "$final_dir/java-jvm.$service.options" || fail "immutable service JVM config mismatch"
      cmp -s "$CURRENT_STAGE/java-entrypoint.sh" "$final_dir/java-entrypoint.sh" || fail "immutable Java entrypoint mismatch"
    fi
    cleanup_stage
    return 0
  fi
  mv -- "$CURRENT_STAGE" "$final_dir"
  CURRENT_STAGE=""
  CURRENT_SERVICE=""
}

mkdir -p -- "$CONFIG_ROOT"
chmod 0755 -- "$CONFIG_ROOT"
{{ range .Services -}}
prepare_service_config {{ quote .Name }} {{ if .Java }}true{{ else }}false{{ end }} {{ quote .AppCPUs }} {{ quote .AppMemoryLimit }} {{ quote .JVMInitialRAMPercentage }} {{ quote .JVMMaxRAMPercentage }} {{ quote .NacosEphemeral }} {{ quote .ConfigHash }} {{ quote .ConfigDir }}
{{ end -}}
echo "AIFAR runtime config prepared, version $CONFIG_VERSION"
