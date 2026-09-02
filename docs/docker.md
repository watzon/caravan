# Docker deployment

Caravan's server-hosted mode (SPEC §2.1): one container,
one `/config` volume, one `/data` volume.

The whole design goal is that the *arr Docker folder-path problem cannot happen
here. There is one app, one database and one storage root, so there is no
remote path mapping to get wrong and no per-app volume for hardlinks to break
across — as long as you keep `/data` a single mount.

## Quickstart

```sh
git clone https://github.com/watzon/caravan && cd caravan
mkdir -p config data
docker compose up -d
```

> **Secure the first boot.** Compose publishes port 8677 on every host
> interface, while the initial administrator setup is intentionally available
> before an account exists. Start on a trusted LAN and claim the administrator
> account immediately. On an untrusted network, change the published port to
> `127.0.0.1:8677:8677` or place Caravan behind an access-controlled reverse
> proxy before running `docker compose up`.

Open <http://localhost:8677>.

The container already knows its storage root is `/data`, but a new install still
opens the setup flow so you can create the administrator account. The storage
step is already satisfied. If `/data` contains media, Caravan queues an initial
library scan when it seeds the root; **Rescan library** under **Settings →
Storage** is available if you add or move files outside Caravan later.

If you would rather choose the root in setup, blank the variable —
`CARAVAN_STORAGE_ROOT: ""` under `environment:` in `compose.yaml`. An empty
value reads as unset, so the storage step remains open and offers to scan the
selected root immediately.

`compose.yaml` builds the image from the checkout on first `up`. To point at
your real media instead of `./data`, change one line:

```yaml
    volumes:
      - ./config:/config
      - /mnt/media:/data      # <- your media filesystem
```

## The one-volume rule

**Library and downloads must be the same filesystem.** Non-negotiable.

Caravan writes everything below the storage root:

```
/data/
├── incomplete/     in-progress downloads
└── library/        imported, renamed media
```

An import is a hardlink from `incomplete/` into `library/`, so the file exists
in both places while it seeds and costs disk space once. **A hardlink cannot
cross a filesystem.** Mount those two paths from different places and the
import degrades to a full copy: double disk usage, a long stall on every
import, and seeding that reads from a second copy nobody is tracking.

So:

```yaml
# Right — one mount, hardlinks work by construction.
- /mnt/media:/data

# Wrong — two mounts, two filesystems, every import is a copy.
- /mnt/downloads:/data/incomplete
- /mnt/library:/data/library
```

This also applies to the host side. `/mnt/media` must itself be one
filesystem: if `/mnt/media/library` is a separate disk mounted inside
`/mnt/media`, the container sees one path and still cannot hardlink across it.

If your library really does live on a different disk than your downloads, that
is a storage-root decision, not a volume decision — put both under one root, or
accept copies.

## Ports, binding, and first boot

The image binds `0.0.0.0:8677` (`CARAVAN_LISTEN`), because a container that
bound loopback would be unreachable from outside its own network namespace.
That is the opposite of portable mode, which binds `127.0.0.1` by default
(SPEC §11).

Binding every interface before the administrator account exists creates a
first-boot race: anyone who can reach the host may be able to claim that first
account. Complete setup immediately on a trusted network, or use the loopback
publishing example below. This trusted-LAN bootstrap model is a release decision,
not a protection supplied by the later password warning.

Once a password exists, the API is session-cookie gated. External tools use the
API key instead (**Settings → Security**), and the calendar feed
(`/api/v1/calendar.ics`) authenticates with that key in the query string.

To expose Caravan to only the host, publish it on loopback:

```yaml
    ports:
      - "127.0.0.1:8677:8677"
```

The nag still fires — Caravan sees its own bind address, not Docker's port
publishing — which is the conservative direction to be wrong in.

## Configuration

The image needs no config file. Three environment variables carry the
container's conventions:

| Variable | Image default | Meaning |
| --- | --- | --- |
| `CARAVAN_DATA_DIR` | unset | preferred sqlite database and process-state override |
| `CARAVAN_CONFIG_DIR` | `/config` | deprecated image default for upgrade compatibility |
| `CARAVAN_STORAGE_ROOT` | `/data` | seeds the storage root on first run |
| `CARAVAN_LISTEN` | `0.0.0.0:8677` | HTTP listen address |

Precedence, highest first: **command-line flag → environment → config file →
built-in default.** Environment beating the file is deliberate: it is what lets
one image ship the `/config` + `/data` conventions while still honouring a
`caravan.yaml` an operator bind-mounts in for everything else.

`CARAVAN_CONFIG_DIR` remains the image's deprecated default so an existing
deployment that overrides only that variable does not start against an empty
database after upgrading. Set `CARAVAN_DATA_DIR` in new deployments; it has
higher precedence.

`CARAVAN_STORAGE_ROOT` only *seeds* the settings table, and only when it has no
root yet. Re-point the root from the UI and the new value survives restarts —
the environment does not drag it back.

Everything else — indexers, quality profiles, TMDB key, engine limits — is
runtime configuration in the database and is managed from the web UI. There is
no environment variable for it and there will not be one.

### `TZ`

`compose.yaml` passes `TZ` through (default `UTC`). It affects the calendar and
log timestamps. The image ships `tzdata`, so any zone name resolves.

