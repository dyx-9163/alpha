#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
WORK_DIR={{ quote .WorkDir }}
SERVICE_ORDER={{ quote .ServiceOrder }}
SERVICE_NAME={{ quote .ServiceName }}
ARTIFACT_REMOTE={{ quote .ArtifactRemote }}
ARTIFACT_FILE={{ quote .ArtifactFileName }}
ARTIFACT_SHA256={{ quote .ArtifactSHA256 }}
ARTIFACT_SIZE={{ .ArtifactSize }}
VERSION={{ quote .Version }}
RELEASE_ID={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
RELEASE_KEEP_COUNT={{ .ReleaseKeepCount }}
COMPOSE_PROJECT_NAME={{ quote .ComposeProject }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
INTERNAL_NETWORK={{ quote .InternalNetwork }}
INGRESS_CONTAINER={{ quote .IngressContainer }}

RELEASES_DIR="$INSTALL_ROOT/releases"
CURRENT_LINK="$INSTALL_ROOT/current"
RELEASE_DIR="$RELEASES_DIR/$RELEASE_ID"
APP_DIR="$RELEASE_DIR/docker-apps"
ENV_DIR="$RELEASE_DIR/env"
AIFAR_DIR="$RELEASE_DIR/.aifar"
INGRESS_DIR="$INSTALL_ROOT/ingress"
INGRESS_CONFIG="$INGRESS_DIR/nginx.conf"
INGRESS_CONFIG_BACKUP="$INGRESS_DIR/nginx.conf.previous"
TMP_ARTIFACT_DIR="$INSTALL_ROOT/.partial-$RELEASE_ID-$$"

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose "$@"
    return
  fi
  if command -v docker-compose >/dev/null 2>&1; then
    docker-compose "$@"
    return
  fi
  fail "docker compose or docker-compose is required"
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

retag_image() {
  image="$1"
  case "$image" in
    *@*) printf "%s" "$image" ;;
    *:*) printf "%s:%s" "${image%:*}" "$RELEASE_ID" ;;
    *) printf "%s:%s" "$image" "$RELEASE_ID" ;;
  esac
}

service_container_name() {
  printf "aifar-%s-%s" "$1" "$RELEASE_ID" | tr '. _/' '----'
}

current_release() {
  if [ -L "$CURRENT_LINK" ] || [ -d "$CURRENT_LINK" ]; then
    readlink -f "$CURRENT_LINK" 2>/dev/null || printf "%s" "$CURRENT_LINK"
  fi
}

manifest_value() {
  mv_file="$1"
  mv_key="$2"
  [ -f "$mv_file" ] || return 1
  sed -n "s/.*\"$mv_key\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$mv_file" | head -n 1
}

release_by_id() {
  rbi_id="$(printf "%s" "$1" | tr -d '\r\n')"
  [ -n "$rbi_id" ] || return 1
  rbi_candidate="$RELEASES_DIR/$rbi_id"
  [ -d "$rbi_candidate" ] || return 1
  printf "%s" "$rbi_candidate"
}

