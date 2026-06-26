#!/usr/bin/env sh
set -eu
ROOT="$(CDPATH= cd "$(dirname "$0")/.." && pwd)"
ZIP="$ROOT/aifar-deployment.zip"
if [ ! -f "$ZIP" ]; then
  echo "Missing $ZIP" >&2
  exit 1
fi
python3 - "$ZIP" "$ROOT/resources" <<'PY'
import sys, zipfile, pathlib
zip_path = pathlib.Path(sys.argv[1])
target = pathlib.Path(sys.argv[2])
with zipfile.ZipFile(zip_path) as z:
    for info in z.infolist():
        name = info.filename.replace("\\", "/")
        if not name.startswith("resources/") or name.endswith("/"):
            continue
        rel = pathlib.Path(name[len("resources/"):])
        out = target / rel
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_bytes(z.read(info))
print(f"Resources extracted to {target}")
PY
