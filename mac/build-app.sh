#!/usr/bin/env bash
# Build SwiftDrop.app — a proper macOS app bundle.
#
# A bundle (not a bare binary) is REQUIRED on modern macOS: the Local Network
# privacy prompt only appears for apps whose Info.plist declares
# NSLocalNetworkUsageDescription + NSBonjourServices. Without it, mDNS discovery
# and LAN transfers are silently blocked.
set -euo pipefail

cd "$(dirname "$0")"

APP="SwiftDrop.app"
BIN="swiftdrop"

echo "› building binary…"
go build -o "$BIN" .

echo "› assembling ${APP}…"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/SwiftDrop"
cp Info.plist "$APP/Contents/Info.plist"
cp AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"

# Ad-hoc code signature. Required so macOS grants a stable identity to the app
# for the Local Network permission (otherwise the grant resets every launch).
echo "› ad-hoc signing…"
codesign --force --deep --sign - "$APP"

echo
echo "✓ Built $APP"
echo
echo "Run it with:   open ./$APP"
echo "On first launch macOS will ask to allow Local Network access — click Allow."
echo "The ⇅ icon appears in your menu bar; click it → Open SwiftDrop."
