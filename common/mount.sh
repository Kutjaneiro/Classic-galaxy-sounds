# shellcheck shell=sh
# Systemless mounting, done by the module itself with plain bind mounts.
#
# Why not let the root manager mount a system/ folder? Official KernelSU 3.0+
# only does that through a "metamodule", and the reference one
# (meta-overlayfs) panics kernels rooted through an exploit (temp root on
# Samsung devices with KDP): overlayfs swaps process credentials, which is
# exactly what Samsung's kernel protection guards. Bind mounts are a basic
# VFS operation, behave the same under Magisk, every KernelSU flavour and
# APatch, and need nothing else installed.
#
# Module layout (written by customize.sh):
#   media/ui/<One UI name>   replaces the stock file with the same name
#   media/<category>/<file>  added next to the stock sounds of <category>
#   media/alarms/<file>      added to .../alarms, or to .../ringtones/alarms
#                            on devices without an alarms folder (One UI 9);
#                            Android flags any path with /alarms/ as an alarm
#
# A bind mount can replace a file but can't add one to a read-only folder.
# For the categories, the folder is rebuilt under /dev (a tmpfs that Android
# wipes on every boot): stock entries bound back in, ours added. Then that
# folder is bind-mounted over the original: the same idea as Magisk's magic
# mount, with no mount types other than bind.
#
# When to mount: late, once boot has completed (service.sh), never in
# post-fs-data. KernelSU's kernel_umount hides module mounts from apps that
# weren't granted root, and the mounts it hides are the ones that already
# exist when its boot stages run (a soft reboot runs them again). Mounts made
# afterwards stay visible to every app, which matters here: apps like the
# media scanner, the clock or the dialer have to read the sounds. So every
# mount_sounds call first drops the module's previous mounts and mounts fresh.
#
# Environment: SYSROOT (tests only), DRY_RUN=1 prints the plan instead of
# mounting. Needs find_media_dir from common/functions.sh.

MOUNT_CON=u:object_r:system_file:s0
_bb=""

_find_busybox() {
  for _b in /data/adb/ksu/bin/busybox /data/adb/magisk/busybox /data/adb/ap/bin/busybox; do
    if [ -x "$_b" ]; then _bb=$_b; return 0; fi
  done
  return 0
}

_mount() {
  if [ -n "$_bb" ]; then "$_bb" mount "$@"; else mount "$@"; fi
}

# Module scripts must mount in init's mount namespace so that every process
# (and every app started later) sees the result. Re-run the calling script
# there if needed.
enter_global_ns() {
  [ -n "$CGS_GLOBAL_NS" ] && return 0
  [ "$(readlink /proc/self/ns/mnt)" = "$(readlink /proc/1/ns/mnt)" ] && return 0
  _find_busybox
  export CGS_GLOBAL_NS=1
  if [ -n "$_bb" ]; then
    exec "$_bb" nsenter -t 1 -m -- /system/bin/sh "$@"
  else
    exec nsenter -t 1 -m -- /system/bin/sh "$@"
  fi
}

_op_bind() {   # src dst
  if [ -n "$DRY_RUN" ]; then
    echo "bind $1 -> $2"
  elif ! _mount -o bind "$1" "$2"; then
    echo "! bind failed: $2"; return 1
  fi
}

_op_rbind() {  # src dst: bind a folder together with the mounts inside it
  if [ -n "$DRY_RUN" ]; then
    echo "rbind $1 -> $2"
  elif ! _mount -o rbind "$1" "$2"; then
    echo "! rbind failed: $2"; return 1
  fi
}

_op_label() {  # path: apps may only read folders labelled as system files
  [ -n "$DRY_RUN" ] && return 0
  command -v chcon >/dev/null 2>&1 && chcon "$MOUNT_CON" "$1" 2>/dev/null
  return 0
}

# "module file<TAB>where the system sees it", checked by action.sh
_record() { printf '%s\t%s\n' "$1" "$2" >> "$_list"; }

