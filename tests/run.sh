#!/usr/bin/env bash
# Runs the installer and the mount logic against a fake One UI filesystem.
#
# Usage: tests/run.sh [shell]     e.g. tests/run.sh "busybox ash"
# The shell is what interprets the module scripts (default: sh). On real
# devices that is busybox ash, so CI runs it with busybox and dash.
# Run as root on Linux (CI uses sudo) to also test real bind mounts, inside
# a private mount namespace so nothing leaks into the host.
set -euo pipefail

SHELL_UNDER_TEST=${1:-sh}
REPO=$(cd "$(dirname "$0")/.." && pwd)
PASS=0; FAIL=0; SKIP=0

check() {  # check <description> <command...>
  local desc=$1; shift
  if "$@"; then PASS=$((PASS + 1)); echo "  ok   $desc"
  else FAIL=$((FAIL + 1)); echo "  FAIL $desc"; fi
}

# Creates a fake root + module dir and runs the installer.
# $1: name of a setup function that populates $ROOT and $MODPATH
run_install() {
  ROOT=$(mktemp -d); MODPATH=$(mktemp -d)
  cp -r "$REPO"/{common,customize.sh,action.sh,service.sh,uninstall.sh,config.conf,ui-map.conf} "$MODPATH/"
  mkdir -p "$MODPATH/sounds" "$ROOT/dev"
  "$1"
  set +e
  # shellcheck disable=SC2016  # expanded by the shell under test
  OUTPUT=$(PATH="$ROOT/bin:$PATH" SYSROOT=$ROOT MODPATH=$MODPATH ZIPFILE=${ZIPFILE:-} API=36 $SHELL_UNDER_TEST -c '
    ui_print() { echo "$1"; }
    abort() { echo "$1"; exit 1; }
    set_perm_recursive() { :; }
    getprop() { echo 90000; }
    . "$MODPATH/customize.sh"' 2>&1)
  STATUS=$?
  set -e
}

# Runs mount_sounds from the installed module. $1: extra env (e.g. DRY_RUN=1)
run_mount() {
  # SC2086: "busybox ash" must split into two words
  # shellcheck disable=SC2016,SC2086
  env SYSROOT="$ROOT" MODPATH="$MODPATH" "$@" $SHELL_UNDER_TEST -c '
    . "$MODPATH/common/functions.sh"
    . "$MODPATH/common/mount.sh"
    ${CGS_FN:-mount_sounds} "$MODPATH"' 2>&1
}

cleanup() { rm -rf "$ROOT" "$MODPATH"; }

# Mirrors One UI 9: UI sounds as plain files, ringtones and notifications
# holding only a SoundTheme/ folder, and no alarms folder at all.
stock_device() {
  local a="$ROOT/system/media/audio"
  mkdir -p "$a/ui" "$a/ringtones/SoundTheme/Galaxy" "$a/notifications/SoundTheme/Galaxy"
  for f in Lock.ogg Unlock.ogg Effect_Tick.ogg Touch_OneUI.ogg LowBattery.ogg; do
    echo stock > "$a/ui/$f"
  done
  echo stock > "$a/ringtones/SoundTheme/Galaxy/Over_the_Horizon.ogg"
  echo stock > "$a/notifications/SoundTheme/Galaxy/Spaceline.ogg"
}

s3_on_sdcard() {
  local s="$ROOT/sdcard/ClassicGalaxySounds"
  mkdir -p "$s"/{ui,ringtones,notifications,alarms}
  echo s3 > "$s/ui/lock.ogg"            # case differs from device
  echo s3 > "$s/ui/Unlock.ogg"
  echo s3 > "$s/ui/Touch.ogg"           # needs the map
  echo s3 > "$s/ui/Only_On_S3.ogg"      # no counterpart, must be ignored
  echo s3 > "$s/ringtones/Over the horizon.ogg"   # space in name
  echo s3 > "$s/notifications/Whistle.ogg"
  echo s3 > "$s/alarms/Morning.ogg"
  echo "not audio" > "$s/ringtones/notes.txt"
  printf 'Touch_OneUI.ogg = Touch.ogg\nMissing.ogg=Nope.ogg\n' > "$s/ui-map.conf"
  printf 'DEFAULT_NOTIFICATION=Whistle.ogg\nDEFAULT_ALARM=../../etc/x\nDEFAULT_RINGTONE=Missing.ogg\n' > "$s/config.conf"
  echo "GT-I9300 / Android 4.3 / I9300XXUGNG3" > "$s/firmware-info.txt"
}

setup_full() { stock_device; s3_on_sdcard; }
setup_no_sounds() { stock_device; }
setup_product() {
  mkdir -p "$ROOT/product/media/audio/ui"
  echo stock > "$ROOT/product/media/audio/ui/Lock.ogg"
  mkdir -p "$MODPATH/sounds/ui"
  echo bundled > "$MODPATH/sounds/ui/Lock.ogg"
}

echo "Shell: $SHELL_UNDER_TEST"

echo "[full install from /sdcard]"
run_install setup_full
M="$MODPATH/media"
check "exits successfully"              [ "$STATUS" -eq 0 ]
check "nothing for the manager to mount" [ ! -e "$MODPATH/system" ]
check "same-name match (case-insens.)"  grep -q s3 "$M/ui/Lock.ogg"
check "same-name match (exact)"         grep -q s3 "$M/ui/Unlock.ogg"
check "explicit map applied"            grep -q s3 "$M/ui/Touch_OneUI.ogg"
check "unmatched stock files untouched" [ ! -e "$M/ui/Effect_Tick.ogg" ]
check "S3-only UI file not added"       [ ! -e "$M/ui/Only_On_S3.ogg" ]
check "ringtone with space copied"      [ -f "$M/ringtones/Over the horizon.ogg" ]
check "non-audio files ignored"         [ ! -e "$M/ringtones/notes.txt" ]
check "notifications + alarms copied"   [ -f "$M/notifications/Whistle.ogg" ] && [ -f "$M/alarms/Morning.ogg" ]
check "default notification prop"       grep -qx 'ro.config.notification_sound=Whistle.ogg' "$MODPATH/system.prop"
check "path traversal rejected"         bash -c "! grep -q alarm_alert '$MODPATH/system.prop'"
check "missing default skipped"         bash -c "! grep -q 'ro.config.ringtone=' '$MODPATH/system.prop'"
check "bad map line reported"           grep -q 'Nope.ogg not found' "$MODPATH/install.log"
check "staging sounds/ removed"         [ ! -e "$MODPATH/sounds" ]
check "offers to delete the folder"     grep -q 'ClassicGalaxySounds/ (folder)' "$MODPATH/install.log"
check "folder kept without an answer"  [ -d "$ROOT/sdcard/ClassicGalaxySounds" ]
check "firmware origin logged"          grep -q 'from GT-I9300' "$MODPATH/install.log"
check "never suggests a full reboot"    bash -c "! grep -q 'Reboot, or' '$MODPATH/install.log'"
check "warns about temp root"           grep -q 'temporary root' "$MODPATH/install.log"

echo "[mount plan on a One UI 9 layout (dry run)]"
A="$ROOT/system/media/audio"; S="$ROOT/dev/classic_galaxy_sounds"
PLAN=$(run_mount DRY_RUN=1)
has() { grep -qxF -- "$1" <<<"$PLAN"; }
check "UI files bound one by one"       has "bind $M/ui/Lock.ogg -> $A/ui/Lock.ogg"
check "mapped UI file bound"            has "bind $M/ui/Touch_OneUI.ogg -> $A/ui/Touch_OneUI.ogg"
check "untouched UI files not bound"    bash -c "! grep -q 'Effect_Tick' <<<\"\$1\"" _ "$PLAN"
check "new ringtone staged"             has "bind $M/ringtones/Over the horizon.ogg -> $S/ringtones/Over the horizon.ogg"
check "stock SoundTheme kept"           has "bind $A/ringtones/SoundTheme -> $S/ringtones/SoundTheme"
check "alarms nested in ringtones"      has "bind $M/alarms -> $S/ringtones/alarms"
check "ringtones folder replaced"       has "rbind $S/ringtones -> $A/ringtones"
check "notifications folder replaced"   has "rbind $S/notifications -> $A/notifications"
check "no alarms folder invented"       bash -c "! grep -q 'audio/alarms' <<<\"\$1\"" _ "$PLAN"
check "only bind mounts used"           bash -c "! grep -qvE '^(bind|rbind) |^- mounted' <<<\"\$1\"" _ "$PLAN"
check "alarm recorded at nested path"   grep -qF "$A/ringtones/alarms/Morning.ogg" "$MODPATH/mounts.list"
check "all sounds counted"              grep -q 'mounted 6 sounds' <<<"$PLAN"
IDX=$(run_mount DRY_RUN=1 CGS_FN=index_sounds)
check "added sounds get indexed"        grep -qxF "index /system/media/audio/ringtones/alarms/Morning.ogg" <<<"$IDX"
check "UI sounds not indexed"           bash -c "! grep -q '/ui/' <<<\"\$1\"" _ "$IDX"
check "index count"                     grep -q 'indexed 3 sounds' <<<"$IDX"

echo "[action button (dry run)]"
# shellcheck disable=SC2086  # "busybox ash" must split
action() { SYSROOT=$ROOT DRY_RUN=1 CGS_GLOBAL_NS=1 $SHELL_UNDER_TEST "$MODPATH/action.sh" 2>&1; }
cp "$M/ui/Lock.ogg" "$A/ui/Lock.ogg"        # pretend only this one is mounted
OUT=$(action)
check "mounts right away"               grep -q 'mounted 6 sounds' <<<"$OUT"
check "counts live sounds"              grep -q '1 of 6 sounds are live' <<<"$OUT"
check "names one that isn't"            grep -q 'not live: /system/media/audio/' <<<"$OUT"
while IFS=$'\t' read -r src dst; do mkdir -p "${dst%/*}"; cp "$src" "$dst"; done < "$MODPATH/mounts.list"
OUT=$(action)
check "all live"                        grep -q '6 of 6 sounds are live' <<<"$OUT"

echo "[uninstall (dry run)]"
# shellcheck disable=SC2086  # "busybox ash" must split
OUT=$(SYSROOT=$ROOT DRY_RUN=1 CGS_GLOBAL_NS=1 $SHELL_UNDER_TEST "$MODPATH/uninstall.sh" 2>&1)
check "forgets added ringtones"         grep -q '^index /system/media/audio/ringtones/Over the horizon.ogg' <<<"$OUT"
check "forgets alarms"                  grep -q '^index /system/media/audio/ringtones/alarms/' <<<"$OUT"
# shellcheck disable=SC2016  # $1 expands inside bash -c
check "UI sounds need no forgetting"    bash -c '! grep -q "/ui/" <<<"$1"' _ "$OUT"
check "leaves nothing in /dev"          [ ! -e "$ROOT/dev/classic_galaxy_sounds.forget" ]
cleanup

echo "[mounts left by an updated module]"
ROOT=$(mktemp -d); MODPATH=$(mktemp -d)
A="$ROOT/system/media/audio"; S="$ROOT/dev/classic_galaxy_sounds"; M=/data/adb/modules/classic_galaxy_sounds
{
  echo "1 0 253:1 / / ro - ext4 /dev/root ro"
  echo "2 1 253:1 $M/media/ui/Lock.ogg//deleted $A/ui/Lock.ogg ro - ext4 /dev/sda ro"
  echo "3 1 253:1 $M/media/ui/Unlock.ogg $A/ui/Unlock.ogg ro - ext4 /dev/sda ro"
  echo "4 1 0:5 /classic_galaxy_sounds/ringtones//deleted $A/ringtones ro - tmpfs tmpfs rw"
  echo "5 1 0:5 / $S/ringtones rw - tmpfs tmpfs rw"
  echo "6 1 253:1 /other/module/Lock.ogg $A/ui/Other.ogg ro - ext4 /dev/sda ro"
} > "$ROOT/mountinfo"
mkdir -p "$A/ui"
# shellcheck disable=SC2016,SC2086  # expanded by the shell under test; shell splits on purpose
LEFT=$(env SYSROOT="$ROOT" CGS_MOUNTINFO="$ROOT/mountinfo" REPO="$REPO" $SHELL_UNDER_TEST -c '
  . "$REPO/common/functions.sh"; . "$REPO/common/mount.sh"
  _base="$SYSROOT/system/media/audio" _stage="$SYSROOT/dev/classic_galaxy_sounds"; _our_mounts')
check "finds mounts of deleted files"   grep -qx "$A/ui/Lock.ogg" <<<"$LEFT"
check "deleted category folder too"     grep -qx "$A/ringtones" <<<"$LEFT"
check "still finds live ones"           grep -qx "$A/ui/Unlock.ogg" <<<"$LEFT"
check "staging folder mounts"           grep -qx "$S/ringtones" <<<"$LEFT"
# shellcheck disable=SC2016  # $1 expands inside bash -c
check "leaves other modules alone"      bash -c '! grep -q Other <<<"$1"' _ "$LEFT"
cleanup

echo "[real bind mounts]"
if [ "$(id -u)" = 0 ] && command -v unshare >/dev/null && [ "$(uname -s)" = Linux ]; then
  run_install setup_full
  A="$ROOT/system/media/audio"
  # shellcheck disable=SC2016,SC2086  # expanded inside the namespace; shell splits on purpose
  OUT=$(unshare -m --propagation private env SYSROOT="$ROOT" MODPATH="$MODPATH" A="$A" \
        $SHELL_UNDER_TEST -c '
    . "$MODPATH/common/functions.sh"; . "$MODPATH/common/mount.sh"
    mount_sounds "$MODPATH" || echo MOUNT_FAILED
    echo "ui=$(cat "$A/ui/Lock.ogg") effect=$(cat "$A/ui/Effect_Tick.ogg")"
    echo "ring=$(cat "$A/ringtones/Over the horizon.ogg")"
    echo "theme=$(cat "$A/ringtones/SoundTheme/Galaxy/Over_the_Horizon.ogg")"
    echo "alarm=$(cat "$A/ringtones/alarms/Morning.ogg")"
    echo "notif=$(cat "$A/notifications/Whistle.ogg") ntheme=$(cat "$A/notifications/SoundTheme/Galaxy/Spaceline.ogg")"
    ours() { _base="$SYSROOT/system/media/audio" _stage="$SYSROOT/dev/classic_galaxy_sounds"; _our_mounts | wc -l; }
    echo "first=$(ours)"
    mount_sounds "$MODPATH" >/dev/null
    echo "second=$(ours) ui=$(cat "$A/ui/Lock.ogg")"
    # Module update: the root manager replaces the folder, the old files
    # behind the current mounts are deleted
    cp -r "$MODPATH/media" "$MODPATH/media.new"; echo s3new > "$MODPATH/media.new/ui/Lock.ogg"
    rm -rf "$MODPATH/media"; mv "$MODPATH/media.new" "$MODPATH/media"
    mount_sounds "$MODPATH" > "$SYSROOT/update.log" 2>&1 || echo UPDATE_FAILED
    echo "update=$(ours) ui=$(cat "$A/ui/Lock.ogg") errors=$(grep -c failed "$SYSROOT/update.log")"
    CGS_GLOBAL_NS=1 sh "$MODPATH/action.sh" | grep "sounds are live"
    unmount_sounds
    echo "after-unmount=$(ours) ui=$(cat "$A/ui/Lock.ogg") stage=$(ls -d "$SYSROOT/dev/classic_galaxy_sounds" 2>/dev/null | wc -l)"
    echo "module-alarms=$(ls "$MODPATH/media/alarms" | wc -l)"' 2>&1)
  check "mounts without errors"         bash -c "! grep -qE 'MOUNT_FAILED|failed' <<<\"\$1\"" _ "$OUT"
  check "UI sound replaced"             grep -q 'ui=s3 effect=stock' <<<"$OUT"
  check "new ringtone visible"          grep -q 'ring=s3' <<<"$OUT"
  check "stock theme still visible"     grep -q 'theme=stock' <<<"$OUT"
  check "alarm visible under ringtones" grep -q 'alarm=s3' <<<"$OUT"
  check "notifications merged"          grep -q 'notif=s3 ntheme=stock' <<<"$OUT"
  F=$(sed -n 's/^first=//p' <<<"$OUT")
  check "finds its own mounts"          [ "${F:-0}" -gt 3 ]
  check "second call refreshes, no stacking" grep -q "^second=$F ui=s3" <<<"$OUT"
  check "remount after a module update"  grep -q "^update=$F ui=s3new errors=0" <<<"$OUT"
  check "unmount removes everything"    grep -q '^after-unmount=0 ui=stock stage=0' <<<"$OUT"
  check "module files survive cleanup"  grep -q '^module-alarms=1' <<<"$OUT"
  check "action sees everything live"   grep -q '6 of 6 sounds are live' <<<"$OUT"
  check "host untouched afterwards"     grep -qx stock "$A/ui/Lock.ogg"
  cleanup
else
  SKIP=$((SKIP + 1)); echo "  skip (needs root on Linux: sudo tests/run.sh)"
fi

# A stand-in for bin/cgs-extract-arm64: records its arguments and writes a
# sounds folder like the real one, or fails when $1 says so.
fake_extractor() {
  mkdir -p "$MODPATH/bin" "$MODPATH/maps"
  cat > "$MODPATH/bin/cgs-extract-arm64" <<'EOF'
#!/bin/sh
if [ "$1" = --check ]; then
  case "$2" in
    *photos*) echo "error: not a firmware package" >&2; exit 1 ;;
    *S928B*) printf '\nerror: %s is the firmware of a phone with Android 10 or newer\n' "${2##*/}" >&2; exit 3 ;;
  esac
  echo "firmware zip"; exit 0
fi
out=$3
echo "$*" > "${out%/*}/extractor-args"
case "$1" in *broken*) echo "error: MD5 mismatch"; exit 1 ;; esac
mkdir -p "$out/ui" "$out/ringtones"
echo s3 > "$out/ui/Lock.ogg"
echo s3 > "$out/ringtones/Over the horizon.ogg"
echo "GT-I9300 / Android 4.3 / I9300XXUGPE1" > "$out/firmware-info.txt"
echo "Firmware: GT-I9300, Android 4.3"
echo "  ui             1 sounds"
EOF
}
big_file() { mkdir -p "${1%/*}"; truncate -s 200M "$1"; }

