#!/system/bin/sh
# "Action" button in Magisk 28+, KernelSU and APatch.
# Mounts (or refreshes) the sounds right away, handy with temp root since
# no reboot is needed, and checks that the system really sees every file.
MODDIR=${0%/*}
# shellcheck source=common/functions.sh
. "$MODDIR/common/functions.sh"
# shellcheck source=common/mount.sh
. "$MODDIR/common/mount.sh"
enter_global_ns "$0"

echo "== Classic Galaxy Sounds =="
[ -f "$MODDIR/install.log" ] && cat "$MODDIR/install.log"

echo
echo "== Last boot =="
cat "$MODDIR/mount.log" 2>/dev/null || echo "  (not mounted yet)"

# Refresh the mounts: no reboot needed after installing, and it fixes apps
# that stopped seeing the sounds (see common/mount.sh)
echo
echo "== Mounting now =="
mount_sounds "$MODDIR"
if [ -z "$SYSROOT" ]; then
  echo "- indexing the new sounds (about 30 s)..."
  index_sounds "$MODDIR"
fi

# Compare every module file with what the system sees at its target path
echo
echo "== Mount check =="
total=0; live=0; first_bad=""
TAB=$(printf '\t')
[ -f "$MODDIR/mounts.list" ] || : > "$MODDIR/mounts.list"
while IFS="$TAB" read -r src dst; do
  [ -n "$src" ] || continue
  total=$((total + 1))
  if cmp -s "$src" "$dst"; then
    live=$((live + 1))
  elif [ -z "$first_bad" ]; then
    first_bad=${dst#"$SYSROOT"}
  fi
done < "$MODDIR/mounts.list"
echo "  $live of $total sounds are live"
if [ "$live" -lt "$total" ]; then
  echo "  e.g. not live: $first_bad"
  echo "  Try rebooting, or check mount.log above for errors."
fi

echo
echo "Done. You can close this screen."