# Mount points of this module's mounts, deepest first. Only mounts that are
# clearly ours: anything inside the staging folder, UI files bound from a
# media/ui/ folder, and category folders bound from the staging folder.
# A source that was deleted (the module was updated: the root manager
# replaced its folder) shows up as "<path>//deleted" and is still ours.
_our_mounts() {
  awk -v base="$_base" -v stage="$_stage" '
    { root = $4; mp = $5 }
    mp == stage || index(mp, stage "/") == 1 { print mp; next }
    index(mp, base "/ui/") == 1 && root ~ /\/media\/ui\/[^\/]+(\/\/deleted)?$/ { print mp; next }
    (mp == base "/ringtones" || mp == base "/notifications" || mp == base "/alarms") &&
      root ~ /classic_galaxy_sounds\/(ringtones|notifications|alarms)(\/\/deleted)?$/ { print mp }
  ' "${CGS_MOUNTINFO:-/proc/self/mountinfo}" | sort -r
}

# Drop every mount made by an earlier mount_sounds call.
unmount_sounds() {
  find_media_dir || return 0
  _base="$SYSROOT$MEDIA_DIR"
  _stage="$SYSROOT/dev/classic_galaxy_sounds"
  [ -n "$DRY_RUN" ] && return 0
  _find_busybox
  for _pass in 1 2 3; do
    _left=$(_our_mounts)
    [ -n "$_left" ] || break
    echo "$_left" | while IFS= read -r _m; do
      umount -l "$(printf '%b' "$_m")" 2>/dev/null ||
        { [ -n "$_bb" ] && "$_bb" umount -l "$(printf '%b' "$_m")" 2>/dev/null; }
    done
  done
  if [ -n "$(_our_mounts)" ]; then
    echo "! could not remove the previous mounts"
    return 1
  fi
  # Safe now: nothing is mounted inside it any more
  rm -rf "$_stage"
}

# Mount everything in $1 (the module folder). Calling it again refreshes the
# mounts instead of stacking new ones on top.
mount_sounds() {
  _mod=$1
  _media="$_mod/media"
  _list="$_mod/mounts.list"

  find_media_dir || { echo "! stock sound folder not found"; return 1; }
  unmount_sounds || return 1
  _base="$SYSROOT$MEDIA_DIR"
  _find_busybox
  : > "$_list"
  _n=0; _fail=0

  # A bind mount keeps the source file's SELinux label, and apps can only
  # read system_file. Re-label every time: files moved around under
  # /data/adb (module updates, backups made by root tools) may come back
  # as adb_data_file, which mutes them for every app.
  if [ -z "$DRY_RUN" ] && command -v chcon >/dev/null 2>&1; then
    chcon -R "$MOUNT_CON" "$_media" 2>/dev/null
    chmod -R a+rX "$_media" 2>/dev/null
  fi

  # 1. UI sounds: one bind mount per replaced file
  for _f in "$_media/ui"/*; do
    [ -f "$_f" ] || continue
    _t="$_base/ui/${_f##*/}"
    [ -f "$_t" ] || continue
    if _op_bind "$_f" "$_t"; then _record "$_f" "$_t"; _n=$((_n + 1)); else _fail=$((_fail + 1)); fi
  done

  # 2. Where do the alarms go?
  _alarms_into=""
  if [ -n "$(ls -A "$_media/alarms" 2>/dev/null)" ] && [ ! -d "$_base/alarms" ]; then
    if [ -d "$_base/ringtones" ]; then
      _alarms_into=ringtones
    else
      echo "! no alarms or ringtones folder on this device, alarms skipped"
    fi
  fi

  # 3. Categories: rebuild the folder under /dev and mount it over the stock one
  for _c in ringtones notifications alarms; do
    [ -d "$_base/$_c" ] || continue
    _nest=""; [ "$_alarms_into" = "$_c" ] && _nest=1
    [ -n "$(ls -A "$_media/$_c" 2>/dev/null)" ] || [ -n "$_nest" ] || continue

    _s="$_stage/$_c"
    mkdir -p "$_s"
    chmod 755 "$_stage" "$_s"

    # Ours first, so they win over a stock file with the same name
    for _f in "$_media/$_c"/*; do
      [ -f "$_f" ] || continue
      : > "$_s/${_f##*/}"
      if _op_bind "$_f" "$_s/${_f##*/}"; then
        _record "$_f" "$_base/$_c/${_f##*/}"; _n=$((_n + 1))
      else
        _fail=$((_fail + 1))
      fi
    done
    if [ -n "$_nest" ]; then
      mkdir -p "$_s/alarms"
      if _op_bind "$_media/alarms" "$_s/alarms"; then
        for _f in "$_media/alarms"/*; do
          [ -f "$_f" ] || continue
          _record "$_f" "$_base/$_c/alarms/${_f##*/}"; _n=$((_n + 1))
        done
      else
        _fail=$((_fail + 1))
      fi
    fi

    # Then every stock entry (files and folders such as SoundTheme/), bound back
    for _e in "$_base/$_c"/* "$_base/$_c"/.[!.]*; do
      [ -e "$_e" ] || continue
      _name=${_e##*/}
      [ -e "$_s/$_name" ] && continue
      if [ -d "$_e" ]; then mkdir "$_s/$_name"; else : > "$_s/$_name"; fi
      _op_bind "$_e" "$_s/$_name" || _fail=$((_fail + 1))
    done

    _op_label "$_s"
    _op_rbind "$_s" "$_base/$_c" || _fail=$((_fail + 1))
  done

  echo "- mounted $_n sounds over $MEDIA_DIR ($_fail errors)"
  [ "$_fail" -eq 0 ]
}

