#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_NAME={{ quote .ServiceName }}
RELEASE_ID={{ quote .ReleaseID }}
REPLICA_ID={{ .ReplicaID }}
CONTAINER_NAME={{ quote .ContainerName }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
INGRESS_CONTAINER={{ quote .IngressContainer }}
MAX_REPLICAS={{ .MaxReplicas }}

CURRENT_LINK="$INSTALL_ROOT/current"
ENV_DIR="$CURRENT_LINK/env"
INGRESS_DIR="$INSTALL_ROOT/ingress"
INGRESS_CONFIG="$INGRESS_DIR/nginx.conf"
INGRESS_CONFIG_BACKUP="$INGRESS_CONFIG.bak"

fail() {
  echo "$*" >&2
  exit 1
}

read_env_value() {
  rev_file="$1"
  rev_key="$2"
  rev_default="${3:-}"
  if [ -f "$rev_file" ]; then
    rev_value="$(awk -F= -v key="$rev_key" '$1==key {print substr($0, index($0, "=")+1)}' "$rev_file" | tail -n 1)"
    if [ -n "$rev_value" ]; then
      printf "%s" "$rev_value"
      return 0
    fi
  fi
  printf "%s" "$rev_default"
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

memory_to_bytes() {
  mtb_value="$(printf "%s" "$1" | tr '[:upper:]' '[:lower:]' | tr -d ' ')"
  case "$mtb_value" in
    *gib) mtb_num="${mtb_value%gib}"; awk -v n="$mtb_num" 'BEGIN {printf "%.0f", n*1024*1024*1024}' ;;
    *gb) mtb_num="${mtb_value%gb}"; awk -v n="$mtb_num" 'BEGIN {printf "%.0f", n*1000*1000*1000}' ;;
    *mib) mtb_num="${mtb_value%mib}"; awk -v n="$mtb_num" 'BEGIN {printf "%.0f", n*1024*1024}' ;;
    *mb) mtb_num="${mtb_value%mb}"; awk -v n="$mtb_num" 'BEGIN {printf "%.0f", n*1000*1000}' ;;
    *kib) mtb_num="${mtb_value%kib}"; awk -v n="$mtb_num" 'BEGIN {printf "%.0f", n*1024}' ;;
    *kb) mtb_num="${mtb_value%kb}"; awk -v n="$mtb_num" 'BEGIN {printf "%.0f", n*1000}' ;;
    ''|*[!0-9]*) printf "0" ;;
    *) printf "%s" "$mtb_value" ;;
  esac
}

service_container_count() {
  docker ps --filter "label=aifar.app=aifar" \
    --filter "label=aifar.install-root=$INSTALL_ROOT" \
    --filter "label=aifar.service=$SERVICE_NAME" \
    --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null | wc -l | tr -d ' '
}

container_status() {
  docker inspect --format '{{ "{{" }}.State.Status{{ "}}" }}' "$1" 2>/dev/null || true
}

container_health() {
  docker inspect --format '{{ "{{" }}if .State.Health{{ "}}" }}{{ "{{" }}.State.Health.Status{{ "}}" }}{{ "{{" }}end{{ "}}" }}' "$1" 2>/dev/null || true
}

container_restart_count() {
  docker inspect --format '{{ "{{" }}.RestartCount{{ "}}" }}' "$1" 2>/dev/null || printf "0"
}

wait_container_ready() {
  wcr_container="$1"
  wcr_timeout="$(read_env_value "$ENV_DIR/compose.env" APP_STARTUP_TIMEOUT 180)"
  case "$wcr_timeout" in ""|*[!0-9]*) wcr_timeout=180 ;; esac
  wcr_deadline=$(( $(date +%s) + wcr_timeout ))
  while :; do
    wcr_status="$(container_status "$wcr_container")"
    wcr_health="$(container_health "$wcr_container")"
    if [ "$wcr_status" = "running" ] && { [ -z "$wcr_health" ] || [ "$wcr_health" = "healthy" ]; }; then
      break
    fi
    if [ "$(date +%s)" -ge "$wcr_deadline" ]; then
      docker logs --tail 120 "$wcr_container" >&2 || true
      return 1
    fi
    sleep 3
  done
  wcr_restarts="$(container_restart_count "$wcr_container")"
  case "$wcr_restarts" in ""|*[!0-9]*) wcr_restarts=0 ;; esac
  if [ "$SERVICE_NAME" != "web-vue3" ] && [ "$wcr_restarts" -gt 0 ]; then
    docker logs --tail 120 "$wcr_container" >&2 || true
    return 1
  fi
}