setup_firmware_in_download() {
  stock_device; fake_extractor
  big_file "$ROOT/sdcard/Download/SAMFW.COM_GT-I9300_I9300XXUGPE1_fac.zip"
  echo "small" > "$ROOT/sdcard/Download/ClassicGalaxySounds-v1.0.0-root-module.zip"
  big_file "$ROOT/sdcard/Download/holiday photos.zip"
}
setup_broken_firmware() {
  stock_device; fake_extractor
  big_file "$ROOT/sdcard/Download/broken_firmware.zip"
}
setup_folder_and_firmware() { setup_firmware_in_download; s3_on_sdcard; }
setup_new_phone_firmware() {
  stock_device; fake_extractor
  big_file "$ROOT/sdcard/Download/SM-S928B_EUX_S928BXXU6EZIF.zip"
}
setup_two_firmwares() {
  setup_firmware_in_download
  big_file "$ROOT/sdcard/Download/GT-I9300_older.zip"
  touch -d '2020-01-01' "$ROOT/sdcard/Download/GT-I9300_older.zip"
}
# Pressing Vol+ when asked. The manager installs from a copy of the zip
# the user downloaded (a different one with the same name must survive)
setup_firmware_delete() {
  setup_firmware_in_download
  ZIPFILE="$ROOT/cache/module.zip"
  mkdir -p "$ROOT/cache"
  echo "the module zip" > "$ZIPFILE"
  cp "$ZIPFILE" "$ROOT/sdcard/Download/ClassicGalaxySounds-v1.0.1-root-module.zip"
  mkdir -p "$ROOT/bin"
  printf '#!/bin/sh\necho "/dev/input/event0: EV_KEY KEY_VOLUMEUP DOWN"\n' > "$ROOT/bin/getevent"
  chmod 755 "$ROOT/bin/getevent"
}

