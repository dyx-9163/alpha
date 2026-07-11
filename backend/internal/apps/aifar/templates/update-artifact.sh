#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
WORK_DIR={{ quote .WorkDir }}
RELEASE_DIR={{ quote .ReleaseDir }}
SERVICE_ORDER={{ quote .ServiceOrder }}
SERVICE_NAME={{ quote .ServiceName }}
DESIRED_REPLICAS={{ quote .DesiredReplicas }}
ARTIFACT_REMOTE={{ quote .ArtifactRemote }}
RELEASE_ARTIFACT={{ quote .ReleaseArtifact }}
ARTIFACT_FILE={{ quote .ArtifactFileName }}
ARTIFACT_SHA256={{ quote .ArtifactSHA256 }}
ARTIFACT_SIZE={{ .ArtifactSize }}
VERSION={{ quote .Version }}
REVISION={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
INGRESS_NETWORK={{ quote .IngressNetwork }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
APP_DIR="$RUNTIME_DIR/services"
ENV_DIR="$RUNTIME_DIR/env"
LOG_DIR="$RUNTIME_DIR/logs"
SNAPSHOT_DIR="$RELEASE_DIR/services/$SERVICE_NAME/snapshot"
TMP_DIR="$INSTALL_ROOT/.rollout-$REVISION-$$"
DRAIN_SECONDS="${DRAIN_SECONDS:-30}"
ORCHESTRATION_MODEL="agent-runtime-v2"

fail() {
  echo "ERROR: $*" >&2
  exit 1
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

set_env() {
  key="$1"
  value="$2"
  file="$3"
  tmp="$file.tmp"
  [ -f "$file" ] || touch "$file"
  grep -v "^${key}=" "$file" > "$tmp" || true
  printf "%s=%s\n" "$key" "$value" >> "$tmp"
  mv "$tmp" "$file"
}

json_escape() {
  printf "%s" "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

nacos_ephemeral() {
  value="$(read_env_value "$ENV_DIR/compose.env" AIFAR_NACOS_EPHEMERAL true)"
  case "$(printf "%s" "$value" | tr '[:upper:]' '[:lower:]')" in
    false|0|no|off) printf "false" ;;
    *) printf "true" ;;
  esac
}

check_agent_dependency() {
  command -v aifar-agent >/dev/null 2>&1 || fail "aifar-agent is required; install or upgrade Docker runtime first"
  aifar-agent status >/dev/null 2>&1 || fail "aifar-agent service is not reachable; install or upgrade Docker runtime first"
}

service_known() {
  for service in $SERVICE_ORDER; do
    [ "$service" = "$SERVICE_NAME" ] && return 0
  done
  return 1
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

write_jvm_options_if_missing() {
  file="$1"
  [ -f "$file" ] && return 0
  initial="$(read_env_value "$ENV_DIR/compose.env" JVM_INITIAL_RAM_PERCENTAGE 20)"
  max="$(read_env_value "$ENV_DIR/compose.env" JVM_MAX_RAM_PERCENTAGE 70)"
  cat > "$file" <<EOF
-XX:+UseContainerSupport
-XX:InitialRAMPercentage=${initial}
-XX:MaxRAMPercentage=${max}
-XX:+ExitOnOutOfMemoryError
EOF
  chmod 0644 "$file"
}

ensure_runtime_config_files() {
  compose_env="$ENV_DIR/compose.env"
  cpus="$(read_env_value "$compose_env" APP_CPUS "")"
  memory="$(read_env_value "$compose_env" APP_MEMORY_LIMIT "")"
  resource_file="$(resource_file_for_service "$SERVICE_NAME")"
  [ -f "$resource_file" ] || {
    : > "$resource_file"
    set_env APP_CPUS "$cpus" "$resource_file"
    set_env APP_MEMORY_LIMIT "$memory" "$resource_file"
    chmod 0644 "$resource_file"
  }
  if [ "$SERVICE_NAME" != "web-vue3" ]; then
    write_jvm_options_if_missing "$ENV_DIR/java-jvm.options"
    write_jvm_options_if_missing "$ENV_DIR/java-jvm.$SERVICE_NAME.options"
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

health_cmd_for_service() {
  service="$1"
  port="$(service_port "$service")"
  protocol="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_PROTOCOL http)"
  host="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_HOST 127.0.0.1)"
  timeout="$(read_env_value "$ENV_DIR/compose.env" APP_HEALTH_CONNECT_TIMEOUT 3)"
  if [ "$service" = "web-vue3" ]; then
    path="$(read_env_value "$ENV_DIR/compose.env" APP_WEB_HEALTH_PATH "/")"
    printf "wget -q -T %s -O /dev/null %s://%s:%s%s || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  else
    path="$(read_env_value "$ENV_DIR/$service.env" HEALTH_PATH "")"
    [ -n "$path" ] || path="$(read_env_value "$ENV_DIR/compose.env" APP_BACKEND_HEALTH_PATH "/actuator/health/readiness")"
    [ -n "$path" ] || path="/actuator/health/readiness"
    printf "curl -fsS --connect-timeout %s %s://%s:%s%s >/dev/null || exit 1" "$timeout" "$protocol" "$host" "$port" "$path"
  fi
}

desired_replicas_for_service() {
  value=""
  for pair in $DESIRED_REPLICAS; do
    case "$pair" in
      "$SERVICE_NAME="*) value="${pair#*=}" ;;
    esac
  done
  case "$value" in ""|*[!0-9]*) value=1 ;; esac
  [ "$value" -ge 0 ] || value=0
  printf "%s" "$value"
}

desired_replicas_from_env() {
  wanted="$1"
  for pair in $(read_env_value "$ENV_DIR/compose.env" AIFAR_DESIRED_REPLICAS ""); do
    case "$pair" in
      "$wanted="*) printf "%s" "${pair#*=}"; return 0 ;;
    esac
  done
  return 1
}

current_replicas_for_service() {
  docker ps --filter "label=aifar.app=aifar" \
    --filter "label=aifar.install-root=$INSTALL_ROOT" \
    --filter "label=aifar.component=pod" \
    --filter "label=aifar.service=$1" \
    --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null | wc -l | tr -d ' '
}

replicas_for_service() {
  service="$1"
  if [ "$service" = "$SERVICE_NAME" ]; then
    desired_replicas_for_service
    return
  fi
  if value="$(desired_replicas_from_env "$service")"; then
    case "$value" in ""|*[!0-9]*) value=1 ;; esac
    [ "$value" -ge 0 ] || value=0
    printf "%s" "$value"
    return
  fi
  replicas="$(current_replicas_for_service "$service")"
  case "$replicas" in ""|*[!0-9]*) replicas=1 ;; esac
  [ "$replicas" -ge 0 ] || replicas=0
  if [ "$replicas" -eq 0 ]; then
    replicas=1
  fi
  printf "%s" "$replicas"
}

