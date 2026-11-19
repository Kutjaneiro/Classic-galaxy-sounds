# Classic Galaxy Sounds

[![CI](../../actions/workflows/ci.yml/badge.svg)](../../actions/workflows/ci.yml)

A **root module** that brings the original sounds of classic Galaxy phones
(Galaxy S3 and friends) to Samsung phones running **One UI 8 / 9**: ringtones,
notifications, alarms and UI sounds (lock, charging, touch...).

> **No Samsung audio is included.** The module extracts the sounds on your
> phone from the official firmware of the old Galaxy, which you download yourself.

## Requirements

- A Samsung phone on **One UI 8 or 9 with root**: Magisk, KernelSU or APatch
  (temporary root works too).
- The **official firmware of the old Galaxy** you want the sounds from, e.g.
  `GT-I9300` for the Galaxy S3 (about 1 GB, any region), from sites such as
  SamFW or SamMobile. Your current phone's firmware won't do.
- About **2 GB free** while installing.

## Install

1. Put the firmware in the phone's **Download** folder. The file name doesn't matter.
2. Download **`ClassicGalaxySounds-…-root-module.zip`** from [Releases](../../releases)
   (the other files there are optional PC tools) and install it in your root
   manager. It extracts the sounds from the firmware (about a minute).
3. Done: the sounds are active right away. Pick the new ringtones in
   **Settings > Sounds and vibration**.

> ⚠️ **With temporary root, don't press the root manager's *Reboot* button
> after installing:** a full reboot drops root, and no reboot is needed.

At the end the installer offers to delete the firmware and the module zip
(**Vol+** delete, **Vol-** keep).

## Good to know

- **Uninstalling** in the root manager leaves nothing behind: the stock sounds
  come back and the old ones leave the ringtone lists.
- **Galaxy S3:** 54 of the 94 One UI sounds are replaced; the rest belong to
  features the S3 didn't have (S Pen, Bixby...). **Other Galaxies:** ringtones,
  notifications and alarms work; UI sounds are only replaced when the names
  match.
- **Already extracted the sounds?** Put them in `/sdcard/ClassicGalaxySounds/`
  (`ui/`, `notifications/`, `ringtones/`, `alarms/`) and flash. The extractor
  also runs on a PC, see [the docs](docs/TECHNICAL.md#using-the-extractor-on-a-pc).
- **Limitations:** Samsung Keyboard sounds and One UI sound themes other than
  *Default* aren't covered; alarms also show up in the ringtone list; where
  the camera shutter sound is required by law, the camera may ignore it.

## How it works

The installer reads the firmware layer by layer (zip, Odin package, lz4,
sparse image, ext4) in a single pass, picks the sounds, and mounts them
over the stock ones with plain bind mounts: no metamodule needed, and safe
with temporary root. All the details, from the ext4 reader to KernelSU's
mount hiding, are in [docs/TECHNICAL.md](docs/TECHNICAL.md).

## License

Code under the [MIT License](LICENSE). Not affiliated with or endorsed by Samsung.
Galaxy, One UI and TouchWiz are trademarks of Samsung Electronics.