echo "[firmware in Download, extracted on the phone]"
run_install setup_firmware_in_download
check "exits successfully"              [ "$STATUS" -eq 0 ]
check "extractor ran on the firmware"   grep -q 'SAMFW.COM_GT-I9300_I9300XXUGPE1_fac.zip -o' "$MODPATH/extractor-args"
check "extractor gets the maps"         grep -q -- "--maps $MODPATH/maps" "$MODPATH/extractor-args"
check "small zips ignored"              bash -c "! grep -q 'ClassicGalaxySounds-v1' '$MODPATH/install.log'"
check "other big zips skipped quietly"  bash -c "! grep -q 'holiday photos' '$MODPATH/install.log'"
check "extractor output shown"          grep -q 'Firmware: GT-I9300' "$MODPATH/install.log"
check "sounds installed"                grep -q s3 "$MODPATH/media/ui/Lock.ogg"
check "firmware origin logged"          grep -q 'from GT-I9300' "$MODPATH/install.log"
check "offers to delete the firmware"   grep -q 'Download/SAMFW.*(210 MB)' "$MODPATH/install.log"
check "keeps it without an answer"      [ -f "$ROOT/sdcard/Download/SAMFW.COM_GT-I9300_I9300XXUGPE1_fac.zip" ]
check "no internal paths in the log"    bash -c "! grep -q 'saved to' '$MODPATH/install.log'"
check "extractor and maps removed"      [ ! -e "$MODPATH/bin" ] && [ ! -e "$MODPATH/maps" ]
check "temp folders removed"            [ ! -e "$MODPATH/.extract-tmp" ] && [ ! -e "$MODPATH/sounds" ]
cleanup

