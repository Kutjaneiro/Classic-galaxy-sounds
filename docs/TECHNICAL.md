# How Classic Galaxy Sounds works

Technical details for the curious and for contributors. For installing and
using the module, see the [README](../README.md).

- [Installer flow](#installer-flow)
- [The firmware extractor](#the-firmware-extractor)
- [The module](#the-module)
- [How the sounds are mounted](#how-the-sounds-are-mounted)
- [Configuration](#configuration)
- [Adding a phone](#adding-a-phone)
- [Development](#development)

## Installer flow

The firmware's file name doesn't matter. The installer looks at every file
over 100 MB in Download (and in `/sdcard/ClassicGalaxySounds`) and runs
`cgs-extract --check` on it, which reads only a zip's directory or a tar's
headers. Anything that isn't firmware (photo or music zips, the BL / CP / CSC
parts of a split firmware) is skipped silently; if several firmwares qualify,
the newest one is used. The firmware of a phone with Android 10 or newer
(`super.img`, EROFS), such as the One UI phone itself, is recognised and
explained instead of failing obscurely.

After extracting, the sounds are mounted right away (no reboot: a full reboot
would also drop a temporary root). Then the installer offers to delete the
firmware and the downloaded module zip (**Vol+** delete, **Vol-** or no key
keep). The module zip is found by comparing files byte for byte with the copy
the root manager installs from, so nothing else can match. Temporary files
and the extractor are removed, also after an install that was cut short.

Removing the module runs `uninstall.sh` at the next boot or soft reboot: it
drops the mounts, then a detached job waits for Android to come up and takes
the added sounds out of MediaProvider, since the module folder is deleted
right after the script.

### Using the extractor on a PC

The release also has `cgs-extract` for Windows, macOS and Linux:

```bash
cgs-extract SAMFW.COM_GT-I9300_AMN_I9300XXUGPE1_fac.zip
#  -> creates ClassicGalaxySounds-I9300XXUGPE1/ next to the firmware
adb push ClassicGalaxySounds-I9300XXUGPE1 /sdcard/ClassicGalaxySounds
```

On Windows you can also drag the firmware file onto `cgs-extract.exe`: the
window stays open with the result and the output folder opens when it's done.

## The firmware extractor

```
$ cgs-extract SAMFW.COM_GT-I9300_AMN_I9300XXUGPE1_fac.zip
Decompressing AP_I9300XXUGPE1_2921268_REV00_user_low_ship.tar.md5 from SAMFW.COM_GT-I9300_AMN_I9300XXUGPE1_fac.zip...
Odin package: AP_I9300XXUGPE1_2921268_REV00_user_low_ship.tar.md5
  found system.img (1367 MB)
  MD5 OK
  sparse image
Firmware: GT-I9300, Android 4.3, build I9300XXUGPE1
  ui             54 sounds
  notifications  16 sounds
  ringtones      33 sounds
  alarms         7 sounds
UI map: galaxy-s3.conf
```

A Samsung firmware is several layers of containers. The extractor goes through
all of them in a single pass, and writes at most one temporary file (the
system image), deleted when it finishes:

```
firmware.zip ─► Odin .tar.md5 ─► system.img ─► [lz4] ─► Android sparse ─► ext4 ─► media/audio
 zip member      tar stream +     temp file     lz4      chunk index       own reader
 (or in place)   MD5 on the fly   (or offset)            (random access)
```

| Layer | How it's handled |
|---|---|
| `.zip` download | picks the `AP_` / single-file Odin package. A stored (uncompressed) member is read in place; a compressed one is streamed |
| `.tar.md5` | the MD5 footer is checked on the same pass that reads the tar. A plain file is read at the system image's offset without touching the other members |
| `.lz4` (newer firmwares) | decompressed to a temp file |
| Android sparse | raw / fill / don't-care / CRC chunks become a block index that serves reads (Samsung's extended chunk headers too) |
| ext2 / ext3 / ext4 | a small read-only reader: superblock, 32/64-bit group descriptors, inodes, block maps (direct + indirect) and extent trees, holes and unwritten extents |

It reads `build.prop` to identify the model, picks a matching UI map from
[`maps/`](../maps), and refuses to mix sounds into a folder that already has some.
`--list` shows the sounds without extracting anything, and `--check` only says
whether a file is a firmware package (the installer uses it to skip other zips).
It's a static binary with no dependencies, so Android runs the same build as
any Linux arm64 machine.

## The module

```
 firmware in Download ──► cgs-extract ──┐
                                        ├─► customize.sh ──► $MODPATH/media/... ──► service.sh (boot completed)
 /sdcard/ClassicGalaxySounds ───────────┘        │                                  bind mounts over /system/media/audio,
                                      picks and renames the                         then indexes the new sounds for the
                                      sounds for this phone                         ringtone pickers (../common/mount.sh)
```

1. `find_media_dir` finds the stock sound folder (`/system/media/audio`, or
   `/product/media/audio` on builds that moved it).
2. `find_source_dir` looks for sounds bundled in the zip, then in
   `ClassicGalaxySounds` on internal storage. If there are none,
   `find_firmware` looks for firmware-sized files there and in Download, and
   `extract_firmware` runs the bundled `bin/cgs-extract-<arch>` on the first one
   that `--check` accepts. The extractor is deleted from the module afterwards.
3. Ringtones, notifications and alarms are copied as new files. UI sounds go
   through `ui-map.conf` first, then automatic case-insensitive name matching.
   Only files that exist on the device are overwritten.
4. Optional defaults go to `system.prop`, loaded by the root manager at boot.

The config files are **parsed, never sourced**. They come from shared storage
that any app can write to, and sourcing them would run their contents as root.
File names containing paths are rejected for the same reason.

The *Action* button in the root manager mounts the sounds if they aren't yet
(no reboot needed, handy with temp root), then compares every module file with
what the system sees at its target path and reports how many are live.

## How the sounds are mounted

Root managers normally mount a module's `system/` folder for it. Official
KernelSU 3.0+ delegates that to a *metamodule*, and the reference one,
meta-overlayfs, builds an overlayfs that swaps process credentials. On phones
rooted through a kernel exploit (temp root on Samsung devices with KDP/DEFEX),
that panics the kernel.

So the module ships no `system/` folder and mounts its files itself
([`common/mount.sh`](../common/mount.sh)) with plain **bind mounts** only,
which behave the same under Magisk, every KernelSU flavour and APatch:

- **UI sounds** have fixed names, so each replaced file gets its own bind
  mount over the stock one.
- **Ringtones, notifications and alarms** are new files, and a read-only folder
  can't gain files. The folder is rebuilt under `/dev` (a tmpfs Android wipes
  on every boot), with the stock entries bound back in and ours added, and the
  rebuilt folder is bind-mounted over the original. This is the same idea as
  Magisk's magic mount.
- One UI 9 has **no `alarms` folder**, and creating one would mean rebuilding
  the whole `media/audio` folder. Alarms go to `ringtones/alarms/` instead:
  Android flags any path containing `/alarms/` as an alarm sound.
- Mounting happens in init's mount namespace (`nsenter -t 1 -m`), so every
  app, including ones already running, sees the change.
- Mounting happens **late**, once boot has completed, and always drops the
  previous mounts first. KernelSU's `kernel_umount` hides module mounts from
  apps without root, and it picks the ones that exist when its boot stages run
  (a soft reboot runs them again). Those apps include the media scanner, the
  clock and the dialer, which need to read the sounds. Mounts made after the
  stages stay visible to every app.
- `service.sh` then asks MediaProvider to index each added file
  (`scan_file`). A full volume scan skips folders it has already seen.

`tests/run.sh` checks the mount plan with a dry run on any machine. On Linux as
root, it also performs the real mounts inside a private namespace.

## Configuration

Both files are optional; a copy in `/sdcard/ClassicGalaxySounds/` overrides the
one in the zip.

- **`ui-map.conf`** maps One UI file names to old ones (`OneUI.ogg=Old.ogg`).
  The extractor fills it in from `maps/` when it knows the model.
  `tools/dump-device-sounds.sh` compares both sets of names to help write one.
- **`config.conf`** sets the default ringtone, notification and alarm used
  after a reset. It never overrides sounds you've already chosen.

## Adding a phone

UI sounds have different names on every Galaxy generation, so each old phone
needs a map from One UI names to its own. Add `maps/<phone>.conf` with a
`# models:` line listing the model numbers it covers (prefix match, so
`GT-I9300` also covers `GT-I9300T`) and `OneUI_name.ogg=Old_name.ogg` lines.
`tools/dump-device-sounds.sh` lists both sets of names side by side.

## Development

```bash
(cd extractor && go test ./...)        # extractor tests
tests/run.sh                           # installer + mount tests on a fake One UI filesystem
sudo tests/run.sh                      # ...including real bind mounts (Linux)
tests/run.sh "busybox ash"             # ...with the shell root managers use
tools/build.sh                         # builds dist/ClassicGalaxySounds-<version>-Android-root-module.zip (needs Go)
tools/build.sh --pc                    # ...plus cgs-extract for Windows, macOS and Linux
```

The extractor tests build ext2/ext4 and sparse images in memory
([`builder_test.go`](../extractor/firmware/builder_test.go)) to cover block maps,
double indirect blocks, extent trees, holes and every sparse chunk type, plus
whole Odin, zip and lz4 packages, corrupt downloads and temp file cleanup. CI also checks
the reader against images made by the real `mke2fs` across ext2, ext3, ext4,
64-bit and `metadata_csum` filesystems.

CI runs ShellCheck, gofmt, `go vet` and every test suite (Go with the race
detector), then builds the module zip and the PC extractors.
Pushing a `v*` tag publishes a GitHub release.

```
├── extractor/                 firmware extractor (Go): firmware/ library, cmd/cgs-extract
├── maps/                      UI sound maps per old phone
├── customize.sh               module installer entry point
├── common/functions.sh        installer logic (POSIX sh)
├── common/mount.sh            bind-mount logic, no metamodule needed
├── service.sh                 mounts and indexes the sounds once boot completes
├── action.sh                  mount now + live check for the Action button
├── uninstall.sh               clean removal: stock sounds back, pickers cleaned
├── config.conf, ui-map.conf   module defaults
├── update.json, CHANGELOG.md  update info for the root managers
├── META-INF/                  Magisk installer for recovery flashing
├── tests/                     installer + mount tests
└── tools/                     build, pull-from-device, dump-device-sounds
```

