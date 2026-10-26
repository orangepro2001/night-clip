# Clipboard probes (development only)

Three tiny programs that run **as the shell user** through `app_process`, to test clipboard
behaviour on a device or emulator without any UI:

| Probe | What it does |
| --- | --- |
| `SetClip "text"` | Copies a text the way "another app" would (the app under test is not focused). |
| `GetClip` | Reads the clipboard once. |
| `WatchClip [ms]` | Polls every 300 ms and prints each change. |

```sh
tools/clipboard-probes/build.sh
adb push tools/clipboard-probes/out/SetClip.dex /data/local/tmp/setclip.dex
adb shell "CLASSPATH=/data/local/tmp/setclip.dex app_process /system/bin SetClip hello"
```

They use hidden `IClipboard` methods through reflection (signatures checked on Android 15); they
are not part of the app. Findings from using them: `docs/plan-android-background-read.md` §7.
