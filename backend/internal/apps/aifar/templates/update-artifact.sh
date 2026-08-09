#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
SERVICE_NAME={{ quote .ServiceName }}
ARTIFACT_REMOTE={{ quote .ArtifactRemote }}
RELEASE_ARTIFACT={{ quote .ReleaseArtifact }}
ARTIFACT_FILE={{ quote .ArtifactFileName }}
ARTIFACT_SHA256={{ quote .ArtifactSHA256 }}
REVISION={{ quote .ReleaseID }}

APP_DIR="$INSTALL_ROOT/runtime/services/$SERVICE_NAME"
ENV_DIR="$INSTALL_ROOT/runtime/env"
SERVICE_ENV="$ENV_DIR/$SERVICE_NAME.env"
TMP_DIR="$INSTALL_ROOT/.artifact-$REVISION-$$"

fail() { echo "ERROR: $*" >&2; exit 1; }

read_env() {
  file="$1" key="$2" fallback="${3:-}"
  value=""
  if [ -f "$file" ]; then
    value="$(awk -F= -v k="$key" '$1==k {print substr($0, index($0, "=")+1)}' "$file" | tail -n 1)"
  fi
  [ -n "$value" ] && printf "%s" "$value" || printf "%s" "$fallback"
}

set_env() {
  key="$1" value="$2" file="$3" tmp="$file.tmp"
  [ -f "$file" ] || touch "$file"
  grep -v "^${key}=" "$file" > "$tmp" || true
  printf "%s=%s\n" "$key" "$value" >> "$tmp"
  mv "$tmp" "$file"
}

cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT INT TERM

[ -f "$ARTIFACT_REMOTE" ] || fail "artifact is missing"
[ -d "$APP_DIR" ] || fail "service directory is missing"
[ -f "$SERVICE_ENV" ] || fail "service environment is missing"
actual="$(sha256sum "$ARTIFACT_REMOTE" | awk '{print $1}')"
[ "$actual" = "$ARTIFACT_SHA256" ] || fail "artifact checksum mismatch"

mkdir -p "$(dirname "$RELEASE_ARTIFACT")"
cp "$ARTIFACT_REMOTE" "$RELEASE_ARTIFACT"
printf "%s  %s\n" "$ARTIFACT_SHA256" "$(basename "$RELEASE_ARTIFACT")" > "$(dirname "$RELEASE_ARTIFACT")/sha256"

case "$SERVICE_NAME" in
  web-vue3)
    mkdir -p "$TMP_DIR/web"
    case "$ARTIFACT_FILE" in
      *.zip) unzip -q "$RELEASE_ARTIFACT" -d "$TMP_DIR/web" ;;
      *.tar|*.tgz|*.tar.gz) tar -xf "$RELEASE_ARTIFACT" -C "$TMP_DIR/web" ;;
      *) fail "unsupported web artifact type" ;;
    esac
    rm -rf "$APP_DIR/dist"
    if [ -d "$TMP_DIR/web/dist" ]; then
      cp -a "$TMP_DIR/web/dist" "$APP_DIR/dist"
    else
      mkdir -p "$APP_DIR/dist"
      cp -a "$TMP_DIR/web/." "$APP_DIR/dist/"
    fi
    ;;
  *)
    mkdir -p "$APP_DIR/target"
    cp "$RELEASE_ARTIFACT" "$APP_DIR/app.jar"
    rm -f "$APP_DIR"/target/*.jar
    cp "$RELEASE_ARTIFACT" "$APP_DIR/target/$(basename "$ARTIFACT_FILE")"
    ;;
esac

image="aifar-$SERVICE_NAME:$REVISION"
set_env APP_IMAGE "$image" "$SERVICE_ENV"
set_env AIFAR_REVISION "$REVISION" "$SERVICE_ENV"
docker build -t "$image" "$APP_DIR"
