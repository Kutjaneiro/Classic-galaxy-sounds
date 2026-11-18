# shellcheck shell=sh
# Install script run by Magisk / KernelSU / APatch after extracting the zip.
# shellcheck disable=SC2034  # SKIPUNZIP is read by the installer
SKIPUNZIP=0

# shellcheck source=common/functions.sh
. "$MODPATH/common/functions.sh"
# shellcheck source=common/mount.sh
. "$MODPATH/common/mount.sh"

LOG_FILE="$MODPATH/install.log"
: > "$LOG_FILE"

log_line "*************************************"
log_line "  Classic Galaxy Sounds"
log_line "*************************************"

oneui=$(getprop ro.build.version.oneui 2>/dev/null)
if [ -z "$oneui" ]; then
  log_line "  ! This does not look like One UI. Continuing anyway."
else
  log_line "- One UI version code: $oneui (API ${API:-?})"
fi

find_media_dir || abort "! Stock sound folder not found (looked in /system and /product)"
log_line "- Stock sounds: $MEDIA_DIR"

# No ready-made sounds folder: look for an official firmware to read them from
FIRMWARE=""
if ! find_source_dir; then
  find_firmware > "$MODPATH/.candidates"
  usable_firmware "$MODPATH/.candidates" "$MODPATH/.firmware"
  count=$(wc -l < "$MODPATH/.firmware")
  [ "$count" -gt 1 ] && log_line "- Found $count firmware files, using the newest"
  # Redirect, not a pipe: the loop must run in this shell to set FIRMWARE
  while IFS= read -r fw; do
    if extract_firmware "$fw" && find_source_dir; then
      FIRMWARE=$fw
      break
    fi
  done < "$MODPATH/.firmware"
  rm -f "$MODPATH/.candidates" "$MODPATH/.firmware"
fi
if [ -z "$SRC_DIR" ]; then
  log_line "! No sounds found."
  log_line "  Download the official firmware of the old Galaxy"
  log_line "  (e.g. GT-I9300 for the S3) to the Download folder,"
  log_line "  or copy already extracted sounds to internal storage as:"
  log_line "    $SOURCE_FOLDER_NAME/ui/"
  log_line "    $SOURCE_FOLDER_NAME/notifications/"
  log_line "    $SOURCE_FOLDER_NAME/ringtones/"
  log_line "    $SOURCE_FOLDER_NAME/alarms/"
  abort "  and flash this zip again."
fi
case "$SRC_DIR" in
  "$MODPATH"/sounds) if [ -n "$FIRMWARE" ]; then log_line "- Source: the firmware"; else log_line "- Source: sounds in the zip"; fi ;;
  *) log_line "- Source: ${SRC_DIR#"$SYSROOT"}" ;;
esac
if [ -f "$SRC_DIR/firmware-info.txt" ]; then
  log_line "  from $(head -n 1 "$SRC_DIR/firmware-info.txt")"
fi

load_config
for c in $CATEGORIES; do install_category "$c"; done
install_ui
write_props

if [ $((COUNT_UI + COUNT_OTHER)) -eq 0 ]; then
  abort "! Nothing was installed. Check folder names and ui-map.conf"
fi

# The staging copy, the extractor and the maps are only needed while installing
rm -rf "${MODPATH:?}/sounds" "${MODPATH:?}/bin" "${MODPATH:?}/maps"
set_perm_recursive "$MODPATH" 0 0 0755 0644

log_line "- Done: $COUNT_UI UI sounds replaced, $COUNT_OTHER sounds added"
log_line "- Applying them now (about 30 s)..."
if apply_now; then
  log_line "- The new sounds are active. No reboot needed."
  log_line "  With temporary root, DON'T press Reboot: it drops root."
else
  log_line "- They will be active after the next boot or soft reboot."
  log_line "  With temporary root, use a soft reboot, not Reboot."
fi
if [ -n "$FIRMWARE" ]; then
  offer_cleanup "$FIRMWARE"
else
  case "$SRC_DIR" in
    "$MODPATH"/*) ;;
    */"$SOURCE_FOLDER_NAME") offer_cleanup "$SRC_DIR" ;;
  esac
fi
