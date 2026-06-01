# SwiftDrop — Android

A single-screen Android app for fast LAN file transfer, symmetric with the Mac
app: it both **serves** (receives) and **sends**. Discovers Macs/phones on the
network via mDNS, streams files over raw HTTP for max speed, and integrates
with the system **share sheet**. Received files land in **Downloads/SwiftDrop**.

No Android Studio required — build and install entirely from the terminal.

## Features

- **Device pairing** — PIN-based pairing before any file transfer; paired keys are persisted
- **Bilateral unpairing** — unpairing on one device notifies the other
- **Auto-close pairing dialog** — when the remote device confirms the PIN, the local dialog closes automatically
- **SHA-256 integrity verification** — sender hashes the file, receiver verifies after write; corrupted files are rejected and deleted
- **Dynamic notifications** — status bar icon changes based on transfer state (sending/receiving/idle)
- **Live transfer progress** — real-time progress bars and speed display
- **Open folder** — tap the folder icon next to a completed transfer to open Downloads
- **Cancel transfers** — cancel an in-flight send; disconnects the connection immediately
- **Share sheet integration** — share files into SwiftDrop from any app
- **Wake + WiFi locks** — CPU and WiFi radio stay active during transfers, even with screen off
- **Stall detection** — 30s read timeout detects dead peers
- **No file size cap** — transfers of any size; disk space checked before writing

## What it does

- **Receive:** runs a small HTTP server in a foreground service; files pushed by
  peers are streamed straight to `Downloads/SwiftDrop` via MediaStore (no
  storage permission needed). A notification fires on receipt.
- **Send:** pick files (or **Share → SwiftDrop** from any app), choose a device,
  hit Send. The app streams the file with a fixed-length HTTP body — no
  buffering — so transfers run at full LAN speed.
- **Discovery:** registers + browses `_swiftdrop._tcp` with the system
  NsdManager, so devices find each other automatically.

The UI is a WebView served by the in-app server (same origin, no CORS); file
picking uses the native SAF picker, bridged to the page.

## One-time toolchain setup (terminal only)

```bash
brew install --cask temurin@17          # or: brew install openjdk@17
brew install --cask android-commandlinetools
export ANDROID_HOME=/opt/homebrew/share/android-commandlinetools
yes | sdkmanager --licenses
sdkmanager "platform-tools" "platforms;android-34" "build-tools;34.0.0"
```

`local.properties` already points `sdk.dir` at the cmdline-tools location; edit
it if your SDK is elsewhere.

## Build

```bash
./build.sh
# → app/build/outputs/apk/debug/app-debug.apk  (~7 MB)
```

`build.sh` pins `JAVA_HOME` to JDK 17 (AGP 8.5 requires it) and uses the Gradle
8.7 wrapper.

## Install over Wi-Fi adb

On the phone: **Settings → Developer options → Wireless debugging → ON**.

```bash
# First time only — pair (tap "Pair device with pairing code" for IP:PORT + code)
./install.sh pair 192.168.1.42:37123      # then type the 6-digit code

# Then connect to the "IP & address" shown under Wireless debugging, and install
./install.sh 192.168.1.42:39000
```

Re-running `./install.sh <ip:port>` reinstalls after a rebuild. (adb lives at
`$ANDROID_HOME/platform-tools/adb`.)

## Using it

1. Launch SwiftDrop. Allow the notification prompt (so it can run in the
   background and alert you on receipt).
2. Devices on your Wi-Fi appear under **Devices**. Tap one to select.
3. Tap **Choose files** (or share files into SwiftDrop from another app), then
   **Send**.
4. To receive: just leave it running — pushed files arrive in
   `Downloads/SwiftDrop` with a notification.

Tap the device name in the header to rename this device.

> **Same network required.** Phone and the other device must be on the same
> Wi-Fi (or the phone's hotspot). Hotspot is often the fastest path.

## Project layout

| File | Role |
|------|------|
| `MainActivity.kt` | WebView UI, JS bridge, SAF picker, share-intent handling |
| `SwiftDropService.kt` | foreground service: hosts server + discovery, multicast lock |
| `HttpServer.kt` | NanoHTTPD: `/inbox`, `/api/me`, `/api/devices`, `/api/transfers`, `/api/send-path`, UI |
| `Discovery.kt` | mDNS register + browse via NsdManager |
| `Sender.kt` | streams a content URI to a peer's `/inbox` with progress |
| `State.kt` | identity, peer registry, transfer tracker |
| `assets/web/index.html` | the mobile UI |

Shares the exact HTTP contract with the Mac app, so Mac ↔ Android works both
directions.

## Roadmap

- Battery optimization exemption prompt on first launch
- Windows companion app
- Optional self-signed TLS with cached fingerprint
- Resume interrupted transfers via HTTP range
