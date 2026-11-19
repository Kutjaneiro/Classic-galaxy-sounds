#!/usr/bin/env bash
# Builds the flashable zip into dist/, with the firmware extractor compiled
# for the phone (needs Go). Any audio files you placed in sounds/ are
# included (personal builds only, never publish a zip that contains
# Samsung's sounds).
#
#   tools/build.sh            module zip
#   tools/build.sh --pc       also the extractor for Windows, macOS and Linux
set -euo pipefail

cd "$(dirname "$0")/.."

version=$(sed -n 's/^version=//p' module.prop | tr -d '\r')
out="dist/ClassicGalaxySounds-$version-root-module.zip"
stage=build/bin
files=(META-INF module.prop customize.sh action.sh service.sh uninstall.sh common config.conf ui-map.conf sounds maps LICENSE)

mkdir -p dist
rm -rf "$out" build
mkdir -p "$stage"

# CRLF line endings break the scripts on Android
if grep -rlI $'\r' "${files[@]}" 2>/dev/null; then
  echo "error: the files above have CRLF line endings" >&2
  exit 1
fi

command -v go >/dev/null || { echo "error: Go is needed to build the extractor (https://go.dev/dl/)" >&2; exit 1; }

# Static binaries: Android runs plain Linux ELF files, no NDK needed
build_extractor() {  # goos goarch output
  (cd extractor && CGO_ENABLED=0 GOOS=$1 GOARCH=$2 \
    go build -trimpath -ldflags="-s -w" -o "../$3" ./cmd/cgs-extract)
}
build_extractor linux arm64 "$stage/cgs-extract-arm64"   # every One UI 8/9 phone
build_extractor linux amd64 "$stage/cgs-extract-x64"     # emulators

if [ "${1:-}" = "--pc" ]; then
  version_tag=${version#v}
  build_extractor windows amd64 "dist/ClassicGalaxySounds-v$version_tag-sound-extractor-windows-x64.exe"
  build_extractor darwin arm64 "dist/ClassicGalaxySounds-v$version_tag-sound-extractor-macos-arm64"
  build_extractor darwin amd64 "dist/ClassicGalaxySounds-v$version_tag-sound-extractor-macos-x64"
  build_extractor linux amd64 "dist/ClassicGalaxySounds-v$version_tag-sound-extractor-linux-x64"
fi

if command -v zip >/dev/null; then
  zip -qr9 "$out" "${files[@]}" -x '*.DS_Store' '*/.gitkeep'
  (cd build && zip -qr9 "../$out" bin)
else
  py=""
  for c in python3 python; do   # skips the Windows Store "python3" stub
    if "$c" -c 'import zipfile' >/dev/null 2>&1; then py=$c; break; fi
  done
  [ -n "$py" ] || { echo "error: need zip or python to build" >&2; exit 1; }
  "$py" - "$out" "${files[@]}" <<'PY'
import os, sys, zipfile
out, *paths = sys.argv[1:]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    def add(f, arc):
        arc = arc.replace(os.sep, "/")  # forward slashes for Android
        info = zipfile.ZipInfo.from_file(f, arc)
        info.compress_type = zipfile.ZIP_DEFLATED
        if arc.startswith("bin/"):
            info.external_attr = 0o100755 << 16  # executable
        with open(f, "rb") as src:
            z.writestr(info, src.read())
    for p in paths + ["build/bin"]:
        walk = [(os.path.dirname(p), [], [os.path.basename(p)])] if os.path.isfile(p) else os.walk(p)
        for root, _, names in walk:
            for n in sorted(names):
                if n in (".DS_Store", ".gitkeep"):
                    continue
                f = os.path.join(root, n)
                add(f, os.path.relpath(f, "build") if f.startswith("build") else f)
PY
fi

echo "Built $out ($(du -h "$out" | cut -f1))"
