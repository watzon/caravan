# External download clients

Caravan can hand grabs to a download client running elsewhere instead of (or
alongside) its own embedded torrent engine (SPEC §5.1, §7; PLAN phase 6).
Configuration lives in the `download_clients` table and is edited under
**Settings → Download clients**.

Nothing is preconfigured, and nothing here is required. With no client
configured, torrents go to the built-in BitTorrent engine and NZBs go to the
built-in Usenet engine — see [docs/usenet.md](usenet.md), which needs a news
server and no download client. Configure a client here only to hand grabs to a
machine you already run.

## Supported backends

| Type | Protocol | Credentials |
| --- | --- | --- |
| `qbittorrent` | torrent | username + password (WebUI login) |
| `sabnzbd` | usenet | API key |
| `nzbget` | usenet | username + password (control login, HTTP basic auth) |

`GET /api/v1/download-clients/types` reports this table, plus a `supported`
flag per backend: a build that does not carry an implementation for a backend
still lets it be configured and stored, and says so rather than pretending the
client is broken. Probing an unimplemented backend answers `501`.

## qBittorrent

Caravan speaks the qBittorrent **WebUI API v2** (`internal/clients/qbittorrent`).
Point the URL at the WebUI's base address — `http://127.0.0.1:8080`, or the
prefix a reverse proxy serves it under — and give it the WebUI username and
password.

**Which torrents are Caravan's.** Every torrent Caravan adds is tagged
`caravan`. The queue is "the torrents carrying that tag", never "everything in
qBittorrent": the category is the *user's* field — configurable, possibly
shared with whatever else feeds that client — while the tag is Caravan's own
marker. Untagging a torrent in qBittorrent removes it from Caravan's queue and
leaves it seeding, which is the escape hatch. Servers older than Web API 2.8.3
ignore the `tag` query parameter, so the filter is re-applied on the answer.

**Categories and save paths.** The category sent with a grab is the one on the
client's configuration; empty means qBittorrent's own default. Caravan's
internal `movies`/`tv` routing labels are not sent, and no save path is sent at
all — where qBittorrent writes is qBittorrent's configuration.

**No mirrored state.** Unlike the embedded engine, this backend persists
nothing of its own: qBittorrent already remembers its queue, so every call is a
question asked of it and the `downloads` table stays a cache the import watcher
refreshes each poll.

**States.** qBittorrent's states collapse onto Caravan's six. Three of the
mappings are decisions rather than translations:

| qBittorrent | Caravan | Why |
| --- | --- | --- |
| `stoppedUP` / `pausedUP` | `completed` | The transfer finished; the user only stopped it seeding. Calling it `paused` would hide a finished download from the importer forever. |
| `checkingResumeData`, `checkingDL`, `moving` | `downloading` | qBittorrent is still touching the files. Importing from underneath a running move is how a library gets a half-copied file. |
| `unknown`, anything unrecognised | `queued` | The only state that claims nothing. Guessing `failed` fails a healthy download; guessing `completed` imports an unfinished one. |

**Version compatibility.** qBittorrent 5.0 (Web API 2.11) renamed
`torrents/pause` and `torrents/resume` to `torrents/stop` and `torrents/start`,
and renamed the `pausedUP`/`pausedDL` states to `stoppedUP`/`stoppedDL`. Both
spellings of the states are accepted, and a server that answers the modern
endpoint with a 404 is retried on the old one — once, after which the choice is
remembered.

**Sessions.** The WebUI issues a `SID` cookie that expires when it feels like
it and always when qBittorrent restarts. A call that comes back `403` logs in
again and replays exactly once; a login that is refused is not retried, so a
wrong password fails fast instead of spinning. A rejected login is an HTTP
`200` with the body `Fails.`, which is why the status code alone is not
trusted.

## SABnzbd

Caravan speaks SABnzbd's HTTP API (`internal/clients/sabnzbd`). Point the URL
at SABnzbd's base address — `http://127.0.0.1:8080`, or the prefix a reverse
proxy serves it under, *without* the trailing `/api` — and give it the API key
from **Config → General**.

**Grabs are handed over as links.** `mode=addurl` gives SABnzbd the NZB URL and
lets it fetch the NZB itself: it keeps the same `nzo_id` from the moment it
accepts the link to the moment the job lands in its history, so the id is a
usable download handle straight away, and SABnzbd retries a flaky indexer
better than a single grab would. The release title is sent as `nzbname` so the
queue, the directory names and Caravan's grab all say the same thing.

