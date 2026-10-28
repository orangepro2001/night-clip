#!/usr/bin/env bash
# Asks an update service the way an installed ClipBridge does: with the real core code
# (core/update.Client), the public key compiled into this checkout (core/update/keys.go),
# the app id and the platform names. It checks as if `--code` were installed, downloads the
# offered package completely (resume, size and SHA-256 checks included) and prints
#
#   <platform> <versionCode> <versionName> <size> <sha256> <path>
#
# one line per platform. Read-only: nothing is published and no token is sent.
#
#   scripts/probe-update.sh [-u URL] [-c INSTALLED_CODE] [-p android|windows|all] [-o DIR]
#
#   -u URL   update service base URL (default: CB_UPDATE_URL from release.local.env)
#   -c CODE  versionCode to pretend to have installed (default 1)
#   -p       platform(s) (default all)
#   -o DIR   keep the downloaded packages in DIR (default: a temp directory, deleted)
#
# Use it after a release or a key change, and BEFORE pointing already-installed apps at a new
# service: a shipped build whose key, URL, app id or platform name is wrong can never update
# itself again. Needs the Go toolchain; the first run compiles for a few seconds.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
if [ -f "$root/release.local.env" ]; then set -a; . "$root/release.local.env"; set +a; fi
url="${CB_UPDATE_URL:-}"; code=1; platform=all; out=""
while getopts "u:c:p:o:" opt; do
  case "$opt" in
    u) url="$OPTARG" ;;
    c) code="$OPTARG" ;;
    p) platform="$OPTARG" ;;
    o) out="$OPTARG" ;;
    *) exit 2 ;;
  esac
done
[ -n "$url" ] || { echo "no update URL: pass -u or set CB_UPDATE_URL in release.local.env" >&2; exit 2; }
case "$platform" in android|windows|all) ;; *) echo "-p must be android, windows or all" >&2; exit 2 ;; esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
[ -n "$out" ] || out="$work/packages"
mkdir -p "$out"

# A throwaway module next to nothing else, so the probe cannot pick up stray files: it imports
# the repository's core through replace directives (the same ones core/go.mod uses).
gover="$(awk '/^go /{print $2; exit}' "$root/core/go.mod")"
cat > "$work/go.mod" <<EOF
module probe

go $gover

require (
	nightclip/core v0.0.0
	relhub v0.0.0
)

replace nightclip/core => $root/core

replace relhub => $root/core/third_party/relhub

replace github.com/tobischo/gokeepasslib/v3 => $root/core/third_party/gokeepasslib
EOF
cat > "$work/main.go" <<'EOF'
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"nightclip/core/update"
)

func main() {
	base, err := update.ValidateBaseURL(os.Args[1])
	check(err)
	installed, err := strconv.ParseInt(os.Args[2], 10, 64)
	check(err)
	out := os.Args[4]
	platforms := []string{update.PlatformAndroid, update.PlatformWindows}
	if os.Args[3] != "all" {
		platforms = []string{os.Args[3]}
	}
	for _, p := range platforms {
		c := &update.Client{BaseURL: base, Platform: p, Keys: update.TrustedKeys(), UserAgent: "clipbridge-probe/1"}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		m, err := c.Check(ctx, installed)
		if err != nil {
			fail("%s: check: %v", p, err)
		}
		path, err := c.Download(ctx, m, out, nil)
		cancel()
		if err != nil {
			fail("%s: download: %v", p, err)
		}
		f, err := os.Open(path)
		check(err)
		h := sha256.New()
		_, err = io.Copy(h, f)
		check(err)
		f.Close()
		fmt.Printf("%s %d %s %d %s %s\n", p, m.VersionCode, m.VersionName, m.Size, hex.EncodeToString(h.Sum(nil)), path)
	}
}

func check(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "probe failed: "+format+"\n", args...)
	os.Exit(1)
}
EOF
(cd "$work" && GOFLAGS=-mod=mod go run . "$url" "$code" "$platform" "$out")
