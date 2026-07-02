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

RELEASES_DIR="$INSTALL_ROOT/releases"
CURRENT_LINK="$INSTALL_ROOT/current"
RELEASE_DIR="$RELEASES_DIR/$RELEASE_ID"
APP_DIR="$RELEASE_DIR/docker-apps"
ENV_DIR="$RELEASE_DIR/env"
AIFAR_DIR="$RELEASE_DIR/.aifar"
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

current_release() {
  if [ -L "$CURRENT_LINK" ] || [ -d "$CURRENT_LINK" ]; then
    readlink -f "$CURRENT_LINK" 2>/dev/null || printf "%s" "$CURRENT_LINK"
  fi
}

service_known() {
  for service in $SERVICE_ORDER; do
    [ "$service" = "$SERVICE_NAME" ] && return 0
  done
  return 1
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

retag_selected_service() {
  service_env="$ENV_DIR/$SERVICE_NAME.env"
  source_env="$APP_DIR/$SERVICE_NAME/.env"
  [ -f "$service_env" ] || fail "service env not found: $service_env"
  image="$(read_env_value "$service_env" APP_IMAGE "$(read_env_value "$source_env" APP_IMAGE "aifar-$SERVICE_NAME:latest")")"
  set_env APP_IMAGE "$(retag_image "$image")" "$service_env"
}

ensure_network() {
  network="$(read_env_value "$ENV_DIR/compose.env" APP_NETWORK_NAME aifar-network)"
  docker network inspect "$network" >/dev/null 2>&1 || docker network create --driver bridge "$network" >/dev/null
}

container_for_service() {
  service="$1"
  read_env_value "$ENV_DIR/$service.env" APP_CONTAINER_NAME ""
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
    compose --env-file env/compose.env -f compose.yaml up -d --build "$SERVICE_NAME"
  )
}

rollback_service() {
  previous="$1"
  [ -n "$previous" ] || return 0
  [ -f "$previous/compose.yaml" ] || return 0
  echo "rolling back $SERVICE_NAME to previous AIFAR release: $previous"
  (
    cd "$previous"
    compose --env-file env/compose.env -f compose.yaml up -d "$SERVICE_NAME" || true
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
  "changedServices": ["$SERVICE_NAME"],
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

cleanup_old_releases() {
  [ -d "$RELEASES_DIR" ] || return 0
  current="$(current_release || true)"
  count=0
  find "$RELEASES_DIR" -mindepth 1 -maxdepth 1 -type d | sort -r | while read -r release_dir; do
    [ -f "$release_dir/.aifar/manifest.json" ] || continue
    grep -q '"status"[[:space:]]*:[[:space:]]*"success"' "$release_dir/.aifar/manifest.json" || continue
    count=$((count + 1))
    if [ "$count" -le "$RELEASE_KEEP_COUNT" ]; then
      continue
    fi
    if [ -n "$current" ] && [ "$(readlink -f "$release_dir" 2>/dev/null || printf "%s" "$release_dir")" = "$current" ]; then
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
[ -d "$BASE_RELEASE/docker-apps/$SERVICE_NAME" ] || fail "service directory is missing in current release: $SERVICE_NAME"
BASE_RELEASE_ID="$(basename "$BASE_RELEASE")"

mkdir -p "$INSTALL_ROOT" "$WORK_DIR" "$RELEASES_DIR"
rm -rf "$RELEASE_DIR" "$TMP_ARTIFACT_DIR"
mkdir -p "$RELEASE_DIR"
cp -a "$BASE_RELEASE/." "$RELEASE_DIR/"
mkdir -p "$ENV_DIR" "$AIFAR_DIR"

apply_artifact
retag_selected_service
write_manifest "pending" "$BASE_RELEASE_ID"
ensure_network

if ! start_updated_service; then
  write_manifest "failed" "$BASE_RELEASE_ID"
  rollback_service "$BASE_RELEASE"
  fail "AIFAR service $SERVICE_NAME failed to start in partial release $RELEASE_ID"
fi

if ! wait_service_ready; then
  write_manifest "failed" "$BASE_RELEASE_ID"
  rollback_service "$BASE_RELEASE"
  fail "AIFAR service $SERVICE_NAME did not become stable in partial release $RELEASE_ID"
fi

apply_restart_policy_for_service
write_manifest "success" "$BASE_RELEASE_ID"
activate_release
cleanup_old_releases
rm -rf "$TMP_ARTIFACT_DIR"

echo "AIFAR service $SERVICE_NAME updated under $INSTALL_ROOT release $RELEASE_ID"