echo "[firmware deleted with Vol+]"
run_install setup_firmware_delete
check "exits successfully"              [ "$STATUS" -eq 0 ]
check "firmware deleted"                [ ! -e "$ROOT/sdcard/Download/SAMFW.COM_GT-I9300_I9300XXUGPE1_fac.zip" ]
check "downloaded module zip deleted"   [ ! -e "$ROOT/sdcard/Download/ClassicGalaxySounds-v1.0.1-root-module.zip" ]
check "different zip, same name kept"   [ -f "$ROOT/sdcard/Download/ClassicGalaxySounds-v1.0.0-root-module.zip" ]
check "other files untouched"           [ -f "$ROOT/sdcard/Download/holiday photos.zip" ]
ZIPFILE=""
check "says so"                         grep -q 'Deleted' "$MODPATH/install.log"
cleanup

echo "[the new phone's own firmware]"
run_install setup_new_phone_firmware
check "aborts"                          [ "$STATUS" -ne 0 ]
check "explains why it can't be used"   grep -q 'S928B.*Android 10 or newer' <<<"$OUTPUT"
check "extractor not run on it"         [ ! -e "$MODPATH/extractor-args" ]
cleanup

echo "[two firmwares: the newest wins]"
run_install setup_two_firmwares
check "exits successfully"              [ "$STATUS" -eq 0 ]
check "says there are two"              grep -q 'Found 2 firmware files' "$MODPATH/install.log"
check "uses the newest"                 grep -q 'SAMFW' "$MODPATH/extractor-args"
cleanup

