#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
WORK_DIR={{ quote .WorkDir }}
SERVICE_ORDER={{ quote .ServiceOrder }}
CHANGED_SERVICES={{ quote .ChangedServices }}
VERSION={{ quote .Version }}
RELEASE_ID={{ quote .ReleaseID }}
CREATED_AT={{ quote .CreatedAt }}
CONFIG_HASH={{ quote .ConfigHash }}
RELEASE_KEEP_COUNT={{ .ReleaseKeepCount }}
COMPOSE_PROJECT_NAME={{ quote .ComposeProject }}
INGRESS_NETWORK={{ quote .IngressNetwork }}
INTERNAL_NETWORK={{ quote .InternalNetwork }}
INGRESS_CONTAINER={{ quote .IngressContainer }}
DEPLOYMENT_CONCURRENCY={{ .Concurrency }}

RELEASES_DIR="$INSTALL_ROOT/releases"
CURRENT_LINK="$INSTALL_ROOT/current"
RELEASE_DIR="$RELEASES_DIR/$RELEASE_ID"
APP_DIR="$RELEASE_DIR/docker-apps"
ENV_DIR="$RELEASE_DIR/env"
AIFAR_DIR="$RELEASE_DIR/.aifar"
INGRESS_DIR="$INSTALL_ROOT/ingress"
INGRESS_CONFIG="$INGRESS_DIR/nginx.conf"
INGRESS_CONFIG_BACKUP="$INGRESS_DIR/nginx.conf.previous"
TMP_ARTIFACT_DIR="$INSTALL_ROOT/.partial-bundle-$RELEASE_ID-$$"

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
  sk_service="$1"
  for service in $SERVICE_ORDER; do
    [ "$service" = "$sk_service" ] && return 0
  done
  return 1
}

service_changed() {
  sc_service="$1"
  for service in $CHANGED_SERVICES; do
    [ "$service" = "$sc_service" ] && return 0
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

artifact_remote_for_service() {
  case "$1" in
{{- range .Artifacts }}
    {{ .ServiceName }}) printf "%s" {{ quote .ArtifactRemote }} ;;
{{- end }}
    *) return 1 ;;
  esac
}

artifact_file_for_service() {
  case "$1" in
{{- range .Artifacts }}
    {{ .ServiceName }}) printf "%s" {{ quote .ArtifactFile }} ;;
{{- end }}
    *) return 1 ;;
  esac
}

artifact_sha_for_service() {
  case "$1" in
{{- range .Artifacts }}
    {{ .ServiceName }}) printf "%s" {{ quote .ArtifactSHA256 }} ;;
{{- end }}
    *) return 1 ;;
  esac
}

artifact_size_for_service() {
  case "$1" in
{{- range .Artifacts }}
    {{ .ServiceName }}) printf "%s" "{{ .ArtifactSize }}" ;;
{{- end }}
    *) return 1 ;;
  esac
}