list_running_containers() {
  lrc_service="$1"
  docker ps --filter "label=aifar.app=aifar" \
    --filter "label=aifar.install-root=$INSTALL_ROOT" \
    --filter "label=aifar.service=$lrc_service" \
    --format '{{ "{{" }}.Names{{ "}}" }}' 2>/dev/null || true
}

write_ingress_config() {
  mkdir -p "$INGRESS_DIR"
  gateway_port="$(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)"
  web_port="$(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)"
  gateway_servers="$(list_running_containers gateway)"
  web_servers="$(list_running_containers web-vue3)"
  [ -n "$gateway_servers" ] || fail "gateway upstream has no running endpoints"
  [ -n "$web_servers" ] || fail "web-vue3 upstream has no running endpoints"
  tmp="$INGRESS_CONFIG.tmp"
  cat > "$tmp" <<NGINX
events {}
http {
  map \$http_upgrade \$connection_upgrade {
    default upgrade;
    '' close;
  }
  map \$http_x_forwarded_for \$client_real_ip {
    ~^(?<first_ip>[^,]+) \$first_ip;
    default \$remote_addr;
  }
  upstream aifar_gateway {
NGINX
  for endpoint in $gateway_servers; do
    printf "    server %s:%s;\n" "$endpoint" "$gateway_port" >> "$tmp"
  done
  cat >> "$tmp" <<NGINX
  }
  upstream aifar_web {
NGINX
  for endpoint in $web_servers; do
    printf "    server %s:%s;\n" "$endpoint" "$web_port" >> "$tmp"
  done
  cat >> "$tmp" <<NGINX
  }
  server {
    listen ${gateway_port};
    client_max_body_size 1000m;
    proxy_connect_timeout 300s;
    proxy_buffering off;
    proxy_request_buffering off;
    location / {
      proxy_set_header Host \$http_host;
      proxy_set_header X-Real-IP \$client_real_ip;
      proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto \$scheme;
      proxy_set_header X-NginX-Proxy true;
      proxy_pass http://aifar_gateway;
    }
  }
  server {
    listen ${web_port};
    client_max_body_size 1000m;
    proxy_connect_timeout 300s;
    proxy_buffering off;
    proxy_request_buffering off;
    location /api/ {
      proxy_intercept_errors off;
      proxy_set_header Host \$http_host;
      proxy_set_header X-Real-IP \$client_real_ip;
      proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto \$scheme;
      proxy_set_header X-NginX-Proxy true;
      proxy_pass http://aifar_gateway;
    }
    location /im/ws/ {
      proxy_intercept_errors off;
      proxy_http_version 1.1;
      proxy_set_header Upgrade \$http_upgrade;
      proxy_set_header Connection \$connection_upgrade;
      proxy_set_header Host \$http_host;
      proxy_set_header X-Real-IP \$client_real_ip;
      proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto \$scheme;
      proxy_set_header X-NginX-Proxy true;
      proxy_read_timeout 3600s;
      proxy_send_timeout 3600s;
      proxy_pass http://aifar_gateway;
    }
    location / {
      proxy_set_header Host \$http_host;
      proxy_set_header X-Real-IP \$client_real_ip;
      proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
      proxy_set_header X-Forwarded-Proto \$scheme;
      proxy_set_header X-NginX-Proxy true;
      proxy_pass http://aifar_web;
    }
  }
}
NGINX
  mv "$tmp" "$INGRESS_CONFIG"
}

reload_ingress_if_needed() {
  case "$SERVICE_NAME" in
    gateway|web-vue3) ;;
    *) return 0 ;;
  esac
  if [ -f "$INGRESS_CONFIG" ]; then
    cp "$INGRESS_CONFIG" "$INGRESS_CONFIG_BACKUP"
  fi
  write_ingress_config
  if docker exec "$INGRESS_CONTAINER" nginx -t >/dev/null && docker exec "$INGRESS_CONTAINER" nginx -s reload >/dev/null; then
    return 0
  fi
  if [ -f "$INGRESS_CONFIG_BACKUP" ]; then
    cp "$INGRESS_CONFIG_BACKUP" "$INGRESS_CONFIG"
    docker exec "$INGRESS_CONTAINER" nginx -s reload >/dev/null 2>&1 || true
  fi
  return 1
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
[ -d "$ENV_DIR" ] || fail "AIFAR current release env directory is missing"
[ -f "$ENV_DIR/$SERVICE_NAME.env" ] || fail "AIFAR service env is missing: $SERVICE_NAME"

