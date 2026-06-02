# SwiftDrop — Mac

A menu-bar app for fast, one-click LAN file transfer. The whole UI lives in a
**popover drawer** that drops from the ⇅ menu-bar icon — no Dock icon, no
separate window, no browser. Built with Wails v3 (native WebKit webview, not
Chromium) wrapping a Go core that does raw HTTP streaming.

Symmetric: every device both **serves** (`/inbox`) and **sends**. Received
files land in `~/Downloads/SwiftDrop/`.

## Features

- **AES-256-GCM encryption** — all transfers between paired devices are encrypted end-to-end
- **Device pairing** — PIN-based and QR code pairing; paired keys are persisted across restarts
- **Bilateral unpairing** — unpairing on one device notifies the other
- **Auto-close pairing dialog** — when the remote device confirms the PIN, the local dialog closes automatically
- **SHA-256 integrity verification** — sender hashes the file, receiver verifies after write; corrupted files are rejected and deleted
- **Live transfer progress** — real-time progress bars with transferred data (MB/GB), percentage, and speed; survives closing the drawer
- **Retry failed transfers** — retry button on failed/canceled outbound sends
- **Open folder** — click the folder icon next to a completed transfer to open `~/Downloads/SwiftDrop/` in Finder
- **Native macOS notifications** — transfer notifications show the SwiftDrop app icon (not Script Editor)
- **Cancel transfers** — cancel an in-flight send from the UI; connection is closed immediately
- **Stall detection** — 30s response header timeout detects dead peers
- **Rounded corners** — Lunar-style compact UI with CSS clip-path rounded corners and transparent window background
- **File picker re-shows window** — the drawer reappears after the native file picker closes
- **No file size cap** — transfers of any size; disk space checked before writing
- **Drag-and-drop** — drop files directly onto the drawer
- **LAN subnet scan** — fallback discovery for networks where mDNS is unavailable

## Speed: no compromise

Sending is a **pure Go path** — drag-and-drop and the native file picker hand
Go the real file path, so the Go process opens the file and streams it straight
to the peer's `/inbox` (`io.Copy`, compression off, 256 KB buffers, no overall
timeout). The browser is never in the transfer path and nothing is buffered in
memory, so even multi-GB files run at full LAN speed. Receiving is the same
tight `io.Copy(file, socket)`.

Because the transfer engine is the Go process behind the menu bar (not the UI),
**transfers keep running even if you close the drawer** — they're goroutines.
Reopen the drawer and live progress is still there.

## Build & run

```bash
brew install go            # if you don't have it
go install github.com/wailsapp/wails/v3/cmd/wails3@latest   # for `wails3 doctor` (optional)
./build-app.sh             # builds & signs SwiftDrop.app
open ./SwiftDrop.app
```

On first launch macOS asks to **allow Local Network access** — click **Allow**.
This is required: without it, discovery and LAN transfers are silently blocked
on macOS 15+. The ⇅ icon then appears in your menu bar — click it to toggle the
drawer.

> **Why a `.app` and not just `go run .`?** The Local Network permission prompt
> only appears for a bundled, signed app whose `Info.plist` declares
> `NSLocalNetworkUsageDescription` + `NSBonjourServices`. A bare binary can't get
> LAN access on modern macOS. (`go run .` still works over loopback for UI
> iteration.)

## Using it

1. Click the ⇅ icon → the drawer drops down.
2. Devices on your network appear on the right automatically.
   - Not showing? Type its IP in **Add by IP** (e.g. `192.168.1.5`) and hit ＋.
3. Pick a device, then **drop files on the drawer** or click the drop zone to
   choose. Press **Send**.
4. The other device receives them in `~/Downloads/SwiftDrop/` with a
   notification. Progress bars update live (and survive closing the drawer).

## How it's built

| File | Role |
|------|------|
| `main.go` | Wails app: systray + frameless popover, native picker + drop, server/discovery startup |
| `server.go` | HTTP endpoints: `/inbox`, `/api/*`; also backs the webview via Wails asset handler |
| `transfer.go` | `sendFileByPath` (native streaming send), progress counting reader, notifications |
| `transfers.go` | live transfer tracker the UI polls |
| `discovery.go` | mDNS register + browse (`libp2p/zeroconf/v2`) |
| `device.go` | identity, peer registry (mDNS + manual) |
| `icon.go` | menu-bar template icon (generated at runtime) |
| `web/index.html` | the drawer UI |

The same Go `http.Handler` serves two transports: the LAN port `:53317`
(peer-facing `/inbox`, `/api/me`) and the Wails webview (UI + `/api/*`).

### Flags (dev/testing)

```
-port N        serve on a different port (default 53317)
-name NAME     override device name
-id ID         override device id (run several instances on one host)
-headless      run server + discovery with no UI (for automated testing)
```

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| POST/PUT | `/inbox` | receive a pushed file (`X-Filename`, `X-From` headers) |
| GET | `/api/me` | this device's identity |
| GET | `/api/devices` | discovered + manual peers |
| POST | `/api/send-path` | native fast send: `{"to":id,"paths":[...]}` |
| GET | `/api/transfers` | live transfer progress |
| POST | `/api/pick` | open the native file dialog, returns staged files |
| POST | `/api/peers/add` | add a peer by `{"host":"ip[:port]"}` |
| POST | `/api/peers/remove` | drop a manual peer by `{"id":"..."}` |
| GET | `/` | the drawer UI |

## Shared Core

The transfer engine, discovery, encryption, and HTTP API all live in
[swiftdrop-core](https://github.com/dilip1232/swiftdrop-core) — a shared Go
module imported by all platform apps. See the core README for full API docs.

## Roadmap

- Optional self-signed TLS with cached fingerprint
- Resume interrupted transfers via HTTP range
