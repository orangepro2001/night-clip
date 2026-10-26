#!/usr/bin/env bash
# Builds the three shell-side clipboard probes into dex files under tools/clipboard-probes/out/.
# Needs the Android SDK (build-tools d8, platforms/android-35). See README.md.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
sdk="${ANDROID_HOME:-$HOME/Android/Sdk}"
bt="$(ls -d "$sdk"/build-tools/* | sort | tail -1)"
aj="$sdk/platforms/android-35/android.jar"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$here/out"
for c in SetClip GetClip WatchClip; do
  mkdir -p "$work/classes" "$work/$c"
  javac -source 8 -target 8 -cp "$aj" -d "$work/classes" "$here/$c.java" 2>&1 | grep -v warning || true
  "$bt/d8" --release --lib "$aj" --output "$work/$c" "$work/classes/$c.class"
  cp "$work/$c/classes.dex" "$here/out/$c.dex"
done
echo "built: $(ls "$here/out")"
