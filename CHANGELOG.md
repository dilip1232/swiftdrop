# Changelog

All notable changes to SwiftDrop macOS will be documented in this file.

Format based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-06-02

### Added
- **Windows cross-discovery** — LAN subnet scanner finds Windows devices that can't use mDNS
- **Bidirectional announcements** — devices announce themselves to peers for instant mutual discovery
- **Improved peer removal** — clicking ✕ permanently removes device from known-peers cache

### Changed
- **Core refactor** — shared logic moved to `swiftdrop-core` module (discovery, transfers, encryption, peer management)
- Mac app is now a thin shell importing `swiftdrop-core`
- Keepalive prober and network watcher use shared core implementations

### Fixed
- Removed device reappearing after a few seconds (keepalive re-adding from known cache)
- Stale windows-build.yml removed from mac repo
- Release workflow updated to clone core module for CI builds

## [0.1.0] - 2025-06-02

### Added
- Initial release
- Wireless file transfer between Mac and Android over local network
- mDNS auto-discovery of nearby devices
- End-to-end encrypted transfers
- Menu bar app with popover UI
- Native file picker and drag-and-drop support
- DMG installer with drag-to-Applications
