#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
INSTANCE_ID={{ quote .InstanceID }}
WORK_DIR={{ quote .WorkDir }}
ARCHIVE={{ quote .ArchiveRemote }}
AGENT_BINARY={{ quote .AgentBinaryRemote }}
SERVICE_ORDER={{ quote .ServiceOrder }}
SERVICE_APPLICATIONS={{ quote .ServiceApplications }}
SERVICE_PORTS={{ quote .ServicePorts }}
SERVICE_KINDS={{ quote .ServiceKinds }}
SERVICE_HEALTH_PATHS={{ quote .ServiceHealthPaths }}
SERVICE_AFFINITIES={{ quote .ServiceAffinities }}
SERVICE_SPEC_HASHES={{ quote .ServiceSpecHashes }}
GATEWAY_SERVICE={{ quote .GatewayService }}
WEB_SERVICE={{ quote .WebService }}
VERSION={{ quote .Version }}
REVISION={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
INGRESS_NETWORK={{ quote .IngressNetwork }}

RUNTIME_DIR="$INSTALL_ROOT/runtime"
APP_DIR="$RUNTIME_DIR/services"
IMAGE_DIR="$RUNTIME_DIR/images"
ENV_DIR="$RUNTIME_DIR/env"
LOG_DIR="$RUNTIME_DIR/logs"
AGENT_DIR="$RUNTIME_DIR/agent"
AIFAR_DIR="$INSTALL_ROOT/.aifar"
BUNDLE_DIR="$RUNTIME_DIR/bundle"
DEFAULT_ENV="$BUNDLE_DIR/defaults.env"
TMP_DIR="$INSTALL_ROOT/.extract-$REVISION-$$"
INSTALL_SUCCEEDED=0

TIMEZONE={{ quote .Options.Timezone }}
SPEC_TIMEZONE={{ quote .Options.Timezone }}
APP_CPUS={{ quote .Options.AppCPUs }}
APP_MEMORY_LIMIT={{ quote .Options.AppMemoryLimit }}
JVM_INITIAL_RAM_PERCENTAGE={{ quote .Options.JVMInitialRAMPercentage }}
JVM_MAX_RAM_PERCENTAGE={{ quote .Options.JVMMaxRAMPercentage }}
GATEWAY_PORT={{ quote .Options.GatewayPort }}
WEB_VUE3_PORT={{ quote .Options.WebPort }}
NACOS_PORT_WEB={{ quote .Options.NacosWebPort }}
NACOS_PORT_API={{ quote .Options.NacosAPIPort }}
NACOS_CONNECT_HOST={{ quote .Options.NacosHost }}
NACOS_USER={{ quote .Options.NacosUser }}
NACOS_PASSWORD={{ quote .Options.NacosPassword }}
NACOS_NS={{ quote .Options.NacosNamespace }}
NACOS_REGISTRATION_MODE="agent-proxy"
ORCHESTRATION_MODEL="agent-service-controller-v1"
AGENT_LISTEN_ADDR="127.0.0.1:18081"
SUDO=""
if [ "$(id -u)" != "0" ]; then
  SUDO="sudo -n"
fi

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
  fallback="$3"
  if [ -f "$file" ]; then
    value="$(awk -F= -v k="$key" '$1==k {sub(/^[^=]*=/,""); print; exit}' "$file" 2>/dev/null || true)"
    if [ -n "$value" ]; then
      printf "%s" "$value"
      return
    fi
  fi
  printf "%s" "$fallback"
}

sanitize_name() {
  printf "%s" "$1" | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9_.-]/-/g; s/^[._-]*//; s/[._-]*$//'
}

retag_image() {
  image="$1"
  case "$image" in
    *@*) printf "%s" "$image" ;;
    *:*) printf "%s:%s" "${image%:*}" "$REVISION" ;;
    *) printf "%s:%s" "$image" "$REVISION" ;;
  esac
}

pod_name() {
  printf "aifar-pod-admin-%s-%s-r%s" "$1" "$2" "$3" | tr '. _/' '----'
}

catalog_value() {
  pairs="$1"
  wanted="$2"
  for pair in $pairs; do
    case "$pair" in "$wanted="*) printf "%s" "${pair#*=}"; return 0 ;; esac
  done
  return 1
}

