#!/usr/bin/env bash
# Builds an x86_64-only AAR whose Go core survives Android's seccomp policy on an x86_64
# emulator. Production builds (`make aar`) are not touched: the patch goes into a throw-away copy
# of modernc.org/libc inside a temporary GOPATH. See README.md.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
cd "$repo/core"
libc_ver="$(go list -m -f '{{.Version}}' modernc.org/libc)"
libc_src="$(go list -m -f '{{.Dir}}' modernc.org/libc)"
real_gopath="$(go env GOPATH)"
real_mod="$(go env GOMODCACHE)"
work="$(mktemp -d)"
trap 'chmod -R u+w "$work" 2>/dev/null; rm -rf "$work"' EXIT

# gomobile builds in its own temporary module, so neither -modfile nor go.work reach it. What it
# does share is GOPATH / the module cache. Build a throw-away GOPATH whose module cache is the
# real one (symlinks) except for modernc.org/libc, which is a patched copy: every raw syscall in
# the two files that issue them goes through the shim. The real cache and the repository
# (go.mod/go.sum included) are not modified.
fake="$work/gopath"
mkdir -p "$fake/pkg/mod/modernc.org"
ln -s "$real_gopath/bin" "$fake/bin"
ln -s "$real_gopath/pkg/gomobile" "$fake/pkg/gomobile"
for e in "$real_mod"/*; do
  n="$(basename "$e")"
  [ "$n" = "modernc.org" ] || ln -s "$e" "$fake/pkg/mod/$n"
done
for m in "$real_mod"/modernc.org/*; do
  n="$(basename "$m")"
  [ "$n" = "libc@$libc_ver" ] || ln -s "$m" "$fake/pkg/mod/modernc.org/$n"
done
cp -r "$libc_src" "$fake/pkg/mod/modernc.org/libc@$libc_ver"
chmod -R u+w "$fake/pkg/mod/modernc.org/libc@$libc_ver"
patched="$fake/pkg/mod/modernc.org/libc@$libc_ver"
for f in syscall_musl.go libc_linux_amd64.go; do
  sed -i -e 's/unix\.Syscall6(/shimSyscall6(/g' -e 's/unix\.Syscall(/shimSyscall(/g' "$patched/$f"
done
cp "$here/shim_syscall_amd64.go" "$patched/zz_emulator_shim_amd64.go"
export GOPATH="$fake" GOMODCACHE="$fake/pkg/mod"
mkdir -p "$repo/android/app/libs"
gomobile bind -target=android/amd64 -androidapi "${ANDROIDAPI:-26}" \
  -javapkg "${JAVAPKG:-com.nightclip.core}" -o "$repo/android/app/libs/clipbridge-core.aar" ./mobile
echo "built x86_64-only AAR for emulator testing: android/app/libs/clipbridge-core.aar"
