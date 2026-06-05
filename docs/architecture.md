# SwiftDrop Architecture

A deep dive into SwiftDrop's design decisions — what we chose, what we rejected, and why.

---

## 1. Overview

SwiftDrop is a **peer-to-peer LAN file transfer app**. No cloud server in the middle. Devices find each other on the local network, pair securely, then send files directly at full network speed.

```
┌──────────┐       LAN (Wi-Fi / Ethernet)       ┌──────────┐
│  MacBook  │ ──── HTTP POST /inbox ────────────→│  Android  │
│  (sender) │     encrypted file stream           │(receiver) │
└──────────┘                                      └──────────┘
     ↑                                                  ↑
     │  mDNS: "I'm SwiftDrop, ID=abc, port=53317"      │
     └──────────────────────────────────────────────────┘
```

---

## 2. Device Discovery — mDNS + LAN Scan Fallback

When you open SwiftDrop, it automatically finds other SwiftDrop devices on your network. No manual IP entry needed.

**How it works:**
1. **mDNS (Multicast DNS)** — each device broadcasts a service announcement: "I'm a SwiftDrop device, my ID is X, I'm on port 53317." Other devices listen for these broadcasts and add peers to their list.
2. **LAN Subnet Scan (fallback)** — on Windows, mDNS often doesn't work because the DNS Client service occupies port 5353. So SwiftDrop also scans every IP on the local /24 subnet, does a fast TCP dial check (200ms timeout), and probes responsive hosts via `/api/me`.
3. **Keepalive Prober** — every 3 seconds, probes all *known* devices to confirm they're still reachable. If a device goes offline, it disappears. If it comes back, it reappears automatically.
4. **Network Watcher** — monitors the local IP. When you switch Wi-Fi networks, it restarts discovery so stale peers from the old network are cleared.

### Why mDNS?

| Alternative | Why we didn't use it |
|---|---|
| **Bluetooth discovery** | Short range (10m), slow data transfer, complex pairing. SwiftDrop is a LAN-speed app. |
| **Central server** | Requires internet. SwiftDrop works offline, on an airplane, on a private network. |
| **Manual IP entry only** | Users don't know their device's IP. mDNS makes it zero-config. |
| **UDP broadcast** | Works but mDNS is a well-defined standard (RFC 6762) with libraries on every platform. We use `libp2p/zeroconf` which handles SO_REUSEPORT and interoperates with macOS's mDNSResponder. |

---

## 3. Device Pairing — SPAKE2 (Zero-Knowledge Proof)

Before two devices can exchange files, they must "pair" — like Bluetooth pairing. One device shows a 6-digit PIN, the other types it in.

**The problem:** We can't just send the PIN over the network — anyone sniffing Wi-Fi packets would see it and could impersonate a device.

**How SPAKE2 works (simplified):**

```
Device A (shows PIN "482901")          Device B (user types "482901")
                                       
1. Generate random secret x            1. Generate random secret y
2. Compute: msgA = x·G + PIN·M         2. Compute: msgB = y·G + PIN·N
3. Send msgA ─────────────────────────→ 
4.                    ←──────────────── Send msgB
5. Both compute: shared_key = f(x, y, PIN)
6. Verify: both sides prove they got the same key (confirmation MAC)
7. Wrap the AES pairing key with the shared_key and send it
```

`msgA` and `msgB` look like random garbage to an eavesdropper. Even if someone captures both messages, they **cannot** recover the PIN or the shared key — the math makes it impossible without knowing the PIN.

### Why SPAKE2?

| Alternative | Why we didn't use it |
|---|---|
| **Send PIN in plaintext** | Anyone on the same Wi-Fi can sniff it. |
| **Send PIN over HTTPS/TLS** | No trusted CA on a local network with no internet. Self-signed certs are vulnerable to MITM. |
| **Diffie-Hellman (no PIN)** | DH gives encryption but no *authentication*. A man-in-the-middle could intercept both sides. The PIN proves you're talking to the right device. |
| **SRP (Secure Remote Password)** | SRP is designed for client-server (one side stores a password hash). SPAKE2 is symmetric — better fit for peer-to-peer. |

### Brute-force protection
- PIN expires after 60 seconds
- 3 wrong attempts → PIN is invalidated, must generate a new one
- QR tokens are 256-bit random (not brute-forceable)

After pairing, both devices store a shared 32-byte AES key. Every future transfer uses this key — no re-pairing needed.

---

## 4. File Encryption — AES-256-GCM (Streaming)

Every file transfer between paired devices is encrypted. Even on your own Wi-Fi, nobody else on the network can read the file contents.

### Stream format

```
[12-byte base nonce]
[4-byte chunk length][encrypted chunk + 16-byte GCM tag] ...
[0x00000000 end marker]
```

Each chunk: up to 256 KB of plaintext, encrypted with `nonce = baseNonce XOR chunkIndex`. The chunk index is bound as AAD (Additional Authenticated Data) to prevent reordering/splicing attacks.

### Why AES-256-GCM?

