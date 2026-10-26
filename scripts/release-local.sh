#!/usr/bin/env bash
# Builds the release packages and publishes them to the local release directory that
# `cbdist serve` serves, so installed apps pick them up (docs/update.md).
#
#   scripts/release-local.sh [--platform android|windows|all] [--notes TEXT] [--force]
#                            [--no-build] [--allow-dirty]
#
#   --force        also raise minVersionCode: older apps must update (mandatory update)
#   --allow-dirty  publish from a tree with uncommitted changes (normally refused: the
#                  version number is the commit count, so the build must match a commit)
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
if [ -f release.local.env ]; then set -a; . ./release.local.env; set +a; fi

platform=all; notes=""; force=""; build=1; dirty=0
while [ $# -gt 0 ]; do
  case "$1" in
    --platform) platform="${2:?--platform needs a value}"; shift ;;
    --notes) notes="${2:?--notes needs a value}"; shift ;;
    --force) force="-force" ;;
    --no-build) build=0 ;;
    --allow-dirty) dirty=1 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
case "$platform" in android|windows|all) ;; *) echo "--platform must be android, windows or all" >&2; exit 2 ;; esac

ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
releases="${CLIPBRIDGE_RELEASES:-$HOME/.local/share/clipbridge/releases}"
key="${CLIPBRIDGE_RELEASE_KEY:-$HOME/.config/clipbridge/release-ed25519.key}"
[ -f "$key" ] || { echo "release signing key not found: $key (cbdist keygen -key ...)" >&2; exit 1; }
[ -n "${CB_UPDATE_URL:-}" ] || { echo "CB_UPDATE_URL is empty: copy release.local.env.example to release.local.env" >&2; exit 1; }

if [ "$dirty" = 0 ] && [ -n "$(git status --porcelain)" ]; then
  echo "working tree has uncommitted changes; commit first (the version is the commit count) or pass --allow-dirty" >&2
  exit 1
fi

eval "$(scripts/version.sh)"
echo "release $CB_VERSION_NAME (code $CB_VERSION_CODE) -> $releases"

make cbdist >/dev/null
cbdist="$root/core/dist/host/cbdist"

publish() { # platform file
  "$cbdist" publish -dir "$releases" -key "$key" -platform "$1" -file "$2" \
    -code "$CB_VERSION_CODE" -name "$CB_VERSION_NAME" -notes "$notes" $force
}

if [ "$platform" = android ] || [ "$platform" = all ]; then
  [ "$build" = 1 ] && make dist-android
  apk="$root/android/app/build/outputs/apk/release/app-release.apk"
  [ -f "$apk" ] || { echo "APK not found: $apk" >&2; exit 1; }

  # Installed apps only accept an update signed with the same key: pin the signer.
  apksigner="$(ls -d "$ANDROID_HOME"/build-tools/*/apksigner 2>/dev/null | sort -V | tail -1)"
  [ -n "$apksigner" ] || { echo "apksigner not found under \$ANDROID_HOME/build-tools" >&2; exit 1; }
  cert="$("$apksigner" verify --print-certs "$apk" | sed -n 's/.*certificate SHA-256 digest: //p' | head -1)"
  [ -n "$cert" ] || { echo "APK signature does not verify" >&2; exit 1; }
  mkdir -p "$releases"
  pinned="$releases/android-signer.sha256"
  if [ -f "$pinned" ] && [ "$(cat "$pinned")" != "$cert" ]; then
    echo "APK signer changed (pinned $(cat "$pinned")): installed apps would refuse this update" >&2
    exit 1
  fi
  publish android "$apk"
  echo "$cert" > "$pinned"
fi

if [ "$platform" = windows ] || [ "$platform" = all ]; then
  [ "$build" = 1 ] && make dist-windows
  zip="$root/core/dist/clipbridge-windows.zip"
  [ -f "$zip" ] || { echo "package not found: $zip" >&2; exit 1; }
  publish windows "$zip"
fi

# Keep the installed server in step with this checkout (new endpoints ship with the code).
if [ -x "$HOME/.local/bin/cbdist" ] && ! cmp -s "$cbdist" "$HOME/.local/bin/cbdist"; then
  install -m 755 "$cbdist" "$HOME/.local/bin/cbdist"
  systemctl --user try-restart clipbridge-dist.service 2>/dev/null || true
  sleep 1
fi

for p in android windows; do
  if [ "$platform" = all ] || [ "$platform" = "$p" ]; then "$cbdist" verify -dir "$releases" -platform "$p"; fi
done
