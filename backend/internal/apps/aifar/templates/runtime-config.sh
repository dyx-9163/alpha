#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
CONFIG_VERSION={{ .ConfigVersion }}
GLOBAL_APP_CPUS={{ quote .GlobalAppCPUs }}
GLOBAL_APP_MEMORY_LIMIT={{ quote .GlobalAppMemoryLimit }}
GLOBAL_JVM_INITIAL_RAM_PERCENTAGE={{ quote .GlobalJVMInitialRAMPercentage }}
GLOBAL_JVM_MAX_RAM_PERCENTAGE={{ quote .GlobalJVMMaxRAMPercentage }}
NACOS_EPHEMERAL={{ quote .NacosEphemeral }}

ENV_DIR="$INSTALL_ROOT/runtime/env"
LOG_DIR="$INSTALL_ROOT/runtime/logs"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

set_env() {
  key="$1"
  value="$2"
  file="$3"
  tmp="${file}.tmp"
  [ -f "$file" ] || touch "$file"
  grep -v "^${key}=" "$file" > "$tmp" || true
  printf "%s=%s\n" "$key" "$value" >> "$tmp"
  mv "$tmp" "$file"
}

service_order() {
  printf "%s\n"{{ range .Services }} {{ quote .Name }}{{ end }}
}

service_cpus() {
  case "$1" in
{{- range .Services }}
    {{ .Name }}) printf "%s" {{ quote .AppCPUs }} ;;
{{- end }}
    *) printf "%s" "$GLOBAL_APP_CPUS" ;;
  esac
}

service_memory() {
  case "$1" in
{{- range .Services }}
    {{ .Name }}) printf "%s" {{ quote .AppMemoryLimit }} ;;
{{- end }}
    *) printf "%s" "$GLOBAL_APP_MEMORY_LIMIT" ;;
  esac
}

service_jvm_initial() {
  case "$1" in
{{- range .Services }}
    {{ .Name }}) printf "%s" {{ quote .JVMInitialRAMPercentage }} ;;
{{- end }}
    *) printf "%s" "$GLOBAL_JVM_INITIAL_RAM_PERCENTAGE" ;;
  esac
}

service_jvm_max() {
  case "$1" in
{{- range .Services }}
    {{ .Name }}) printf "%s" {{ quote .JVMMaxRAMPercentage }} ;;
{{- end }}
    *) printf "%s" "$GLOBAL_JVM_MAX_RAM_PERCENTAGE" ;;
  esac
}

is_java_service() {
  [ "$1" != "web-vue3" ]
}

resource_file_for_service() {
  printf "%s/resource.%s.env" "$ENV_DIR" "$1"
}

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

write_runtime_files() {
  mkdir -p "$ENV_DIR" "$LOG_DIR"
  compose_env="$ENV_DIR/compose.env"
  set_env AIFAR_RUNTIME_CONFIG_VERSION "$CONFIG_VERSION" "$compose_env"
  set_env APP_CPUS "$GLOBAL_APP_CPUS" "$compose_env"
  set_env APP_MEMORY_LIMIT "$GLOBAL_APP_MEMORY_LIMIT" "$compose_env"
  set_env JVM_INITIAL_RAM_PERCENTAGE "$GLOBAL_JVM_INITIAL_RAM_PERCENTAGE" "$compose_env"
  set_env JVM_MAX_RAM_PERCENTAGE "$GLOBAL_JVM_MAX_RAM_PERCENTAGE" "$compose_env"
  set_env AIFAR_NACOS_EPHEMERAL "$NACOS_EPHEMERAL" "$compose_env"
  write_jvm_options "$ENV_DIR/java-jvm.options" "$GLOBAL_JVM_INITIAL_RAM_PERCENTAGE" "$GLOBAL_JVM_MAX_RAM_PERCENTAGE"
  for service in $(service_order); do
    resource_file="$(resource_file_for_service "$service")"
    : > "$resource_file"
    set_env APP_CPUS "$(service_cpus "$service")" "$resource_file"
    set_env APP_MEMORY_LIMIT "$(service_memory "$service")" "$resource_file"
    chmod 0644 "$resource_file"
    if is_java_service "$service"; then
      write_jvm_options "$ENV_DIR/java-jvm.$service.options" "$(service_jvm_initial "$service")" "$(service_jvm_max "$service")"
    fi
  done
  java_start_command > "$ENV_DIR/java-entrypoint.sh"
  chmod 0755 "$ENV_DIR/java-entrypoint.sh"
}

[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
write_runtime_files
echo "AIFAR runtime config prepared, version $CONFIG_VERSION"