release_owning_service_dir() {
  ros_service="$1"
  ros_real_dir="$2"
  for ros_release in "$RELEASES_DIR"/*; do
    [ -d "$ros_release" ] || continue
    ros_service_dir="$ros_release/docker-apps/$ros_service"
    [ -e "$ros_service_dir" ] || [ -L "$ros_service_dir" ] || continue
    [ ! -L "$ros_service_dir" ] || continue
    ros_candidate_real="$(readlink -f "$ros_service_dir" 2>/dev/null || printf "%s" "$ros_service_dir")"
    if [ "$ros_candidate_real" = "$ros_real_dir" ]; then
      printf "%s" "$ros_release"
      return 0
    fi
  done
  return 1
}

release_for_service() {
  rfs_service="$1"
  rfs_candidate="$2"
  rfs_seen=""
  while [ -n "$rfs_candidate" ] && [ -d "$rfs_candidate" ]; do
    rfs_resolved="$(readlink -f "$rfs_candidate" 2>/dev/null || printf "%s" "$rfs_candidate")"
    case " $rfs_seen " in
      *" $rfs_resolved "*) break ;;
    esac
    rfs_seen="$rfs_seen $rfs_resolved"
    rfs_service_dir="$rfs_candidate/docker-apps/$rfs_service"
    if [ -d "$rfs_service_dir" ] || [ -L "$rfs_service_dir" ]; then
      if [ ! -L "$rfs_service_dir" ]; then
        printf "%s" "$rfs_candidate"
        return 0
      fi
      rfs_real_dir="$(readlink -f "$rfs_service_dir" 2>/dev/null || printf "%s" "$rfs_service_dir")"
      rfs_owner="$(release_owning_service_dir "$rfs_service" "$rfs_real_dir" || true)"
      if [ -n "$rfs_owner" ]; then
        printf "%s" "$rfs_owner"
        return 0
      fi
      printf "%s" "$rfs_candidate"
      return 0
    fi
    rfs_base_id="$(manifest_value "$rfs_candidate/.aifar/manifest.json" baseReleaseId || true)"
    rfs_candidate="$(release_by_id "$rfs_base_id" || true)"
  done
  return 1
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

set_service_runtime_port() {
  srp_service="$1"
  [ "$srp_service" != "web-vue3" ] || return 0
  srp_port_var="$(service_port_var "$srp_service")"
  [ -n "$srp_port_var" ] || return 0
  srp_port_value="$(read_env_value "$ENV_DIR/compose.env" "$srp_port_var" "")"
  [ -n "$srp_port_value" ] || return 0
  set_env SERVER_PORT "$srp_port_value" "$ENV_DIR/$srp_service.env"
}

verify_artifact() {
  [ -f "$ARTIFACT_REMOTE" ] || fail "artifact file not found: $ARTIFACT_REMOTE"
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$ARTIFACT_REMOTE" | awk '{print $1}')"
    [ "$actual" = "$ARTIFACT_SHA256" ] || fail "artifact SHA256 mismatch: expected $ARTIFACT_SHA256 got $actual"
  else
    echo "warning: sha256sum not found, skip remote artifact checksum verification"
  fi
}

copy_file_required() {
  cfr_source="$1"
  cfr_target="$2"
  [ -f "$cfr_source" ] || fail "required release file is missing: $cfr_source"
  mkdir -p "$(dirname "$cfr_target")"
  cp "$cfr_source" "$cfr_target"
}

copy_shared_release_files() {
  csrf_source="$1"
  [ -f "$csrf_source/compose.yaml" ] || fail "current AIFAR release compose.yaml is missing"
  [ -d "$csrf_source/env" ] || fail "current AIFAR release env directory is missing"
  mkdir -p "$RELEASE_DIR" "$ENV_DIR" "$APP_DIR" "$AIFAR_DIR"
  copy_file_required "$csrf_source/compose.yaml" "$RELEASE_DIR/compose.yaml"
  cp -a "$csrf_source/env/." "$ENV_DIR/"
}

copy_service_release_files() {
  csrf_service_source="$1"
  csrf_service_dir="$csrf_service_source/docker-apps/$SERVICE_NAME"
  [ -d "$csrf_service_dir" ] || fail "service directory is missing in release chain: $SERVICE_NAME"
  csrf_real_dir="$(readlink -f "$csrf_service_dir" 2>/dev/null || printf "%s" "$csrf_service_dir")"
  [ -d "$csrf_real_dir" ] || fail "service directory is missing in release chain: $SERVICE_NAME"
  mkdir -p "$APP_DIR"
  rm -rf "$APP_DIR/$SERVICE_NAME"
  cp -a "$csrf_real_dir" "$APP_DIR/$SERVICE_NAME"
  [ ! -L "$APP_DIR/$SERVICE_NAME" ] || fail "changed service directory must be materialized: $SERVICE_NAME"
  if [ ! -f "$ENV_DIR/$SERVICE_NAME.env" ]; then
    copy_file_required "$csrf_service_source/env/$SERVICE_NAME.env" "$ENV_DIR/$SERVICE_NAME.env"
  fi
}

link_inherited_service_files() {
  lis_service="$1"
  lis_source="$2"
  lis_service_dir="$lis_source/docker-apps/$lis_service"
  [ -d "$lis_service_dir" ] || fail "service directory is missing in release chain: $lis_service"
  lis_real_dir="$(readlink -f "$lis_service_dir" 2>/dev/null || printf "%s" "$lis_service_dir")"
  [ -d "$lis_real_dir" ] || fail "service directory is missing in release chain: $lis_service"
  mkdir -p "$APP_DIR"
  if [ ! -e "$APP_DIR/$lis_service" ] && [ ! -L "$APP_DIR/$lis_service" ]; then
    ln -s "$lis_real_dir" "$APP_DIR/$lis_service"
  fi
  if [ ! -f "$ENV_DIR/$lis_service.env" ]; then
    copy_file_required "$lis_source/env/$lis_service.env" "$ENV_DIR/$lis_service.env"
  fi
}

materialize_effective_service_dirs() {
  for mes_service in $SERVICE_ORDER; do
    if [ -e "$APP_DIR/$mes_service" ] || [ -L "$APP_DIR/$mes_service" ]; then
      continue
    fi
    mes_source="$(release_for_service "$mes_service" "$BASE_RELEASE" || true)"
    [ -n "$mes_source" ] || fail "service directory is missing in current release chain: $mes_service"
    link_inherited_service_files "$mes_service" "$mes_source"
  done
}

write_partial_compose_env() {
  compose_env="$ENV_DIR/compose.env"
  [ -f "$compose_env" ] || fail "compose env is missing: $compose_env"
  set_env COMPOSE_PROJECT_NAME "$COMPOSE_PROJECT_NAME" "$compose_env"
  set_env AIFAR_RELEASE_ID "$RELEASE_ID" "$compose_env"
  set_env APP_NETWORK_NAME "$INGRESS_NETWORK" "$compose_env"
  set_env AIFAR_INGRESS_NETWORK "$INGRESS_NETWORK" "$compose_env"
  set_env AIFAR_INTERNAL_NETWORK "$INTERNAL_NETWORK" "$compose_env"
}

apply_java_artifact() {
  case "$ARTIFACT_FILE" in
    *.jar) ;;
    *) fail "$SERVICE_NAME update requires a .jar artifact" ;;
  esac
  service_dir="$APP_DIR/$SERVICE_NAME"
  [ -d "$service_dir" ] || fail "service directory not found: $service_dir"
  target_dir="$service_dir/target"
  mkdir -p "$target_dir"
  rm -f "$target_dir"/*.jar
  cp "$ARTIFACT_REMOTE" "$target_dir/$ARTIFACT_FILE"
}

frontend_source_dir() {
  if [ -f "$TMP_ARTIFACT_DIR/index.html" ]; then
    printf "%s" "$TMP_ARTIFACT_DIR"
    return
  fi
  if [ -d "$TMP_ARTIFACT_DIR/dist" ] && [ -f "$TMP_ARTIFACT_DIR/dist/index.html" ]; then
    printf "%s" "$TMP_ARTIFACT_DIR/dist"
    return
  fi
  candidate="$(find "$TMP_ARTIFACT_DIR" -mindepth 1 -maxdepth 1 -type d | head -n 1 || true)"
  if [ -n "$candidate" ] && [ -f "$candidate/index.html" ]; then
    printf "%s" "$candidate"
    return
  fi
  if [ -n "$candidate" ] && [ -d "$candidate/dist" ] && [ -f "$candidate/dist/index.html" ]; then
    printf "%s" "$candidate/dist"
    return
  fi
  return 1
}

apply_frontend_artifact() {
  rm -rf "$TMP_ARTIFACT_DIR"
  mkdir -p "$TMP_ARTIFACT_DIR"
  case "$ARTIFACT_FILE" in
    *.tar.gz|*.tgz) tar -xzf "$ARTIFACT_REMOTE" -C "$TMP_ARTIFACT_DIR" ;;
    *.tar) tar -xf "$ARTIFACT_REMOTE" -C "$TMP_ARTIFACT_DIR" ;;
    *.zip)
      command -v unzip >/dev/null 2>&1 || fail "unzip is required for frontend zip artifacts"
      unzip -q "$ARTIFACT_REMOTE" -d "$TMP_ARTIFACT_DIR"
      ;;
    *) fail "web-vue3 update requires a zip, tar, tgz, or tar.gz artifact" ;;
  esac
  source_dir="$(frontend_source_dir || true)"
  [ -n "$source_dir" ] || fail "frontend artifact must contain index.html or dist/index.html"
  target_dir="$APP_DIR/web-vue3/dist"
  rm -rf "$target_dir"
  mkdir -p "$target_dir"
  cp -a "$source_dir/." "$target_dir/"
}

apply_artifact() {
  if [ "$SERVICE_NAME" = "web-vue3" ]; then
    apply_frontend_artifact
  else
    apply_java_artifact
  fi
}

patch_web_nginx_gateway_target() {
  nginx_conf="$APP_DIR/web-vue3/nginx/default.conf"
  [ -f "$nginx_conf" ] || return 0
  gateway_container="$(route_container_for_service gateway)"
  gateway_port="$(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)"
  [ -n "$gateway_container" ] || fail "gateway route container is empty for web nginx config"
  [ -n "$gateway_port" ] || gateway_port=38000
  tmp="$nginx_conf.tmp"
  sed "s#http://aifar-gateway:[0-9][0-9]*#http://${gateway_container}:${gateway_port}#g; s#http://aifar-gateway#http://${gateway_container}:${gateway_port}#g" "$nginx_conf" > "$tmp"
  mv "$tmp" "$nginx_conf"
}

retag_selected_service() {
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  source_env="$APP_DIR/$SERVICE_NAME/.env"
  [ -f "$service_env" ] || fail "service env not found: $service_env"
  image="$(read_env_value "$service_env" APP_IMAGE "$(read_env_value "$source_env" APP_IMAGE "aifar-$SERVICE_NAME:latest")")"
  set_env APP_IMAGE "$(retag_image "$image")" "$service_env"
  set_env APP_CONTAINER_NAME "$(service_container_name "$SERVICE_NAME")" "$service_env"
  set_service_runtime_port "$SERVICE_NAME"
}

patch_compose_service_release() {
  pcs_service="$1"
  pcs_image="$(read_env_value "$ENV_DIR/$pcs_service.env" APP_IMAGE "")"
  pcs_container="$(read_env_value "$ENV_DIR/$pcs_service.env" APP_CONTAINER_NAME "")"
  [ -n "$pcs_image" ] || fail "service image is empty: $pcs_service"
  [ -n "$pcs_container" ] || fail "service container name is empty: $pcs_service"
  pcs_tmp="$RELEASE_DIR/compose.yaml.tmp"
  awk -v svc="$pcs_service" -v image="$pcs_image" -v container="$pcs_container" -v release="$RELEASE_ID" -v project="$COMPOSE_PROJECT_NAME" '
    function service_header(line) { return line ~ /^  [A-Za-z0-9_-]+:$/ }
    $0 == "  " svc ":" { in_service=1; print; next }
    in_service && service_header($0) { in_service=0; skip_networks=0 }
    in_service && $0 ~ /^    image:/ { print "    image: " image; next }
    in_service && $0 ~ /^    container_name:/ { print "    container_name: " container; next }
    in_service && $0 ~ /^      aifar\.release:/ { print "      aifar.release: \"" release "\""; next }
    in_service && $0 ~ /^      aifar\.compose-project:/ { print "      aifar.compose-project: \"" project "\""; next }
    in_service && $0 ~ /^    networks:/ { print "    networks:"; print "      - ingress"; skip_networks=1; next }
    in_service && skip_networks && $0 ~ /^      - / { next }
    in_service && skip_networks { skip_networks=0 }
    { print }
  ' "$RELEASE_DIR/compose.yaml" > "$pcs_tmp"
  mv "$pcs_tmp" "$RELEASE_DIR/compose.yaml"
}

ensure_network() {
  network="$(read_env_value "$ENV_DIR/compose.env" AIFAR_INGRESS_NETWORK "$INGRESS_NETWORK")"
  docker network inspect "$network" >/dev/null 2>&1 || docker network create --driver bridge "$network" >/dev/null
}

release_internal_network() {
  rin_release="$1"
  [ -n "$rin_release" ] || return 0
  read_env_value "$rin_release/env/compose.env" AIFAR_INTERNAL_NETWORK ""
}

connect_container_to_network() {
  ctn_container="$1"
  ctn_network="$2"
  [ -n "$ctn_container" ] || return 0
  [ -n "$ctn_network" ] || return 0
  [ "$ctn_network" != "$INGRESS_NETWORK" ] || return 0
  [ "$ctn_network" != "$INTERNAL_NETWORK" ] || return 0
  docker network inspect "$ctn_network" >/dev/null 2>&1 || return 0
  docker network connect "$ctn_network" "$ctn_container" >/dev/null 2>&1 || true
}

connect_service_to_legacy_internal_networks() {
  csl_service="$1"
  csl_container="$(container_for_service "$csl_service")"
  [ -n "$csl_container" ] || return 0
  csl_seen=""
  for csl_peer in $SERVICE_ORDER; do
    csl_source="$(release_for_service "$csl_peer" "$BASE_RELEASE" || true)"
    [ -n "$csl_source" ] || continue
    csl_network="$(release_internal_network "$csl_source")"
    [ -n "$csl_network" ] || continue
    case " $csl_seen " in
      *" $csl_network "*) continue ;;
    esac
    csl_seen="$csl_seen $csl_network"
    connect_container_to_network "$csl_container" "$csl_network"
  done
}

container_for_service() {
  service="$1"
  read_env_value "$ENV_DIR/$service.env" APP_CONTAINER_NAME ""
}

container_for_service_in_release() {
  cf_release="$1"
  cf_service="$2"
  read_env_value "$cf_release/env/$cf_service.env" APP_CONTAINER_NAME ""
}

route_container_for_service() {
  rc_service="$1"
  rc_container="$(container_for_service "$rc_service")"
  if [ -n "$rc_container" ]; then
    printf "%s" "$rc_container"
    return
  fi
  rc_release="$(release_for_service "$rc_service" "$BASE_RELEASE" || true)"
  if [ -n "$rc_release" ]; then
    container_for_service_in_release "$rc_release" "$rc_service"
  fi
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

service_runtime_ready() {
  service="$1"
  container="$(container_for_service "$service")"
  [ -n "$container" ] || return 0
  status="$(container_status "$container")"
  if [ "$status" != "running" ]; then
    echo "$service container $container is not running: ${status:-missing}" >&2
    return 1
  fi
  health="$(container_health "$container")"
  if [ -n "$health" ] && [ "$health" != "healthy" ]; then
    echo "$service container $container health is $health" >&2
    return 1
  fi
  if [ "$service" != "web-vue3" ]; then
    restarts="$(container_restart_count "$container")"
    case "$restarts" in
      ""|*[!0-9]*) restarts=0 ;;
    esac
    if [ "$restarts" -gt 0 ]; then
      echo "$service container $container restarted $restarts time(s) during startup" >&2
      return 1
    fi
  fi
  return 0
}

wait_service_ready() {
  startup_timeout="$(read_env_value "$ENV_DIR/compose.env" APP_STARTUP_TIMEOUT 180)"
  stable_window="$(read_env_value "$ENV_DIR/compose.env" APP_STABLE_WINDOW 10)"
  case "$startup_timeout" in
    ""|*[!0-9]*) startup_timeout=180 ;;
  esac
  deadline=$(( $(date +%s) + startup_timeout ))
  while :; do
    if service_runtime_ready "$SERVICE_NAME"; then
      break
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "AIFAR service $SERVICE_NAME did not become ready in release $RELEASE_ID" >&2
      return 1
    fi
    sleep 3
  done
  case "$stable_window" in
    ""|*[!0-9]*) stable_window=10 ;;
  esac
  if [ "$stable_window" -gt 0 ]; then
    sleep "$stable_window"
  fi
  service_runtime_ready "$SERVICE_NAME"
}

write_ingress_config() {
  mkdir -p "$INGRESS_DIR"
  gateway_container="$(route_container_for_service gateway)"
  web_container="$(route_container_for_service web-vue3)"
  gateway_port="$(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)"
  web_port="$(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)"
  [ -n "$gateway_container" ] || fail "gateway route container is empty"
  [ -n "$web_container" ] || fail "web-vue3 route container is empty"
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
    server ${gateway_container}:${gateway_port};
  }
  upstream aifar_web {
    server ${web_container}:${web_port};
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

ingress_running() {
  [ "$(docker inspect --format '{{ "{{" }}.State.Running{{ "}}" }}' "$INGRESS_CONTAINER" 2>/dev/null || echo false)" = "true" ]
}

reload_ingress() {
  echo "reloading AIFAR ingress $INGRESS_CONTAINER"
  docker exec "$INGRESS_CONTAINER" nginx -t >/dev/null
  docker exec "$INGRESS_CONTAINER" nginx -s reload >/dev/null
  echo "AIFAR ingress reloaded"
}

ingress_config_needs_route_patch() {
  [ -f "$INGRESS_CONFIG" ] || return 0
  grep -q 'location /api/' "$INGRESS_CONFIG" || return 0
  grep -q 'location /im/ws/' "$INGRESS_CONFIG" || return 0
  grep -q 'proxy_pass http://aifar_gateway;' "$INGRESS_CONFIG" || return 0
  return 1
}

configure_ingress_if_needed() {
  case "$SERVICE_NAME" in
    gateway|web-vue3) ;;
    *)
      if ! ingress_config_needs_route_patch; then
        return 0
      fi
      ;;
  esac
  mkdir -p "$INGRESS_DIR"
  if [ -f "$INGRESS_CONFIG" ]; then
    cp "$INGRESS_CONFIG" "$INGRESS_CONFIG_BACKUP"
  fi
  write_ingress_config
  if ingress_running && reload_ingress; then
    return 0
  fi
  if [ -f "$INGRESS_CONFIG_BACKUP" ]; then
    cp "$INGRESS_CONFIG_BACKUP" "$INGRESS_CONFIG"
    reload_ingress || true
  fi
  return 1
}

apply_restart_policy_for_service() {
  policy="$(read_env_value "$ENV_DIR/compose.env" APP_RESTART_POLICY unless-stopped)"
  [ -n "$policy" ] || policy="unless-stopped"
  container="$(container_for_service "$SERVICE_NAME")"
  [ -n "$container" ] || return 0
  docker update --restart "$policy" "$container" >/dev/null 2>&1 || true
}

start_updated_service() {
  (
    cd "$RELEASE_DIR"
    APP_RESTART_POLICY=no
    export APP_RESTART_POLICY
    compose --env-file env/compose.env -f compose.yaml up -d --build --no-deps "$SERVICE_NAME"
  )
}

stop_service_in_release() {
  ss_release="$1"
  ss_service="$2"
  [ -n "$ss_release" ] || return 0
  [ -f "$ss_release/compose.yaml" ] || return 0
  (
    cd "$ss_release"
    compose --env-file env/compose.env -f compose.yaml stop "$ss_service" >/dev/null 2>&1 || true
    compose --env-file env/compose.env -f compose.yaml rm -f "$ss_service" >/dev/null 2>&1 || true
  )
}

rollback_service() {
  previous="$1"
  [ -n "$previous" ] || return 0
  [ -f "$previous/compose.yaml" ] || return 0
  echo "rolling back $SERVICE_NAME to previous AIFAR release: $previous"
  (
    cd "$previous"
    compose --env-file env/compose.env -f compose.yaml up -d --no-deps "$SERVICE_NAME" || true
  )
}

activate_release() {
  if [ -L "$CURRENT_LINK" ] || [ -f "$CURRENT_LINK" ]; then
    rm -f "$CURRENT_LINK"
  elif [ -d "$CURRENT_LINK" ]; then
    rm -rf "$CURRENT_LINK"
  fi
  ln -s "$RELEASE_DIR" "$CURRENT_LINK"
}

json_escape() {
  printf "%s" "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

write_containers_json() {
  wc_first=1
  for wc_service in $SERVICE_ORDER; do
    wc_container="$(container_for_service "$wc_service")"
    [ -n "$wc_container" ] || continue
    if [ "$wc_first" -eq 0 ]; then
      printf ",\n"
    fi
    printf '    "%s": "%s"' "$wc_service" "$wc_container"
    wc_first=0
  done
}

write_manifest() {
  status="$1"
  base_release_id="$2"
  artifact_file_json="$(json_escape "$ARTIFACT_FILE")"
  mkdir -p "$AIFAR_DIR"
  cat > "$AIFAR_DIR/manifest.json" <<MANIFEST
{
  "app": "aifar",
  "version": "$VERSION",
  "releaseId": "$RELEASE_ID",
  "layout": "release-v1",
  "kind": "partial",
  "status": "$status",
  "configHash": "$CONFIG_HASH",
  "baseReleaseId": "$base_release_id",
  "createdAt": "$CREATED_AT",
  "releaseRetention": $RELEASE_KEEP_COUNT,
  "composeProject": "$COMPOSE_PROJECT_NAME",
  "ingressNetwork": "$INGRESS_NETWORK",
  "internalNetwork": "$INTERNAL_NETWORK",
  "changedServices": ["$SERVICE_NAME"],
  "containers": {
$(write_containers_json)
  },
  "routes": {
    "gateway": {"container": "$(route_container_for_service gateway)", "port": $(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)},
    "web-vue3": {"container": "$(route_container_for_service web-vue3)", "port": $(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)}
  },
  "artifacts": {
    "$SERVICE_NAME": {
      "file": "$artifact_file_json",
      "sha256": "$ARTIFACT_SHA256",
      "size": $ARTIFACT_SIZE
    }
  }
}
MANIFEST
}

release_chain_ids() {
  rci_candidate="$1"
  rci_seen=""
  while [ -n "$rci_candidate" ] && [ -d "$rci_candidate" ]; do
    rci_id="$(basename "$rci_candidate")"
    case " $rci_seen " in
      *" $rci_id "*) break ;;
    esac
    rci_seen="$rci_seen $rci_id"
    printf "%s\n" "$rci_id"
    rci_base_id="$(manifest_value "$rci_candidate/.aifar/manifest.json" baseReleaseId || true)"
    rci_candidate="$(release_by_id "$rci_base_id" || true)"
  done
}

release_id_protected() {
  rip_id="$1"
  for rip_keep_id in $PROTECTED_RELEASE_IDS; do
    [ "$rip_keep_id" = "$rip_id" ] && return 0
  done
  return 1
}

cleanup_old_releases() {
  [ -d "$RELEASES_DIR" ] || return 0
  current="$(current_release || true)"
  PROTECTED_RELEASE_IDS=""
  if [ -n "$current" ]; then
    PROTECTED_RELEASE_IDS="$(release_chain_ids "$current" || true)"
  fi
  count=0
  find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d | sort -r | while read -r release_dir; do
    [ -f "$release_dir/.aifar/manifest.json" ] || continue
    grep -q '"status"[[:space:]]*:[[:space:]]*"success"' "$release_dir/.aifar/manifest.json" || continue
    count=$((count + 1))
    if [ "$count" -le "$RELEASE_KEEP_COUNT" ]; then
      PROTECTED_RELEASE_IDS="$PROTECTED_RELEASE_IDS $(release_chain_ids "$release_dir" || true)"
      continue
    fi
    if [ -n "$current" ] && [ "$(readlink -f "$release_dir" 2>/dev/null || printf "%s" "$release_dir")" = "$current" ]; then
      continue
    fi
    if release_id_protected "$(basename "$release_dir")"; then
      continue
    fi
    rm -rf "$release_dir"
  done
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
service_known || fail "unsupported AIFAR service: $SERVICE_NAME"
verify_artifact

BASE_RELEASE="$(current_release || true)"
[ -n "$BASE_RELEASE" ] || fail "current AIFAR release is missing"
[ -d "$BASE_RELEASE" ] || fail "current AIFAR release directory is missing: $BASE_RELEASE"
[ -f "$BASE_RELEASE/compose.yaml" ] || fail "current AIFAR release compose.yaml is missing"
BASE_RELEASE_ID="$(basename "$BASE_RELEASE")"
SERVICE_BASE_RELEASE="$(release_for_service "$SERVICE_NAME" "$BASE_RELEASE" || true)"
[ -n "$SERVICE_BASE_RELEASE" ] || fail "service directory is missing in current release chain: $SERVICE_NAME"

mkdir -p "$INSTALL_ROOT" "$WORK_DIR" "$RELEASES_DIR"
rm -rf "$RELEASE_DIR" "$TMP_ARTIFACT_DIR"
mkdir -p "$RELEASE_DIR" "$ENV_DIR" "$AIFAR_DIR"
copy_shared_release_files "$BASE_RELEASE"
copy_service_release_files "$SERVICE_BASE_RELEASE"
materialize_effective_service_dirs
write_partial_compose_env

apply_artifact
retag_selected_service
if [ "$SERVICE_NAME" = "web-vue3" ]; then
  patch_web_nginx_gateway_target
fi
patch_compose_service_release "$SERVICE_NAME"
write_manifest "pending" "$BASE_RELEASE_ID"
ensure_network

if ! start_updated_service; then
  write_manifest "failed" "$BASE_RELEASE_ID"
  stop_service_in_release "$RELEASE_DIR" "$SERVICE_NAME"
  rollback_service "$SERVICE_BASE_RELEASE"
  fail "AIFAR service $SERVICE_NAME failed to start in partial release $RELEASE_ID"
fi
connect_service_to_legacy_internal_networks "$SERVICE_NAME"

if ! wait_service_ready; then
  write_manifest "failed" "$BASE_RELEASE_ID"
  stop_service_in_release "$RELEASE_DIR" "$SERVICE_NAME"
  rollback_service "$SERVICE_BASE_RELEASE"
  fail "AIFAR service $SERVICE_NAME did not become stable in partial release $RELEASE_ID"
fi

if ! configure_ingress_if_needed; then
  write_manifest "failed" "$BASE_RELEASE_ID"
  stop_service_in_release "$RELEASE_DIR" "$SERVICE_NAME"
  rollback_service "$SERVICE_BASE_RELEASE"
  fail "AIFAR ingress switch failed for service $SERVICE_NAME in partial release $RELEASE_ID"
fi

apply_restart_policy_for_service
stop_service_in_release "$SERVICE_BASE_RELEASE" "$SERVICE_NAME"
write_manifest "success" "$BASE_RELEASE_ID"
activate_release
cleanup_old_releases
rm -rf "$TMP_ARTIFACT_DIR"

echo "AIFAR service $SERVICE_NAME updated under $INSTALL_ROOT release $RELEASE_ID"
