# External clients: completion, import, and health

How a download that lives in qBittorrent, SABnzbd or NZBGet becomes a file in
your library, what Caravan requires of your filesystem to do it, and what
happens when the client stops answering (SPEC §5.1, §13; PLAN phase 6, tasks 2
and 4).

Configuring a client and routing releases to one is
[docs/download-clients.md](download-clients.md). This page starts after that:
the client has the download.

---

## The v1 constraint: same path, same machine

**The client's download directory must be readable by Caravan at the same
absolute path the client reports.**

There is no remote path-mapping matrix in v1 — no "the client says
`/downloads`, Caravan should look in `/mnt/nas/dl`" table. Whatever directory
the client says it wrote into is the directory Caravan opens.

That is a deliberate limit, not an oversight. A path-mapping table is a second
source of truth about where media lives, and the failure mode when it drifts is
an import that reads the wrong file. One rule that is either true or visibly
false beats a mapping that is quietly wrong.

In practice this means:

| Setup | Works? |
| --- | --- |
| Client and Caravan on the same host | Yes, nothing to do |
| Both in Docker, same `/data` volume mounted at the same path in both | Yes |
| Both in Docker, client mounts `/downloads`, Caravan mounts `/media` | **No** — mount the client's directory at `/downloads` in Caravan too |
| Client on another machine, its directory exported over NFS/SMB and mounted at the same path | Yes |
| Client on another machine, nothing shared | **No** |

When the path is not readable, the import does not retry forever. It records
one warning in the activity feed naming the directory and saying *"the client's
download path must be accessible to Caravan at the same location"*, and marks
the grab failed. Fix the mount and re-grab (or re-run the import); nothing on
disk was touched, and the client still holds the data.

### Strongly recommended: put the client's completed directory inside the storage root

Not required, but it is the setup everything else is nicer under:

```
<storage root>/
  library/          # Caravan's organized library
  downloads/        # point your client's completed directory here
```

Three things improve at once:

- **Imports hardlink instead of copying.** A hardlink is instantaneous and
  costs no extra space, so a finished 40 GB remux appears in the library
  immediately and the client keeps seeding the same bytes. Across filesystems
  Caravan falls back to a copy — correct, but it doubles the space until you
  remove the download.
- **A file that cannot be matched gets a stuck-import queue row**, not just a
  feed entry, so you can resolve it with *"this is actually X"* from
  **Scan Review**. A path outside the storage root cannot be stored — every
  path in Caravan's database is relative to the storage root (SPEC §1.2
  pillar 3) — so a mismatch out there is reported in the activity feed only.
- **One volume to back up, move or unplug** (SPEC §2.3).

---

## What happens when a download finishes

Identical to the embedded engine, deliberately — external downloads go through
the same watcher, the same job queue and the same `ImportDownload` seam:

1. **Poll.** Every 5 seconds the import watcher asks the router for every
   engine's downloads. Each status is stamped with the backend that answered,
   so a queue row says which client holds it.
2. **Persist.** The state, progress and sizes land in the `downloads` table.
   The client's own absolute save path deliberately does **not**: that path
   belongs to the client, and the import re-reads it live rather than resolving
   a foreign path out of Caravan's database.
3. **Queue an import.** A download that reaches `completed` or `seeding` is
   enqueued once as a durable `import` job. Seeding counts as finished — the
   transfer is done, and hardlinking is exactly what lets a file be in the
   library *and* still be seeded.
4. **Import.** The job re-reads the live status and the grab, walks the payload
   for video files, and reconciles each against what the grab was for. The
   grab is the evidence of intent; the filename is only a sanity check.
5. **Place.** Files are hardlinked into the library where possible and copied
   where not. The client's copy is **never moved or deleted** — removing a
   download must never cost you media, and a torrent must keep seeding.

Where each client says its payload is:

