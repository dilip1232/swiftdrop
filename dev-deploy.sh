#!/usr/bin/env bash
# Dev deploy: build, sign, install to ~/Applications, fix firewall, and launch.
# Usage:  bash dev-deploy.sh
set -euo pipefail
cd "$(dirname "$0")"

INSTALL_DIR="$HOME/Applications"
APP="SwiftDrop.app"
BIN_PATH="$INSTALL_DIR/$APP/Contents/MacOS/SwiftDrop"
FW="/usr/libexec/ApplicationFirewall/socketfilterfw"

# 1. Kill existing instance
pkill -f SwiftDrop 2>/dev/null && sleep 1 || true

# 2. Build + sign
bash build-app.sh

# 3. Install
rm -rf "$INSTALL_DIR/$APP"
cp -R "$APP" "$INSTALL_DIR/$APP"

# 4. Firewall: remove stale rule, add + allow the new binary (single sudo)
sudo bash -c "\"$FW\" --remove \"$BIN_PATH\" 2>/dev/null; \"$FW\" --add \"$BIN_PATH\"; \"$FW\" --unblockapp \"$BIN_PATH\""

# 5. Launch
open "$INSTALL_DIR/$APP"
echo "✓ Deployed and launched $APP"
