#!/usr/bin/env bash
# Builds the release packages and publishes them to the local release-publish service
# (`relhub`, https://<host>/release), which signs the manifests and serves the update API
# to the installed apps (docs/update.md, docs/release-publish-integration.md).
#
#   scripts/release-local.sh [--platform android|windows|all] [--notes TEXT] [--force]
#                            [--no-build] [--allow-dirty] [--check]
#
#   --check        only run the checks that come before building (service reachable, signing
#                  key trusted by this build, versionCode free) and stop
#   --force        also raise minVersionCode: older apps must update (mandatory update)
#   --allow-dirty  publish from a tree with uncommitted changes (normally refused: the
#                  version number is the commit count, so the build must match a commit)
#
# Publishing is not undoable (a versionCode is used up for good; `relhub yank` withdraws a
# release but the number stays taken). So this script checks everything it can BEFORE it
# builds or uploads, and verifies the result the way an installed app will see it.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
if [ -f release.local.env ]; then set -a; . ./release.local.env; set +a; fi

platform=all; notes=""; force=""; build=1; dirty=0; check=0
while [ $# -gt 0 ]; do
  case "$1" in
    --platform) platform="${2:?--platform needs a value}"; shift ;;
    --notes) notes="${2:?--notes needs a value}"; shift ;;
    --force) force="-force" ;;
    --no-build) build=0 ;;
    --allow-dirty) dirty=1 ;;
    --check) check=1 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
case "$platform" in android|windows|all) ;; *) echo "--platform must be android, windows or all" >&2; exit 2 ;; esac

die() { echo "$*" >&2; exit 1; }

ANDROID_HOME="${ANDROID_HOME:-$HOME/Android/Sdk}"
app=clipbridge
relhub="${RELHUB_BIN:-$(command -v relhub || echo "$HOME/.local/bin/relhub")}"
[ -x "$relhub" ] || die "relhub not found (install release-publish: ~/dev/release-publish/deploy/install.sh)"
[ -n "${CB_UPDATE_URL:-}" ] || die "CB_UPDATE_URL is empty: copy release.local.env.example to release.local.env"

if [ "$dirty" = 0 ] && [ -n "$(git status --porcelain)" ]; then
  die "working tree has uncommitted changes; commit first (the version is the commit count) or pass --allow-dirty"
fi

# --- 1. can the apps in this build update from this service? ---------------------------
# The service signs with one key and every app has the public key compiled in
# (core/update/keys.go). If they differ, the build would reject every later update and
# could only be fixed by reinstalling by hand: refuse before anything is built.
trusted="$(sed -n 's/^[[:space:]]*"\([A-Za-z0-9+\/]\{43\}=\)".*/\1/p' core/update/keys.go)"
[ -n "$trusted" ] || die "no trusted keys found in core/update/keys.go"
info="$(curl -fsS --max-time 15 "${CB_UPDATE_URL%/}/v1/info")" \
  || die "cannot reach the update service at $CB_UPDATE_URL (tailscale serve /release -> 127.0.0.1:18790; is the release-publish service up? see its docs)"
active="$(printf '%s' "$info" | python3 -c 'import json,sys; print("\n".join(k["publicKey"] for k in json.load(sys.stdin)["keys"] if k["active"]))')"
[ -n "$active" ] || die "the service at $CB_UPDATE_URL reports no active signing key"
grep -qxF "$active" <<<"$trusted" \
  || die "the service signs with $active, which core/update/keys.go does not trust: apps built now could never update again"
pinned_keys="$(paste -sd, <<<"$trusted")"

eval "$(scripts/version.sh)"
echo "release $CB_VERSION_NAME (code $CB_VERSION_CODE) -> $CB_UPDATE_URL (app $app)"

# A platform's versionCode only goes up; learn now if this one is taken instead of after a
# 5-minute build. (The local CLI talks to 127.0.0.1:18790 with the admin token.)
for p in android windows; do
  if [ "$platform" = all ] || [ "$platform" = "$p" ]; then
    if out="$("$relhub" list -all -app "$app" -platform "$p" 2>/dev/null)" \
      && awk -v c="$CB_VERSION_CODE" 'NR>1 && $1+0>=c {f=1} END{exit !f}' <<<"$out"; then
      die "$p already has versionCode $CB_VERSION_CODE or higher in the service: commit first (the version is the commit count)"
    fi
  fi
