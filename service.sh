#!/system/bin/sh
# Mounts the sounds once boot has completed, then lets MediaProvider index
# the added ringtones, notifications and alarms for the pickers.
# Mounting this late is deliberate, see common/mount.sh.
MODDIR=${0%/*}
# shellcheck source=common/functions.sh
. "$MODDIR/common/functions.sh"
# shellcheck source=common/mount.sh
. "$MODDIR/common/mount.sh"
enter_global_ns "$0"
# Left behind only if an install was cut short (reboot, kernel panic)
rm -rf "${MODDIR:?}/.extract-tmp" "${MODDIR:?}/sounds" "${MODDIR:?}/bin" "${MODDIR:?}/maps"
until [ "$(getprop sys.boot_completed)" = "1" ]; do sleep 2; done
sleep 10
{
  echo "$(date '+%F %T') boot completed"
  mount_sounds "$MODDIR"
  index_sounds "$MODDIR"
} > "$MODDIR/mount.log" 2>&1