alpha_service_name() {
  catalog_value "$SERVICE_APPLICATIONS" "$1" || true
}

service_kind() {
  catalog_value "$SERVICE_KINDS" "$1" || printf "java"
}

service_port() {
  service="$1"
  value="$(read_env_value "$ENV_DIR/$service.env" AIFAR_SERVICE_PORT "")"
  [ -n "$value" ] || value="$(read_env_value "$ENV_DIR/$service.env" SERVER_PORT "")"
  [ -n "$value" ] || value="$(catalog_value "$SERVICE_PORTS" "$service" || true)"
  printf "%s" "${value:-0}"
}

{{ serviceAccessHelpers }}

open_service_ports() {
  ports=""
  for service in "$@"; do
    port="$(service_port "$service")"
    [ -n "$port" ] && [ "$port" != "0" ] || continue
    ports="$ports $port"
  done
  [ -n "$ports" ] || return 0
  # shellcheck disable=SC2086
  open_firewall_ports $ports
  # shellcheck disable=SC2086
  allow_selinux_ports http_port_t $ports
}

load_docker_images() {
  [ -d "$IMAGE_DIR" ] || return 0
  for image_tar in "$IMAGE_DIR"/*.tar; do
    [ -f "$image_tar" ] || continue
    docker load -i "$image_tar"
  done
}

require_local_image() {
  image="$1"
  docker image inspect "$image" >/dev/null 2>&1 || fail "required offline Docker image is missing after docker load: $image"
}

agent_status_ok() {
  command -v aifar-agent >/dev/null 2>&1 && wait_agent_status
}

wait_agent_status() {
  for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do
    if agent_runtime_status="$(aifar-agent status 2>/dev/null)" && agent_has_runtime_features "$agent_runtime_status"; then
      return 0
    fi
    sleep 1
  done
  return 1
}

agent_has_runtime_features() {
  status="$1"
  for feature in service-manifest-v1 service-generation-v1 per-service-reconcile per-service-restart service-conditions-v1 runtime-instance-snapshot-v1 durable-legacy-archive-v1 verified-bootstrap-stream-v1; do
    printf "%s" "$status" | grep -q "\"$feature\"" || return 1
  done
  return 0
}

install_agent_dependency() {
  if [ -z "$AGENT_BINARY" ] || [ ! -f "$AGENT_BINARY" ]; then
    if agent_status_ok; then
      echo "aifar-agent is already running"
      return 0
    fi
    fail "aifar-agent is not installed and no agent binary was uploaded; rebuild the backend or use a release package containing bin/aifar-agent-linux-amd64"
  fi
  echo "installing or upgrading AIFAR runtime agent"
  $SUDO mkdir -p /etc/aifar /var/lib/aifar-agent /var/log/aifar-agent
  $SUDO install -m 0755 "$AGENT_BINARY" /usr/local/bin/aifar-agent
  cat > "$WORK_DIR/aifar-agent.service" <<SERVICE
[Unit]
Description=AIFAR Runtime Agent
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/aifar-agent serve --addr $AGENT_LISTEN_ADDR
ExecStopPost=-/usr/local/bin/aifar-agent deregister-nacos --state-dir /var/lib/aifar-agent/instances
Restart=always
RestartSec=2
WorkingDirectory=/var/lib/aifar-agent

[Install]
WantedBy=multi-user.target
SERVICE
  $SUDO install -m 0644 "$WORK_DIR/aifar-agent.service" /etc/systemd/system/aifar-agent.service
  $SUDO systemctl daemon-reload
  if $SUDO systemctl is-active --quiet aifar-agent; then
    $SUDO systemctl enable aifar-agent >/dev/null 2>&1 || true
    agent_start_cmd="restart"
  else
    agent_start_cmd="enable --now"
  fi
  if ! $SUDO systemctl $agent_start_cmd aifar-agent; then
    echo "AIFAR runtime agent service failed to start"
    $SUDO systemctl --no-pager --full status aifar-agent || true
    $SUDO journalctl -u aifar-agent -n 80 --no-pager || true
    exit 1
  fi
  if ! wait_agent_status; then
    echo "aifar-agent service is not reachable after installation"
    $SUDO systemctl --no-pager --full status aifar-agent || true
    $SUDO journalctl -u aifar-agent -n 80 --no-pager || true
    exit 1
  fi
}

check_agent_dependency() {
  install_agent_dependency
}

resolve_system_timezone() {
  case "$TIMEZONE" in
    ""|"system"|"SYSTEM"|"System")
      detected=""
      if command -v timedatectl >/dev/null 2>&1; then
        detected="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
      fi
      if [ -z "$detected" ] && [ -f /etc/timezone ]; then
        detected="$(head -n 1 /etc/timezone 2>/dev/null || true)"
      fi
      TIMEZONE="${detected:-UTC}"
      ;;
  esac
}

ensure_network() {
  docker network inspect "$INGRESS_NETWORK" >/dev/null 2>&1 || docker network create "$INGRESS_NETWORK" >/dev/null
}

write_compose_env() {
  compose_env="$ENV_DIR/compose.env"
  : > "$compose_env"
  set_env ORCHESTRATION_MODEL "$ORCHESTRATION_MODEL" "$compose_env"
  set_env AIFAR_REVISION "$REVISION" "$compose_env"
  set_env AIFAR_NETWORK "$INGRESS_NETWORK" "$compose_env"
  set_env TZ "$TIMEZONE" "$compose_env"
  set_env AIFAR_RUNTIME_CONFIG_VERSION "1" "$compose_env"
  set_env APP_CPUS "$APP_CPUS" "$compose_env"
  set_env APP_MEMORY_LIMIT "$APP_MEMORY_LIMIT" "$compose_env"
  set_env JVM_INITIAL_RAM_PERCENTAGE "$JVM_INITIAL_RAM_PERCENTAGE" "$compose_env"
  set_env JVM_MAX_RAM_PERCENTAGE "$JVM_MAX_RAM_PERCENTAGE" "$compose_env"
  set_env AIFAR_NACOS_EPHEMERAL "true" "$compose_env"
  set_env APP_RESTART_POLICY "$(read_env_value "$DEFAULT_ENV" APP_RESTART_POLICY unless-stopped)" "$compose_env"
  set_env APP_HEALTH_PROTOCOL "$(read_env_value "$DEFAULT_ENV" APP_HEALTH_PROTOCOL http)" "$compose_env"
  set_env APP_HEALTH_HOST "$(read_env_value "$DEFAULT_ENV" APP_HEALTH_HOST 127.0.0.1)" "$compose_env"
  set_env APP_HEALTH_PATH "$(read_env_value "$DEFAULT_ENV" APP_HEALTH_PATH '')" "$compose_env"
  set_env APP_WEB_HEALTH_PATH "$(read_env_value "$DEFAULT_ENV" APP_WEB_HEALTH_PATH /)" "$compose_env"
  set_env APP_BACKEND_HEALTH_PATH "$(read_env_value "$DEFAULT_ENV" APP_BACKEND_HEALTH_PATH /actuator/health/readiness)" "$compose_env"
  set_env APP_HEALTH_CONNECT_TIMEOUT "$(read_env_value "$DEFAULT_ENV" APP_HEALTH_CONNECT_TIMEOUT 3)" "$compose_env"
  set_env APP_HEALTH_INTERVAL "$(read_env_value "$DEFAULT_ENV" APP_HEALTH_INTERVAL 15s)" "$compose_env"
  set_env APP_HEALTH_TIMEOUT "$(read_env_value "$DEFAULT_ENV" APP_HEALTH_TIMEOUT 5s)" "$compose_env"
  set_env APP_HEALTH_RETRIES "$(read_env_value "$DEFAULT_ENV" APP_HEALTH_RETRIES 3)" "$compose_env"
  set_env APP_HEALTH_START_PERIOD "$(read_env_value "$DEFAULT_ENV" APP_HEALTH_START_PERIOD 30s)" "$compose_env"
  set_env APP_STARTUP_TIMEOUT "$(read_env_value "$DEFAULT_ENV" APP_STARTUP_TIMEOUT 300)" "$compose_env"
  set_env APP_STABLE_WINDOW "$(read_env_value "$DEFAULT_ENV" APP_STABLE_WINDOW 10)" "$compose_env"
  set_env GATEWAY_PORT "$GATEWAY_PORT" "$compose_env"
  set_env WEB_VUE3_PORT "$WEB_VUE3_PORT" "$compose_env"
  set_env OAUTH_PORT "$(read_env_value "$DEFAULT_ENV" OAUTH_PORT 38001)" "$compose_env"
  set_env PERMISSION_PORT "$(read_env_value "$DEFAULT_ENV" PERMISSION_PORT 38010)" "$compose_env"
  set_env SYSTEM_PORT "$(read_env_value "$DEFAULT_ENV" SYSTEM_PORT 38002)" "$compose_env"
  set_env FILE_PORT "$(read_env_value "$DEFAULT_ENV" FILE_PORT 38005)" "$compose_env"
  set_env MESSAGE_PORT "$(read_env_value "$DEFAULT_ENV" MESSAGE_PORT 38008)" "$compose_env"
  set_env IM_PORT "$(read_env_value "$DEFAULT_ENV" IM_PORT 38031)" "$compose_env"
  set_env CONTACTS_PORT "$(read_env_value "$DEFAULT_ENV" CONTACTS_PORT 38032)" "$compose_env"
  set_env MEETING_PORT "$(read_env_value "$DEFAULT_ENV" MEETING_PORT 38033)" "$compose_env"
}

write_java_env() {
  common_env="$ENV_DIR/java-common.env"
  secrets_env="$ENV_DIR/java-secrets.env"
  : > "$common_env"
  : > "$secrets_env"
  set_env TZ "$TIMEZONE" "$common_env"
  set_env NACOS_HOST "${NACOS_CONNECT_HOST}:${NACOS_PORT_WEB}" "$common_env"
  set_env NACOS_PORT_WEB "$NACOS_PORT_WEB" "$common_env"
  set_env NACOS_PORT_API "$NACOS_PORT_API" "$common_env"
  set_env NACOS_USER "$NACOS_USER" "$common_env"
  set_env NACOS_NS "$NACOS_NS" "$common_env"
  set_env SPRING_CLOUD_NACOS_DISCOVERY_REGISTER_ENABLED "false" "$common_env"
  set_env NACOS_PASSWORD "$NACOS_PASSWORD" "$secrets_env"
  chmod 0644 "$common_env"
  chmod 0600 "$secrets_env"
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

write_runtime_resource_files() {
  write_jvm_options "$ENV_DIR/java-jvm.options" "$JVM_INITIAL_RAM_PERCENTAGE" "$JVM_MAX_RAM_PERCENTAGE"
  java_start_command > "$ENV_DIR/java-entrypoint.sh"
  chmod 0755 "$ENV_DIR/java-entrypoint.sh"
  for service in $SERVICE_ORDER; do
    resource_file="$(resource_file_for_service "$service")"
    : > "$resource_file"
    set_env APP_CPUS "$APP_CPUS" "$resource_file"
    set_env APP_MEMORY_LIMIT "$APP_MEMORY_LIMIT" "$resource_file"
    chmod 0644 "$resource_file"
    [ "$(service_kind "$service")" = "web" ] || write_jvm_options "$ENV_DIR/java-jvm.$service.options" "$JVM_INITIAL_RAM_PERCENTAGE" "$JVM_MAX_RAM_PERCENTAGE"
  done
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

configure_web_nginx_runtime_routes() {
  nginx_conf="$APP_DIR/$WEB_SERVICE/nginx/default.conf"
  [ -f "$nginx_conf" ] || return 0
  upstream="host.docker.internal:${GATEWAY_PORT}"
  tmp="$nginx_conf.tmp"
  sed "s#__AIFAR_GATEWAY_UPSTREAM__#${upstream}#g" "$nginx_conf" > "$tmp"
  mv "$tmp" "$nginx_conf"
}

write_service_envs() {
  for service in $SERVICE_ORDER; do
    [ -d "$APP_DIR/$service" ] || continue
    service_env="$ENV_DIR/$service.env"
    image="$(retag_image "aifar-$service:latest")"
    : > "$service_env"
    set_env APP_IMAGE "$image" "$service_env"
    set_env APP_CONTAINER_NAME "$(pod_name "$service" "$REVISION" 1)" "$service_env"
    set_env AIFAR_SERVICE_PROXY "aifar-agent" "$service_env"
    set_env AIFAR_REVISION "$REVISION" "$service_env"
    set_env AIFAR_SERVICE_KIND "$(service_kind "$service")" "$service_env"
    set_env AIFAR_AFFINITY_POLICY "$(catalog_value "$SERVICE_AFFINITIES" "$service" || printf round-robin)" "$service_env"
    set_env HEALTH_PATH "$(catalog_value "$SERVICE_HEALTH_PATHS" "$service" || true)" "$service_env"
    app_name="$(alpha_service_name "$service")"
    if [ -n "$app_name" ]; then
      set_env SPRING_APPLICATION_NAME "$app_name" "$service_env"
    fi
    port_value="$(catalog_value "$SERVICE_PORTS" "$service" || true)"
    [ -n "$port_value" ] && set_env AIFAR_SERVICE_PORT "$port_value" "$service_env"
    if [ "$(service_kind "$service")" != "web" ] && [ -n "$port_value" ]; then
      set_env SERVER_PORT "$port_value" "$service_env"
    fi
    chmod 0644 "$service_env"
  done
}

build_images() {
  for service in $SERVICE_ORDER; do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$REVISION")"
    docker build -t "$image" "$APP_DIR/$service"
  done
}

health_cmd_for_service() {
  service="$1"
  port="$(service_port "$service")"
  if [ "$(service_kind "$service")" = "web" ]; then
    path="$(catalog_value "$SERVICE_HEALTH_PATHS" "$service" || true)"
    [ -n "$path" ] || path="/"
    printf "wget -q -T 3 -O /dev/null 'http://127.0.0.1:%s%s' || exit 1" "$port" "$path"
  else
    path="$(catalog_value "$SERVICE_HEALTH_PATHS" "$service" || true)"
    [ -n "$path" ] || path="/actuator/health/readiness"
    printf "curl -fsS --connect-timeout 3 'http://127.0.0.1:%s%s' >/dev/null || exit 1" "$port" "$path"
  fi
}

write_bootstrap_input() {
  spec="$WORK_DIR/bootstrap-runtime.json"
  mkdir -p "$WORK_DIR"
  cat > "$spec" <<JSON
{
  "version": "runtime-v2",
  "instanceId": "${INSTANCE_ID}",
  "installRoot": "${INSTALL_ROOT}",
  "network": "${INGRESS_NETWORK}",
  "deployments": [
JSON
  first_deployment=1
  for service in $SERVICE_ORDER; do
    service_env="$ENV_DIR/$service.env"
    [ -f "$service_env" ] || continue
    image="$(read_env_value "$service_env" APP_IMAGE "aifar-$service:$REVISION")"
    deployment_name="$(alpha_service_name "$service")"
    port="$(service_port "$service")"
    health_cmd="$(health_cmd_for_service "$service")"
    app_cpus="$(resource_value "$service" APP_CPUS "$APP_CPUS")"
    app_memory_limit="$(resource_value "$service" APP_MEMORY_LIMIT "$APP_MEMORY_LIMIT")"
    log_dir="$INSTALL_ROOT/logs/$service"
    mkdir -p "$log_dir"
    if [ "$first_deployment" = "1" ]; then
      first_deployment=0
    else
      printf ",\n" >> "$spec"
    fi
    printf '    {\n' >> "$spec"
    printf '      "serviceName": "%s",\n' "$service" >> "$spec"
    printf '      "deploymentName": "%s",\n' "$(json_escape "$deployment_name")" >> "$spec"
    printf '      "image": "%s",\n' "$(json_escape "$image")" >> "$spec"
    printf '      "podRevision": "%s",\n' "$(json_escape "$REVISION")" >> "$spec"
    printf '      "replicas": 1,\n' >> "$spec"
    printf '      "ports": [{"name":"http","containerPort":%s}],\n' "$port" >> "$spec"
    if [ "$(service_kind "$service")" = "web" ]; then
      printf '      "envFiles": ["%s"],\n' "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/logs","readOnly":false},{"source":"%s","target":"/var/log/nginx","readOnly":false}],\n' "$(json_escape "$log_dir")" "$(json_escape "$log_dir")" >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_LOG_DIR":"/opt/aifar/logs","LOG_DIR":"/opt/aifar/logs","TZ":"%s"},\n' "$(json_escape "$SPEC_TIMEZONE")" >> "$spec"
    else
      printf '      "envFiles": ["%s","%s","%s"],\n' "$(json_escape "$ENV_DIR/java-common.env")" "$(json_escape "$ENV_DIR/java-secrets.env")" "$(json_escape "$service_env")" >> "$spec"
      printf '      "volumes": [{"source":"%s","target":"/opt/aifar/runtime/env","readOnly":true},{"source":"%s","target":"/opt/aifar/logs","readOnly":false},{"source":"%s","target":"/data/aifarsoft/javaApi/aifar-%s/log","readOnly":false}],\n' "$(json_escape "$ENV_DIR")" "$(json_escape "$log_dir")" "$(json_escape "$log_dir")" "$(json_escape "$service")" >> "$spec"
      printf '      "entrypoint": ["/bin/sh"],\n' >> "$spec"
      printf '      "command": ["/opt/aifar/runtime/env/java-entrypoint.sh"],\n' >> "$spec"
      printf '      "environment": {"APP_CONTAINER_NAME":"${containerName}","AIFAR_SERVICE_NAME":"%s","AIFAR_LOG_DIR":"/opt/aifar/logs","LOG_DIR":"/opt/aifar/logs","TZ":"%s"},\n' "$service" "$(json_escape "$SPEC_TIMEZONE")" >> "$spec"
    fi
    printf '      "resources": {"cpus":"%s","memory":"%s"},\n' "$(json_escape "$app_cpus")" "$(json_escape "$app_memory_limit")" >> "$spec"
    printf '      "healthCheck": {"command":"%s","interval":"%s","timeout":"%s","retries":%s,"startPeriod":"%s"}\n' \
      "$(json_escape "$health_cmd")" \
      "15s" "5s" "3" "30s" >> "$spec"
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
    affinity="$(catalog_value "$SERVICE_AFFINITIES" "$service" || true)"
    [ -n "$affinity" ] || affinity="round-robin"
    printf '    {"name":"%s","appName":"%s","listenPort":%s,"targetPort":%s,"affinityPolicy":"%s"}' "$service" "$app_name" "$port" "$port" "$affinity" >> "$spec"
  done
  cat >> "$spec" <<JSON
  ],
  "ingress": {
    "mode": "web-nginx",
    "gatewayService": "${GATEWAY_SERVICE}",
    "webService": "${WEB_SERVICE}",
    "gatewayPort": ${GATEWAY_PORT},
    "webPort": ${WEB_VUE3_PORT}
  },
  "nacos": {
    "namespace": "${NACOS_NS}",
    "group": "DEFAULT_GROUP",
    "ephemeral": $(nacos_ephemeral),
    "agentIPStrategy": "auto"
  }
}
JSON
  printf "%s" "$spec"
}

readback_bootstrap_acceptance() {
  response='{"accepted":true,"instanceId":"'"$INSTANCE_ID"'","deployments":['
  separator=""
  for service in $SERVICE_ORDER; do
    state="$(aifar-agent get-deployment --instance "$INSTANCE_ID" --service "$service" 2>/dev/null)" || return 1
    state_instance="$(printf "%s" "$state" | sed -n 's/.*"instanceId":"\([^"]*\)".*/\1/p')"
    state_service="$(printf "%s" "$state" | sed -n 's/.*"serviceName":"\([^"]*\)".*/\1/p')"
    generation="$(printf "%s" "$state" | sed -n 's/.*"generation":\([0-9][0-9]*\).*/\1/p')"
    spec_hash="$(printf "%s" "$state" | sed -n 's/.*"specHash":"\([0-9a-f][0-9a-f]*\)".*/\1/p')"
    expected_hash="$(catalog_value "$SERVICE_SPEC_HASHES" "$service" || true)"
    [ "$state_instance" = "$INSTANCE_ID" ] || return 1
    [ "$state_service" = "$service" ] || return 1
    [ "$generation" = "1" ] || return 1
    [ -n "$expected_hash" ] && [ "$spec_hash" = "$expected_hash" ] || return 1
    response="${response}${separator}{\"accepted\":true,\"instanceId\":\"${state_instance}\",\"serviceName\":\"${state_service}\",\"generation\":1,\"specHash\":\"${spec_hash}\"}"
    separator=,
  done
  printf '%s]}' "$response"
}

accept_runtime_manifests() {
  check_agent_dependency
  spec="$(write_bootstrap_input)"
  legacy_hash="$(sha256sum "$spec" | awk '{print $1}')"
  if aifar-agent bootstrap-runtime-stdin --instance "$INSTANCE_ID" --sha256 "$legacy_hash" < "$spec" >/dev/null 2>&1; then
    :
  else
    :
  fi
  acceptance="$(readback_bootstrap_acceptance)" || fail "aifar-agent could not prove the exact runtime Manifests"
  rm -f "$spec"
  printf 'AIFAR_BOOTSTRAP_ACCEPTANCE=%s\n' "$acceptance"
}

write_model_manifest() {
  mkdir -p "$AIFAR_DIR"
  cat > "$AIFAR_DIR/model.json" <<JSON
{
  "model": "${ORCHESTRATION_MODEL}",
  "revision": "${REVISION}",
  "version": "${VERSION}",
  "createdAt": "${CREATED_AT}",
  "configHash": "${CONFIG_HASH}",
  "network": "${INGRESS_NETWORK}",
  "nacosRegistrationMode": "${NACOS_REGISTRATION_MODE}"
}
JSON
}

cleanup_failed_install() {
  [ "$INSTALL_SUCCEEDED" = "1" ] && return 0
  rm -f "$WORK_DIR/bootstrap-runtime.json" >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR" >/dev/null 2>&1 || true
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
command -v tar >/dev/null 2>&1 || fail "tar command is required"
[ -f "$ARCHIVE" ] || fail "bundle archive not found: $ARCHIVE"
check_agent_dependency

mkdir -p "$INSTALL_ROOT" "$WORK_DIR" "$RUNTIME_DIR" "$ENV_DIR" "$LOG_DIR" "$AGENT_DIR" "$AIFAR_DIR"
rm -rf "$TMP_DIR" "$APP_DIR" "$IMAGE_DIR"
mkdir -p "$TMP_DIR"
tar -xzf "$ARCHIVE" -C "$TMP_DIR"
[ -f "$TMP_DIR/manifest.json" ] || fail "runtime-v2 manifest.json is missing in bundle"
[ -d "$TMP_DIR/services" ] || fail "services directory is missing in runtime-v2 bundle"
[ -d "$TMP_DIR/images" ] || fail "images directory is missing in runtime-v2 bundle"
[ -f "$TMP_DIR/runtime/defaults.env" ] || fail "runtime/defaults.env is missing in runtime-v2 bundle"
mv "$TMP_DIR/services" "$APP_DIR"
mv "$TMP_DIR/images" "$IMAGE_DIR"
rm -rf "$BUNDLE_DIR"
mv "$TMP_DIR/runtime" "$BUNDLE_DIR"
cp "$TMP_DIR/manifest.json" "$RUNTIME_DIR/manifest.json"
rm -rf "$TMP_DIR"

trap 'cleanup_failed_install' EXIT INT TERM
resolve_system_timezone
write_compose_env
write_java_env
write_service_envs
write_runtime_resource_files
configure_web_nginx_runtime_routes
load_docker_images
require_local_image "bellsoft/liberica-openjre-rocky:21"
require_local_image "nginx:stable-alpine"
ensure_network
build_images
open_service_ports $SERVICE_ORDER
write_model_manifest
accept_runtime_manifests
INSTALL_SUCCEEDED=1
trap - EXIT INT TERM
echo "AIFAR k8s-like orchestration installed, revision $REVISION"