write_desired_replicas_env() {
  pairs=""
  for service in $SERVICE_ORDER; do
    [ -f "$ENV_DIR/$service.env" ] || continue
    replicas="$(replicas_for_service "$service")"
    case "$replicas" in ""|*[!0-9]*) replicas=1 ;; esac
    [ "$replicas" -ge 0 ] || replicas=0
    if [ -z "$pairs" ]; then
      pairs="$service=$replicas"
    else
      pairs="$pairs $service=$replicas"
    fi
  done
  set_env AIFAR_DESIRED_REPLICAS "$pairs" "$ENV_DIR/compose.env"
}

pod_name() {
  service="$1"
  revision="$2"
  replica="$3"
  printf "aifar-pod-admin-%s-%s-r%s" "$service" "$revision" "$replica" | tr '. _/' '----'
}

retag_image() {
  image="$1"
  case "$image" in
    *@*) printf "%s" "$image" ;;
    *:*) printf "%s:%s" "${image%:*}" "$REVISION" ;;
    *) printf "%s:%s" "$image" "$REVISION" ;;
  esac
}

install_java_artifact() {
  service_dir="$1"
  artifact_remote="$2"
  artifact_file="$3"
  artifact_name="$(basename "$artifact_file")"
  mkdir -p "$service_dir" "$service_dir/target"
  cp "$artifact_remote" "$service_dir/app.jar"
  for old in "$service_dir"/target/*.jar; do
    [ -e "$old" ] || continue
    rm -f "$old"
  done
  cp "$artifact_remote" "$service_dir/target/$artifact_name"
}

snapshot_current_state() {
  mkdir -p "$SNAPSHOT_DIR" "$RELEASE_DIR/snapshot" "$(dirname "$RELEASE_ARTIFACT")"
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  [ -f "$service_env" ] && cp "$service_env" "$SNAPSHOT_DIR/before.env" || true
  [ -f "$INSTALL_ROOT/runtime/agent/runtime-spec.json" ] && cp "$INSTALL_ROOT/runtime/agent/runtime-spec.json" "$RELEASE_DIR/snapshot/before-runtime-spec.json" || true
  image="$(read_env_value "$service_env" APP_IMAGE "")"
  revision="$(read_env_value "$service_env" AIFAR_REVISION "")"
  printf "%s" "$image" > "$SNAPSHOT_DIR/before-image.txt"
  printf "%s" "$revision" > "$SNAPSHOT_DIR/before-revision.txt"
  case "$SERVICE_NAME" in
    web-vue3)
      if [ -d "$APP_DIR/$SERVICE_NAME/dist" ]; then
        rm -rf "$SNAPSHOT_DIR/dist"
        cp -a "$APP_DIR/$SERVICE_NAME/dist" "$SNAPSHOT_DIR/dist"
      fi
      ;;
    *)
      [ -f "$APP_DIR/$SERVICE_NAME/app.jar" ] && cp "$APP_DIR/$SERVICE_NAME/app.jar" "$SNAPSHOT_DIR/app.jar" || true
      ;;
  esac
}

stage_release_artifact() {
  mkdir -p "$(dirname "$RELEASE_ARTIFACT")"
  cp "$ARTIFACT_REMOTE" "$RELEASE_ARTIFACT"
  printf "%s  %s\n" "$ARTIFACT_SHA256" "$(basename "$RELEASE_ARTIFACT")" > "$(dirname "$RELEASE_ARTIFACT")/sha256"
}

apply_artifact() {
  service_dir="$APP_DIR/$SERVICE_NAME"
  [ -d "$service_dir" ] || fail "service directory is missing: $SERVICE_NAME"
  [ -f "$RELEASE_ARTIFACT" ] || fail "artifact file is missing: $RELEASE_ARTIFACT"
  actual="$(sha256sum "$RELEASE_ARTIFACT" | awk '{print $1}')"
  [ "$actual" = "$ARTIFACT_SHA256" ] || fail "artifact checksum mismatch for $ARTIFACT_FILE"
  case "$SERVICE_NAME" in
    web-vue3)
      rm -rf "$service_dir/dist" "$service_dir/html" "$TMP_DIR"
      mkdir -p "$TMP_DIR/web"
      case "$ARTIFACT_FILE" in
        *.zip) unzip -q "$RELEASE_ARTIFACT" -d "$TMP_DIR/web" ;;
        *.tar|*.tgz|*.tar.gz) tar -xf "$RELEASE_ARTIFACT" -C "$TMP_DIR/web" ;;
        *) fail "unsupported web artifact type: $ARTIFACT_FILE" ;;
      esac
      if [ -d "$TMP_DIR/web/dist" ]; then
        cp -a "$TMP_DIR/web/dist" "$service_dir/dist"
      else
        mkdir -p "$service_dir/dist"
        cp -a "$TMP_DIR/web/." "$service_dir/dist/"
      fi
      ;;
    *)
      install_java_artifact "$service_dir" "$RELEASE_ARTIFACT" "$ARTIFACT_FILE"
      ;;
  esac
}

ensure_service_runtime_env() {
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  [ -f "$service_env" ] || fail "service env is missing: $SERVICE_NAME"
  image="$(read_env_value "$service_env" APP_IMAGE "")"
  [ -n "$image" ] || image="aifar-$SERVICE_NAME:$REVISION"
  new_image="$(retag_image "$image")"
  set_env APP_IMAGE "$new_image" "$service_env"
  set_env APP_CONTAINER_NAME "$(pod_name "$SERVICE_NAME" "$REVISION" 1)" "$service_env"
  set_env AIFAR_REVISION "$REVISION" "$service_env"
  if [ "$SERVICE_NAME" != "web-vue3" ]; then
    port="$(service_port "$SERVICE_NAME")"
    [ -n "$port" ] || fail "service port is missing: $SERVICE_NAME"
    set_env SERVER_PORT "$port" "$service_env"
    set_env SPRING_CLOUD_NACOS_DISCOVERY_REGISTER_ENABLED false "$service_env"
  fi
}

build_image() {
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  image="$(read_env_value "$service_env" APP_IMAGE "")"
  [ -n "$image" ] || fail "service image is empty: $SERVICE_NAME"
  docker build -t "$image" "$APP_DIR/$SERVICE_NAME"
}

reconcile_runtime() {
  spec="$INSTALL_ROOT/runtime/agent/runtime-spec.json"
  check_agent_dependency
  write_desired_replicas_env
  write_runtime_spec >/dev/null
  [ -f "$spec" ] || fail "AIFAR runtime spec is missing: $spec"
  aifar-agent reconcile-runtime --spec "$spec"
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
  for service in $SERVICE_ORDER; do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$REVISION")"
    service_revision="$(read_env_value "$service_env" AIFAR_REVISION "$REVISION")"
    replicas="$(replicas_for_service "$service")"
    port="$(service_port "$service")"
    health_cmd="$(health_cmd_for_service "$service")"
    app_cpus="$(resource_value "$service" APP_CPUS "$(read_env_value "$ENV_DIR/compose.env" APP_CPUS "")")"
    app_memory_limit="$(resource_value "$service" APP_MEMORY_LIMIT "$(read_env_value "$ENV_DIR/compose.env" APP_MEMORY_LIMIT "")")"
    log_dir="$LOG_DIR/$service"
    mkdir -p "$log_dir"
    if [ "$first_deployment" = "1" ]; then
      first_deployment=0
    else
      printf ",\n" >> "$spec"
    fi
    deployment_name="$(alpha_service_name "$service")"
    printf '    {\n' >> "$spec"
    printf '      "serviceName": "%s",\n' "$service" >> "$spec"
    printf '      "deploymentName": "%s",\n' "$(json_escape "$deployment_name")" >> "$spec"
    printf '      "image": "%s",\n' "$(json_escape "$image")" >> "$spec"
    printf '      "podRevision": "%s",\n' "$(json_escape "$service_revision")" >> "$spec"
    printf '      "replicas": %s,\n' "$replicas" >> "$spec"
    printf '      "ports": [{"name":"http","containerPort":%s}],\n' "$port" >> "$spec"
    if [ "$service" = "web-vue3" ]; then
      printf '      "envFiles": ["%s"],\n' "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/logs","readOnly":false},{"source":"%s","target":"/var/log/nginx","readOnly":false}],\n' "$(json_escape "$log_dir")" "$(json_escape "$log_dir")" >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_LOG_DIR":"/opt/aifar/logs","LOG_DIR":"/opt/aifar/logs","TZ":"%s"},\n' "$(json_escape "$tz_value")" >> "$spec"
    else
      printf '      "envFiles": ["%s","%s","%s"],\n' "$(json_escape "$ENV_DIR/java-common.env")" "$(json_escape "$ENV_DIR/java-secrets.env")" "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/runtime/env","readOnly":true},{"source":"%s","target":"/opt/aifar/logs","readOnly":false},{"source":"%s","target":"/data/aifarsoft/javaApi/aifar-%s/log","readOnly":false}],\n' "$(json_escape "$ENV_DIR")" "$(json_escape "$log_dir")" "$(json_escape "$log_dir")" "$(json_escape "$service")" >> "$spec"
      printf '      "entrypoint": ["/bin/sh"],\n' >> "$spec"
      printf '      "command": ["/opt/aifar/runtime/env/java-entrypoint.sh"],\n' >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_SERVICE_NAME":"%s","AIFAR_LOG_DIR":"/opt/aifar/logs","LOG_DIR":"/opt/aifar/logs","TZ":"%s"},\n' "$service" "$(json_escape "$tz_value")" >> "$spec"
    fi
    printf '      "resources": {"cpus":"%s","memory":"%s"},\n' "$(json_escape "$app_cpus")" "$(json_escape "$app_memory_limit")" >> "$spec"
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
  for service in $SERVICE_ORDER; do
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
    "ephemeral": $(nacos_ephemeral),
    "agentIPStrategy": "auto"
  }
}
JSON
  printf "%s" "$spec"
}

stop_old_pods() {
  old="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$SERVICE_NAME" --format '{{ "{{" }}.Names{{ "}}" }}|{{ "{{" }}.Label "aifar.revision"{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$old" ] && return 0
  sleep "$DRAIN_SECONDS"
  printf "%s\n" "$old" | while IFS='|' read -r name revision; do
    [ -n "$name" ] || continue
    [ "$revision" != "$REVISION" ] || continue
    docker rm -f "$name" >/dev/null 2>&1 || true
  done
}

write_model_manifest() {
  mkdir -p "$INSTALL_ROOT/.aifar"
  cat > "$INSTALL_ROOT/.aifar/last-rollout.json" <<JSON
{
  "model": "${ORCHESTRATION_MODEL}",
  "kind": "rollout",
  "service": "${SERVICE_NAME}",
  "revision": "${REVISION}",
  "version": "${VERSION}",
  "createdAt": "${CREATED_AT}",
  "configHash": "${CONFIG_HASH}",
  "artifact": {
    "file": "${ARTIFACT_FILE}",
    "sha256": "${ARTIFACT_SHA256}",
    "size": ${ARTIFACT_SIZE}
  }
}
JSON
}

cleanup_failed_rollout() {
  pods="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$SERVICE_NAME" --filter "label=aifar.revision=$REVISION" --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true)"
  [ -z "$pods" ] || docker rm -f $pods >/dev/null 2>&1 || true
}

restore_previous_runtime() {
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  [ -f "$SNAPSHOT_DIR/before.env" ] && cp "$SNAPSHOT_DIR/before.env" "$service_env" || true
  [ -f "$RELEASE_DIR/snapshot/before-runtime-spec.json" ] && cp "$RELEASE_DIR/snapshot/before-runtime-spec.json" "$INSTALL_ROOT/runtime/agent/runtime-spec.json" || true
  if [ -f "$INSTALL_ROOT/runtime/agent/runtime-spec.json" ]; then
    aifar-agent reconcile-runtime --spec "$INSTALL_ROOT/runtime/agent/runtime-spec.json" >/dev/null 2>&1 || true
  fi
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
check_agent_dependency
service_known || fail "unsupported AIFAR service: $SERVICE_NAME"
[ -d "$APP_DIR" ] || fail "AIFAR runtime app directory is missing"
[ -d "$ENV_DIR" ] || fail "AIFAR runtime env directory is missing"
[ -f "$INSTALL_ROOT/.aifar/model.json" ] || fail "AIFAR agent-runtime-v2 model manifest is missing"
grep -q '"model"[[:space:]]*:[[:space:]]*"agent-runtime-v2"' "$INSTALL_ROOT/.aifar/model.json" || fail "AIFAR_RUNTIME_REINSTALL_REQUIRED: reinstall AIFAR with agent-runtime-v2"

trap 'cleanup_failed_rollout' INT TERM
java_start_command > "$ENV_DIR/java-entrypoint.sh"
chmod 0755 "$ENV_DIR/java-entrypoint.sh"
snapshot_current_state
stage_release_artifact
apply_artifact
ensure_service_runtime_env
ensure_runtime_config_files
build_image
if ! reconcile_runtime; then
  cleanup_failed_rollout
  restore_previous_runtime
  fail "AIFAR runtime reconcile failed: $SERVICE_NAME"
fi
write_model_manifest
echo "AIFAR Deployment rollout completed: $SERVICE_NAME -> $REVISION"
