# Caravan Development Plan

**Status:** v1.0 (companion to `SPEC.md` Draft v0.2)
**Date:** 2026-07-31

Seven phases. Each absorbs one hat of the existing *arr ecosystem and ends with a deliverable a real user can run and test — no phase ends in scaffolding-only limbo.

A standing principle sharpened after phase 6: **Caravan is completely standalone; external clients are explicitly optional.** The embedded engines (torrent since phase 2, Usenet in phase 7) are the defaults, and nothing in the UI or docs may imply an external client is required for any workflow.

## Standing rules (all phases)

- **Bare binary is the dev target from day one.** Docker and portable packaging are phase-5 polish, not new capability; cross-compilation is verified in CI from the first commit.
- **DB disposability is tested, not assumed.** "Delete `caravan.db`, rescan, library comes back" is an acceptance criterion starting in phase 1 and must keep passing in every later phase.
- **The parser corpus only grows.** Every parser bug found in any phase adds a test-corpus entry before the fix lands.
- **Paths are relative to the storage root everywhere.** No absolute paths in the DB, ever; violations are phase-blocking bugs.

---

## Phase 1 — Library manager

**Hat:** the library/metadata half of Sonarr/Radarr (i.e. the tinyMediaManager/FileBot role).
**Deliverable:** point Caravan at a folder of existing, messy media; it parses, matches against TMDB, renames into Jellyfin conventions, writes NFOs and posters, and shows the library in a web UI. Useful standalone before a single byte is ever downloaded.

### Tasks

1. **Scaffold:** Go module, `caravan serve`, YAML bootstrap config, structured logging, sqlite (modernc.org/sqlite, WAL) with embedded sequential migrations, `settings` table.
2. **Storage root abstraction:** join/validate helpers, all DB paths relative; the one place absolute paths are resolved.
3. **Release/filename parser v1:** title, year, SxxEyy (incl. multi-episode), quality, source, codec, group, PROPER/REPACK — with the versioned test corpus started.
4. **TMDB client:** search, details, images; fixture-based tests; API key via settings.
5. **Data model:** `movies`, `series`, `seasons`, `episodes`, `media_files` (+ episode join table), `events`, `jobs` skeleton (claim/lease/retry).
6. **Library scanner:** walk the library root, parse, match, reconcile the DB (add/update/remove); unmatched files park in a review queue.
7. **Organize engine:** naming templates, hardlink-or-move, NFO + poster writers.
8. **REST API slice:** `/settings`, `/system/status`, `/library/*`, `/library/rescan`, `/search?q=` (metadata), unmatched-review endpoints.
9. **Svelte SPA:** Vite build embedded via `go:embed`; first-run flow, library browse (posters), item detail, unmatched-review screen, settings.
10. **CI:** test + lint + cross-compile all release targets.

### Acceptance criteria

- Pointing Caravan at a messy real-world folder matches the majority automatically; results land renamed in the Jellyfin layout with NFO + poster.
- Deleting `caravan.db` and rescanning rebuilds the library view with zero file modifications.
- An unmatched file can be manually matched from the UI and imported.
- Jellyfin pointed at the library root picks everything up cleanly.
- The same binary runs on macOS, Linux, and Windows.

---

## Phase 2 — Search & download

**Hat:** Prowlarr + qBittorrent, bundled deliberately so the phase ends with the end-to-end magic moment.
**Deliverable:** add a Torznab indexer, interactively search, grab a release, watch the embedded torrent engine download it, and find it auto-imported into the library.

### Tasks

