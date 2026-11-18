# Classic Galaxy Sounds

[![CI](../../actions/workflows/ci.yml/badge.svg)](../../actions/workflows/ci.yml)

Bring the original sounds of classic Galaxy phones (Galaxy S3 and friends) to
modern Samsung devices running **One UI 8 / 9**.

The project has two parts:

- **A root module** for **Magisk, KernelSU and APatch** that installs the sounds
  systemlessly: UI sounds replace their One UI counterparts, while ringtones,
  notifications and alarms are added to the system pickers.
- **A firmware extractor** (`cgs-extract`, in Go) that pulls the stock sounds
  straight out of an official Samsung firmware package. The module ships it
  and runs it on the phone while installing, so **no PC is needed**. The same
  program runs on Windows, macOS and Linux.

> **No Samsung audio is included in this repository or in the releases.** The
> sounds belong to Samsung, so they are extracted from the official firmware
> of the phone you want them from.

## Quick start

1. Download the official firmware of the old phone (for the Galaxy S3,
   `GT-I9300`) to the phone's **Download** folder. Any region works.
2. Flash the module zip from Releases in Magisk / KernelSU / APatch. It finds
   the firmware, checks its MD5, extracts the sounds (about a minute) and
   installs them.
3. That's it: the installer mounts the sounds right away, **no reboot
   needed**. Choose the new ringtones in **Settings > Sounds and vibration**.
   With temporary root, don't press the manager's *Reboot* button after
   installing: a full reboot drops root.

The file name doesn't matter: the module looks at what each big file in
Download contains, skips anything that isn't firmware (photo or music zips)
and uses the newest one if there are several. The firmware of a phone with
Android 10 or newer (such as the One UI phone itself) is recognised and
explained, since it holds neither the old sounds nor a readable system image.

At the end the installer asks whether to delete the firmware (**Vol+** delete,
**Vol-** keep; it keeps it if no key is pressed). Nothing else is left behind:
the temporary files and the extractor itself are removed, also after an
install that was cut short.

Nothing else is needed: the module mounts its own files, so it works on official
KernelSU 3.0+ **without a metamodule** (no meta-overlayfs), and on temp-rooted
phones where overlayfs-based mounting crashes the kernel. See
[How the sounds are mounted](#how-the-sounds-are-mounted).

Already have the sounds? Put them in `/sdcard/ClassicGalaxySounds/` (`ui/`,
`notifications/`, `ringtones/`, `alarms/`) and the module uses them instead.
If you still have the old phone, `tools/pull-from-device.sh` copies its sounds
over ADB (no root needed).

### On a PC

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
[`maps/`](maps), and refuses to mix sounds into a folder that already has some.
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
                                      sounds for this phone                         ringtone pickers (common/mount.sh)
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

### How the sounds are mounted

Root managers normally mount a module's `system/` folder for it. Official
KernelSU 3.0+ delegates that to a *metamodule*, and the reference one,
meta-overlayfs, builds an overlayfs that swaps process credentials. On phones
rooted through a kernel exploit (temp root on Samsung devices with KDP/DEFEX),
that panics the kernel.

So the module ships no `system/` folder and mounts its files itself
([`common/mount.sh`](common/mount.sh)) with plain **bind mounts** only,
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

### Configuration

Both files are optional; a copy in `/sdcard/ClassicGalaxySounds/` overrides the
one in the zip.

- **`ui-map.conf`** maps One UI file names to old ones (`OneUI.ogg=Old.ogg`).
  The extractor fills it in from `maps/` when it knows the model.
  `tools/dump-device-sounds.sh` compares both sets of names to help write one.
- **`config.conf`** sets the default ringtone, notification and alarm used
  after a reset. It never overrides sounds you've already chosen.

### Supported phones

| Old phone (source) | UI map |
|---|---|
| Galaxy S3 (GT-I9300 and variants) | 54 of 94 One UI sounds (the rest are features the S3 didn't have: S Pen, Bixby, Air command...) |
| Any other Galaxy | ringtones, notifications and alarms work; UI sounds use same-name matching only |

New maps are welcome: add a `maps/<phone>.conf` with a `# models:` line.

### Limitations

- **Samsung Keyboard** sounds live inside the keyboard app, not the media folder.
- One UI **System sound themes** other than *Default* may use their own files.
- If a ringtone from the module is selected and the module is removed (or, with
  temp root, after a reboot before rooting again), Android falls back to the
  default ringtone.

Removing the module in the root manager leaves nothing behind: at the next
boot or soft reboot `uninstall.sh` puts the stock sounds back and, once
Android is up, takes the added sounds out of the ringtone pickers.
- Alarms also show up in the ringtone list, because they live under `ringtones/`.
- Where the camera shutter sound is mandatory by law, the camera may ignore the replacement.

## Development

```bash
(cd extractor && go test ./...)        # extractor tests
tests/run.sh                           # installer + mount tests on a fake One UI filesystem
sudo tests/run.sh                      # ...including real bind mounts (Linux)
tests/run.sh "busybox ash"             # ...with the shell root managers use
tools/build.sh                         # builds dist/classic-galaxy-sounds-<version>.zip (needs Go)
tools/build.sh --pc                    # ...plus cgs-extract for Windows, macOS and Linux
```

The extractor tests build ext2/ext4 and sparse images in memory
([`builder_test.go`](extractor/firmware/builder_test.go)) to cover block maps,
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

## License

Code under the [MIT License](LICENSE). Not affiliated with or endorsed by Samsung.
Galaxy, One UI and TouchWiz are trademarks of Samsung Electronics.
