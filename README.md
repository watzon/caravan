# Caravan

Caravan is a self-hosted media acquisition and library manager. It keeps one
library, one database, and one storage root while supporting embedded torrent
and Usenet downloads, optional external clients, metadata, and playback handoff.

## Prerequisites

- Go 1.26.5 for source builds and verification.
- Node.js 22 and npm for the embedded web application.
- Docker Compose for the checkout-based container setup.
- A real exFAT-on-GPT drive and compatible hosts if you are testing portable
  hardware.

## Choose a setup

### Bare binary

Download the archive for the host OS and architecture, then run:

```sh
caravan version
caravan serve --config ./caravan.yaml
```

See [the technical specification](docs/SPEC.md) for configuration and deployment
contracts.

### Docker checkout

The checked-in Compose file builds from the checkout. It does not pull a
published registry image by default:

```sh
git clone https://github.com/watzon/caravan
cd caravan
mkdir -p config data
docker compose up -d --build
```

Open <http://localhost:8677>. Docker volume and hardlink behavior still needs
manual real-host verification; a green CI image build is not that evidence. See
[the Docker guide](docs/docker.md).

### Portable drive

Unpack all five release targets into a release directory, then prepare an
already-mounted drive:

```sh
caravan prepare /Volumes/CARAVAN -bin-dir ~/caravan-release-bins
```

The command writes the drive-relative config, launchers, and target binary slots.
Real exFAT, multiple host operating systems, safe eject, and TV playback remain
manual hardware checks. See [the portable drive guide](docs/portable.md).

## Verify the repository

```sh
go test -count=1 ./...
go vet ./...
(cd web && npm run check && npm test && npm run build)
```

The web build must leave `web/dist/index.html` current for `go:embed`. CI also
runs a pinned Go vulnerability scan, focused race coverage, a host-binary SPA
smoke, and the Docker image smoke.

## Project documents

- [Technical specification](docs/SPEC.md)
- [Development plan](docs/PLAN.md)
- [Docker deployment guide](docs/docker.md)
- [Portable drive guide](docs/portable.md)
