#!/usr/bin/env bash
# Dev deploy: build, sign, install to ~/Applications, fix firewall, and launch.
# Usage:  bash dev-deploy.sh
set -euo pipefail
cd "$(dirname "$0")"

INSTALL_DIR="$HOME/Applications"
APP="SwiftDrop.app"
# 1. Kill existing instance
pkill -f SwiftDrop 2>/dev/null && sleep 1 || true

# 2. Build + sign
bash build-app.sh

# 3. Install
rm -rf "$INSTALL_DIR/$APP"
cp -R "$APP" "$INSTALL_DIR/$APP"

# 4. Launch
open "$INSTALL_DIR/$APP"
echo "✓ Deployed and launched $APP"
