# Docker deployment

Caravan's server-hosted mode (SPEC §2.1, PLAN phase 5 task 1): one container,
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

Open <http://localhost:8677>.

The container already knows its storage root is `/data`, so it skips the
first-run screen and lands on the library. Two things to do:

1. **Set a password**, under **Settings → Security** — the banner at the top of
   the page is telling you why.
2. If `/data` already contains media, hit **Rescan library** under
   **Settings → Storage**. Caravan does not scan an existing library
   uninvited.

(If you would rather choose the root yourself, blank the variable —
`CARAVAN_STORAGE_ROOT: ""` under `environment:` in `compose.yaml`. An empty
value reads as unset, so Caravan shows the first-run screen, which offers to
scan immediately.)

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

## Ports, binding, and the password nag

The image binds `0.0.0.0:8677` (`CARAVAN_LISTEN`), because a container that
bound loopback would be unreachable from outside its own network namespace.
That is the opposite of portable mode, which binds `127.0.0.1` by default
(SPEC §11).

Binding every interface without a password means anyone who can reach the host
can manage the library. Caravan therefore **nags until a password is set**:
`GET /api/v1/system/status` reports `listening_publicly: true` and
`password_set: false`, and the UI shows a banner pointing at
**Settings → Security**. Dismissing it silences it for that browser session
only; it is back on the next load and stays back until a password exists. The
nag is a warning, not a lock — Caravan still works, on the theory that a
warning you can act on beats a wall on first run.

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
| `CARAVAN_CONFIG_DIR` | `/config` | sqlite database, clean-shutdown marker, logs |
| `CARAVAN_STORAGE_ROOT` | `/data` | seeds the storage root on first run |
| `CARAVAN_LISTEN` | `0.0.0.0:8677` | HTTP listen address |

Precedence, highest first: **command-line flag → environment → config file →
built-in default.** Environment beating the file is deliberate: it is what lets
one image ship the `/config` + `/data` conventions while still honouring a
`caravan.yaml` an operator bind-mounts in for everything else.

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

```sh
docker compose pull        # or: docker compose build --pull
docker compose up -d
```

Database migrations run automatically on start; they are forward-only and each
one is atomic. Nothing else is required.

**Rolling back to an older tag is not supported.** There is no down path — an
older binary does not refuse a newer schema, it just skips the migrations it
does not know about and then trips over columns that were not there when it was
built. Copy `./config` aside before a major upgrade if you want a way back;
everything in it is small.

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

**Not yet verified end to end.** The image, `compose.yaml` and this document
were written and reviewed without a Docker daemon available, so the acceptance
criterion — *"`docker compose up` on a clean host yields a working instance
with hardlink imports"* — is still open.

What *has* been verified: `internal/config`'s environment-override precedence
(`go test ./internal/config/`, including regressions that fail without it), and
that `0.0.0.0:8677` is classified as a public bind by the nag signal
(`internal/api`'s `TestListeningPublicly`).

What CI verifies from the next push onward: the image builds from a clean
checkout and the binary inside it runs (the `Docker image` job in
`.github/workflows/ci.yml`). That covers the Dockerfile; it does not cover the
volume layout, hardlink imports, or ownership, which need a real host.

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
