#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
CONFIG_VERSION={{ .ConfigVersion }}
GLOBAL_APP_CPUS={{ quote .GlobalAppCPUs }}
GLOBAL_APP_MEMORY_LIMIT={{ quote .GlobalAppMemoryLimit }}
GLOBAL_JVM_INITIAL_RAM_PERCENTAGE={{ quote .GlobalJVMInitialRAMPercentage }}
GLOBAL_JVM_MAX_RAM_PERCENTAGE={{ quote .GlobalJVMMaxRAMPercentage }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
ENV_DIR="$RUNTIME_DIR/env"
INGRESS_NETWORK=""

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

read_env_value() {
  file="$1"
  key="$2"
  fallback="${3:-}"
  if [ -f "$file" ]; then
    value="$(awk -F= -v k="$key" '$1==k {print substr($0, index($0, "=")+1)}' "$file" | tail -n 1)"
    if [ -n "$value" ]; then
      printf "%s" "$value"
      return 0
    fi
  fi
  printf "%s" "$fallback"
}

json_escape() {
  printf "%s" "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
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

service_restart_required() {
  case "$1" in
{{- range .Services }}
    {{ .Name }}) {{ if .Restart }}return 0{{ else }}return 1{{ end }} ;;
{{- end }}
    *) return 1 ;;
  esac
}

is_java_service() {
  [ "$1" != "web-vue3" ]
}

service_port_var() {
  case "$1" in
    gateway) printf "GATEWAY_PORT" ;;
    oauth) printf "OAUTH_PORT" ;;
    permission) printf "PERMISSION_PORT" ;;
    system) printf "SYSTEM_PORT" ;;
    file) printf "FILE_PORT" ;;
    message) printf "MESSAGE_PORT" ;;
    im) printf "IM_PORT" ;;
    contacts) printf "CONTACTS_PORT" ;;
    meeting) printf "MEETING_PORT" ;;
    web-vue3) printf "WEB_VUE3_PORT" ;;
    *) printf "" ;;
  esac
}

service_port() {
  var="$(service_port_var "$1")"
  [ -n "$var" ] || fail "unsupported service port: $1"
  read_env_value "$ENV_DIR/compose.env" "$var" ""
}

alpha_service_name() {
  case "$1" in
    gateway) printf "alpha-gateway" ;;
    oauth) printf "alpha-oauth" ;;
    permission) printf "alpha-permission" ;;
    system) printf "alpha-system" ;;
    file) printf "alpha-file" ;;
    message) printf "alpha-message" ;;
    im) printf "alpha-im" ;;
    contacts) printf "alpha-contacts" ;;
    meeting) printf "alpha-meeting" ;;
    *) printf "" ;;
  esac
}

resource_file_for_service() {
  printf "%s/resource.%s.env" "$ENV_DIR" "$1"
}

