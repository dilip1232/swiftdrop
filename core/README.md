# SwiftDrop Core

Shared Go module that powers all SwiftDrop platform apps (macOS, Windows, Android).
Contains the entire transfer engine, discovery stack, encryption layer, and HTTP
API — platform shells only add native UI wiring and system integration.

## Features

### Transfer Engine
- **Streaming HTTP transfers** — files are streamed directly over raw HTTP (`io.Copy`, 256 KB buffers, no buffering in memory) for full LAN speed on any file size
- **AES-256-GCM encryption** — all transfers between paired devices are encrypted end-to-end using chunked AES-256-GCM streaming; integrity is built into GCM
- **SHA-256 integrity verification** — unencrypted transfers include a SHA-256 hash; receiver verifies after write and rejects corrupted files
- **Disk space checks** — receiver checks available disk space before accepting a file; rejects if insufficient
- **Stall detection** — 30-second response header timeout detects dead peers
- **Cancel support** — in-flight sends can be canceled via API; the HTTP connection is closed immediately
- **Retry support** — failed/canceled outbound sends can be retried; file path and peer ID are preserved on the transfer
- **No file size cap** — transfers of any size work; no buffering, no temp files
- **Receiver consent** — incoming transfers require explicit accept/reject via `ConsentHook`; the remote peer is notified of the decision
- **Folder transfer** — send entire folders file-by-file with parallel streaming (up to 4 concurrent files); receiver reconstructs the original directory structure
- **Folder cancellation** — cancelling a folder mid-transfer notifies the receiver, which shows how many files were actually received
- **Duplicate name handling** — receiving a folder or file with a name that already exists automatically creates a unique copy (e.g. "Photos (1)") instead of overwriting
- **Pause/resume** — in-flight transfers can be paused and resumed; the remote peer is signaled so its UI reflects the paused state
- **HMAC authentication** — `/inbox` requests from paired devices include an HMAC signature; receiver verifies authenticity before writing

### Device Discovery
- **mDNS** — registers and browses `_swiftdrop._tcp` via `libp2p/zeroconf/v2` for automatic zero-config discovery
- **LAN subnet scan** — fallback `/24` scanner for environments where mDNS is unavailable (e.g., Windows where DNS Client occupies port 5353)
- **Network watcher** — restarts discovery when the machine's LAN IP changes (e.g., switching Wi-Fi networks), clearing stale peers from the old network
- **Keepalive prober** — periodically probes all known devices (persisted across restarts) and shows reachable ones; the device list is self-healing
- **Manual peers** — add devices by IP address; persisted and probed like discovered peers

### Pairing & Security
- **SPAKE2 PIN pairing** — 6-digit PIN exchange using SPAKE2 (Password-Authenticated Key Exchange) over P-256; PIN never crosses the wire; both devices derive a shared AES-256 key with mutual confirmation
- **Two-phase PAKE** — server holds exchange state without committing until the client proves it derived the correct key; wrong PINs are rejected cleanly (up to 3 retries before PIN is voided)
- **QR code pairing** — one device generates a QR code containing a one-time token (256-bit entropy); the other scans it to pair instantly
- **Bilateral unpairing** — unpairing on one device notifies the remote peer to also unpair
- **Persistent key store** — paired keys stored on disk (macOS: Keychain, Windows: DPAPI, others: `<UserConfigDir>/SwiftDrop/paired-keys.json`)
- **API token protection** — all UI-facing `/api/*` endpoints require a per-session token; only the embedded UI (loopback) can obtain it
- **HMAC replay protection** — `/inbox` requests include an HMAC signature with a timestamp; a replay cache rejects duplicates within the 5-minute window
- **Route separation** — peer-facing routes (LANHandler) are isolated from UI routes (Handler); headless mode only exposes peer-facing routes

### Chat
- **Per-device chat** — send and receive text messages with individual paired peers
- **In-memory history** — messages are kept per-peer in memory; UI polls for new messages
- **Notification hook** — incoming messages trigger a notification flag so the UI can alert the user

### Transfer Tracking
- **Live progress** — atomic byte counters updated during streaming; UI polls for real-time progress bars
- **Transfer history** — recent transfers (up to 50) are kept in memory so the UI shows history even after reconnect
- **Retryable flag** — failed outbound sends are marked retryable in the API response

### Platform Abstractions
- **Notifications** — `Notify()` with platform-specific implementations (macOS: `UNUserNotificationCenter` via cgo, Windows: PowerShell toast)
- **Consent dialog** — macOS consent alert floats above all windows and stays visible even when notifications arrive or the app loses focus
- **Open folder** — `OpenFolder()` opens the download directory (macOS: `open`, Windows: `explorer.exe`)
- **Disk free** — `DiskFree()` checks available space (macOS: `statfs`, Windows: `GetDiskFreeSpaceExW`)
- **Tray icon** — `TrayIcon()` generates a menu-bar/system-tray icon at runtime

## HTTP API

All endpoints are served on port **53317** TCP.