- **AES** — the most widely used encryption algorithm. Hardware-accelerated (AES-NI) on every modern Intel/AMD/Apple chip.
- **256-bit key** — more possible keys than atoms in the observable universe.
- **GCM (Galois/Counter Mode)** — an AEAD cipher that provides both confidentiality (encryption) and authenticity (tamper detection) in a single pass.

| Alternative | Why we didn't use it |
|---|---|
| **AES-CBC** | Encrypts but does NOT detect tampering. An attacker could flip bits in the ciphertext without detection. |
| **AES-CTR** | No authentication. You'd need a separate HMAC — error-prone (encrypt-then-MAC ordering is a classic security footgun). |
| **ChaCha20-Poly1305** | Great alternative (used by WireGuard). We chose AES-GCM because AES-NI hardware acceleration makes it faster on all our target platforms. |
| **RSA** | RSA is for small data (a few hundred bytes). Cannot encrypt bulk file data. |

### Why streaming chunks?

A 10 GB file would require 10 GB of RAM to encrypt in one shot. Chunking into 256 KB pieces keeps memory constant (~512 KB regardless of file size), lets the receiver start writing immediately, and enables pause/resume.

### Nonce uniqueness

Each chunk's nonce is `baseNonce XOR chunkIndex`. If two chunks ever share the same nonce+key, the encryption breaks catastrophically. XORing with the index guarantees uniqueness without extra random number generation.

---

## 5. Transfer Authentication — HMAC-SHA256

Every transfer includes proof that the sender is a paired device. This prevents a rogue device on your LAN from impersonating a paired peer.

```
Sender:  HMAC = SHA256(shared_key, "device-id|filename|timestamp")
         → sends X-Auth-HMAC and X-Auth-Time headers

Receiver: recomputes HMAC with its copy of the key
         → rejects if mismatch, timestamp > 5 min old, or HMAC was seen before
```

### Why HMAC?

| Alternative | Why we didn't use it |
|---|---|
| **Just send the device ID** | Anyone who overhears one transfer can impersonate that device forever. |
| **Digital signatures (Ed25519)** | Overkill — signatures prove identity to third parties. We only need proof for the receiver, who already shares a key. |
| **JWT tokens** | Designed for stateless web APIs. We have a direct peer connection with shared state. HMAC is lighter. |

### Replay protection

Two layers prevent an attacker from capturing a valid HMAC and re-sending it:
1. **Timestamp window** — requests older than 5 minutes are rejected.
2. **Replay cache** — each HMAC value can only be used once. Auto-cleans entries older than 6 minutes.

---

## 6. Transfer Protocol — HTTP Streaming

Files are sent as HTTP POST requests directly from sender to receiver over the LAN.

```
POST /inbox HTTP/1.1
Content-Type: application/octet-stream
Content-Length: 104857600
X-Filename: photo.jpg
X-From: MacBook
X-From-ID: a1b2c3d4
X-Auth-HMAC: 9f3a...
X-Encrypted: aes-gcm-v2
X-File-Size: 100000000

[encrypted file bytes — streamed directly, no buffering]
```

### Why HTTP?

| Alternative | Why we didn't use it |
|---|---|
| **WebSocket** | Designed for bidirectional real-time communication. File transfer is unidirectional — HTTP POST is simpler. |
| **Raw TCP** | Would need a custom protocol for framing, error handling, metadata. HTTP gives that standardized. |
| **gRPC** | Adds protobuf compilation and runtime dependency. HTTP is simpler for streaming. |
| **QUIC / HTTP/3** | LAN has near-zero packet loss, so QUIC's advantages don't apply. TCP is fine locally. |

### Zero-copy streaming

Files are **never held in memory**. The sender reads from disk → encrypts on-the-fly → pipes into the HTTP body via `io.Pipe()`. Memory stays ~1 MB regardless of file size.

### LAN tuning

The HTTP client is configured for throughput:
- **256 KB read/write buffers** — fewer system calls, better throughput
- **No timeout** — large files can take hours
- **Compression disabled** — encrypted bytes don't compress; saves CPU
- **16 idle connections** — keeps TCP warm for back-to-back transfers

---

## 7. Folder Transfer — Per-File Streaming

Folders are sent file-by-file (not zipped). This enables per-file progress, pause/resume, and avoids memory spikes.

```
Phase 1: ANNOUNCE
  Sender → Receiver: "Sending 'Photos' (150 files, 2.3 GB)"
  Receiver → Sender: "Accepted. Session token: abc123"

Phase 2: STREAM (up to 4 concurrent)
  POST /inbox (X-Folder-Session: abc123, X-Folder-Rel: vacation/photo1.jpg)
  POST /inbox (X-Folder-Session: abc123, X-Folder-Rel: vacation/photo2.jpg)
  ...

Phase 3: DONE
  Sender → Receiver: "Complete" (or "Cancelled — 87/150 sent")
```

| Approach | Trade-off |
|---|---|
| **Zip entire folder, then send** | Requires temp disk space + user waits for zip before transfer starts |
| **Zip-stream (on-the-fly)** | Loses per-file progress. Failure at 90% means re-send everything. |
| **Per-file (our approach)** | Instant start, individual progress, only remaining files need re-sending on failure |