resource_value() {
  service="$1"
  key="$2"
  fallback="$3"
  file="$(resource_file_for_service "$service")"
  read_env_value "$file" "$key" "$fallback"
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

write_runtime_files() {
  mkdir -p "$ENV_DIR"
  compose_env="$ENV_DIR/compose.env"
  set_env AIFAR_RUNTIME_CONFIG_VERSION "$CONFIG_VERSION" "$compose_env"
  set_env APP_CPUS "$GLOBAL_APP_CPUS" "$compose_env"
  set_env APP_MEMORY_LIMIT "$GLOBAL_APP_MEMORY_LIMIT" "$compose_env"
  set_env JVM_INITIAL_RAM_PERCENTAGE "$GLOBAL_JVM_INITIAL_RAM_PERCENTAGE" "$compose_env"
  set_env JVM_MAX_RAM_PERCENTAGE "$GLOBAL_JVM_MAX_RAM_PERCENTAGE" "$compose_env"
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

container_names_for_service() {
  docker ps -a \
    --filter "label=aifar.app=aifar" \
    --filter "label=aifar.install-root=$INSTALL_ROOT" \
    --filter "label=aifar.component=pod" \
    --filter "label=aifar.service=$1" \
    --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true
}

current_replicas_for_service() {
  container_names_for_service "$1" | wc -l | tr -d ' '
}

health_cmd_for_service() {
  service="$1"
  port="$(service_port "$service")"
  protocol="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PROTOCOL http)"
  host="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_HOST 127.0.0.1)"
  path="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PATH "")"
  timeout="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_CONNECT_TIMEOUT 3)"
  if [ "$service" = "web-vue3" ]; then
    [ -n "$path" ] || path="/"
    printf "wget -q -T %s -O /dev/null %s://%s:%s%s || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  elif [ -n "$path" ]; then
    printf "curl -fsS --connect-timeout %s %s://%s:%s%s >/dev/null || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  else
    printf "curl -sS --connect-timeout %s -o /dev/null %s://%s:%s/ || exit 1" "$timeout" "$protocol" "$host" "$port"
  fi
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

write_runtime_spec() {
  spec="$INSTALL_ROOT/runtime/agent/runtime-spec.json"
  gateway_port="$(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)"
  web_port="$(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)"
  nacos_ns="$(read_env_value "$ENV_DIR/java-common.env" NACOS_NS prod)"
  tz_value="$(read_env_value "$ENV_DIR/compose.env" TZ system)"
  mkdir -p "$INSTALL_ROOT/runtime/agent"
  cat > "$spec" <<JSON
{
  "version": "runtime-v2",
  "instanceId": "admin",
  "installRoot": "${INSTALL_ROOT}",
  "network": "${INGRESS_NETWORK}",
  "deployments": [
JSON
  first_deployment=1
  for service in $(service_order); do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:latest")"
    revision="$(read_env_value "$service_env" AIFAR_REVISION "$(read_env_value "$ENV_DIR/compose.env" AIFAR_REVISION current)")"
    replicas="$(current_replicas_for_service "$service")"
    case "$replicas" in ""|*[!0-9]*) replicas=1 ;; esac
    [ "$replicas" -ge 1 ] || replicas=1
    port="$(service_port "$service")"
    health_cmd="$(health_cmd_for_service "$service")"
    cpus="$(resource_value "$service" APP_CPUS "$(service_cpus "$service")")"
    memory="$(resource_value "$service" APP_MEMORY_LIMIT "$(service_memory "$service")")"
    deployment_name="$(alpha_service_name "$service")"
    if [ "$first_deployment" = "1" ]; then
      first_deployment=0
    else
      printf ",\n" >> "$spec"
    fi
    printf '    {\n' >> "$spec"
    printf '      "serviceName": "%s",\n' "$service" >> "$spec"
    printf '      "deploymentName": "%s",\n' "$(json_escape "$deployment_name")" >> "$spec"
    printf '      "image": "%s",\n' "$(json_escape "$image")" >> "$spec"
    printf '      "podRevision": "%s",\n' "$(json_escape "$revision")" >> "$spec"
    printf '      "replicas": %s,\n' "$replicas" >> "$spec"
    printf '      "ports": [{"name":"http","containerPort":%s}],\n' "$port" >> "$spec"
    if [ "$service" = "web-vue3" ]; then
      printf '      "envFiles": ["%s"],\n' "$(json_escape "$service_env")" >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","TZ":"%s"},\n' "$(json_escape "$tz_value")" >> "$spec"
    else
      printf '      "envFiles": ["%s","%s","%s"],\n' "$(json_escape "$ENV_DIR/java-common.env")" "$(json_escape "$ENV_DIR/java-secrets.env")" "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/runtime/env","readOnly":true}],\n' "$(json_escape "$ENV_DIR")" >> "$spec"
      printf '      "entrypoint": ["/bin/sh"],\n' >> "$spec"
      printf '      "command": ["/opt/aifar/runtime/env/java-entrypoint.sh"],\n' >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_SERVICE_NAME":"%s","TZ":"%s"},\n' "$service" "$(json_escape "$tz_value")" >> "$spec"
    fi
    printf '      "resources": {"cpus":"%s","memory":"%s"},\n' "$(json_escape "$cpus")" "$(json_escape "$memory")" >> "$spec"
    printf '      "healthCheck": {"command":"%s","interval":"%s","timeout":"%s","retries":%s,"startPeriod":"%s"}\n' \
      "$(json_escape "$health_cmd")" \
      "$(json_escape "$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_INTERVAL 15s)")" \
      "$(json_escape "$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_TIMEOUT 5s)")" \
      "$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_RETRIES 3)" \
      "$(json_escape "$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_START_PERIOD 30s)")" >> "$spec"
    printf '    }' >> "$spec"
  done
  cat >> "$spec" <<JSON
  ],
  "services": [
JSON
  first_service=1
  for service in $(service_order); do
    [ -f "$ENV_DIR/$service.env" ] || continue
    port="$(service_port "$service")"
    app_name="$(alpha_service_name "$service")"
    if [ "$first_service" = "1" ]; then
      first_service=0
    else
      printf ",\n" >> "$spec"
    fi
    affinity="round-robin"
    case "$service" in gateway|file) affinity="stable" ;; esac
    printf '    {"name":"%s","appName":"%s","listenPort":%s,"targetPort":%s,"affinityPolicy":"%s"}' "$service" "$app_name" "$port" "$port" "$affinity" >> "$spec"
  done
  cat >> "$spec" <<JSON
  ],
  "ingress": {
    "mode": "web-nginx",
    "gatewayService": "gateway",
    "webService": "web-vue3",
    "gatewayPort": ${gateway_port},
    "webPort": ${web_port}
  },
  "nacos": {
    "namespace": "${nacos_ns}",
    "group": "DEFAULT_GROUP",
    "ephemeral": true,
    "agentIPStrategy": "auto"
  }
}
JSON
  printf "%s" "$spec"
}

reconcile_runtime() {
  spec="$INSTALL_ROOT/runtime/agent/runtime-spec.json"
  command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required"
  write_runtime_spec >/dev/null
  [ -f "$spec" ] || fail "AIFAR runtime spec is missing: $spec"
  aifar-agent reconcile-runtime --spec "$spec"
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
INGRESS_NETWORK="$(read_env_value "$ENV_DIR/compose.env" AIFAR_NETWORK aifar-network)"

write_runtime_files
reconcile_runtime
echo "AIFAR runtime config applied, version $CONFIG_VERSION"