| Client | Reported save path |
| --- | --- |
| qBittorrent | `content_path` (the torrent's own folder or file), falling back to `save_path` |
| SABnzbd | the history slot's `storage` — **empty while the job is still in the queue**, so nothing is imported before post-processing finishes |
| NZBGet | `FinalDir`, falling back to `DestDir` |

**Exactly once.** An import runs once per download however long it seeds and
however often Caravan restarts: the in-process queue set covers repeated polls,
and the grab's durable `imported` status covers restarts.

---

## Client health

Each configured client's reachability is tracked from the queue poll.

- **Down.** Three consecutive failed polls mark a client unreachable. A banner
  names it and quotes the poll's own error; an entry goes in the activity feed
  (once — a client that is down is down every poll).
- **While it is down.** Its downloads stop updating and disappear from the live
  queue view, though their last known state stays on screen. New grabs routed
  to it fail immediately with the poll's reason rather than producing a
  download nobody can see.
- **Everything else keeps working.** The embedded engine and every other client
  are untouched: torrent grabs still go to the embedded engine, another
  client's queue keeps updating, and the system panel still reports the engine
  as healthy. One dead seedbox is not Caravan being broken.
- **Up.** The first successful poll clears the banner and records a recovery
  entry. Nothing to click.

A client you delete or disable is forgotten immediately — a banner you cannot
act on is worse than none.

`GET /api/v1/system/status` carries this as `unhealthy_download_clients`, an
array of `{id, name, type, error, since}`. It is empty in the normal case and
never contains a credential.

---

## Per-client setup walkthrough

### qBittorrent

1. **Settings → Download clients → Add**, type *qBittorrent*, URL
   `http://127.0.0.1:8080` (or your reverse-proxy prefix), WebUI username and
   password. **Test**, then **Save**.
2. Under **Download routing** at the bottom of the same screen, set the torrent
   default to this client. (Leave it on *Embedded engine* to keep using
   Caravan's own.)
3. In qBittorrent: set **Options → Downloads → Default Save Path** to a
   directory Caravan can read — ideally `<storage root>/downloads`.
4. Optionally set a **Category** on the Caravan client row. It is the label
   your client sorts by; Caravan does not need it.

Caravan tags every torrent it adds with `caravan`, and the queue means "the
torrents carrying that tag". Untag one in qBittorrent and it leaves Caravan's
queue and keeps seeding.

### SABnzbd

1. **Add**, type *SABnzbd*, URL `http://127.0.0.1:8080`, and the **API key**
   from SAB's **Config → General** (the full API key, not the NZB key).
   **Test**, **Save**.
2. Set the usenet default to it under **Download routing**.
3. In SAB, give the category you configure on the Caravan row a folder Caravan
   can read (**Config → Categories → Folder/Path**). Leave the category blank
   to use SAB's default.

Nothing is imported until SAB finishes post-processing: a job that is repairing
or unpacking reports as `downloading`, and its final location is only known
once it lands in the history.

### NZBGet

1. **Add**, type *NZBGet*, URL `http://127.0.0.1:6789`, and the **control
   username and password** from NZBGet's `ControlUsername` /
   `ControlPassword`. **Test**, **Save**.
2. Set the usenet default to it.
3. Point the category's `DestDir` at a directory Caravan can read.

Caravan uploads the NZB itself rather than handing NZBGet a link, so your
indexer URL and API key never reach NZBGet's queue or log.

---

## Troubleshooting

**"could not read the downloaded data" in the activity feed.** The v1
constraint above. The message names the exact directory Caravan tried to open;
open a shell where Caravan runs and check that path exists and is readable.

**A finished download never imports.** Check the grab exists: a download added
directly in the client — not through Caravan — has no library item to import
into, and is left alone on purpose. The library scan is how such files get in.

**The import copied instead of hardlinking.** The client's directory is on a
different filesystem from the storage root. Correct but slower and double the
space; move the client's completed directory under the storage root to fix it.

**A file landed in the stuck-import queue.** It contradicted its grab —
usually a season pack carrying an extra, or a mislabelled release. Resolve it
from **Scan Review** with *"this is actually X"*.

---

## Verification status

**Not yet verified against a real client.** The completion, import and health
paths were built and reviewed without a real qBittorrent, SABnzbd or NZBGet
available, so two acceptance criteria are still open:

- *"A grab routed to a real qBittorrent and a real SABnzbd instance completes
  and auto-imports."*
- *"Killing the external client mid-download surfaces a health banner and
  pauses only that queue."*

What *has* been verified automatically, with tests that fail without the code
they cover:

- Import from a foreign absolute path, including the cross-filesystem copy
  fallback and that the client's own copy survives
  (`internal/library/importexternal_test.go`).
- An unreadable path produces the documented message and a failed grab rather
  than a retry loop (same file).
- No foreign path reaches `media_files`, `unmatched_files`, or
  `downloads.output_path` (same file, plus `internal/library/watch_test.go`).
- Exactly one import per download across repeated polls and a restart
  (`internal/library/watch_test.go`).
- The health model: threshold, single announcement, per-client isolation,
  recovery, and forgetting a deleted client (`internal/download/health_test.go`,
  `cmd/caravan/acquisition_test.go`).
- A grab to an unreachable client fails with the poll's reason and records no
  download (`internal/api/clienthealth_test.go`).
- The banner text and the queue row's client label
  (`web/src/lib/download.test.ts`, `web/src/App.test.ts`).

Run this against a real pair of clients to close the criteria out:

```sh
# 0. Storage root at /data, client completed dir at /data/downloads,
#    readable by Caravan at that exact path.

# --- qBittorrent: grab -> complete -> auto-import -----------------------
# 1. Settings -> Download clients: add qBittorrent, Test must be green.
#    Download routing: torrent default = that client.
# 2. Open a movie -> Interactive search -> Grab a small torrent.
# 3. The torrent appears in qBittorrent tagged "caravan", and in Caravan's
#    Queue with a "qBittorrent" badge.
# 4. When it finishes, within ~5s: Queue shows seeding, the file appears
#    renamed under /data/library, and the History screen shows "Imported".
# 5. Hardlink, not copy — the two paths must share an inode and show 2 links:
find /data/downloads /data/library -name '*.mkv' -exec stat -c '%i %h %n' {} +
# 6. qBittorrent is still seeding it. Remove the download in Caravan with
#    "keep data" and confirm the library file is untouched.

# --- SABnzbd: grab -> complete -> auto-import ---------------------------
# 7. Add SABnzbd (API key from Config -> General), set the usenet default.
# 8. Grab a Usenet release from a Newznab indexer.
# 9. While SAB repairs/unpacks, Caravan must show "downloading" and import
#    nothing. Only after it lands in SAB's history does the import run.
# 10. Confirm the imported file and the History entry, as in step 4.

# --- Health: kill the client mid-download -------------------------------
# 11. Start a download in qBittorrent through Caravan, then stop qBittorrent:
docker stop qbittorrent    # or: systemctl stop qbittorrent-nox
# 12. Within ~15s (three 5s polls) a warning banner reads
#     "Download client <name> is unreachable" with the connection error, and
#     the activity feed has one matching entry.
# 13. Only that queue is affected — with SABnzbd still up, a Usenet grab must
#     still succeed and its downloads must keep updating. System panel still
#     reports the engine healthy.
# 14. A torrent grab routed to the dead client must fail immediately with the
#     connection error, and no download row must be created for it.
# 15. Start qBittorrent again. Within one poll the banner clears, the feed
#     records "is reachable again", and its downloads resume updating.
```