### Public (peer-facing)

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/inbox` | Receive a pushed file (headers: `X-Filename`, `X-From`, `X-From-ID`, `X-Encrypted`, `X-File-Size`, `X-SHA256`) |
| GET | `/api/me` | This device's identity (id, name, platform, port) |
| GET | `/health` | Health check (returns `ok`) |
| POST | `/api/peers/add` | Announce presence: `{"host":"ip:port"}` |
| POST | `/api/pair/claim` | SPAKE2 Phase 1: `{"pake_msg":"...","id":"...","name":"..."}` |
| POST | `/api/pair/pake-confirm` | SPAKE2 Phase 2: `{"pake_confirm":"...","id":"..."}` |
| POST | `/api/pair/qr-claim` | Claim a QR token: `{"token":"...","id":"...","name":"..."}` |
| POST | `/api/pair/remote-unpair` | Notify of remote unpair |

### Protected (UI-facing, requires API token)

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/devices` | List discovered + manual peers |
| POST | `/api/send` | Browser-upload send (multipart) |
| POST | `/api/send-path` | Native fast send: `{"to":"id","paths":["..."]}` |
| GET | `/api/transfers` | Live transfer progress |
| POST | `/api/transfers/clear` | Remove finished transfers |
| POST | `/api/transfers/cancel` | Cancel an in-flight send |
| POST | `/api/transfers/retry` | Retry a failed/canceled send |
| POST | `/api/pick` | Open native file dialog |
| POST | `/api/peers/remove` | Remove a peer: `{"id":"..."}` |
| POST | `/api/pair/begin` | Generate a pairing PIN |
| GET | `/api/pair/status` | Check if a device is paired |
| POST | `/api/pair/submit` | Submit PIN to a remote device |
| POST | `/api/pair/unpair` | Unpair a device (bilateral) |
| POST | `/api/pair/qr-begin` | Generate QR pairing code |
| POST | `/api/quit` | Quit the app |
| POST | `/api/open-folder` | Open the download directory |

## Module Layout

| File | Role |
|------|------|
| `server.go` | HTTP server, all API endpoints, file receiving (`/inbox`) |
| `transfer.go` | `SendFileByPath` — native streaming send with progress, encryption, hash |
| `transfers.go` | `Tracker` — live transfer state, history, retry metadata |
| `crypto.go` | AES-256-GCM streaming encryption/decryption, PIN/QR pairing, key store |
| `device.go` | `Identity`, `Peer`, `PeerRegistry` — device identity and peer management |
| `discovery.go` | mDNS register + browse via `libp2p/zeroconf/v2` |
| `lanscan.go` | LAN `/24` subnet scanner fallback |
| `keepalive.go` | Periodic prober for known devices |
| `netwatch.go` | Network change detector; restarts discovery on IP change |
| `notify_darwin.go` | macOS notifications via `NSUserNotification` (cgo) |
| `notify_windows.go` | Windows toast notifications via PowerShell |
| `diskfree_darwin.go` | macOS disk space check via `statfs` |
| `diskfree_windows.go` | Windows disk space check via `GetDiskFreeSpaceExW` |
| `openfolder_darwin.go` | macOS `open` command |
| `openfolder_windows.go` | Windows `explorer.exe` |
| `text.go` | Per-peer chat: message store, send/receive, notification flag |
| `keystore_darwin.go` | macOS Keychain key storage via `security` CLI |
| `keystore_windows.go` | Windows DPAPI-encrypted key storage |
| `keystore_other.go` | File-based key storage for Linux/Android |
| `icon.go` | Runtime-generated tray icon |

## Usage

This module is not used standalone — it's imported by platform apps:

```go
import core "swiftdrop-core"
```

Platform apps use a local `replace` directive during development:

```
replace swiftdrop-core => ../swiftdrop-core
```

CI workflows clone core as a sibling directory so the replace works in CI too.

## Security & Trust Model

SwiftDrop is a **LAN-only** file transfer tool. It is designed to be safe on networks you trust (home, office). It is not designed for hostile networks or use over the public internet.

### What SwiftDrop protects against
- **Unpaired senders** — files are only accepted from paired devices; unpaired requests are rejected
- **PIN interception** — PIN-based pairing uses SPAKE2 (a Password-Authenticated Key Exchange); the PIN never crosses the wire
- **Transfer eavesdropping** — all transfers between paired devices are encrypted with AES-256-GCM using the shared pairing key
- **Transfer tampering** — GCM provides authenticated encryption; tampered data is rejected
- **HMAC replay** — `/inbox` requests include a timestamped HMAC; a replay cache rejects duplicates
- **Token theft** — the API token is only served to loopback; LAN peers cannot obtain it

### What SwiftDrop does NOT protect against
- **Compromised device on the LAN** — if an attacker controls a device on your network, they can observe mDNS announcements and attempt to pair (but cannot succeed without the PIN or QR token)
- **Timing side-channels on SPAKE2** — the current implementation uses P-256 big.Int arithmetic which is not constant-time; this is a theoretical concern for an on-LAN attacker measuring pairing timing

### Cryptographic note
The SPAKE2 implementation is hand-rolled over Go's `crypto/elliptic` (P-256) and a matching Kotlin implementation for Android. It is the single primitive securing both PIN and QR pairing. The math is correct (asymmetric SPAKE2 with hash-to-curve, key derivation, and mutual confirmation MAC), but it has not been independently audited and uses big.Int arithmetic that is not constant-time. Before marketing the app as "secure," this implementation should be reviewed by an independent cryptographer or replaced with a maintained PAKE library.

## Privacy

SwiftDrop collects **no data**. There are no analytics, no telemetry, no cloud services, and no network requests outside your LAN. All transfers are direct device-to-device. Pairing keys are stored locally on each device and never leave it.