**Queue plus history.** SABnzbd moves a job out of the queue and into the
history the instant its transfer ends, so Caravan's queue is always both lists.
Only the history knows where the payload landed — its `storage` field — which
is what makes a completed download importable at all. History is read with an
explicit limit (100 rows): with no limit SABnzbd falls back to its own display
default of ten, which could push a finished download out of sight before the
import watcher saw it.

**Numbers arrive as strings.** Sizes are formatted with `%.2f` into JSON
strings, percentages into decimal strings, and which fields are strings has
changed between versions — so every numeric field decodes both forms. A
SABnzbd upgrade must not stop a working install from decoding.

## NZBGet

Caravan speaks NZBGet's JSON-RPC API (`internal/clients/nzbget`). Point the URL
at NZBGet's base address — `http://127.0.0.1:6789`, *without* the trailing
`/jsonrpc` — and give it the control username and password from
`nzbget.conf` (`ControlUsername` / `ControlPassword`), which NZBGet checks as
HTTP basic auth.

**Grabs are uploaded, not linked.** `append` accepts a URL, but NZBGet files a
URL as a placeholder with its own `NZBID` and mints a **different** one once it
has fetched the NZB — the handle Caravan was handed would stop naming anything
a minute later and the download would vanish from the queue. Caravan therefore
fetches the NZB itself and uploads the bytes, which returns the real queue
entry's id immediately. It also keeps the indexer's API key, which is in that
URL, out of NZBGet's queue, its web UI and its logs (SPEC §12). A fetch that
comes back as an indexer's HTML rate-limit page rather than an NZB is rejected
where the user can see it, instead of becoming a download that fails silently.

**Split 64-bit sizes.** NZBGet's RPC protocol predates 64-bit integers, so
every byte count arrives as `…SizeLo` and `…SizeHi` unsigned 32-bit halves and
is recombined on the way in.

**Removal has a limit.** NZBGet deletes a download's partial and failed data
itself, but it exposes no API for deleting a *completed* download's payload —
`deleteData` cannot be honoured there, and the files stay for NZBGet's own
retention settings to deal with. Removal is otherwise idempotent: it deletes
from the queue, falls back to the history when the queue did not know the id,
and treats "neither list has it" as already done.

## Usenet states

Both Usenet backends collapse their client's vocabulary onto Caravan's six.
There is no `seeding`: Usenet has no swarm, so a Usenet download never reports
a seeding state, an upload rate or a share ratio.

| Client state | Caravan | Why |
| --- | --- | --- |
| SAB `Extracting`, `Repairing`, `Moving`, `Running`; NZBGet `UNPACKING`, `REPAIRING`, `MOVING`, `EXECUTING_SCRIPT` | `downloading` | The transfer is over but the client is still touching the files. Importing from underneath a running unpack is how a library gets a half-copied file. |
| SAB `Grabbing` | `downloading` | Fetching the NZB from the link Caravan handed over is a transfer already under way. |
| SAB `Propagating` | `queued` | The job is deliberately held back until the articles have spread; nothing is moving. |
| SAB history `Queued` | `downloading` | In the *history*, `Queued` means "waiting for post-processing", not "waiting to download". This is the one word whose meaning depends on which list it came from. |
| NZBGet `WARNING/*` | `completed` | NZBGet's warnings are post-processing complaints over data it did finish fetching. Failing them would block the import of a download that is sitting right there; an import that finds nothing usable already parks itself. |
| NZBGet `DELETED/*` | `failed` | Removed in the client, so the grab did not deliver — and `failed` is the state that lets it be retried. |
| anything unrecognised | `queued` | The only state that claims nothing. Guessing `failed` fails a healthy download; guessing `completed` imports an unfinished one. |

**Which downloads are Caravan's.** Neither SABnzbd nor NZBGet has tags, so the
configured category is the only marker available — and it is the field these
clients are conventionally partitioned by. With a category set, the queue is
the downloads in it; with none set, the user's other Usenet downloads appear
too, surfaced as grab-less rows rather than hidden. Setting a category is the
fix.

## Routing

A grab is routed on the **release's protocol**, never on a per-grab choice.
Torznab results are torrents and go to the torrent engine; Newznab results are
usenet and go to the usenet client. There is one dispatch point for this
(`download.Router`), and both the interactive picker and the automatic
RSS/backlog grabs pass through it, so the two paths cannot disagree.

Two settings hold the whole configuration, edited under **Settings → Download
clients → Routing**:

| Key | Value | Default |
| --- | --- | --- |
| `route_torrent` | `embedded`, or a `download_clients.id` | `embedded` (the built-in engine) |
| `route_usenet` | empty, or a `download_clients.id` | empty (the built-in engine) |

- **Both protocols always have somewhere to go.** Each has a built-in engine
  that is the default and is always offered. A picked client that is later
  deleted or disabled falls back to it rather than failing every grab of that
  protocol.
