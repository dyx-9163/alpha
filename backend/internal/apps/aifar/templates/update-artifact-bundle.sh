#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
REVISION={{ quote .ReleaseID }}

fail() { echo "ERROR: $*" >&2; exit 1; }

set_env() {
  key="$1" value="$2" file="$3" tmp="$file.tmp"
  [ -f "$file" ] || touch "$file"
  grep -v "^${key}=" "$file" > "$tmp" || true
  printf "%s=%s\n" "$key" "$value" >> "$tmp"
  mv "$tmp" "$file"
}

prepare_artifact() {
  service="$1" remote="$2" release="$3" file_name="$4" expected="$5"
  app_dir="$INSTALL_ROOT/runtime/services/$service"
  service_env="$INSTALL_ROOT/runtime/env/$service.env"
  tmp_dir="$INSTALL_ROOT/.artifact-$REVISION-$service-$$"
  [ -f "$remote" ] || fail "artifact is missing"
  [ -d "$app_dir" ] || fail "service directory is missing"
  [ -f "$service_env" ] || fail "service environment is missing"
  actual="$(sha256sum "$remote" | awk '{print $1}')"
  [ "$actual" = "$expected" ] || fail "artifact checksum mismatch"
  mkdir -p "$(dirname "$release")"
  cp "$remote" "$release"
  printf "%s  %s\n" "$expected" "$(basename "$release")" > "$(dirname "$release")/sha256"
  case "$service" in
    web-vue3)
      rm -rf "$tmp_dir" "$app_dir/dist"
      mkdir -p "$tmp_dir/web"
      case "$file_name" in
        *.zip) unzip -q "$release" -d "$tmp_dir/web" ;;
        *.tar|*.tgz|*.tar.gz) tar -xf "$release" -C "$tmp_dir/web" ;;
        *) fail "unsupported web artifact type" ;;
      esac
      if [ -d "$tmp_dir/web/dist" ]; then cp -a "$tmp_dir/web/dist" "$app_dir/dist"; else mkdir -p "$app_dir/dist"; cp -a "$tmp_dir/web/." "$app_dir/dist/"; fi
      rm -rf "$tmp_dir"
      ;;
    *)
      mkdir -p "$app_dir/target"
      cp "$release" "$app_dir/app.jar"
      rm -f "$app_dir"/target/*.jar
      cp "$release" "$app_dir/target/$(basename "$file_name")"
      ;;
  esac
  image="aifar-$service:$REVISION"
  set_env APP_IMAGE "$image" "$service_env"
  set_env AIFAR_REVISION "$REVISION" "$service_env"
  docker build -t "$image" "$app_dir"
}

{{ range .Artifacts -}}
prepare_artifact {{ quote .ServiceName }} {{ quote .ArtifactRemote }} {{ quote .ReleaseArtifact }} {{ quote .ArtifactFile }} {{ quote .ArtifactSHA256 }}
{{ end -}}
