#!/usr/bin/env bash
# Build SwiftDrop.app and package it into a distributable DMG.
set -euo pipefail

cd "$(dirname "$0")"

APP="SwiftDrop.app"
DMG="SwiftDrop.dmg"
DMG_TITLE="SwiftDrop"
STAGING_DIR=".dmg-staging"

# ── 1. Build the .app bundle ──
echo "› building binary…"
go build -o swiftdrop .

echo "› assembling ${APP}…"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp swiftdrop "$APP/Contents/MacOS/SwiftDrop"
cp Info.plist "$APP/Contents/Info.plist"
[ -f AppIcon.icns ] && cp AppIcon.icns "$APP/Contents/Resources/AppIcon.icns"

echo "› ad-hoc signing…"
codesign --force --deep --sign - "$APP"

# ── 2. Create the DMG ──
echo "› creating ${DMG}…"
rm -rf "$STAGING_DIR" "$DMG"
mkdir -p "$STAGING_DIR"
cp -R "$APP" "$STAGING_DIR/"

# Create a symbolic link to /Applications for drag-install
ln -s /Applications "$STAGING_DIR/Applications"

hdiutil create \
    -volname "$DMG_TITLE" \
    -srcfolder "$STAGING_DIR" \
    -ov \
    -format UDZO \
    "$DMG"

rm -rf "$STAGING_DIR"

echo
echo "✓ Built $DMG ($(du -h "$DMG" | cut -f1))"
echo
echo "Distribute this file. Users open it and drag SwiftDrop to Applications."