1. **Torznab/Newznab client** behind a common indexer interface; indexer CRUD + "test" in API and UI.
2. **Interactive search screen:** per-item search returns the parsed release table (quality/source/codec/size/seeders); manual pick → grab.
3. **Embedded torrent engine:** anacrolix/torrent wrapped in the `Engine` interface; `incomplete/` directory; resume-after-restart.
4. **Download queue:** API + UI for progress, pause/remove; `downloads` and `grabs` tables.
5. **Minimal import pipeline:** completed download → parse → match to the library item it was grabbed for → organize (reusing phase 1's engine) → event log. Failures park in a stuck-import queue with manual match.
6. **Fake Torznab server + fake torrent data** for integration and e2e tests.

### Acceptance criteria

- Real indexer → search → grab → download → file appears renamed in the library, and Jellyfin sees it, with no manual file handling.
- Torrent downloads survive a process restart.
- A deliberately mismatched download parks in the stuck-import queue and can be manually resolved.
- Removing a download optionally deletes its data; the library is never touched by download removal.

---

## Phase 3 — Automation

**Hat:** the Sonarr/Radarr brain — the part that works while you sleep.
**Deliverable:** add a monitored movie or series with a quality profile and never touch it again; Caravan finds, grabs, imports, and upgrades on its own.

### Tasks

1. **Quality profiles:** ladder, cutoff, per-item assignment; profile CRUD UI.
2. **Wanted semantics:** monitored flags with downward-cascade bulk updates; wanted list views (missing / below-cutoff).
3. **Release scoring:** score parsed releases against the profile; rejection reasons recorded on `grabs` for the "why was this skipped" question.
4. **Backlog search job** per wanted item, and **RSS sync** polling enabled indexers on an interval; dedup against seen `releases`.
5. **Automatic grab decisioning:** best-candidate selection, no duplicate grabs across restarts (idempotent jobs against the lease queue).
6. **Upgrade-until-cutoff:** below-cutoff items stay wanted; a better import replaces the file (and the old file is removed only after the new one is verified in place).
7. **Season pack handling:** a single download satisfying many episodes imports as multiple linked files.
8. **Job scheduler hardening:** backoff, lease reclaim, visible job/activity feed in the UI.
9. **Combined calendar:** `/calendar` API merging episode air dates and movie release dates; month + agenda views, entries status-colored with the standard vocabulary; `/calendar.ics` iCal feed (API-key auth) for external calendar apps.
10. **Torrent controls & insight (design first):** the embedded engine grows the controls a standalone client has — global/per-torrent rate limits, seeding targets (ratio + time), connection/port settings — and per-download insight (peers, trackers, availability). Gate: a UI/UX design pass in Paper before any implementation; the queue's minimal v1 shape is deliberate until then.

### Acceptance criteria

- A monitored series acquires new and backlog episodes with zero user interaction.
- The calendar shows upcoming and recent movies and episodes together, each entry reflecting its live status, and the iCal feed subscribes cleanly in an external calendar app.
- A file below cutoff is replaced automatically when a better release appears, and the old file is gone afterward.
- Restarting mid-search/mid-grab never produces duplicate downloads.
- Every automatic decision (grab, skip, upgrade) is explainable from the events/grabs history in the UI.

---

## Phase 4 — Playback handoff

**Hat:** none — this is the seam Caravan hands to players.
**Deliverable:** imports notify Jellyfin, LAN TVs browse the library over DLNA, and the Convert-for-TV queue makes files safe for dumb TV USB playback.

### Tasks

1. **Jellyfin integration:** config + test connection; library-scan trigger on import.
2. **DLNA DMS:** SSDP advertisement + ContentDirectory browse over the library, serving files directly (no transcoding).
3. **TV profiles:** target-set capability descriptions (safe default: H.264 8-bit + AAC in MP4 ≤1080p); parser-tagged codec/audio/container surfaced as compatibility flags in the release picker and library.
4. **Convert-for-TV queue:** ffmpeg detection, remux-first strategy, explicit transcode fallback, queue UI; graceful degradation when ffmpeg is absent.

### Acceptance criteria

- An import triggers a Jellyfin library scan automatically.
- A smart TV on the LAN browses and plays library files via DLNA.
- A DTS/HEVC release is visibly flagged against the active TV profile in the release picker.
- Remuxing an incompatible file produces a TV-safe file and the library record updates to it.

---

## Phase 5 — Deployment & portable integrity

**Hat:** none — this is Caravan's differentiator.
**Deliverable:** the three deployment modes are real products: Docker Compose, bare binary, and the portable drive with dirty-eject hardening.

### Tasks

1. **Docker:** image + compose file, `/config` + `/data` conventions, bind-all + password nag.
2. **`caravan prepare`:** drive scaffold, multi-OS binary placement, launcher scripts, drive-relative config, exFAT/GPT warnings.
3. **Portable integrity flow:** clean-shutdown flag, Shut-down-safely UI + `/system/shutdown` (checkpoint WAL, close, flush), dirty-start detection with fsck prompt + rescan, seeding paused by default.
4. **Storage root operations:** re-point (instant) and migrate (progress job, rollback-on-failure).
5. **Auth polish:** optional password, session cookie, API key; portable binds `127.0.0.1`.

### Acceptance criteria

- `docker compose up` on a clean host yields a working instance with hardlink imports. *(Manual procedure: `docs/docker.md` § Verification status.)*
- `prepare` on a real exFAT drive produces a layout that launches via the click-launcher on at least two OSes, and the drive's library plays in a TV's USB browser. *(Manual procedure: `docs/portable.md` § Verification status — hardware, per-step pass criteria, and where to record the result.)*
- A simulated dirty eject is detected on next start; recovery flow completes and downloads stay paused until the DB verifies.
- Disk-to-server migration = copy files, re-point root, rescan — with history intact.

---

## Phase 6 — External download clients

**Hat:** the bridges — qBittorrent, SABnzbd, NZBGet, opening the Usenet path.
**Deliverable:** grabs route to external clients and import identically to embedded downloads.

### Tasks

1. **Engine implementations:** qBittorrent, SABnzbd, NZBGet APIs behind the existing `Engine` interface; `download_clients` config + test UI.
2. **Completion tracking + import:** poll client state, locate completed data, feed the same import pipeline. v1 requires the client's download path to be visible on Caravan's filesystem (documented constraint; no remote path-mapping matrix).
3. **Routing rules:** default engine per protocol (torrent → embedded or qBit, nzb → SAB/NZBGet); category assignment.
4. **Health model:** unreachable client pauses its queue with a banner; embedded engine unaffected.

### Acceptance criteria

- A grab routed to a real qBittorrent and a real SABnzbd instance completes and auto-imports.
- Torznab and Newznab results route to the correct engine by protocol automatically.
- Killing the external client mid-download surfaces a health banner and pauses only that queue.

---

## Phase 7 — Embedded Usenet engine

**Hat:** the SABnzbd/NZBGet role itself — natively, in-process.
**Deliverable:** with nothing but a news-server account configured, a Newznab grab downloads over NNTP, repairs with par2, extracts, and auto-imports — no external client anywhere. This completes the standalone story: one binary now covers both protocols end to end, and phase 6's bridges become a pure preference.

### Tasks

1. **News-server config:** `usenet_servers` table — host, port, TLS, credentials, max connections, priority (backup servers fill missing articles); CRUD + test connection in API and UI, mirroring the download-clients pattern. Distinct from `download_clients`: these are article sources for the embedded engine, not engines.
2. **NNTP client:** connection pool per server (capped, reused), AUTHINFO, TLS, BODY-by-message-id fetches, retry/backoff, failover to lower-priority servers for missing articles; fixture-tested against a fake NNTP server.
3. **NZB download pipeline:** NZB parse (files/segments), segment scheduler, yEnc decode with CRC verification, assembly into `incomplete/` — resume-after-restart via persisted segment state (never redownload completed segments), disk-space preflight.
4. **Verify & repair:** par2 parse + verify; Reed-Solomon repair when blocks allow; unrepairable downloads fail visibly with the block deficit as the reason, never silently.
5. **Extraction:** rar (pure-Go rardecode) and zip archives extracted in place, archives + par2 files cleaned after a verified extract; obfuscated inner filenames handed to the existing parser/import flow as-is (the stuck-import queue is the designed fallback).
6. **Engine integration:** the whole pipeline behind the existing `Engine` interface as `embedded-usenet`; selectable as the usenet default in phase 6's routing (and the default default when no external usenet client is configured); queue UI shows the phase (downloading/repairing/extracting) with progress; import runs through the same pipeline as every other engine.
7. **Test corpus:** fixture NZBs + yEnc articles + par2 sets, including corrupted-segment, missing-article, and unrepairable cases; fake NNTP server joins the fake Torznab server in the e2e suite.

### Acceptance criteria

- With only a news server configured — zero external clients — a Newznab grab completes, repairs, extracts, and lands renamed in the library automatically.
- A download with corrupt/missing segments repairs via par2 and imports; an unrepairable one fails with a visible, specific reason.
- Restarting mid-download resumes without refetching completed segments.
- A rar'd release extracts, imports, and leaves no archive debris in the library.
- Nothing in the UI presents external clients as required; a fresh install reaches both torrent and usenet downloads without configuring any.

---

## Risks and long poles

- **The release parser is the long pole.** It gates phase 1 matching quality and phase 3 automation quality. Mitigation: the corpus starts in phase 1 and grows forever; the interactive picker and unmatched queue are the designed graceful-degradation paths.
- **anacrolix/torrent quirks** (resume data, exFAT-friendly file handling, seeding lifecycle) deserve an early spike in phase 2 before UI work builds on it.
- **DLNA client variance** is unbounded; scope phase 4 to browse+serve with documented reference clients, not per-TV workarounds.
- **exFAT integrity** can't be fully simulated in CI; phase 5 needs a manual test matrix with a physical drive and at least one real TV.
- **par2 and yEnc correctness are phase 7's long pole.** Repair math that is subtly wrong corrupts media silently; the fixture corpus (including deliberately damaged sets cross-checked against a reference par2 implementation) must exist before the repair code, and CRC failures always prefer "fail loudly" over "best effort".
