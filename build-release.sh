#!/usr/bin/env bash
# Build the SwiftDrop release APK from the terminal (no Android Studio).
# Generates a signing keystore automatically if one doesn't exist.
set -euo pipefail
cd "$(dirname "$0")"

export JAVA_HOME="${JAVA_HOME:-/opt/homebrew/opt/openjdk@17}"
export ANDROID_HOME="${ANDROID_HOME:-/opt/homebrew/share/android-commandlinetools}"
export ANDROID_SDK_ROOT="$ANDROID_HOME"

KEYSTORE="release-keystore.jks"
KEYSTORE_PASS="swiftdrop123"
KEY_ALIAS="swiftdrop"

# ── 1. Generate a signing keystore if missing ──
if [ ! -f "$KEYSTORE" ]; then
    echo "› generating signing keystore…"
    keytool -genkeypair \
        -keystore "$KEYSTORE" \
        -alias "$KEY_ALIAS" \
        -keyalg RSA -keysize 2048 -validity 10000 \
        -storepass "$KEYSTORE_PASS" \
        -keypass "$KEYSTORE_PASS" \
        -dname "CN=SwiftDrop, OU=Dev, O=SwiftDrop, L=Unknown, ST=Unknown, C=US"
    echo "✓ Created $KEYSTORE"
fi

# ── 2. Build release APK ──
echo "› building release APK…"
./gradlew assembleRelease --no-daemon \
    -Pswiftdrop.storeFile="$(pwd)/$KEYSTORE" \
    -Pswiftdrop.storePassword="$KEYSTORE_PASS" \
    -Pswiftdrop.keyAlias="$KEY_ALIAS" \
    -Pswiftdrop.keyPassword="$KEYSTORE_PASS" \
    "$@"

APK="app/build/outputs/apk/release/app-release.apk"
echo
echo "✓ Release APK: $APK ($(du -h "$APK" | cut -f1))"
echo "Install with:  adb install -r $APK"
