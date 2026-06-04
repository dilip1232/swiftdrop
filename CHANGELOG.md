# Changelog

## v1.2.0

### New Features
- **Folder transfer** — send entire folders to any device; files are streamed in parallel and the original directory structure is preserved on the receiver
- **Partial transfer status** — if a folder transfer is cancelled mid-way, the receiver shows exactly how many files were received instead of a generic error
- **Duplicate name protection** — receiving a folder or file that already exists creates a uniquely named copy (e.g. "Photos (1)") instead of silently overwriting

### Improvements
- **Consent dialog stays visible (Mac)** — the accept/reject dialog no longer hides when a notification arrives or the app loses focus
- **Empty folder cleanup** — cancelling a folder transfer removes any empty directories that were created

## v1.1.0

### New Features
- **SPAKE2 PIN pairing** — secure device pairing where the PIN never crosses the network
- **QR code pairing** — scan a QR code to pair devices instantly
- **Per-device chat** — send text messages to individual paired devices
- **Receiver consent** — incoming transfers require explicit approval before files are written
- **Pause and resume** — pause in-flight transfers and resume them later
- **Encrypted transfers** — all transfers between paired devices are encrypted with AES-256-GCM

## v1.0.0

- Initial release
