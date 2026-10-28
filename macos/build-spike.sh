#!/usr/bin/env bash
# Builds the macOS feasibility spike entirely on Linux (docs/plan-macos.md §2.4):
#   Go core -> libclipbridge_core.dylib (zig as the C toolchain, stub .tbd files instead of the Apple SDK)
#   Avalonia app -> dotnet publish osx-arm64, assembled into ClipBridgeSpike.app
#   ad-hoc signature with rcodesign, zipped with symlinks and permissions kept.
# Needs: go, zig, dotnet, rcodesign, zip. Output: macos/out/ClipBridgeSpike.zip
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
out="$root/macos/out"
app="$out/ClipBridgeSpike.app"
stubs="$root/macos/sdk-stubs"
dotnet="$(command -v dotnet || echo "$HOME/.dotnet/dotnet")"
min_macos=13.0

rm -rf "$out" && mkdir -p "$out"

echo "== core dylib (darwin/arm64, macOS $min_macos+)"
(cd "$root/core" && CGO_ENABLED=1 GOOS=darwin GOARCH=arm64 \
  CC="zig cc -target aarch64-macos.$min_macos -L$stubs/usr/lib -F$stubs/System/Library/Frameworks" \
  go build -buildmode=c-shared -trimpath -ldflags="-s -w" -o "$out/core/libclipbridge_core.dylib" ./cmd/cbcore-dll)

echo "== avalonia app (osx-arm64, self-contained)"
DOTNET_CLI_TELEMETRY_OPTOUT=1 "$dotnet" publish "$root/macos/ClipBridge.MacSpike" -c Release -r osx-arm64 \
  --self-contained true -o "$out/publish" -v quiet

echo "== bundle"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp -a "$out/publish/." "$app/Contents/MacOS/"
cp "$out/core/libclipbridge_core.dylib" "$app/Contents/MacOS/"
cat > "$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>ClipBridge Spike</string>
  <key>CFBundleDisplayName</key><string>ClipBridge Spike</string>
  <key>CFBundleIdentifier</key><string>com.nightclip.clipbridge.spike</string>
  <key>CFBundleExecutable</key><string>ClipBridge.MacSpike</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.0.1</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>$min_macos</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSPrincipalClass</key><string>NSApplication</string>
</dict>
</plist>
EOF

echo "== ad-hoc signature"
rcodesign sign "$app" >/dev/null
rcodesign verify "$app/Contents/MacOS/ClipBridge.MacSpike" >/dev/null && echo "verify ok"

(cd "$out" && zip -qry ClipBridgeSpike.zip ClipBridgeSpike.app)
ls -la "$out/ClipBridgeSpike.zip"
