# Changelog

## v0.1.0 — Initial Release

### Features
- **Full-window UI** with system tray integration — click the tray icon to show/hide
- **LAN device discovery** via subnet scanning (fallback for mDNS-blocked environments)
- **Cross-platform file transfer** — send and receive files to/from Mac, Android, and other Windows devices
- **Drag-and-drop** file sending — drop files onto the window to stage them
- **Native file picker** — browse and select files to send
- **Encrypted transfers** — AES-256-GCM encryption for paired devices
- **Pairing system** — secure device pairing with shared secret
- **Auto-reconnect** — keepalive prober maintains device list across restarts
- **System tray** — minimize to tray, right-click menu for quick actions
- **Embedded app icon** — custom blue SwiftDrop icon in taskbar, title bar, and exe

### Architecture
- Built on shared `swiftdrop-core` Go module for cross-platform logic
- Wails v3 for native Windows WebView2 UI
- Headless mode (`-headless`) for testing and CI
