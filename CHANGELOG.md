# Changelog

All notable changes to Caravan will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and Caravan uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `just dev` runs Vite HMR beside Air and reverse-proxies the SPA from
  `:8677`, so frontend edits no longer need a Go restart.
- Initial public-release documentation and release checklist.

### Fixed

- Session cookies survive a process restart (Air, Docker, portable remount)
  instead of forcing a new login.

## [0.1.0] - TBD

### Added

- A single self-hosted service for media discovery, requests, acquisition,
  import, library management, and playback handoff.
- Embedded torrent and Usenet download engines, with optional qBittorrent,
  SABnzbd, and NZBGet integrations.
- Movie, series, and access-controlled adult libraries with per-library
  metadata providers, indexers, quality profiles, routing, and DLNA visibility.
- Svelte web UI with first-run administrator setup, role-based access,
  discovery, requests, wanted items, queue, history, calendar, and settings.
- Jellyfin, Stash, DLNA, webhook, and iCalendar handoff surfaces.
- Bare binaries for Linux, macOS, and Windows; a Docker Compose deployment; and
  a portable-drive preparation flow.
- Reproducible GitHub Release archives and SHA-256 checksums for five OS/CPU
  targets.
- MIT license for the project source.

### Known limitations

- Portable-drive behavior still requires a recorded real exFAT, multi-OS, and
  television hardware verification run.
- External-client completion and failure behavior still requires a recorded
  run against real qBittorrent, SABnzbd, and NZBGet instances.
- Release binaries are not code-signed or notarized.
- The Docker image is built from a checkout; no registry image is published by
  the initial release workflow.

[Unreleased]: https://github.com/watzon/caravan/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/watzon/caravan/releases/tag/v0.1.0
