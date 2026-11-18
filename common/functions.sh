# shellcheck shell=sh
# Core install logic. Sourced by customize.sh and by the test suite.
# Must stay POSIX sh / busybox ash compatible (no bash-isms).
#
# Environment:
#   MODPATH  module staging directory (set by Magisk / KernelSU / APatch)
#   SYSROOT  path prefix for device files; empty on a real device,
#            a fake root filesystem when running the tests

SYSROOT=${SYSROOT:-}
LOG_FILE=${LOG_FILE:-}
SOURCE_FOLDER_NAME=ClassicGalaxySounds
# shellcheck disable=SC2034  # used by customize.sh
CATEGORIES="ringtones notifications alarms"

# Filled in by the functions below
MEDIA_DIR=""        # device path of the stock sounds, e.g. /system/media/audio
# Where the sounds live inside the module. Deliberately not system/: the
# module mounts them itself (common/mount.sh), so no root manager or
# KernelSU metamodule has to.
MOD_MEDIA_DIR="media"
SRC_DIR=""          # where the old Galaxy sounds were found
DEFAULT_RINGTONE=""
DEFAULT_RINGTONE_2=""
DEFAULT_NOTIFICATION=""
DEFAULT_ALARM=""
COUNT_UI=0
COUNT_OTHER=0

log_line() {
  if command -v ui_print >/dev/null 2>&1; then ui_print "$1"; else echo "$1"; fi
  if [ -n "$LOG_FILE" ]; then echo "$1" >> "$LOG_FILE"; fi
}

to_lower() { echo "$1" | tr '[:upper:]' '[:lower:]'; }

trim() { echo "$1" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//'; }

is_audio() {
  case "$(to_lower "$1")" in
    *.ogg|*.mp3|*.wav|*.flac|*.m4a|*.aac) return 0 ;;
  esac
  return 1
}

has_audio() {
  [ -d "$1" ] || return 1
  for _f in "$1"/* "$1"/*/*; do
    [ -f "$_f" ] && is_audio "$_f" && return 0
  done
  return 1
}

# Locate the partition holding the stock sounds. One UI keeps them in
# /system, but newer builds may move them to /product.
find_media_dir() {
  for _p in /system/media/audio /product/media/audio /system/product/media/audio; do
    if [ -d "$SYSROOT$_p/ui" ]; then
      MEDIA_DIR=$_p
      return 0
    fi
  done
  return 1
}

# Sounds bundled in the zip (personal builds) win; otherwise use internal storage.
find_source_dir() {
  for _d in "$MODPATH/sounds" \
            "$SYSROOT/sdcard/$SOURCE_FOLDER_NAME" \
            "$SYSROOT/storage/emulated/0/$SOURCE_FOLDER_NAME" \
            "$SYSROOT/data/media/0/$SOURCE_FOLDER_NAME"; do
    if has_audio "$_d"; then
      SRC_DIR=$_d
      return 0
    fi
  done
  return 1
}

file_size() { stat -L -c %s "$1" 2>/dev/null || wc -c < "$1"; }

# Files that could be an official firmware package, newest first: the name
# doesn't matter, only the type and size. Firmware is hundreds of MB, which
# skips this module's own zip and other small archives.
find_firmware() {
  for _d in "$SYSROOT/sdcard/$SOURCE_FOLDER_NAME" "$SYSROOT/sdcard/Download"; do
    for _f in "$_d"/*; do
      [ -f "$_f" ] || continue
      case "$(to_lower "${_f##*/}")" in
        *.zip|*.tar|*.tar.md5|*.img|*.img.lz4|*.img.ext4) ;;
        *) continue ;;
      esac
      [ "$(file_size "$_f")" -gt 100000000 ] 2>/dev/null || continue
      stat -L -c '%Y %n' "$_f" 2>/dev/null
    done
  done | sort -rn | cut -d ' ' -f 2-
}

# The extractor bundled for this CPU (bin/cgs-extract-<arch>, from extractor/)
_extractor() {
  _bin="$MODPATH/bin/cgs-extract-${ARCH:-arm64}"
  [ -f "$_bin" ] && chmod 755 "$_bin"
}

# Writes to $2 the files of list $1 that really are usable firmware. The
# extractor only reads their headers. Other big files (photo or music zips)
# are skipped without a word; firmware it can't use, such as the new
# phone's own, is explained.
usable_firmware() {
  : > "$2"
  if ! _extractor; then
    [ -s "$1" ] && log_line "  ! No firmware extractor for this CPU (${ARCH:-unknown})"
    return 0
  fi
  while IFS= read -r _f; do
    _why=$("$_bin" --check "$_f" 2>&1 < /dev/null)
    case $? in
      0) echo "$_f" >> "$2" ;;
      3) log_line "! Skipping ${_f##*/}:"
         log_line "  $(echo "$_why" | sed -n 's/^error: //p')" ;;
    esac
  done < "$1"
}