done

if [ "$check" = 1 ]; then
  echo "preflight ok: the service's signing key is trusted by this build and versionCode $CB_VERSION_CODE is free"
  exit 0
fi

publish() { # platform file
  "$relhub" publish -app "$app" -platform "$1" -file "$2" \
    -version "$CB_VERSION_NAME" -code "$CB_VERSION_CODE" -notes "$notes" $force
}

# --- 2. build + publish -----------------------------------------------------------------
if [ "$platform" = android ] || [ "$platform" = all ]; then
  [ "$build" = 1 ] && make dist-android
  apk="$root/android/app/build/outputs/apk/release/app-release.apk"
  [ -f "$apk" ] || die "APK not found: $apk"

  # Installed apps only accept an update signed with the same key: pin the signer.
  apksigner="$(ls -d "$ANDROID_HOME"/build-tools/*/apksigner 2>/dev/null | sort -V | tail -1)"
  [ -n "$apksigner" ] || die "apksigner not found under \$ANDROID_HOME/build-tools"
  cert="$("$apksigner" verify --print-certs "$apk" | sed -n 's/.*certificate SHA-256 digest: //p' | head -1)"
  [ -n "$cert" ] || die "APK signature does not verify"
  pin="${CLIPBRIDGE_SIGNER_PIN:-$HOME/.config/clipbridge/android-signer.sha256}"
  legacy_pin="$HOME/.local/share/clipbridge/releases/android-signer.sha256" # where the cbdist flow kept it
  if [ ! -f "$pin" ] && [ -f "$legacy_pin" ]; then cp "$legacy_pin" "$pin"; fi
  if [ -f "$pin" ] && [ "$(cat "$pin")" != "$cert" ]; then
    die "APK signer changed (pinned $(cat "$pin")): installed apps would refuse this update"
  fi

  # release-publish needs the Android versionCode to be the APK's own (the system installer
  # compares them); a mismatch would offer an update that can never install.
  aapt2="$(ls -d "$ANDROID_HOME"/build-tools/*/aapt2 2>/dev/null | sort -V | tail -1)"
  if [ -n "$aapt2" ]; then
    apk_code="$("$aapt2" dump badging "$apk" | sed -n "s/^package: .*versionCode='\([0-9]*\)'.*/\1/p")"
    [ "$apk_code" = "$CB_VERSION_CODE" ] || die "the APK says versionCode '$apk_code', this release is $CB_VERSION_CODE"
  fi
  publish android "$apk"
  mkdir -p "$(dirname "$pin")"
  echo "$cert" > "$pin"
fi

if [ "$platform" = windows ] || [ "$platform" = all ]; then
  [ "$build" = 1 ] && make dist-windows
  zip="$root/core/dist/clipbridge-windows.zip"
  [ -f "$zip" ] || die "package not found: $zip"
  publish windows "$zip"
fi

# --- 3. verify as an installed app would ------------------------------------------------
# scripts/probe-update.sh runs the real core update client (compiled-in key, app id, platform
# names) against the public URL as if the previous version were installed, and downloads the
# whole package; here we only compare it with what was built.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
for p in android windows; do
  if [ "$platform" = all ] || [ "$platform" = "$p" ]; then
    src=$([ "$p" = android ] && echo "$root/android/app/build/outputs/apk/release/app-release.apk" || echo "$root/core/dist/clipbridge-windows.zip")
    read -r _ got_code _ got_size got_sha _ < <(scripts/probe-update.sh -u "$CB_UPDATE_URL" -c "$((CB_VERSION_CODE - 1))" -p "$p" -o "$tmp") \
      || die "$p: an installed app would not accept what the service offers (see the probe output above)"
    [ "$got_code" = "$CB_VERSION_CODE" ] || die "$p: the service offers versionCode $got_code, expected $CB_VERSION_CODE"
    [ "$got_sha" = "$(sha256sum "$src" | cut -d' ' -f1)" ] || die "$p: what the service offers differs from what was built"
    echo "verified $p: $CB_UPDATE_URL offers $CB_VERSION_NAME ($got_size bytes); the core client accepted the signature and package"
  fi
done
