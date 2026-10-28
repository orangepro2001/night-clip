#!/usr/bin/env bash
# Builds ClipBridge.app for macOS (Apple Silicon) entirely on Linux, and zips it for release-publish
# (docs/plan-macos.md §2.4, §6):
#   Go core  -> libclipbridge_core.dylib  (zig as the C toolchain; macos/sdk-stubs replace the Apple SDK)
#   shell    -> dotnet publish osx-arm64, self-contained, assembled into ClipBridge.app
#   signing  -> ad hoc with rcodesign (Apple Silicon refuses unsigned code)
#   package  -> core/dist/clipbridge-macos.zip (zip -y keeps symlinks and modes; ditto unpacks it on the Mac)
# Version and update URL come from the environment (make dist-macos sets them): CB_VERSION_CODE,
# CB_VERSION_NAME, CB_UPDATE_URL. Needs: go, zig, dotnet, rcodesign, zip.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
dist="$root/core/dist"
work="$dist/macos"
app="$work/ClipBridge.app"
stubs="$root/macos/sdk-stubs"
dotnet="${DOTNET:-$(command -v dotnet || echo "$HOME/.dotnet/dotnet")}"
min_macos=13.0
code="${CB_VERSION_CODE:-0}"
name="${CB_VERSION_NAME:-0.0.0-dev}"
url="${CB_UPDATE_URL:-}"

for tool in go zig rcodesign zip; do command -v "$tool" >/dev/null || { echo "$tool not found" >&2; exit 1; }; done
rm -rf "$work" && mkdir -p "$work"

echo "== core dylib (darwin/arm64, macOS $min_macos+)"
(cd "$root/core" && CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  CC="zig cc -target aarch64-macos.$min_macos -L$stubs/usr/lib -F$stubs/System/Library/Frameworks" \
  go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o "$work/core/libclipbridge_core.dylib" ./cmd/cbcore-dll)

echo "== shell (osx-arm64, $name / $code)"
DOTNET_CLI_TELEMETRY_OPTOUT=1 "$dotnet" publish "$root/macos/ClipBridge.Mac" -c Release -r osx-arm64 \
  --self-contained true -p:DebugType=None -p:DebugSymbols=false \
  -p:CbVersionCode="$code" -p:CbVersionName="$name" -p:CbUpdateUrl="$url" \
  -o "$work/publish" -v quiet

echo "== bundle"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp -a "$work/publish/." "$app/Contents/MacOS/"
cp "$work/core/libclipbridge_core.dylib" "$app/Contents/MacOS/"
cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>ClipBridge</string>
  <key>CFBundleDisplayName</key><string>ClipBridge</string>
  <key>CFBundleIdentifier</key><string>com.nightclip.clipbridge</string>
  <key>CFBundleExecutable</key><string>ClipBridge</string>
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

rm -f "$dist/clipbridge-macos.zip"
(cd "$work" && zip -qry "$dist/clipbridge-macos.zip" ClipBridge.app)
ls -l "$dist/clipbridge-macos.zip"