- **A protocol is only unrouted when no engine exists at all**, which in
  practice means no storage root has been set yet, since that is what the
  built-in engines are constructed under. A grab then becomes a **recorded
  rejection**: the `grabs` row gets status `rejected` with a reason naming what
  to configure, a warning lands in the activity feed, and the interactive
  endpoint answers `409` with the same message. It is never a silent drop and
  never a misroute into the other protocol's engine.
- **A client taking a default does not retire the built-in engine.** It rejoins
  the routing table without a protocol, so the downloads it is still holding
  stay listable, pausable, removable and importable.
- A routing value naming a client of the wrong protocol is refused by
  `PUT /settings` rather than stored and ignored.

The `downloads.engine` column records the backend that actually took the
download (`embedded`, `embedded-usenet`, `qbittorrent`, `sabnzbd`, `nzbget`),
not the router. That
column is what addresses the download afterwards, so moving a protocol's
default does not strand what the previous client is still holding: every
enabled client stays addressable for its own downloads, and only the current
default takes new work.

Changes take effect **without a restart**. The router resolves the settings and
the `download_clients` rows on each operation, and the engines behind them are
cached per row and rebuilt when the row is edited, dropped when it is disabled
or deleted. The import watcher takes one engine at startup and drives it for
the life of the process, which is why the routing table is resolved live rather
than captured.

One unreachable client does not blank the queue or stall imports: it
contributes nothing to `List` that tick, and the engines that are working keep
going. Reachability is what the **Test** button reports.

## Credentials

Passwords and API keys are **write-only**. They are stored in the database,
never in `caravan.yaml`, never logged, and never returned by the API
(SPEC §12) — `GET /download-clients` reports `has_password` and `has_api_key`
instead of the values.

The consequence for clients of the API:

- Omitting `password` / `api_key` in a `POST` or `PUT` **keeps** the stored
  credential. Sending `""` clears it deliberately.
- `POST /download-clients/test` takes an unsaved configuration, and an `id` in
  the body names the stored row a blank credential falls back to. That is how
  the edit form can test a client without asking the user to retype a password
  it was never shown.

## Paths

A client's base URL and the download directories it reports are absolute paths
on a foreign machine — the one place absolute foreign paths are legitimate.
They live in the client configuration and the in-memory download state and
never reach `media_files` or anything else the library owns, which stays
root-relative (SPEC §1.2 pillar 3).

The client's completed-download path must be visible on Caravan's filesystem.
When Caravan and the client see it at different absolute paths, configure a
remote path mapping under **Settings → Downloads**. Caravan applies the longest
matching client-path prefix and opens the mapped local path.

What that means in practice — how a finished download becomes a library file,
which directory layout to use, and what happens when a client stops answering —
is [docs/external-clients.md](external-clients.md).

## Manual acceptance test

The automated tests cover the wire formats against local fake servers; they
cannot prove Caravan works against a real client. Run this by hand once per
backend before shipping a phase-6 change, and record the result in the PR.

**Setup.** Run the client on the same host (or on a host whose download
directory Caravan can see) with a category/label reserved for Caravan.

1. **Configure.** Settings → Download clients → Add client. Enter the base URL
   and credentials. Press **Test** *before* saving: it must report `Reachable`.
2. **Wrong credentials.** Break the password or API key and press Test again:
   the failure must quote the client's own complaint (for example
   `401 Unauthorized`), and the message must not contain the credential.
3. **Save and re-test.** Save, then press **Test** on the list row: still
   reachable, using the stored credential.
4. **Edit without retyping.** Re-open the client. The credential field must be
   blank with an `Unchanged` placeholder. Change only the name and save; press
   Test again — it must still be reachable, proving the credential survived an
   edit that never saw it.
5. **Grab.** Set the client as its protocol's default under
   **Settings → Download clients → Routing**, then search for a release of that
   protocol and grab it. It must appear in the external client's queue under
   the configured category, and in Caravan's queue with that client as its
   engine. Grab a release of the *other* protocol in the same session and
   confirm it went elsewhere — routing is per protocol, not per grab.
6. **Import.** Let it finish. The file must be imported into the library and the
   grab marked imported, identically to an embedded-engine download.
7. **Unreachable client.** Stop the client mid-download. Caravan must surface a
   health banner for that client and pause only its queue; the embedded engine
   and any other client keep running. Restart it and confirm recovery.
8. **Unrouted usenet.** Clear the usenet default and grab a Newznab result. The
   request must be refused with a message naming what to configure, the grab
   must appear in history as `rejected` with that reason, and nothing may reach
   the embedded torrent engine.

