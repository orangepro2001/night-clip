# MoonPassword

A password manager for Windows, macOS and Android. One Go core holds all the logic (KDBX 4 vault, SSH keys and
agent, self-update); each platform has a thin native shell. The vault is a standard KeePass file, so it can always
be opened with KeePassXC.

MoonPassword grew out of ClipBridge (clipboard sync over a Tailscale tailnet). Clipboard sync, file transfer and
tailnet pairing were removed in 2026-11; the old design documents are kept in
[`docs/archive-clipbridge/`](docs/archive-clipbridge). On Windows and Android, the Settings page offers Moon SSO sign-in
in the system browser. The master password still unlocks the local vault. Cross-device transfer currently
uses encrypted KDBX export and import.

```text
core/                         Go module `moonpassword/core`: all business logic
├── facade/                   Call / NextEvent JSON API shared by every shell
├── vault/                    KDBX 4 vault: entries, groups, history, backups, merge, import/export, TOTP, health
├── sshkey/ sshagent/         SSH private keys in the vault, served by a built-in ssh-agent (docs/adr/009-ssh-agent.md)
├── store/                    SQLite: settings and secrets sealed with the device's database key
├── update/                   signed self-update from release-publish (docs/update.md)
├── migrate/                  reading what an old ClipBridge install left behind
├── markdown/                 Markdown renderer for secure notes
├── cmd/cbcore-dll/           c-shared build → moonpassword_core.dll / .dylib
└── mobile/                   gomobile surface → moonpassword-core.aar
windows/MoonPassword.Windows/ Windows shell (Avalonia 12 + Lumen.UI)
windows/MoonPassword.Windows.Tests/  xUnit tests of the shared C# logic and of the interop with the real core
macos/MoonPassword.Mac/       macOS shell (Avalonia), shares the C# logic with Windows
android/                      Android shell (Kotlin, Jetpack Compose), autofill service, biometric unlock
scripts/                      release, update probe, brand icons, key backup
```

## Build

```sh
make test           # Go unit tests (race detector on)
make dll            # core/dist/windows-amd64/moonpassword_core.dll
make aar            # android/app/libs/moonpassword-core.aar
make dist-windows dist-android dist-macos   # release packages (docs/update.md §7b for the Windows setup)
scripts/release-local.sh --no-build --notes "…"   # publish to https://release.moonl.cloud (app `moonpassword`)
```

Toolchain (installed on the build machine, not by this repo): Go, a MinGW gcc, zig (macOS core),
the .NET 10 SDK, JDK 21, Android SDK 35 with NDK 28.2 and 27.0, `gomobile`, `rcodesign` and `relhub`.
Releases are built and published from the developer PC or KOEI.

## Conventions

- Shells call the core only through `Call` / `NextEvent`; they never import other core packages.
- No Go→C# or Go→Kotlin callbacks; shells pump `NextEvent` on a dedicated thread.
- Logs never contain secrets, vault contents or keys.
- The brand mark is generated from one source: `scripts/brand.py` (docs/brand.md).