# Extract the sounds of the firmware $1 into $MODPATH/sounds.
extract_firmware() {
  _extractor || return 1
  # Temp files live in the module folder; service.sh removes them if an
  # install was cut short (e.g. a reboot), so they can't pile up
  _tmp="$MODPATH/.extract-tmp"
  rm -rf "$_tmp" "${MODPATH:?}/sounds"
  mkdir -p "$_tmp"
  log_line "- Extracting the sounds from ${1##*/}"
  log_line "  (about a minute)"
  # Print the extractor's progress live, keep its exit status
  { "$_bin" "$1" -o "$MODPATH/sounds" --maps "$MODPATH/maps" --tmp "$_tmp" --installer < /dev/null 2>&1
    echo "$?" > "$_tmp/status"; } | while IFS= read -r _l; do
    [ -n "$_l" ] && log_line "  $_l"
  done
  _status=$(cat "$_tmp/status" 2>/dev/null)
  rm -rf "$_tmp"
  [ "$_status" = 0 ]
}

# A Vol+ / Vol- question. True for Vol+; false for Vol-, for no answer
# within $1 seconds, or when the keys can't be read.
volume_key() {
  command -v getevent >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1 || return 1
  _end=$(( $(date +%s) + $1 ))
  while [ "$(date +%s)" -lt "$_end" ]; do
    case "$(timeout 1 getevent -lqc 1 2>/dev/null)" in
      *KEY_VOLUMEUP*DOWN*) return 0 ;;
      *KEY_VOLUMEDOWN*DOWN*) return 1 ;;
    esac
  done
  return 1
}

# The copy of this module's zip the user downloaded. Root managers install
# from a copy in their cache ($ZIPFILE), so look in Download for a zip that
# is byte for byte the same: nothing else can match.
find_installer_zip() {
  [ -f "$ZIPFILE" ] || return 0
  _zsize=$(file_size "$ZIPFILE")
  for _z in "$SYSROOT/sdcard/Download"/*.zip "$SYSROOT/sdcard/$SOURCE_FOLDER_NAME"/*.zip; do
    [ -f "$_z" ] && [ "$(file_size "$_z")" = "$_zsize" ] && cmp -s "$_z" "$ZIPFILE" && echo "$_z"
  done
}

# Offer to delete what is no longer needed now that the sounds are inside
# the module: the firmware or sounds folder they came from ($1, $2...) and
# the downloaded module zip. Nothing is deleted without a key press.
offer_cleanup() {
  _zip=$(find_installer_zip)
  log_line "- The sounds are now inside the module."
  log_line "  Delete what is no longer needed?"
  for _p in "$@" "$_zip"; do
    [ -e "$_p" ] || continue
    if [ -f "$_p" ]; then
      _mb=$(( ($(file_size "$_p") + 999999) / 1000000 ))
      log_line "    ${_p#"$SYSROOT"} ($_mb MB)"
    else
      log_line "    ${_p#"$SYSROOT"}/ (folder)"
    fi
  done
  log_line "  Vol+ = delete   Vol- = keep   (keeps them after 15 s)"
  if ! volume_key 15; then
    log_line "  Kept. You can delete them whenever you want."
    return 0
  fi
  for _p in "$@" "$_zip"; do
    [ -e "$_p" ] || continue
    rm -rf "$_p"
    # Drop it from the media index too, so it doesn't linger in file pickers
    case "${_p#"$SYSROOT"}" in
      /sdcard/*) _rel=${_p#"$SYSROOT"/sdcard/}
                 content call --uri content://media --method scan_file \
                   --arg "/storage/emulated/0/$_rel" > /dev/null 2>&1 ;;
    esac
  done
  log_line "  Deleted."
}

# First existing copy of a config file: user folder, then module defaults.
pick_conf() {
  if [ -f "$SRC_DIR/$1" ]; then echo "$SRC_DIR/$1"; else echo "$MODPATH/$1"; fi
}

# config.conf is parsed, never sourced: it may come from shared storage,
# and sourcing it would run arbitrary code as root.
load_config() {
  _conf=$(pick_conf config.conf)
  [ -f "$_conf" ] || return 0
  while IFS= read -r _line || [ -n "$_line" ]; do
    _line=$(trim "${_line%%#*}")
    case "$_line" in *=*) ;; *) continue ;; esac
    _key=$(trim "${_line%%=*}")
    _val=$(trim "${_line#*=}")
    case "$_val" in */*|*..*) log_line "  ! Ignoring $_key: file names only, no paths"; continue ;; esac
    case "$_key" in
      DEFAULT_RINGTONE)     DEFAULT_RINGTONE=$_val ;;
      DEFAULT_RINGTONE_2)   DEFAULT_RINGTONE_2=$_val ;;
      DEFAULT_NOTIFICATION) DEFAULT_NOTIFICATION=$_val ;;
      DEFAULT_ALARM)        DEFAULT_ALARM=$_val ;;
      *) log_line "  ! Unknown key in config.conf: $_key" ;;
    esac
  done < "$_conf"
}