---

## 8. Receiver Consent — Blocking Channel

Nothing downloads without user permission. The implementation uses Go channels to synchronize the HTTP handler with the UI:

```go
select {
case accepted := <-tr.Decision:   // user clicked accept/reject
    if !accepted { return }
case <-time.After(60 * time.Second):
    // auto-reject after timeout
}
```

The HTTP handler blocks until the user decides or 60 seconds elapse. No polling, no busy-waiting — the goroutine sleeps until signaled.

---

## 9. Pause/Resume — Channel-Based Flow Control

The sender wraps the file reader in a `CountingReader` that checks a pause channel on every `Read()`:

```go
func (c *CountingReader) Read(p []byte) (int, error) {
    if ch := c.Tr.PauseCh; ch != nil {
        <-ch  // blocks until channel is closed (resume)
    }
    n, err := c.R.Read(p)
    atomic.AddInt64(&c.Tr.Sent, int64(n))
    return n, err
}
```

- **Pause:** create channel → `Read()` blocks → HTTP body stops → zero CPU
- **Resume:** close channel → all blocked readers unblock
- **Cancel:** close pause channel, then cancel context

---

## 10. Security Architecture — Defense in Depth

```
Layer 1: PAIRING
  └─ SPAKE2 (zero-knowledge) — proves both sides know the PIN
  └─ Result: 32-byte shared AES key stored on both devices

Layer 2: TRANSFER AUTHENTICATION
  └─ HMAC-SHA256 on every request — proves sender is paired
  └─ Timestamp + replay cache — prevents replay attacks

Layer 3: TRANSFER ENCRYPTION
  └─ AES-256-GCM — encrypts file content, detects tampering

Layer 4: PATH SAFETY
  └─ SafeFilename() — strips path components from incoming filenames
  └─ Relative path validation — rejects ".." in folder transfers

Layer 5: NETWORK ISOLATION
  └─ API token — only the local UI can call sensitive endpoints
  └─ LANHandler — public endpoints are a strict subset
  └─ IP verification — peer announcements must match caller IP
```

---

## 11. Platform Architecture — Thin Shells + Shared Core

```
core/              ← 100% of the business logic (Go)
├── server.go      ← HTTP handlers, routing
├── transfer.go    ← File sending, streaming
├── crypto.go      ← Encryption, SPAKE2
├── device.go      ← Peer registry, identity
├── discovery.go   ← mDNS
└── ...

mac/main.go        ← ~200 lines: Wails window + tray icon
linux/main.go      ← ~170 lines: Wails window + tray icon
windows/main.go    ← ~180 lines: Wails window + tray icon
android/           ← Kotlin app, same HTTP transfer protocol
```

Each platform shell is a thin wrapper that creates a window, sets up the tray icon, and calls `core.NewServer()`. All business logic lives in `core/` and is shared across desktop platforms. A bug fix in transfer logic applies to all 3 platforms automatically.

---

## 12. Design Decisions — Quick Reference

| Decision | Chose | Over | Why |
|---|---|---|---|
| Encryption | AES-256-GCM | AES-CBC, ChaCha20 | AEAD (encrypt + authenticate in one pass) + hardware-accelerated |
| Pairing | SPAKE2 | Plain DH, SRP, plaintext PIN | Zero-knowledge: PIN never leaves the device |
| Discovery | mDNS + LAN scan | Bluetooth, central server | Zero-config + fallback for broken mDNS |
| Transfer | HTTP streaming | WebSocket, gRPC, raw TCP | Simple, framing/errors for free, universal libraries |
| Folders | Per-file streaming | Zip-then-send | Instant start, per-file progress, resumable |
| Authentication | HMAC-SHA256 | JWT, digital signatures | Symmetric, fast, simple, replay-protected |
| Concurrency | Go channels | Locks, callbacks | Pause/resume/consent are natural channel patterns |
| Language | Go | Rust, Node, Python | Cross-compiles to 3 OS's, great concurrency, single binary |
| Desktop UI | Wails (webview) | Electron, Tauri | Lighter than Electron (~15 MB vs ~150 MB), Go-native |

---

## 13. Key Numbers

| Metric | Value |
|---|---|
| Transfer speed | 70–80 MB/s (saturates gigabit LAN) |
| Memory during transfer | ~1 MB (constant, regardless of file size) |
| Encryption overhead | ~2% (AES-NI hardware) |
| Chunk size | 256 KB |
| Key size | 256-bit (AES-256) |
| PIN entropy | 6 digits, expires in 60s, locked after 3 attempts |
| QR token entropy | 256-bit random |
| HMAC timestamp window | 5 minutes |
| Consent timeout | 60 seconds auto-reject |
| mDNS browse cycle | 4s browse + 3s pause |
| Keepalive probe interval | 3 seconds |
| LAN scan interval | 15 seconds |
| Concurrent folder transfers | 4 parallel streams |
