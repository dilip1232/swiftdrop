# SwiftDrop — Windows

Windows desktop app for fast peer-to-peer LAN file transfers. Sends files
to/from Android, macOS, and other Windows machines running SwiftDrop. Built with
[Wails v3](https://wails.io/) (WebView2) wrapping the shared Go core.

Symmetric: every device both **serves** (`/inbox`) and **sends**. Received
files land in `Downloads\SwiftDrop\`.

## Features

- **AES-256-GCM encryption** — all transfers between paired devices are encrypted end-to-end
- **Device pairing** — PIN-based and QR code pairing; paired keys are persisted across restarts
- **Bilateral unpairing** — unpairing on one device notifies the other
- **Auto-close pairing dialog** — when the remote device confirms the PIN, the local dialog closes automatically
- **SHA-256 integrity verification** — sender hashes the file, receiver verifies after write; corrupted files are rejected and deleted
- **Live transfer progress** — real-time progress bars with transferred data (MB/GB), percentage, and speed
- **Retry failed transfers** — retry button on failed/canceled outbound sends
- **Open folder** — click the folder icon next to a completed transfer to open `Downloads\SwiftDrop\` in Explorer
- **Native Windows toast notifications** — transfer notifications via PowerShell
- **Cancel transfers** — cancel an in-flight send from the UI; connection is closed immediately
- **Stall detection** — 30s response header timeout detects dead peers
- **No file size cap** — transfers of any size; disk space checked before writing
- **Drag-and-drop** — drop files directly onto the window
- **LAN subnet scan** — fallback discovery for networks where mDNS is unavailable (common on Windows)
- **System tray** — app lives in the system tray; click the icon to show/hide the window

## Architecture

This is a **platform shell** that imports `swiftdrop-core` (shared Go module)
and adds Windows-specific wiring:

- **Full-window app** that lives in the system tray
- Click the tray icon → show/hide the window
- Native Windows toast notifications (PowerShell)
- `GetDiskFreeSpaceExW` for disk-space checks
- `explorer.exe` to open the download folder

## Build

```powershell
# On a Windows machine (or GitHub Actions windows-latest):
cd swiftdrop-windows
go mod tidy
.\build-windows.ps1
```

Or cross-compile from macOS/Linux (GUI won't work, but binary is produced):

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-s -w -H windowsgui" -o SwiftDrop.exe .
```

## Headless mode

Run the server + discovery without any UI (useful for CI/testing):

```
SwiftDrop.exe -headless
```

## Requirements

- Windows 10 (version 1809+) or Windows 11
- WebView2 Runtime (pre-installed on Win10 21H2+ and Win11)
- LAN connectivity (same network as other SwiftDrop devices)

## Shared Core

The transfer engine, discovery, encryption, and HTTP API all live in
[swiftdrop-core](https://github.com/dilip1232/swiftdrop-core) — a shared Go
module imported by all platform apps. See the core README for full API docs.

## Roadmap

- Optional self-signed TLS with cached fingerprint
- Resume interrupted transfers via HTTP range
