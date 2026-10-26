# night-clip

Windows ↔ Android clipboard sync and file transfer over a Tailscale tailnet.
One Go core, two thin shells. Design documents live in [`../docs`](../docs)
(start with `README.md`, then `adr/005-go-core-and-tailnet-p2p.md`).
**Picking this up? Read [`HANDOFF.md`](HANDOFF.md) first** — current state, decisions, verification matrix and the detailed next steps. The persistence increment (the `store` data layer, now wired into the facade) has its own handoff: [`HANDOFF-STORE.md`](HANDOFF-STORE.md).

```text
night-clip/
├── core/                    Go module `nightclip/core` — all business logic
│   ├── proto/               envelope, canonical JSON, content hash, error codes   [implemented]
│   ├── identity/            Ed25519 key, self-signed cert, device_id, fingerprint [implemented]
│   ├── facade/              Call / NextEvent JSON facade shared by all shells     [skeleton: version, status, identity.get; persists identity via store]
│   ├── cmd/cbcore-dll/      c-shared build → clipbridge_core.dll (5 C functions)  [implemented]
│   ├── cmd/cbridge/         Windows CLI                                          [version only]
│   ├── mobile/              gomobile surface → clipbridge-core.aar                [implemented; AAR builds]
│   ├── store/               SQLite: schema v1, migrations, field encryption       [implemented; identity wired into facade]
│   ├── peer/ pairing/ clip/ xfer/                                                 [doc.go only — Phase 0/1/2]
│   └── test/abi/smoke.c     C ABI smoke test (run via `make dll-smoke`)
├── windows/ClipBridge.Windows/   WPF shell: P/Invoke + event pump + status window [compiles on Linux]
├── windows/ClipBridge.Windows.Tests/   xUnit tests of the C# interop layer against the real native core
├── android/                      Kotlin/Compose shell: AAR wrapper + foreground service [debug/release APK build]
├── .github/workflows/ci.yml      CI (runs on push to origin/main)
└── Makefile
```

## Status

Scaffold plus a verification harness that works on a Linux machine. `make check`
runs everything below and passes on the dev machine (Ubuntu 26.04, no KVM, no Windows).

| Part | What is verified | How |
| --- | --- | --- |
| Go core | proto, identity, store, facade unit tests, race detector on | `make test` |
| C ABI | all 5 exports, UTF-8, timeout, prompt wake on shutdown | `make dll-smoke` (Linux `.so`) |
| Windows DLL | cross-compiled PE DLL loads and works in Windows userland | `make dll-wine` (mingw `smoke_win.exe` under Wine) |
| C# interop | `CoreHost`: P/Invoke marshaling, UTF-8 round trip, event pump thread, `Dispose` stops the pump | `make test-interop` (xUnit against the Linux `.so`) |
| WPF app | compiles (XAML included) for `net10.0-windows` | `make build-windows-app` |
| Android | gomobile AAR builds (arm64-v8a, x86_64); debug and release (R8) APKs build; lint runs | `make apk` |
| `store` + persistent identity | schema v1 + migrations, AES-256-GCM field sealing, identity persistence, data-layer CRUD; 40 `store` + 15 `facade` tests. `device_id` is stable across restarts for the same `data_dir` + key. Shells keep a temporary plaintext `devkey.b64` (DPAPI/Keystore later) | `make check` (see [HANDOFF-STORE.md](HANDOFF-STORE.md)) |
| Networking, pairing, files | not started | — |

### What this machine cannot prove

These need a real Windows PC or Android phone (or the prepared Windows CI job) and are
tracked as Phase 0 PoC items in `../docs/development-plan.md` §3.3:

- WPF window actually rendering, tray, `WM_CLIPBOARDUPDATE`, DPAPI, firewall rule, Tailscale interface appearing late.
- The core running inside an Android process under a real Tailscale VPN (P-A), foreground-service survival on OEM builds (P-C), Doze.
- Anything involving two machines; the in-process two-instance integration tests will cover protocol logic on Linux once `peer`/`store` exist.

## Build

```sh
make check          # everything verifiable on Linux (see table above)
make test           # Go unit tests
make dll            # core/dist/windows-amd64/clipbridge_core.dll (+ .h)
make aar            # android/app/libs/clipbridge-core.aar
make apk            # debug APK
```

Toolchain assumed (installed on the dev machine, not by this repo): Go, mingw-w64, Wine
(`wine64`), a .NET 10 SDK (`dotnet`, or `~/.dotnet/dotnet`), JDK 17, Android SDK 35 with NDK 28.2,
`gomobile` in `$(go env GOPATH)/bin`, and Gradle only to regenerate the wrapper.
`ANDROID_HOME`, `ANDROID_NDK_HOME` and `JAVA_HOME` default to the paths in the Makefile.

On Windows (optional, with the .NET 10 SDK and a MinGW gcc): build the DLL as in
`.github/workflows/ci.yml`, then `dotnet build windows\ClipBridge.Windows`.

## Conventions

- Shells call the core only through `Call` / `NextEvent`; they never import other core packages.
- No Go→C# or Go→Kotlin callbacks; shells pump `NextEvent` on a dedicated thread.
- Logs never contain payloads, PSKs, private keys or the database key.
- Wire names (`clipbridge/v1`, `clipbridge_core.dll`, `\\.\pipe\clipbridge.v1`) follow the design docs;
  `night-clip` is the repository name.
