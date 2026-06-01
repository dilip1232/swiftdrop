#!/usr/bin/env bash
# Install the debug APK to a phone over Wi-Fi adb.
#
# One-time on the phone: Settings > Developer options > Wireless debugging > ON.
# Tap "Pair device with pairing code" to get an IP:PORT + 6-digit code.
set -euo pipefail
cd "$(dirname "$0")"

ADB="${ANDROID_HOME:-/opt/homebrew/share/android-commandlinetools}/platform-tools/adb"
APK="app/build/outputs/apk/debug/app-debug.apk"

if [ ! -f "$APK" ]; then echo "Build first: ./build.sh"; exit 1; fi

if [ "${1:-}" = "pair" ]; then
    # ./install.sh pair 192.168.1.42:37123   (then enter the code)
    "$ADB" pair "$2"
    exit 0
fi

# ./install.sh 192.168.1.42:39000   (the "IP & Port" shown under Wireless debugging)
if [ -n "${1:-}" ]; then
    "$ADB" connect "$1"
fi

echo "Devices:"; "$ADB" devices
echo "Installing…"
"$ADB" install -r "$APK"
echo "✓ Installed. Launch SwiftDrop on the phone."