# Ringtones, notifications and alarms are added next to the stock ones.
install_category() {
  _src="$SRC_DIR/$1"
  _dst="$MODPATH/$MOD_MEDIA_DIR/$1"
  [ -d "$_src" ] || { log_line "- $1: no folder in source, skipped"; return 0; }
  _n=0
  for _f in "$_src"/*; do
    if [ ! -f "$_f" ] || ! is_audio "$_f"; then continue; fi
    mkdir -p "$_dst"
    cp -f "$_f" "$_dst/" && _n=$((_n + 1))
  done
  COUNT_OTHER=$((COUNT_OTHER + _n))
  log_line "- $1: $_n sounds added"
}

# UI sounds are referenced by fixed file names, so they must replace the
# stock files in place. Two passes:
#   1. explicit "OneUI_name=OldGalaxy_name" lines from ui-map.conf
#   2. automatic, case-insensitive same-name matches for everything else
install_ui() {
  _src="$SRC_DIR/ui"
  _dev="$SYSROOT$MEDIA_DIR/ui"
  _dst="$MODPATH/$MOD_MEDIA_DIR/ui"
  [ -d "$_src" ] || { log_line "- ui: no folder in source, skipped"; return 0; }
  mkdir -p "$_dst"

  _map=$(pick_conf ui-map.conf)
  if [ -f "$_map" ]; then
    while IFS= read -r _line || [ -n "$_line" ]; do
      _line=$(trim "${_line%%#*}")
      case "$_line" in *=*) ;; *) continue ;; esac
      _target=$(trim "${_line%%=*}")
      _source=$(trim "${_line#*=}")
      case "$_target$_source" in */*) log_line "  ! Bad map line (no paths allowed): $_line"; continue ;; esac
      if [ ! -f "$_src/$_source" ]; then
        log_line "  ! map: $_source not found in source, skipped"
      elif [ ! -f "$_dev/$_target" ]; then
        log_line "  ! map: $_target does not exist on this device, skipped"
      else
        cp -f "$_src/$_source" "$_dst/$_target"
        log_line "  ~ $_target <- $_source"
        COUNT_UI=$((COUNT_UI + 1))
      fi
    done < "$_map"
  fi

  _list="$MODPATH/.srclist"
  ls -1 "$_src" > "$_list"
  for _f in "$_dev"/*; do
    [ -f "$_f" ] || continue
    _name=${_f##*/}
    [ -f "$_dst/$_name" ] && continue            # already handled by the map
    _match=$(awk -v k="$(to_lower "$_name")" 'tolower($0) == k { print; exit }' "$_list")
    if [ -n "$_match" ] && [ -f "$_src/$_match" ]; then
      cp -f "$_src/$_match" "$_dst/$_name"
      log_line "  = $_name"
      COUNT_UI=$((COUNT_UI + 1))
    fi
  done
  rm -f "$_list"
  log_line "- ui: $COUNT_UI stock sounds replaced"
}

# ro.config.* only decides the sound used when none has been picked yet
# (first boot / after a reset); it never overrides the user's choice.
write_props() {
  _props="$MODPATH/system.prop"
  : > "$_props"
  _set_default ro.config.ringtone      ringtones     "$DEFAULT_RINGTONE"
  _set_default ro.config.ringtone_2    ringtones     "$DEFAULT_RINGTONE_2"
  _set_default ro.config.notification_sound notifications "$DEFAULT_NOTIFICATION"
  _set_default ro.config.alarm_alert   alarms        "$DEFAULT_ALARM"
  [ -s "$_props" ] || rm -f "$_props"
}

_set_default() {
  [ -n "$3" ] || return 0
  if [ -f "$MODPATH/$MOD_MEDIA_DIR/$2/$3" ] || [ -f "$SYSROOT$MEDIA_DIR/$2/$3" ]; then
    echo "$1=$3" >> "$MODPATH/system.prop"
    log_line "- default $2: $3"
  else
    log_line "  ! default $2: $3 not found, skipped"
  fi
}
