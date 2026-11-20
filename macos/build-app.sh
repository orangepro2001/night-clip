#!/usr/bin/env bash
# Builds MoonPassword.app for macOS (Apple Silicon) on Linux or Windows (Git Bash), and zips it for release-publish
# (docs/plan-macos.md §2.4, §6):
#   Go core  -> libmoonpassword_core.dylib  (zig as the C toolchain; macos/sdk-stubs replace the Apple SDK)
#   shell    -> dotnet publish osx-arm64, self-contained, assembled into MoonPassword.app
#   signing  -> ad hoc with rcodesign (Apple Silicon refuses unsigned code)
#   package  -> core/dist/moonpassword-macos.zip (zip -y keeps symlinks and modes; ditto unpacks it on the Mac;
#               without zip, scripts/zipapp.py marks the Mach-O files executable itself)
# Version and update URL come from the environment (make dist-macos sets them): CB_VERSION_CODE,
# CB_VERSION_NAME, CB_UPDATE_URL. Needs: go, zig, dotnet, rcodesign, and zip or python3.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
dist="$root/core/dist"
work="$dist/macos"
app="$work/MoonPassword.app"
stubs="$root/macos/sdk-stubs"
# zig on Windows does not understand Git Bash paths (/c/...) inside CC.
if command -v cygpath >/dev/null 2>&1; then stubs="$(cygpath -m "$stubs")"; fi
dotnet="${DOTNET:-$(command -v dotnet || echo "$HOME/.dotnet/dotnet")}"
min_macos=13.0
code="${CB_VERSION_CODE:-0}"
name="${CB_VERSION_NAME:-0.0.0-dev}"
url="${CB_UPDATE_URL:-}"

for tool in go zig rcodesign; do command -v "$tool" >/dev/null || { echo "$tool not found" >&2; exit 1; }; done
command -v zip >/dev/null || command -v python3 >/dev/null || { echo "zip or python3 needed" >&2; exit 1; }
rm -rf "$work" && mkdir -p "$work"

echo "== core dylib (darwin/arm64, macOS $min_macos+)"
zigcc="zig cc"
ldflags="-s -w"
if command -v cygpath >/dev/null 2>&1; then
  # On Windows, Go's darwin linker rewrites the dylib's UUID by renaming a temp file over the output
  # while the output is still open, which Windows refuses. -B none skips that step but makes Go pass
  # -Wl,-no_uuid, which zig's linker rejects; this wrapper drops that one argument (zig then writes
  # its own LC_UUID).
  mkdir -p "$work/zigcc"
  cat > "$work/zigcc/main.go" <<'GO'
package main

import (
	"os"
	"os/exec"
)

func main() {
	args := []string{"cc"}
	for _, a := range os.Args[1:] {
		if a != "-Wl,-no_uuid" {
			args = append(args, a)
		}
	}
	c := exec.Command("zig", args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			os.Exit(e.ExitCode())
		}
		os.Exit(1)
	}
}
GO
  (cd "$work/zigcc" && GOOS= GOARCH= CGO_ENABLED=0 GOFLAGS= go build -o zigcc.exe main.go)
  zigcc="$(cygpath -m "$work/zigcc/zigcc.exe")"
  ldflags="-s -w -B none"
fi
(cd "$root/core" && CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  CC="$zigcc -target aarch64-macos.$min_macos -L$stubs/usr/lib -F$stubs/System/Library/Frameworks" \
  go build -buildmode=c-shared -trimpath -ldflags="$ldflags" -o "$work/core/libmoonpassword_core.dylib" ./cmd/cbcore-dll)

echo "== shell (osx-arm64, $name / $code)"
DOTNET_CLI_TELEMETRY_OPTOUT=1 "$dotnet" publish "$root/macos/MoonPassword.Mac" -c Release -r osx-arm64 \
  --self-contained true -p:DebugType=None -p:DebugSymbols=false \
  -p:CbVersionCode="$code" -p:CbVersionName="$name" -p:CbUpdateUrl="$url" \
  -o "$work/publish" -v quiet

echo "== bundle"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp -a "$work/publish/." "$app/Contents/MacOS/"
cp "$work/core/libmoonpassword_core.dylib" "$app/Contents/MacOS/"
cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>MoonPassword</string>
  <key>CFBundleDisplayName</key><string>MoonPassword</string>
  <key>CFBundleIdentifier</key><string>cloud.moonl.password</string>
  <key>CFBundleExecutable</key><string>MoonPassword</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$name</string>
  <key>CFBundleVersion</key><string>$code</string>
  <key>LSMinimumSystemVersion</key><string>$min_macos</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.utilities</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSPrincipalClass</key><string>NSApplication</string>
</dict>
</plist>
EOF

echo "== ad-hoc signature"
# rcodesign warns about the managed .dll files in Contents/MacOS (not Mach-O, so not sealed); the app
# runs, but `codesign --verify --deep --strict` on the Mac reports them (docs/plan-macos.md §2.4).
rcodesign sign "$app" >"$work/sign.log" 2>&1 || { cat "$work/sign.log" >&2; exit 1; }

rm -f "$dist/moonpassword-macos.zip"
if command -v zip >/dev/null; then
  (cd "$work" && zip -qry "$dist/moonpassword-macos.zip" MoonPassword.app)
else
  python3 "$root/scripts/zipapp.py" "$work" MoonPassword.app "$dist/moonpassword-macos.zip"
fi
ls -l "$dist/moonpassword-macos.zip"
