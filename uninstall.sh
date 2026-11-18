#!/system/bin/sh
# Run by the root manager when the module is removed, at the next boot or
# soft reboot: puts the stock sounds back and takes the added ones out of
# the ringtone pickers, so nothing of the module is left behind.
MODDIR=${0%/*}
# shellcheck source=common/functions.sh
. "$MODDIR/common/functions.sh"
# shellcheck source=common/mount.sh
. "$MODDIR/common/mount.sh"
enter_global_ns "$0"
forget_sounds "$MODDIR"
