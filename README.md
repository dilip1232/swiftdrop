# SwiftDrop

**Blazing-fast file, folder & chat transfer across Mac, Windows & Android — at full LAN speed. Encrypted. No cloud.**

SwiftDrop lets you send files, folders, and messages between your devices instantly over your local network. No internet required, no file size limits, no cloud storage. Everything stays on your LAN, encrypted end-to-end.

---

## Features

- **Cross-Platform** — macOS, Windows, Android (all talk to each other)
- **Files & Folders** — drag-and-drop files or entire folders
- **Built-in Chat** — per-device messaging, no separate app needed
- **LAN Speed** — transfers run at your network's full speed (typically 100+ MB/s)
- **End-to-End Encrypted** — AES-256-GCM encryption for all transfers
- **SPAKE2 Pairing** — secure PIN-based pairing, PIN never crosses the wire
- **Pause & Resume** — pause transfers and resume where you left off
- **Receiver Consent** — accept or reject incoming files before they download
- **No Internet Required** — works entirely on your local network
- **Auto-Discovery** — finds devices via mDNS + LAN subnet scanning
- **Native Experience** — macOS menu bar app, Windows system tray, Android native
- **Headless Mode** — run without UI for server/automation use

## Architecture

```
swiftdrop/
├── core/       Shared Go module — discovery, transfers, encryption, chat, pairing
├── mac/        macOS menu-bar app (Wails v3)
├── windows/    Windows system tray app (Wails v3)
└── android/    Android app (Kotlin)
```

## Download

> Coming soon — check [Releases](https://github.com/dilip1232/swiftdrop/releases) for the latest builds.

## Building from Source

### Prerequisites
- **Go 1.25+** (for core, mac, windows)
- **JDK 17 + Android SDK** (for android)

### macOS
```bash
cd mac
go build -o swiftdrop .
# Or use the dev-deploy script for full .app bundle:
bash dev-deploy.sh
```

### Windows
```bash
cd windows
go build -ldflags "-s -w -H windowsgui" -o SwiftDrop.exe .
```

### Android
```bash
cd android
./gradlew assembleDebug
```

## Security

- **SPAKE2 PAKE** — PIN-based pairing where the PIN never leaves your device
- **AES-256-GCM** — all file transfers are encrypted end-to-end
- **HMAC Authentication** — every request is authenticated
- **Platform Keystore** — encryption keys stored in macOS Keychain / Windows DPAPI
- **Replay Protection** — nonce cache prevents replay attacks
- **Loopback-only UI** — web UI only accessible from localhost

## License

[MIT](LICENSE)
