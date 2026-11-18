#!/usr/bin/env bash
# Copies the stock sounds from an old Galaxy you own (USB debugging enabled)
# into ./sounds. No root needed: /system/media/audio is world-readable.
# Without the old phone, use cgs-extract on its official firmware.
#
# Usage: tools/pull-from-device.sh [destination]
set -euo pipefail

export MSYS_NO_PATHCONV=1   # stop Git Bash on Windows from mangling /system paths

repo=$(cd "$(dirname "$0")/.." && pwd)
dest=${1:-"$repo/sounds"}

command -v adb >/dev/null || { echo "adb not found in PATH" >&2; exit 1; }
adb get-state >/dev/null 2>&1 || { echo "No device connected (adb devices)" >&2; exit 1; }

prop() { adb shell getprop "$1" | tr -d '\r'; }
model=$(prop ro.product.model)
echo "Connected device: $model (Android $(prop ro.build.version.release))"

for d in ui notifications ringtones alarms; do
  if [ -n "$(find "$dest/$d" -maxdepth 1 -type f ! -name '.*' ! -name '*.md' 2>/dev/null | head -n 1)" ]; then
    echo "$dest/$d already has sounds. Use an empty destination folder." >&2
    exit 1
  fi
done

for d in ui notifications ringtones alarms; do
  mkdir -p "$dest/$d"
  adb pull "/system/media/audio/$d/." "$dest/$d/" >/dev/null
  printf '  %-14s %s sounds\n' "$d" "$(find "$dest/$d" -type f ! -name '.*' | wc -l | tr -d ' ')"
done

: > "$dest/.nomedia"   # hide from music players and galleries
echo "$model / Android $(prop ro.build.version.release) / $(prop ro.build.display.id)" > "$dest/firmware-info.txt"

# Same map selection as cgs-extract: first maps/*.conf listing the model
map=""
for conf in "$repo"/maps/*.conf; do
  read -ra models <<<"$(sed -n 's/^# models://p' "$conf")"
  for m in "${models[@]}"; do
    case "$model" in "$m"*) map=$conf; break 2 ;; esac
  done
done
if [ -n "$map" ]; then
  cp "$map" "$dest/ui-map.conf"
  echo "UI map: ${map##*/}"
else
  echo "UI map: none for $model, only same-name matching will be used"
fi

echo "Sounds saved in $dest"
echo "Next: adb push \"$dest\" /sdcard/ClassicGalaxySounds, then flash the module."
