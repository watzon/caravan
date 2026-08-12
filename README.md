# Caravan

Caravan is a self-hosted media acquisition and library manager. It keeps one
service, one database, and one storage root while supporting multiple libraries,
embedded torrent and Usenet downloads, optional external clients, metadata, and
playback handoff.

> [!IMPORTANT]
> Caravan is pre-1.0 software for experienced self-hosters. Back up the config
> directory, review the deployment guides, and expect compatibility gaps while
> the first public release is validated on real hardware and external clients.
>
> Docker publishes port 8677 to the LAN by default. Complete administrator setup
> immediately on a trusted network. On an untrusted network, bind the published
> port to `127.0.0.1` or use an access-controlled reverse proxy before starting.

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
(cd web && npm ci && npm run check && npm test && npm run build)
go test -count=1 ./...
go vet ./...
```

`web/dist` is ignored generated output. Build the frontend before Go commands
on a fresh checkout because `go:embed` requires `web/dist/index.html`. CI builds
the SPA once and passes that artifact to Go, cross-compile, and race jobs.

## Project documents

- [Technical specification](docs/SPEC.md)
- [Development plan](docs/PLAN.md)
- [Docker deployment guide](docs/docker.md)
- [Portable drive guide](docs/portable.md)
- [Changelog](CHANGELOG.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Initial release checklist](docs/RELEASE_CHECKLIST.md)

## License

Caravan is released under the [MIT License](LICENSE).
