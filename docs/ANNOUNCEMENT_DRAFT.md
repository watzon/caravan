# Caravan v0.1.0 announcement draft

> Draft only. Do not publish until `docs/RELEASE_CHECKLIST.md` is complete.

## Short version

Caravan is an early, self-hosted media acquisition and library manager that
collapses the usual multi-app stack into one binary, one SQLite database, and
one storage root. It handles discovery, requests, indexer search, embedded
torrent and Usenet downloads, imports, metadata, and playback handoff to tools
such as Jellyfin, Stash, and DLNA clients.

The initial `v0.1.0` release is intended for experienced self-hosters willing to
test early software and report compatibility findings. It ships as Linux,
macOS, and Windows binaries and can also be built with Docker Compose.

## Longer post

### Introducing Caravan

The typical self-hosted media setup works, but it can require a convoy of apps:
one for discovery, separate managers for different library types, an indexer
manager, one or more download clients, and path mappings connecting all of
them. Caravan explores a simpler model: **one process, one database, one storage
root**.

Caravan `v0.1.0` brings these workflows into one self-hosted application:

- movie, series, and access-controlled restricted libraries;
- discovery, member requests, wanted lists, automatic search, and upgrades;
- Torznab/Newznab indexers with embedded torrent and Usenet engines;
- optional qBittorrent, SABnzbd, and NZBGet routing;
- hardlink-aware imports, metadata, NFO/poster output, and library rescans;
- Jellyfin, Stash, DLNA, webhook, and iCalendar handoff surfaces;
- one embedded web UI with administrator/member roles; and
- Linux, macOS, Windows, Docker Compose, and experimental portable-drive modes.

This is an **early release**, not a production-readiness claim. Portable-drive
behavior varies by filesystem, host, and television; external-client behavior
depends on each deployment; and the binaries are not yet signed or notarized.
Keep backups of Caravan's config directory and read the deployment-specific
guides before pointing it at irreplaceable media.

To try the checkout-based Docker setup:

```sh
git clone https://github.com/watzon/caravan
cd caravan
mkdir -p config data
docker compose up -d --build
```

Then open <http://localhost:8677> and complete administrator setup.

Compose publishes port 8677 to the LAN by default. First boot is intended for a
trusted network: claim the administrator account immediately, or bind the port
to loopback/use an access-controlled reverse proxy before starting.

Feedback is most useful when it includes the Caravan version, deployment mode,
host OS/architecture, relevant client or TV version, sanitized logs, and exact
reproduction steps. Please use private vulnerability reporting for security
issues and never post credentials or library contents.