### File ownership

The container runs as uid/gid `1000`, non-root. If your media directory is
owned by someone else, set the ids the container runs as rather than chown-ing
your library — put this in a `.env` next to `compose.yaml`:

```sh
CARAVAN_UID=1001
CARAVAN_GID=1001
```

There is no `PUID`/`PGID` entrypoint shim; `user:` in compose does the same job
without running anything as root first. Whatever ids you choose need write
access to both `./config` and your media mount.

## ffmpeg

Not in the image. The convert-for-TV queue (SPEC §8) is optional and detected
once at startup: without ffmpeg the API refuses to queue conversions, the UI
hides the affordance, and nothing else changes. Bundling it would roughly
quadruple the image for a feature most installs never touch.

To get it, derive an image — the base is Alpine precisely so this is one line:

```dockerfile
FROM caravan:latest
USER root
RUN apk add --no-cache ffmpeg
USER caravan
```

Then point `compose.yaml`'s `build.dockerfile` at it, or build and tag it
yourself. Detection happens at startup, so it takes effect on the next restart
with no configuration.

## Upgrading

Update the checkout, then rebuild the image from the current source:

```sh
git pull --ff-only
docker compose up -d --build
```

Database migrations run automatically on start through Goose; they are
forward-only and each one is atomic. Nothing else is required.

**Rolling back to an older tag is not supported.** There is no down path. An
older binary refuses a migration history it does not know instead of opening a
newer schema unsafely. Copy `./config` aside before a major upgrade if you want
a way back; everything in it is small.

If a database is ever beyond repair, that is survivable by design: stop the
container, delete `config/caravan.db*`, start it, rescan. The library comes
back from the files on disk (SPEC §7 — the database is a cache). What does not
come back is history and wanted lists, which is exactly what the `./config`
copy is for.

## Stopping

`docker compose stop` sends SIGTERM, which runs the same teardown as the UI's
shut-down button: drain HTTP, flush the download queue's state, checkpoint the
write-ahead log, close the database. `stop_grace_period: 60s` in `compose.yaml`
exists so that finishes; Compose's 10s default can land SIGKILL in the middle
of the checkpoint.

## Health

The container's `HEALTHCHECK` fetches `/` — the SPA index — every 30s.

It deliberately does not use `/api/v1/system/status`: that endpoint is inside
the auth gate and starts answering 401 the moment you set a password, which
would flip a healthy container to unhealthy for doing exactly what this page
told you to do. `/` is outside the gate in every configuration and still proves
the process is listening and its handler tree is built.

---

## Verification status

**Partially verified on a real Docker daemon.** On 2026-08-12, a local macOS
Docker run built the image, reached Compose's healthy state, served the SPA with
HTTP 200, denied unauthenticated system status with HTTP 401, stopped with
`caravan.db` and `caravan.state` present, and restarted without a dirty-shutdown
warning. Removing the fixed `container_name` was also checked with two isolated
Compose project names so separate checkouts receive project-scoped containers.

The full acceptance criterion — *"`docker compose up` on a clean host yields a
working instance with hardlink imports"* — remains open. The completed smoke
run does not prove hardlink imports, bind-mount ownership under a non-desktop
Linux host, or behavior against a real media filesystem.

Automated coverage also verifies `internal/config`'s environment-override
precedence (`go test ./internal/config/`), public-bind classification
(`internal/api`'s `TestListeningPublicly`), clean image construction, and that
the binary inside the image runs. Those checks cover the Dockerfile and startup
contract; the remaining storage checks require the real-host procedure below.

Run this on a real host to close it out:

```sh
# 1. Build and start from a clean checkout.
git clone https://github.com/watzon/caravan && cd caravan
mkdir -p config data
docker compose up -d --build

# 2. It is listening and healthy.
curl -fsS http://localhost:8677/ >/dev/null && echo "spa ok"
docker compose ps          # STATUS should reach "healthy"

# 3. The storage root seeded itself, and the nag fires: public bind, no
#    password.
curl -fsS http://localhost:8677/api/v1/system/status |
  grep -o '"storage_root":"[^"]*"\|"listening_publicly":[a-z]*\|"password_set":[a-z]*'
# expect: "storage_root":"/data"  "password_set":false  "listening_publicly":true

# 4. Set a password under Settings → Security and confirm the banner clears
#    and that a reload now requires logging in.

# 5. Hardlink import — the criterion. Drop a release into the storage root,
#    let Caravan import it, then compare inode and link count:
docker compose exec caravan sh -c '
  find /data/incomplete /data/library -name "*.mkv" -exec stat -c "%i %h %n" {} +
'
# expect: the incomplete file and its library counterpart share an inode
#         number and report a link count of 2.

# 6. Ownership: nothing in the mounts is root-owned.
ls -ln config data

# 7. Clean stop: no dirty marker, no -wal left behind.
docker compose stop
ls config          # caravan.db present, caravan.db-wal absent or empty
docker compose start
docker compose logs caravan | grep -i "not shut down cleanly" && echo FAIL
```

Step 5 is the one that matters. If the two files have different inodes, the
`/data` mount is not one filesystem and the one-volume rule above was broken
somewhere — most likely on the host side.