verify_artifacts() {
  for service in $CHANGED_SERVICES; do
    service_known "$service" || fail "unsupported AIFAR service: $service"
    remote="$(artifact_remote_for_service "$service")"
    expected="$(artifact_sha_for_service "$service")"
    [ -f "$remote" ] || fail "artifact file not found for $service: $remote"
    if command -v sha256sum >/dev/null 2>&1; then
      actual="$(sha256sum "$remote" | awk '{print $1}')"
      [ "$actual" = "$expected" ] || fail "$service artifact SHA256 mismatch: expected $expected got $actual"
    else
      echo "warning: sha256sum not found, skip $service artifact checksum verification"
    fi
  done
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
  csrf_service="$1"
  csrf_service_source="$2"
  csrf_service_dir="$csrf_service_source/docker-apps/$csrf_service"
  [ -d "$csrf_service_dir" ] || fail "service directory is missing in release chain: $csrf_service"
  csrf_real_dir="$(readlink -f "$csrf_service_dir" 2>/dev/null || printf "%s" "$csrf_service_dir")"
  [ -d "$csrf_real_dir" ] || fail "service directory is missing in release chain: $csrf_service"
  mkdir -p "$APP_DIR"
  rm -rf "$APP_DIR/$csrf_service"
  cp -a "$csrf_real_dir" "$APP_DIR/$csrf_service"
  [ ! -L "$APP_DIR/$csrf_service" ] || fail "changed service directory must be materialized: $csrf_service"
  if [ ! -f "$ENV_DIR/$csrf_service.env" ]; then
    copy_file_required "$csrf_service_source/env/$csrf_service.env" "$ENV_DIR/$csrf_service.env"
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
  aja_service="$1"
  aja_remote="$2"
  aja_file="$3"
  case "$aja_file" in
    *.jar) ;;
    *) fail "$aja_service update requires a .jar artifact" ;;
  esac
  service_dir="$APP_DIR/$aja_service"
  [ -d "$service_dir" ] || fail "service directory not found: $service_dir"
  target_dir="$service_dir/target"
  mkdir -p "$target_dir"
  rm -f "$target_dir"/*.jar
  cp "$aja_remote" "$target_dir/$aja_file"
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
  afa_remote="$1"
  afa_file="$2"
  rm -rf "$TMP_ARTIFACT_DIR"
  mkdir -p "$TMP_ARTIFACT_DIR"
  case "$afa_file" in
    *.tar.gz|*.tgz) tar -xzf "$afa_remote" -C "$TMP_ARTIFACT_DIR" ;;
    *.tar) tar -xf "$afa_remote" -C "$TMP_ARTIFACT_DIR" ;;
    *.zip)
      command -v unzip >/dev/null 2>&1 || fail "unzip is required for frontend zip artifacts"
      unzip -q "$afa_remote" -d "$TMP_ARTIFACT_DIR"
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

apply_service_artifact() {
  asa_service="$1"
  asa_remote="$(artifact_remote_for_service "$asa_service")"
  asa_file="$(artifact_file_for_service "$asa_service")"
  if [ "$asa_service" = "web-vue3" ]; then
    apply_frontend_artifact "$asa_remote" "$asa_file"
  else
    apply_java_artifact "$asa_service" "$asa_remote" "$asa_file"
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

retag_service() {
  rs_service="$1"
  service_env="$ENV_DIR/$rs_service.env"
  source_env="$APP_DIR/$rs_service/.env"
  [ -f "$service_env" ] || fail "service env not found: $service_env"
  image="$(read_env_value "$service_env" APP_IMAGE "$(read_env_value "$source_env" APP_IMAGE "aifar-$rs_service:latest")")"
  set_env APP_IMAGE "$(retag_image "$image")" "$service_env"
  set_env APP_CONTAINER_NAME "$(service_container_name "$rs_service")" "$service_env"
  set_service_runtime_port "$rs_service"
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
  [ -n "$rc_release" ] || return 1
  container_for_service_in_release "$rc_release" "$rc_service"
}

container_state() {
  docker inspect --format '{{ "{{" }}.State.Running{{ "}}" }}' "$1" 2>/dev/null || echo false
}

container_health() {
  docker inspect --format '{{ "{{" }}if .State.Health{{ "}}" }}{{ "{{" }}.State.Health.Status{{ "}}" }}{{ "{{" }}end{{ "}}" }}' "$1" 2>/dev/null || true
}

container_restart_count() {
  docker inspect --format '{{ "{{" }}.RestartCount{{ "}}" }}' "$1" 2>/dev/null || echo 0
}

service_runtime_ready() {
  service="$1"
  container="$(container_for_service "$service")"
  [ -n "$container" ] || return 1
  running="$(container_state "$container")"
  if [ "$running" != "true" ]; then
    echo "$service container $container is not running: ${running:-missing}" >&2
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
  service="$1"
  startup_timeout="$(read_env_value "$ENV_DIR/compose.env" APP_STARTUP_TIMEOUT 180)"
  stable_window="$(read_env_value "$ENV_DIR/compose.env" APP_STABLE_WINDOW 10)"
  case "$startup_timeout" in
    ""|*[!0-9]*) startup_timeout=180 ;;
  esac
  deadline=$(( $(date +%s) + startup_timeout ))
  while :; do
    if service_runtime_ready "$service"; then
      break
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
      echo "AIFAR service $service did not become ready in release $RELEASE_ID" >&2
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
  service_runtime_ready "$service"
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

entry_service_changed() {
  service_changed gateway || service_changed web-vue3
}

configure_ingress_if_needed() {
  if ! entry_service_changed && ! ingress_config_needs_route_patch; then
    return 0
  fi
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
  service="$1"
  policy="$(read_env_value "$ENV_DIR/compose.env" APP_RESTART_POLICY unless-stopped)"
  [ -n "$policy" ] || policy="unless-stopped"
  container="$(container_for_service "$service")"
  [ -n "$container" ] || return 0
  docker update --restart "$policy" "$container" >/dev/null 2>&1 || true
}

start_service() {
  service="$1"
  (
    cd "$RELEASE_DIR"
    APP_RESTART_POLICY=no
    export APP_RESTART_POLICY
    compose --env-file env/compose.env -f compose.yaml up -d --build --no-deps "$service"
  )
}

start_and_wait_service() {
  service="$1"
  echo "starting AIFAR service $service in release $RELEASE_ID"
  start_service "$service" && connect_service_to_legacy_internal_networks "$service" && wait_service_ready "$service"
}

wait_batch() {
  wb_failed=0
  wb_logs="$1"
  shift
  for wb_pid in "$@"; do
    if ! wait "$wb_pid"; then
      wb_failed=1
    fi
  done
  if [ "$wb_failed" -ne 0 ]; then
    for wb_log in $wb_logs; do
      [ -f "$wb_log" ] && cat "$wb_log" >&2
    done
    return 1
  fi
  return 0
}

run_parallel_services() {
  rps_limit="$DEPLOYMENT_CONCURRENCY"
  case "$rps_limit" in
    ""|*[!0-9]*) rps_limit=1 ;;
  esac
  [ "$rps_limit" -lt 1 ] && rps_limit=1
  rps_count=0
  rps_pids=""
  rps_logs=""
  for rps_service in "$@"; do
    [ -n "$rps_service" ] || continue
    rps_log="$WORK_DIR/update-$rps_service.log"
    ( start_and_wait_service "$rps_service" ) > "$rps_log" 2>&1 &
    rps_pids="$rps_pids $!"
    rps_logs="$rps_logs $rps_log"
    rps_count=$((rps_count + 1))
    if [ "$rps_count" -ge "$rps_limit" ]; then
      wait_batch "$rps_logs" $rps_pids || return 1
      rps_count=0
      rps_pids=""
      rps_logs=""
    fi
  done
  if [ -n "$rps_pids" ]; then
    wait_batch "$rps_logs" $rps_pids || return 1
  fi
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

stop_new_changed_services() {
  for service in $CHANGED_SERVICES; do
    stop_service_in_release "$RELEASE_DIR" "$service"
  done
}

rollback_changed_services() {
  for service in $CHANGED_SERVICES; do
    previous="$(release_for_service "$service" "$BASE_RELEASE" || true)"
    [ -n "$previous" ] || continue
    [ -f "$previous/compose.yaml" ] || continue
    echo "rolling back $service to previous AIFAR release: $previous"
    (
      cd "$previous"
      compose --env-file env/compose.env -f compose.yaml up -d --no-deps "$service" || true
    )
  done
}

stop_old_changed_services() {
  for service in $CHANGED_SERVICES; do
    previous="$(release_for_service "$service" "$BASE_RELEASE" || true)"
    stop_service_in_release "$previous" "$service"
  done
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
  "deploymentConcurrency": $DEPLOYMENT_CONCURRENCY,
  "composeProject": "$COMPOSE_PROJECT_NAME",
  "ingressNetwork": "$INGRESS_NETWORK",
  "internalNetwork": "$INTERNAL_NETWORK",
  "changedServices": [{{ range $index, $artifact := .Artifacts }}{{ if $index }}, {{ end }}"{{ $artifact.ServiceName }}"{{ end }}],
  "containers": {
$(write_containers_json)
  },
  "routes": {
    "gateway": {"container": "$(route_container_for_service gateway)", "port": $(read_env_value "$ENV_DIR/compose.env" GATEWAY_PORT 38000)},
    "web-vue3": {"container": "$(route_container_for_service web-vue3)", "port": $(read_env_value "$ENV_DIR/compose.env" WEB_VUE3_PORT 8080)}
  },
  "artifacts": {
{{- range $index, $artifact := .Artifacts }}
    {{ if $index }},{{ end }}"{{ $artifact.ServiceName }}": {
      "file": "$(json_escape "{{ $artifact.ArtifactFile }}")",
      "sha256": "{{ $artifact.ArtifactSHA256 }}",
      "size": {{ $artifact.ArtifactSize }}
    }
{{- end }}
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

non_entry_changed_services() {
  for service in $CHANGED_SERVICES; do
    case "$service" in
      gateway|web-vue3) ;;
      *) printf "%s\n" "$service" ;;
    esac
  done
}

entry_changed_services() {
  for service in gateway web-vue3; do
    if service_changed "$service"; then
      printf "%s\n" "$service"
    fi
  done
}

fail_update() {
  write_manifest "failed" "$BASE_RELEASE_ID"
  stop_new_changed_services
  rollback_changed_services
  rm -rf "$TMP_ARTIFACT_DIR"
  fail "$1"
}

command -v docker >/dev/null 2>&1 || fail "docker command is required"
docker info >/dev/null 2>&1 || fail "docker daemon is not available"
[ -n "$CHANGED_SERVICES" ] || fail "no AIFAR services selected for bundle update"
verify_artifacts

BASE_RELEASE="$(current_release || true)"
[ -n "$BASE_RELEASE" ] || fail "current AIFAR release is missing"
[ -d "$BASE_RELEASE" ] || fail "current AIFAR release directory is missing: $BASE_RELEASE"
[ -f "$BASE_RELEASE/compose.yaml" ] || fail "current AIFAR release compose.yaml is missing"
BASE_RELEASE_ID="$(basename "$BASE_RELEASE")"

mkdir -p "$INSTALL_ROOT" "$WORK_DIR" "$RELEASES_DIR"
rm -rf "$RELEASE_DIR" "$TMP_ARTIFACT_DIR"
mkdir -p "$RELEASE_DIR" "$ENV_DIR" "$AIFAR_DIR"
copy_shared_release_files "$BASE_RELEASE"

for service in $CHANGED_SERVICES; do
  service_base_release="$(release_for_service "$service" "$BASE_RELEASE" || true)"
  [ -n "$service_base_release" ] || fail "service directory is missing in current release chain: $service"
  copy_service_release_files "$service" "$service_base_release"
done

materialize_effective_service_dirs
write_partial_compose_env

for service in $CHANGED_SERVICES; do
  apply_service_artifact "$service"
  retag_service "$service"
  patch_compose_service_release "$service"
done
if service_changed web-vue3; then
  patch_web_nginx_gateway_target
fi

write_manifest "pending" "$BASE_RELEASE_ID"
ensure_network

non_entry_services="$(non_entry_changed_services | tr '\n' ' ' || true)"
if [ -n "$non_entry_services" ]; then
  run_parallel_services $non_entry_services || fail_update "AIFAR non-entry services failed in partial bundle release $RELEASE_ID"
fi

entry_services="$(entry_changed_services | tr '\n' ' ' || true)"
for service in $entry_services; do
  start_and_wait_service "$service" || fail_update "AIFAR entry service $service failed in partial bundle release $RELEASE_ID"
done

configure_ingress_if_needed || fail_update "AIFAR ingress switch failed in partial bundle release $RELEASE_ID"

for service in $CHANGED_SERVICES; do
  apply_restart_policy_for_service "$service"
done
stop_old_changed_services
write_manifest "success" "$BASE_RELEASE_ID"
activate_release
cleanup_old_releases
rm -rf "$TMP_ARTIFACT_DIR"

echo "AIFAR services updated under $INSTALL_ROOT release $RELEASE_ID: $CHANGED_SERVICES"
