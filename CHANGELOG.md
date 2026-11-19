# Changelog

## v1.0.3

- Ready for the KernelSU Modules repository: `module.yaml` and immutable GitHub releases.
- Release files renamed so it's clear which one to download:
  `ClassicGalaxySounds-…-Android-root-module.zip` for the phone and
  `SoundExtractor-…` for Windows, macOS and Linux.

## v1.0.0

First release.

- Extracts the sounds on the phone from the old Galaxy's official firmware
  in Download (zip, Odin .tar.md5, lz4, sparse, ext2/3/4), with MD5 check.
  No PC needed; an extractor for Windows, macOS and Linux is also attached.
- UI sounds replace their One UI counterparts (54 of 94 for the Galaxy S3);
  ringtones, notifications and alarms are added to the pickers.
- Mounts its own files with bind mounts: works on KernelSU 3.0+ without a
  metamodule, on Magisk and APatch, and with temp root.
- Optional cleanup after installing (Vol+): deletes the firmware and the
  downloaded module zip.
- Clean uninstall: stock sounds back and the added sounds removed from the
  pickers.