Steps 6-7 depend on tracks that land after the routing slice; until then, run
1-5 and 8.

### qBittorrent

Steps 1-4 above exercise the real login handshake and `app/webapiVersion` call
as soon as the backend is registered. Steps 5 and 8 exercise routing. Steps 6-7
need the completion-import track, which lands after routing.

The engine itself (add, list, pause, resume, remove, files) can be checked by
hand ahead of routing, against a real qBittorrent, with a scratch program that
builds `qbittorrent.NewEngine` from the same URL and credentials:

1. **Add.** `Add` a magnet link. The torrent must appear in qBittorrent tagged
   `caravan`, under the configured category, at qBittorrent's own save path,
   and the returned id must be its info hash.
2. **List.** `List` must return that torrent and *only* torrents carrying the
   tag — add one by hand in qBittorrent without the tag and confirm it stays
   out.
3. **Pause / resume.** `Pause` must show `paused` on the next `Status`;
   `Resume` must return it to `downloading`. On a finished torrent, `Pause`
   must report `completed`, not `paused`.
4. **Files.** `Files` must list the payload with qBittorrent-relative names.
5. **Remove.** `Remove` with `deleteData=false` must leave the data on disk;
   with `true` it must take it. Neither must touch the library.
6. **Restart.** Restart qBittorrent mid-poll. The next `List` must recover
   without a Caravan restart — that is the session re-login.

The automated tests cover every one of these against a fake WebUI with recorded
payloads (`internal/clients/qbittorrent/testdata`), including the 4.x endpoint
names and the pre-2.8.3 tag-filter behaviour. What they cannot cover is a real
qBittorrent's own quirks, which is what this list is for.

### SABnzbd

Steps 1-4 above exercise the real API-key check as soon as the backend is
registered. Note that SABnzbd answers `mode=version` *without* checking the
key, which is why the probe also asks for the queue — step 2 is the one that
proves it.

The engine itself can be checked by hand ahead of routing, against a real
SABnzbd, with a scratch program that builds `sabnzbd.NewEngine` from the same
URL and key:

1. **Add.** `Add` a Usenet release. The job must appear in SABnzbd's queue
   under the configured category, named after the release rather than after the
   indexer's link, and the returned id must be its `nzo_id`.
2. **Same id across the move.** Let it finish. `Status` must keep answering for
   the same id after the job leaves the queue for the history, and must then
   report `completed` with `SavePath` set to SABnzbd's `storage` path.
3. **Post-processing is not done.** While SABnzbd is repairing or unpacking,
   `Status` must report `downloading`, not `completed`.
4. **List.** `List` must return queue and history rows together, and only the
   configured category's when one is set.
5. **Pause / resume.** `Pause` must show `paused` on the next `Status`;
   `Resume` must return it to `downloading`.
6. **Remove.** `Remove` must work on a job in the queue *and* on one in the
   history, and the row must be gone from SABnzbd rather than archived.

### NZBGet

Steps 1-4 above exercise the real control login as soon as the backend is
registered.

The engine itself can be checked by hand ahead of routing, against a real
NZBGet, with a scratch program that builds `nzbget.NewEngine`:

1. **Add.** `Add` a Usenet release. The download must appear in NZBGet's queue
   under the configured category, and the returned id must be the `NZBID`
   NZBGet's own UI shows — not a placeholder that changes a minute later. This
   is the whole reason the NZB is uploaded rather than linked, so check the id
   again after NZBGet has started downloading.
2. **The link never reaches NZBGet.** NZBGet's queue and log must not contain
   the indexer URL or its API key.
3. **Same id across the move.** Let it finish. `Status` must keep answering for
   the same id in the history, and report `completed` with `SavePath` set to
   `FinalDir` (or `DestDir` when no post-processing script moved it).
4. **Post-processing is not done.** While NZBGet is repairing or unpacking,
   `Status` must report `downloading`.
5. **A failed repair.** A release NZBGet cannot repair must report `failed`
   with `FAILURE/PAR` in the message, not `completed`.
6. **Pause / resume / remove.** `Pause` and `Resume` must round-trip. `Remove`
   must work on a download in the queue *and* on one in the history. Removing a
   completed download does **not** delete its payload — NZBGet has no API for
   that — so confirm the files are still there and that nothing in the library
   was touched.

The automated tests cover all of this against fake SABnzbd and NZBGet servers
with recorded payloads (`internal/clients/sabnzbd/testdata`,
`internal/clients/nzbget/testdata`), including SABnzbd's string-typed numbers
and NZBGet's split 64-bit sizes and positional parameter convention. What they
cannot cover is a real client's own quirks, which is what these lists are for.