count="$(service_container_count)"
case "$count" in ""|*[!0-9]*) count=0 ;; esac
if [ "$count" -ge "$MAX_REPLICAS" ]; then
  fail "AIFAR service $SERVICE_NAME already reached max replicas: $MAX_REPLICAS"
fi

compose_env="$ENV_DIR/compose.env"
service_env="$ENV_DIR/$SERVICE_NAME.env"
image="$(read_env_value "$service_env" APP_IMAGE "")"
[ -n "$image" ] || fail "AIFAR service image is empty: $SERVICE_NAME"
app_memory_limit="$(read_env_value "$compose_env" APP_MEMORY_LIMIT "")"
required_bytes="$(memory_to_bytes "$app_memory_limit")"
if [ "$required_bytes" -le 0 ]; then
  fail "AIFAR autoscale requires a memory limit for $SERVICE_NAME"
fi
available_bytes="$(awk '/MemAvailable/ {print $2 * 1024}' /proc/meminfo 2>/dev/null | cut -d. -f1)"
[ -n "$available_bytes" ] || available_bytes=0
reserve_bytes=$((required_bytes / 5))
if [ "$available_bytes" -lt $((required_bytes + reserve_bytes)) ]; then
  fail "host memory is insufficient for a new $SERVICE_NAME replica"
fi

docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
port_var="$(service_port_var "$SERVICE_NAME")"
port="$(read_env_value "$compose_env" "$port_var" "")"
health_protocol="$(read_env_value "$compose_env" APP_HEALTH_PROTOCOL http)"
health_host="$(read_env_value "$compose_env" APP_HEALTH_HOST 127.0.0.1)"
health_path="$(read_env_value "$compose_env" APP_HEALTH_PATH "")"
health_connect_timeout="$(read_env_value "$compose_env" APP_HEALTH_CONNECT_TIMEOUT 3)"
health_interval="$(read_env_value "$compose_env" APP_HEALTH_INTERVAL 15s)"
health_timeout="$(read_env_value "$compose_env" APP_HEALTH_TIMEOUT 5s)"
health_retries="$(read_env_value "$compose_env" APP_HEALTH_RETRIES 3)"
health_start_period="$(read_env_value "$compose_env" APP_HEALTH_START_PERIOD 30s)"
app_cpus="$(read_env_value "$compose_env" APP_CPUS "")"
restart_policy="$(read_env_value "$compose_env" APP_RESTART_POLICY unless-stopped)"
tz_value="$(read_env_value "$compose_env" TZ system)"

env_args=""
if [ "$SERVICE_NAME" = "web-vue3" ]; then
  env_args="$env_args --env-file $service_env"
  health_cmd="wget -q -T $health_connect_timeout -O /dev/null ${health_protocol}://${health_host}:${port}${health_path} || exit 1"
else
  env_args="$env_args --env-file $ENV_DIR/java-common.env --env-file $ENV_DIR/java-secrets.env --env-file $service_env"
  health_cmd="curl -fsS --connect-timeout $health_connect_timeout ${health_protocol}://${health_host}:${port}${health_path} >/dev/null || exit 1"
fi

docker run -d \
  --name "$CONTAINER_NAME" \
  --restart no \
  --label aifar.app=aifar \
  --label "aifar.install-root=$INSTALL_ROOT" \
  --label "aifar.release=$RELEASE_ID" \
  --label "aifar.service=$SERVICE_NAME" \
  --label "aifar.replica=$REPLICA_ID" \
  --label aifar.autoscaled=true \
  --network "$INGRESS_NETWORK" \
  --cpus "$app_cpus" \
  --memory "$app_memory_limit" \
  --health-cmd "$health_cmd" \
  --health-interval "$health_interval" \
  --health-timeout "$health_timeout" \
  --health-retries "$health_retries" \
  --health-start-period "$health_start_period" \
  -e "APP_CONTAINER_NAME=$CONTAINER_NAME" \
  -e "TZ=$tz_value" \
  $env_args \
  "$image" >/dev/null

if ! wait_container_ready "$CONTAINER_NAME"; then
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fail "new AIFAR service replica did not become ready: $CONTAINER_NAME"
fi

if ! reload_ingress_if_needed; then
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  fail "AIFAR ingress reload failed for autoscaled endpoint"
fi

docker update --restart "$restart_policy" "$CONTAINER_NAME" >/dev/null 2>&1 || true
echo "AIFAR service $SERVICE_NAME scaled out with $CONTAINER_NAME"
