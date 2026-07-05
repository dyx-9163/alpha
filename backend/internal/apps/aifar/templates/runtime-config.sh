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
}

container_names_for_service() {
  docker ps -a \
    --filter "label=aifar.app=aifar" \
    --filter "label=aifar.install-root=$INSTALL_ROOT" \
    --filter "label=aifar.component=pod" \
    --filter "label=aifar.service=$1" \
    --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true
}

container_status() {
  docker inspect --format '{{ "{{" }}.State.Status{{ "}}" }}' "$1" 2>/dev/null || true
}

container_health() {
  docker inspect --format '{{ "{{" }}if .State.Health{{ "}}" }}{{ "{{" }}.State.Health.Status{{ "}}" }}{{ "{{" }}end{{ "}}" }}' "$1" 2>/dev/null || true
}

container_label() {
  docker inspect --format '{{ "{{" }}index .Config.Labels "'$2'"{{ "}}" }}' "$1" 2>/dev/null || true
}

container_image() {
  docker inspect --format '{{ "{{" }}.Config.Image{{ "}}" }}' "$1" 2>/dev/null || true
}

container_restart_policy() {
  docker inspect --format '{{ "{{" }}.HostConfig.RestartPolicy.Name{{ "}}" }}' "$1" 2>/dev/null || true
}

wait_container_ready() {
  container="$1"
  timeout="$(read_env_value "$ENV_DIR/compose.env" APP_STARTUP_TIMEOUT 300)"
  case "$timeout" in ""|*[!0-9]*) timeout=300 ;; esac
  deadline=$(( $(date +%s) + timeout ))
  while [ "$(date +%s)" -lt "$deadline" ]; do
    status="$(container_status "$container")"
    health="$(container_health "$container")"
    if [ "$status" = "running" ] && { [ -z "$health" ] || [ "$health" = "healthy" ]; }; then
      echo "AIFAR Pod ready: $container"
      return 0
    fi
    echo "$container status=${status:-missing} health=${health:-none}"
    sleep 5
  done
  docker logs --tail 120 "$container" >&2 || true
  return 1
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

update_container_resources() {
  service="$1"
  container="$2"
  cpus="$(resource_value "$service" APP_CPUS "$(service_cpus "$service")")"
  memory="$(resource_value "$service" APP_MEMORY_LIMIT "$(service_memory "$service")")"
  [ -z "$cpus" ] || docker update --cpus "$cpus" "$container" >/dev/null
  [ -z "$memory" ] || docker update --memory "$memory" --memory-swap "$memory" "$container" >/dev/null
}

restart_dynamic_java_container() {
  container="$1"
  docker restart "$container" >/dev/null
  wait_container_ready "$container"
}

recreate_java_container() {
  service="$1"
  container="$2"
  image="$(container_image "$container")"
  [ -n "$image" ] || fail "cannot inspect image for $container"
  revision="$(container_label "$container" "aifar.revision")"
  [ -n "$revision" ] || revision="$(read_env_value "$ENV_DIR/compose.env" AIFAR_REVISION "unknown")"
  replica="$(container_label "$container" "aifar.replica")"
  case "$replica" in ""|*[!0-9]*) replica=1 ;; esac
  restart_policy="$(container_restart_policy "$container")"
  [ -n "$restart_policy" ] && [ "$restart_policy" != "no" ] || restart_policy="$(read_env_value "$ENV_DIR/compose.env" APP_RESTART_POLICY unless-stopped)"
  port="$(service_port "$service")"
  cpus="$(resource_value "$service" APP_CPUS "$(service_cpus "$service")")"
  memory="$(resource_value "$service" APP_MEMORY_LIMIT "$(service_memory "$service")")"
  health_cmd="$(health_cmd_for_service "$service")"
  health_interval="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_INTERVAL 15s)"
  health_timeout="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_TIMEOUT 5s)"
  health_retries="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_RETRIES 3)"
  health_start_period="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_START_PERIOD 30s)"
  tz_value="$(read_env_value "$ENV_DIR/compose.env" TZ system)"
  resource_args=""
  [ -z "$cpus" ] || resource_args="$resource_args --cpus $cpus"
  [ -z "$memory" ] || resource_args="$resource_args --memory $memory --memory-swap $memory"
  java_cmd="$(java_start_command)"
  docker rm -f "$container" >/dev/null 2>&1 || true
  # shellcheck disable=SC2086
  docker run -d \
    --name "$container" \
    --restart no \
    --label aifar.app=aifar \
    --label "aifar.install-root=$INSTALL_ROOT" \
    --label "aifar.component=pod" \
    --label "aifar.service=$service" \
    --label "aifar.revision=$revision" \
    --label "aifar.release=$revision" \
    --label "aifar.pod=$service-$revision-r$replica" \
    --label "aifar.replica=$replica" \
    --label aifar.dynamic-jvm=true \
    --network "$INGRESS_NETWORK" \
    $resource_args \
    --health-cmd "$health_cmd" \
    --health-interval "$health_interval" \
    --health-timeout "$health_timeout" \
    --health-retries "$health_retries" \
    --health-start-period "$health_start_period" \
    -e "APP_CONTAINER_NAME=$container" \
    -e "AIFAR_SERVICE_NAME=$service" \
    -e "TZ=$tz_value" \
    --env-file "$ENV_DIR/java-common.env" \
    --env-file "$ENV_DIR/java-secrets.env" \
    --env-file "$ENV_DIR/$service.env" \
    -v "$ENV_DIR:/opt/aifar/runtime/env:ro" \
    --entrypoint /bin/sh \
    "$image" -c "$java_cmd" >/dev/null
  wait_container_ready "$container"
  docker update --restart "$restart_policy" "$container" >/dev/null 2>&1 || true
}

reconcile_runtime() {
  spec="$INSTALL_ROOT/runtime/agent/runtime-spec.json"
  command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required"
  [ -f "$spec" ] || fail "AIFAR runtime spec is missing: $spec"
  aifar-agent reconcile-runtime --spec "$spec"
}

apply_service() {
  service="$1"
  containers="$(container_names_for_service "$service")"
  [ -n "$containers" ] || return 0
  for container in $containers; do
    echo "applying AIFAR runtime resources: service=$service container=$container"
    update_container_resources "$service" "$container"
    if is_java_service "$service"; then
      dynamic="$(container_label "$container" "aifar.dynamic-jvm")"
      if service_restart_required "$service" || [ "$dynamic" != "true" ]; then
        if [ "$dynamic" = "true" ]; then
          restart_dynamic_java_container "$container"
        else
          recreate_java_container "$service" "$container"
        fi
        reconcile_runtime
      fi
    fi
  done
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
INGRESS_NETWORK="$(read_env_value "$ENV_DIR/compose.env" AIFAR_NETWORK aifar-network)"

write_runtime_files
for service in $(service_order); do
  apply_service "$service"
done
reconcile_runtime
echo "AIFAR runtime config applied, version $CONFIG_VERSION"
