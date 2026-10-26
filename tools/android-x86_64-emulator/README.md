# x86_64 emulator build of the core (development only)

`make aar` / `make apk` produce an AAR whose Go core is killed with `SIGSYS` on an **x86_64**
Android emulator: `modernc.org/libc` issues legacy x86-only syscalls (`lstat`, `open`, `mkdir`,
`unlink`, ...) that Android's seccomp policy for apps does not allow. arm64 has no such syscalls,
so real phones are not affected.

`build-aar.sh` builds an x86_64-only AAR from a throw-away GOPATH in which those calls are
rewritten to their `*at()` equivalents (`shim_syscall_amd64.go`). The repository, `go.mod` and the
real module cache are not modified. Never ship this AAR; use `make aar` for releases.

```sh
tools/android-x86_64-emulator/build-aar.sh        # writes android/app/libs/clipbridge-core.aar
cd android && CB_VERSION_CODE=... ./gradlew :app:assembleDebug   # use scripts/version.sh for the code
```

Status: the patched core starts on an Android 15 x86_64 emulator (`core started` in logcat). The
build script is the working version of the approach; the first attempts (overlay, -modfile,
go.work) do not work with gomobile, which builds in its own temporary module.
