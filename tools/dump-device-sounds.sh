#!/usr/bin/env bash
# Compares the UI sounds on the connected One UI phone with the old Galaxy
# sounds in ./sounds/ui and shows which ones match automatically and which
# need a line in ui-map.conf.
#
# Usage: tools/dump-device-sounds.sh [old_ui_folder]
set -euo pipefail

export MSYS_NO_PATHCONV=1

old_ui=${1:-"$(cd "$(dirname "$0")/.." && pwd)/sounds/ui"}

command -v adb >/dev/null || { echo "adb not found in PATH" >&2; exit 1; }

# shellcheck disable=SC2016  # expanded by the device shell, not here
device_list=$(adb shell 'for d in /system/media/audio/ui /product/media/audio/ui; do
  if [ -d "$d" ]; then ls -1 "$d"; break; fi; done' | tr -d '\r' | sort -f)

[ -n "$device_list" ] || { echo "Could not list UI sounds on the device" >&2; exit 1; }

old_list=""
[ -d "$old_ui" ] && old_list=$(find "$old_ui" -maxdepth 1 -type f ! -name '.*' -exec basename {} \; | sort -f)

printf '%-40s %s\n' "ONE UI FILE" "STATUS"
printf '%-40s %s\n' "-----------" "------"
contains() {  # contains <name> <list>: case-insensitive exact line match
  awk -v k="$(tr '[:upper:]' '[:lower:]' <<<"$1")" 'tolower($0) == k { f = 1; exit } END { exit !f }' <<<"$2"
}

matched=0; total=0
while IFS= read -r f; do
  [ -n "$f" ] || continue
  total=$((total + 1))
  if [ -n "$old_list" ] && contains "$f" "$old_list"; then
    printf '%-40s %s\n' "$f" "auto (same name)"
    matched=$((matched + 1))
  else
    printf '%-40s %s\n' "$f" "-"
  fi
done <<<"$device_list"

echo
echo "$matched of $total One UI sounds match automatically."
if [ -n "$old_list" ]; then
  echo
  echo "Old Galaxy sounds with no automatic match (candidates for ui-map.conf):"
  while IFS= read -r f; do
    contains "$f" "$device_list" || echo "  $f"
  done <<<"$old_list"
fi
