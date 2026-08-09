#!/usr/bin/env sh
set -eu

INSTALL_ROOT={{ quote .InstallRoot }}
ROLLBACK_DIR={{ quote .RollbackDir }}
SERVICE_NAME={{ quote .ServiceName }}
ARTIFACT_REMOTE={{ quote .ArtifactRemote }}
ARTIFACT_FILE={{ quote .ArtifactFileName }}
ARTIFACT_SHA256={{ quote .ArtifactSHA256 }}
TARGET_REVISION={{ quote .TargetRevision }}

APP_DIR="$INSTALL_ROOT/runtime/services/$SERVICE_NAME"
SERVICE_ENV="$INSTALL_ROOT/runtime/env/$SERVICE_NAME.env"
ROLLBACK_ARTIFACT="$ROLLBACK_DIR/services/$SERVICE_NAME/artifact/$(basename "$ARTIFACT_FILE")"
TMP_DIR="$INSTALL_ROOT/.rollback-$SERVICE_NAME-$$"

fail() { echo "ERROR: $*" >&2; exit 1; }

set_env() {
  key="$1" value="$2" file="$3" tmp="$file.tmp"
  [ -f "$file" ] || touch "$file"
  grep -v "^${key}=" "$file" > "$tmp" || true
  printf "%s=%s\n" "$key" "$value" >> "$tmp"
  mv "$tmp" "$file"
}

cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT INT TERM

[ -f "$ARTIFACT_REMOTE" ] || fail "rollback artifact is missing"
[ -d "$APP_DIR" ] || fail "service directory is missing"
[ -f "$SERVICE_ENV" ] || fail "service environment is missing"
actual="$(sha256sum "$ARTIFACT_REMOTE" | awk '{print $1}')"
[ "$actual" = "$ARTIFACT_SHA256" ] || fail "rollback artifact checksum mismatch"
mkdir -p "$(dirname "$ROLLBACK_ARTIFACT")"
cp "$ARTIFACT_REMOTE" "$ROLLBACK_ARTIFACT"
printf "%s  %s\n" "$ARTIFACT_SHA256" "$(basename "$ROLLBACK_ARTIFACT")" > "$(dirname "$ROLLBACK_ARTIFACT")/sha256"

case "$SERVICE_NAME" in
  web-vue3)
    mkdir -p "$TMP_DIR/web"
    case "$ARTIFACT_FILE" in
      *.zip) unzip -q "$ROLLBACK_ARTIFACT" -d "$TMP_DIR/web" ;;
      *.tar|*.tgz|*.tar.gz) tar -xf "$ROLLBACK_ARTIFACT" -C "$TMP_DIR/web" ;;
      *) fail "unsupported web artifact type" ;;
    esac
    rm -rf "$APP_DIR/dist"
    if [ -d "$TMP_DIR/web/dist" ]; then cp -a "$TMP_DIR/web/dist" "$APP_DIR/dist"; else mkdir -p "$APP_DIR/dist"; cp -a "$TMP_DIR/web/." "$APP_DIR/dist/"; fi
    ;;
  *)
    mkdir -p "$APP_DIR/target"
    cp "$ROLLBACK_ARTIFACT" "$APP_DIR/app.jar"
    rm -f "$APP_DIR"/target/*.jar
    cp "$ROLLBACK_ARTIFACT" "$APP_DIR/target/$(basename "$ARTIFACT_FILE")"
    ;;
esac

image="aifar-$SERVICE_NAME:$TARGET_REVISION"
set_env APP_IMAGE "$image" "$SERVICE_ENV"
set_env AIFAR_REVISION "$TARGET_REVISION" "$SERVICE_ENV"
docker build -t "$image" "$APP_DIR"
