# Caravan

Caravan is a self-hosted media manager in one binary. It finds movies,
series, and anime, searches your indexers, downloads with its own torrent and
Usenet engines, imports and names the files, writes metadata, and hands the
library to Jellyfin, a DLNA television, or Stash.

One process. One SQLite database. One storage root. No separate indexer
manager, download client, or per-library app to keep in sync.

> [!IMPORTANT]
> Caravan is pre-1.0 software for people who already run their own servers.
> Back up the application data directory before upgrades, and read the
> deployment guide for your setup. Docker publishes port 8677 to the LAN by
> default: complete the administrator setup right after the first start, or
> bind the port to `127.0.0.1` behind a reverse proxy first.

## What it does

- **Libraries.** Movies, series, and anime with per-library metadata
  providers (TMDB, TheTVDB, TVmaze, AniList), quality profiles, indexers,
  download routing, and DLNA visibility. Restricted libraries are visible only
  to accounts that have been granted access.
- **Discovery and requests.** Trending, popular, upcoming, and genre shelves;
  members request titles and administrators approve them.
- **Search.** A query language (`title:"…" year:2024 -cam`) that fans out to
  every enabled indexer, verifies titles, and flags wrong seasons, years, and
  season packs.
- **Indexers.** Over 500 torrent sites through Prowlarr-compatible definitions
  that Caravan runs itself, any Newznab Usenet indexer, and any Jackett or
  Prowlarr feed. Private trackers log in with your credentials or a session
  cookie. Sites behind a Cloudflare challenge work through FlareSolverr.
- **Downloads.** Embedded BitTorrent and Usenet engines with concurrency
  limits, seeding rules, and NNTP server pools. qBittorrent, SABnzbd, and
  NZBGet are optional, not required.
- **Import.** Hardlink-aware imports, Jellyfin-style folder and file naming,
  NFO and poster output, upgrades, a recycle bin, and library moves.
- **Playback.** Jellyfin rescans after import, a built-in DLNA server, Stash
  handoff for restricted libraries, a convert-for-TV queue, webhooks, and an
  iCalendar feed.
- **Deployment.** A bare binary for Linux, macOS, and Windows; Docker Compose;
  or a portable external drive that a television can play from over USB.

## Install

### Bare binary

Download the archive for your OS and CPU from the releases page, unpack it,
and run:

```sh
caravan serve
```

Open <http://127.0.0.1:8677> and create the administrator account.

Defaults on Unix:

| What | Path |
|---|---|
| Config file | `${XDG_CONFIG_HOME:-$HOME/.config}/caravan/caravan.yaml` |
| Application data (database, state, definition cache) | `${XDG_DATA_HOME:-$HOME/.local/share}/caravan` |
| Media and downloads | The storage root you choose during first-run setup |

Override them with `--config`, `--data-dir`, or the `CARAVAN_DATA_DIR`
environment variable. Add `--listen 0.0.0.0:8677` to serve the LAN.

### Docker Compose

The checked-in Compose file builds the image from the checkout. No registry
image is published yet.

```sh
git clone https://github.com/watzon/caravan
cd caravan
mkdir -p config data
docker compose up -d --build
```

Open <http://localhost:8677>. Mount your media under the same volume as the
downloads so imports can hardlink. See [the Docker guide](docs/docker.md).

### Portable drive

Unpack every release archive into one directory, mount the drive, and run:

```sh
caravan prepare /Volumes/CARAVAN --bin-dir ~/caravan-release-bins
```

The command writes a drive-relative config, launchers for each OS, and the
library layout a television can browse over USB. See
[the portable drive guide](docs/portable.md).

## First run

1. **Create the administrator account.** Until it exists, the server accepts
   nothing else.
2. **Choose the storage root.** Every library path is stored relative to it,
   so the root can move later.
3. **Add a metadata key.** Settings > Metadata takes a TMDB API key; TheTVDB,
   TVmaze, and AniList are optional. The Test button proves a key before it is
   saved.
4. **Add indexers.** Settings > Indexers opens a catalog. Pick a torrent site,
   a Usenet indexer, or a generic Jackett, Prowlarr, Torznab, or Newznab feed,
   fill in the credentials the site needs, and Caravan talks to it before it is
   saved.
5. **Add Usenet servers** under Settings > Usenet servers if you use Usenet.
   NZB downloads run in the built-in engine.
6. **Create a library** and start adding titles.

## Indexers

Caravan refreshes a snapshot of the Prowlarr definition list on every start and
runs those definitions itself, so a torrent site works without Jackett or
Prowlarr. The catalog marks each site as addable, needs FlareSolverr, or
unsupported, with the reason.

- **Public sites** need only a mirror URL, prefilled from the definition.
- **Private trackers** take the username, password, passkey, or API key the
  definition declares. Every login definition also offers an optional
  **session cookie** field: paste the cookie from a browser session and Caravan
  skips the login form. That is the way in for trackers that show a captcha
  at login.
- **Cloudflare and DDoS-Guard sites** need a
  [FlareSolverr](https://github.com/FlareSolverr/FlareSolverr) instance. Save
  its URL in Settings > Indexers. Caravan sends challenged requests through
  it, keeps the resulting cookies, and downloads torrents with them.
- **Usenet indexers** are Newznab feeds. Choose one from the catalog or paste
  its API URL and key.
- **Jackett and Prowlarr** feeds work as Torznab or Newznab sources if you
  already run them.

Not supported: definitions that pin a self-signed TLS certificate, solving a
login captcha inside Caravan, and a handful of upstream definitions with
invalid YAML. They stay visible in the catalog with their reason.

Any saved local indexer is also exposed as a Torznab feed at
`/api/v1/indexers/{id}/feed`, authenticated with Caravan's API key, so other
tools can search through it.

## Configuration

`caravan.yaml` holds only what the server needs before the database exists:

```yaml
listen: 127.0.0.1:8677
data_dir: /var/lib/caravan
storage_root: /srv/media
log_level: info
```

Everything else lives in the database and is edited in Settings. The REST API
under `/api/v1` accepts the API key from Settings > Security in an
`X-Api-Key` header. See [the technical specification](docs/SPEC.md) for the
data model, API surface, and integrity rules.

## Build from source

Requirements: Go 1.26, Node.js 22, npm. The web application is embedded into
the binary, so build it first on a fresh checkout.

```sh
(cd web && npm ci && npm run build)
go build -o caravan ./cmd/caravan
./caravan serve
```

To verify the repository:

```sh
(cd web && npm ci && npm run check && npm test && npm run build)
go test -count=1 ./...
go vet ./...
```

For development, `just dev` runs Vite with hot reload beside
[Air](https://github.com/air-verse/air) for the Go server, both on
<http://127.0.0.1:8677>.

## Documentation

- [Technical specification](docs/SPEC.md)
- [Docker deployment](docs/docker.md)
- [Portable drive](docs/portable.md)
- [Usenet engine](docs/usenet.md)
- [External download clients](docs/download-clients.md) and
  [their import and health behavior](docs/external-clients.md)
- [DLNA media server](docs/dlna.md)
- [Changelog](CHANGELOG.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)

## License

Caravan is released under the [MIT License](LICENSE).
