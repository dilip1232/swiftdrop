#!/usr/bin/env bash
# Build the SwiftDrop debug APK from the terminal (no Android Studio).
# Pins JDK 17 (AGP 8.5 requires it) and the CLI SDK location.
set -euo pipefail
cd "$(dirname "$0")"

export JAVA_HOME="${JAVA_HOME:-/opt/homebrew/opt/openjdk@17}"
export ANDROID_HOME="${ANDROID_HOME:-/opt/homebrew/share/android-commandlinetools}"
export ANDROID_SDK_ROOT="$ANDROID_HOME"

./gradlew assembleDebug --no-daemon "$@"

echo
echo "✓ APK: app/build/outputs/apk/debug/app-debug.apk"
echo "Install over Wi-Fi adb with:  ./install.sh"