echo "[ready-made folder wins over firmware]"
run_install setup_folder_and_firmware
check "exits successfully"              [ "$STATUS" -eq 0 ]
check "extractor not run"               [ ! -e "$MODPATH/extractor-args" ]
cleanup

echo "[firmware that fails to extract]"
run_install setup_broken_firmware
check "aborts"                          [ "$STATUS" -ne 0 ]
check "shows the extractor error"       grep -q 'MD5 mismatch' <<<"$OUTPUT"
check "explains what to download"       grep -q 'official firmware' <<<"$OUTPUT"
cleanup

echo "[no sounds anywhere]"
run_install setup_no_sounds
check "aborts"                          [ "$STATUS" -ne 0 ]
check "explains where to put them"      grep -q ClassicGalaxySounds <<<"$OUTPUT"
cleanup

echo "[sounds in /product, bundled in zip]"
run_install setup_product
check "exits successfully"              [ "$STATUS" -eq 0 ]
check "file stored in the module"       grep -q bundled "$MODPATH/media/ui/Lock.ogg"
PLAN=$(run_mount DRY_RUN=1)
check "mount targets /product"          grep -qF "bind $MODPATH/media/ui/Lock.ogg -> $ROOT/product/media/audio/ui/Lock.ogg" <<<"$PLAN"
check "no system.prop when no defaults" [ ! -e "$MODPATH/system.prop" ]
cleanup

echo
echo "$PASS passed, $FAIL failed, $SKIP skipped"
[ "$FAIL" -eq 0 ]