# New ringtones, notifications and alarms only reach the pickers once
# MediaProvider has indexed them. A full scan of the internal volume skips
# folders it has seen before, so index every added file explicitly.
# Mount right after installing (customize.sh), so no reboot is needed: a
# full reboot also drops a temporary root. The installer may run in an app's
# mount namespace, hence nsenter. At the next boot the root manager moves the
# module folder and service.sh refreshes the mounts from there.
apply_now() {
  [ "$BOOTMODE" = true ] && [ -z "$SYSROOT" ] || return 1
  _find_busybox
  if [ -n "$_bb" ]; then set -- "$_bb" nsenter; else set -- nsenter; fi
  _st="$MODPATH/.apply-status"
  # shellcheck disable=SC2016  # expanded by the inner shell
  { "$@" -t 1 -m -- /system/bin/sh -c '
      . "$1/common/functions.sh"; . "$1/common/mount.sh"
      mount_sounds "$1" && index_sounds "$1"' sh "$MODPATH" < /dev/null 2>&1
    echo "$?" > "$_st"; } | while IFS= read -r _l; do
    [ -n "$_l" ] && log_line "  $_l"
  done
  _ok=$(cat "$_st" 2>/dev/null)
  rm -f "$_st"
  [ "$_ok" = 0 ]
}

# Module removal (uninstall.sh): drop the mounts and, once Android is up,
# the index entries of the added sounds, so they leave the pickers.
# uninstall.sh may run before MediaProvider is up, and the module folder is
# deleted right after it, so what the cleanup needs waits in /dev (a tmpfs:
# nothing survives a reboot) and removes itself when done.
forget_sounds() {
  unmount_sounds
  _dir="$SYSROOT/dev/classic_galaxy_sounds.forget"
  rm -rf "$_dir"
  [ -s "$1/mounts.list" ] || return 0
  mkdir -p "$_dir/common"
  cp "$1/mounts.list" "$_dir/"
  cp "$1/common/functions.sh" "$1/common/mount.sh" "$_dir/common/"
  if [ -n "$DRY_RUN" ]; then
    index_sounds "$_dir"
    rm -rf "$_dir"
    return 0
  fi
  # The files are gone now, so "scanning" them removes their entries
  # shellcheck disable=SC2016  # expanded by the detached shell
  _job='until [ "$(getprop sys.boot_completed)" = 1 ]; do sleep 5; done
    sleep 10
    . "$1/common/functions.sh"; . "$1/common/mount.sh"
    index_sounds "$1"
    rm -rf "$1"'
  # Detached: the root manager doesn't wait for it
  if command -v setsid >/dev/null 2>&1; then
    setsid sh -c "$_job" sh "$_dir" < /dev/null > /dev/null 2>&1 &
  else
    nohup sh -c "$_job" sh "$_dir" < /dev/null > /dev/null 2>&1 &
  fi
}

index_sounds() {
  _list="$1/mounts.list"
  [ -f "$_list" ] || return 0
  _n=0
  _tab=$(printf '	')
  while IFS="$_tab" read -r _src _dst; do
    case "$_dst" in ""|*/ui/*) continue ;; esac
    if [ -n "$DRY_RUN" ]; then
      echo "index ${_dst#"$SYSROOT"}"
    elif ! content call --uri content://media --method scan_file --arg "${_dst#"$SYSROOT"}" < /dev/null > /dev/null 2>&1; then
      continue
    fi
    _n=$((_n + 1))
  done < "$_list"
  echo "- indexed $_n sounds for the pickers"
}
