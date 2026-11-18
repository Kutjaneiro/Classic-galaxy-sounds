# sounds/

Optional. Audio files placed here are bundled into the zip by `tools/build.sh`
and take priority over `/sdcard/ClassicGalaxySounds` at install time.

They are git-ignored on purpose: the original sounds belong to Samsung and
must not be committed or published. Use this only for personal builds.

```
sounds/
├── ui/              lock, unlock, touch, charging... (replace stock files)
├── notifications/   added to the notification sound list
├── ringtones/       added to the ringtone list
└── alarms/          added to the alarm list
```

`cgs-extract FIRMWARE -o sounds` fills this folder from an official firmware
file, and `tools/pull-from-device.sh` from an old Galaxy connected over ADB.
