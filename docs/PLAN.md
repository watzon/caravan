# Caravan Development Plan

**Status:** v1.1 (companion to `SPEC.md` Draft v0.2)
**Date:** 2026-08-03

Twelve numbered phases. An unnumbered interlude between v1.0 and v1.1 records
three tracks that shipped between revisions. Each numbered phase absorbs one
hat of the existing *arr ecosystem and ends with a deliverable a real user can
run and test - no phase ends in scaffolding-only limbo.

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

## Interlude — shipped between plan revisions

Between v1.0 and v1.1, three unplanned tracks shipped and are treated as done: **discover & requests** (TMDB explore, request queue, minimum availability), **multi-user RBAC** (accounts, admin/member roles, request ownership), and **recurring metadata refresh**. Phases 8–10 build on all three.

---

## Phase 8 — Libraries as first-class objects

**Hat:** the multi-instance *arr pattern — people who run a second Sonarr/Radarr just to get different indexers, categories, or clients per library. Caravan absorbs that into one instance.
**Deliverable:** libraries become rows, not implications. Movies, TV (and later Adult) each carry their own indexer set with per-pair category overrides, download routing, default quality profile, and DLNA visibility — with global settings as the fallback. Existing installs migrate with behavior unchanged.

### Tasks

1. **`libraries` table + migration:** `id`, `kind` (`movie`|`tv`), `name`, `root_path`, `dlna_visible` (default true), nullable `route_torrent`/`route_usenet`, nullable `quality_profile_id`. Seed one Movies and one TV row from the existing implicit layout; all existing items adopt their library row.
2. **Settings resolution helper:** one function — library override → global default — with an explicit, deliberately short list of overridable settings (indexers, categories, routing, DLNA visibility, default profile). Everything else stays global.
3. **`library_indexers` join table:** `(library_id, indexer_id, enabled, categories)` where `categories` overrides the indexer's defaults for that pair. Migration default: every library × every indexer, enabled, no override — today's behavior exactly.
4. **Search flow rewire:** interactive, backlog, and season searches resolve item → library → enabled indexers → per-pair categories. A TV search never sends movie or XXX categories.
5. **RSS sync dedup:** each indexer's feed is fetched **once** per cycle using the union of categories across the libraries that enable it; release-to-wanted matching then happens per-library.
6. **DLNA visibility:** the content tree includes only libraries with `dlna_visible`; toggling bumps ContentDirectory `SystemUpdateID` so cached TVs refresh.
7. **API + UI:** Libraries settings screen — library list plus a detail view with the indexer/category matrix, routing overrides, default profile, and DLNA toggle. Gate: Paper design pass before implementation (mirroring phase 3 task 10).

### Acceptance criteria

- Against the fake Torznab server's request log: a TV-library search sends only that library's categories for that indexer; a movie search never includes 5000-series categories.
- Disabling an indexer for one library leaves it active for the others.
- One RSS cycle produces exactly one feed fetch per enabled indexer, regardless of how many libraries share it.
- Toggling `dlna_visible` adds/removes the library's container and reference DLNA clients pick up the change without a restart.
- Migrating an existing DB changes zero observable behavior until a user edits an override.

---

## Phase 9 — Adult module

**Hat:** Whisparr.
**Deliverable:** an admin-enabled, per-user-granted adult library, fed by stash-box metadata (TPDB endpoint by default), acquiring scene releases end to end — and invisible in every sense when disabled: no routes, no UI, no network traffic, no DLNA container, no presence on prepared drives.

### Tasks

1. **stash-box provider client** (`internal/stashbox`): GraphQL client for the stash-box protocol — scene/site search, scene details, studio + performers, images. Endpoint URL + API key in settings, TPDB preset as default (StashDB et al. become config values, not new code). Fixture-tested; fake stash-box server joins the e2e suite.
2. **Data model:** `kind` on `series` (`'tv'`|`'adult'`, default `'tv'`); `stash_id` columns on series and episodes with the same partial-unique treatment as `tmdb_id`; scene side metadata (studio, performers, scene URL) in a JSON column on episodes; `requests.media_type` CHECK extended.
3. **Site-as-series mapping:** site → series, release year → season, scene → episode (air date = scene release date). The wanted list, backlog search, RSS matching, calendar, and import pipeline are reused, not forked.
4. **Scene release parser:** date-based path (`Site.YY.MM.DD.Performers.Title.…`) selected by library kind / 6000-series indexer category; quality/source parsing shared with the existing parser. Corpus entries land with the parser, per the standing rule.
5. **Gating:** `adult_enabled` setting + per-user `adult_access` flag (admin-granted). Router-level 404 for all adult routes when off; refresh/RSS jobs for adult items no-op; **zero** stash-box traffic when disabled. Both the global flag and the user grant are required to see anything.
6. **Exposure defaults:** adult library rooted at `library/Adult`; `dlna_visible` defaults **false** for adult kind (the phase-8 flag renders in the UI as a nested toggle under DLNA, present only when adult is enabled); `caravan prepare` excludes the adult root unless `--include-adult` is passed explicitly.
7. **UI:** adult enable flow in settings (with a plain-language note that DLNA is unauthenticated when that sub-toggle is touched), per-user grant toggles on the users screen, adult library browse (site grid → site detail with scene rows), discover + requests extended to scenes. Gate: Paper design pass before implementation.

### Acceptance criteria

- Adult disabled: no adult routes respond, no UI traces render for any role, and the fake stash-box server logs zero requests across a full job cycle.
- Enabled + granted: a member with `adult_access` can discover and request scenes; a member without it sees nothing adult anywhere, including in shared surfaces like the calendar.
- End to end: add a site, a scene release is found via a search that sends only 6000-series categories, parses by date, imports under `library/Adult`, and the DB disposability rule still holds (delete DB, rescan, adult library returns).
- The DLNA tree gains an Adult container only when its dedicated toggle is on; a fresh enable of the adult module leaves DLNA exposure off.
- `prepare` without `--include-adult` produces a drive with zero adult bytes and no adult references in its config.

---

## Phase 10 — Onboarding & credential gating

**Hat:** none — this is the front door. Phase 1's first run stopped at the storage root; everything downstream assumes credentials it never collected.
**Deliverable:** a first run that cannot end broken — the TMDB key is collected and validated where it's entered, every surface that needs a missing credential says so instead of failing, and enabling adult content runs its own setup that refuses to switch on without a working stash-box credential. Runs after phase 9, before Stash.

### Tasks

1. **First-run wizard restructure:** three light steps — storage root (existing behavior) → metadata (TMDB API key with a live inline test; an explicit "skip for now" escape hatch that names the consequence) → optional scan (existing). Everything else keeps shipping with defaults; SPEC §10.1 is updated to match.
2. **Credential health model:** `/system/status` (and the sidebar card) reports metadata-credential state — absent / invalid / ok — from a cached validation result, never a live upstream call per status poll. State transitions when the key is edited or a metadata call fails auth.
3. **Guarded surfaces:** Add Movie/Series, Discover, and metadata refresh degrade to a directed empty state ("Add your TMDB API key in Settings → Metadata") when the key is absent or invalid — never a raw error toast. A scan without a key still parses and imports; a banner explains why nothing matched.
4. **Settings → Metadata test button:** the same live validation, mirroring the indexer "Test" pattern; the adult metadata-source card already has one from phase 9 — behavior must match.
5. **Adult enable setup:** toggling adult content on opens a setup modal — stash-box endpoint (TPDB preset) + API key with live validation, then a confirm step restating the exposure defaults (DLNA off, prepare excludes, member grants pending). `adult_enabled` only commits when the test passes; cancel leaves everything off. Disabling never prompts.
6. **Onboarding stays clean:** the main first run contains zero adult references, consistent with invisible-when-off.
7. Gate: Paper design pass (revised First Run steps, adult enable modal) before implementation — done alongside planning.

### Acceptance criteria

- A fresh install that completes first run with a valid key can add a movie immediately; one that skips the key lands in a UI where every metadata-needing surface names the fix, with no raw failures anywhere.
- An invalid TMDB key is caught by the inline test at first run and in settings before anything downstream breaks.
- Toggling adult on with a missing or invalid stash-box key cannot leave `adult_enabled` on; cancel changes nothing; a valid key completes and lands on the Adult Content settings section.
- `/system/status` reflects credential-state transitions without upstream calls on every poll.
- The first-run flow renders zero adult-content references.

---

## Phase 11 — Stash integration

**Hat:** none — this is phase 4's Jellyfin seam, replayed for the adult library. Stash is the adult counterpart of Jellyfin, and Caravan treats it that way.
**Deliverable:** an adult import triggers a scoped Stash scan, then Caravan pushes identity — stash-box ID, title, studio, performers — so the scene arrives in Stash already identified, no manual tagging.

### Tasks

1. **Stash client:** GraphQL client with config + test-connection mirroring the Jellyfin card; wired to the existing `library.Notifier` seam, firing a `metadataScan` scoped to the adult root on adult-library changes only.
2. **Identify push:** after the scan, locate the scene in Stash by path and `sceneUpdate` with the stash-box ID, title, studio, and performers. Because phase 9 sources metadata via stash-box, the IDs are shared vocabulary — Stash's identify step just happens. Retry with backoff while the scan is still in flight.
3. **oshash (optional, stretch):** compute the 64KB head+tail oshash at import and include it in the push. phash stays Stash's job permanently — Caravan never decodes video frames for fingerprinting.
4. **Health model:** unreachable Stash surfaces a banner and queues notifications, mirroring the external-client health pattern; imports are never blocked by Stash being down.
5. **Settings UI:** a Stash card beside the Jellyfin card (URL, API key, test, scoped-to-library indicator). Designed in the phase 9 Paper pass alongside the adult settings screen.

### Acceptance criteria

- An adult import fires exactly one scoped Stash scan; non-adult imports fire none.
- The imported scene appears in Stash with its stash-box ID, title, studio, and performers populated, with no manual identify performed.
- With Stash unreachable: the import completes normally, a banner appears, and the queued notification delivers when Stash returns.

---

## Phase 12 — Explore expansion

**Hat:** Overseerr, then past it. Discover today is editorial-only (trending, popular, curated networks) plus a stray adult Scenes tab filed under the library; neither lets anyone *ask* for something specific. This phase makes Discover the one place all three catalogues are browsed, with real faceted filtering.
**Deliverable:** one Discover surface with four scopes — Featured (today's home), Movies, Series, Adult — where the three catalogue scopes are filterable grids: movies/series by genre, cast/crew person, studio/network, keyword, year range, runtime, rating, and language; adult by site (widenable to its whole network), performers, tags (any/all), year, and duration. The Adult section's Scenes tab is retired; its job moves here. Paper pass done ("Explore — Movies (filter browse)", "Explore — Adult (scene browse)").

### Tasks

1. **TMDB discover client:** `DiscoverMovies`/`DiscoverSeries` over `/discover/{movie,tv}` with a filter struct covering genres, people (`with_cast`/`with_people`; TV has no cast param — document the seam), companies/networks, keywords, date ranges, runtime range, vote floor, language, and sort; typeahead passthroughs for person/company/keyword search and a cached `/genre/list`. Filters the API cannot serve are absent from the struct, not silently dropped.
2. **Adult discover filters:** `core.SceneQuery` grows performers (id→name map), tags (id→name map) with any/all modes, site scoping with the Network/Parent widening operator, year, date + comparison operator, duration, and order. The TPDB REST dialect maps them verbatim (verified live: `performers[{id}]`, `tags[{id}]`, `site_operation`, `orderBy` enums); the generic stash-box GraphQL mapping covers what that protocol answers. Typeahead endpoints for performers and tags proxy the provider.
3. **API:** scope-aware browse endpoints with the full filter surface as query params, decorated with in_library/requested exactly as `/discover` rows are today; adult browse and its typeaheads live on the adult mux (gate = visibility, as everywhere).
4. **Scope routing + Scenes tab retirement:** `/discover` (Featured) plus `/discover/movies`, `/discover/series`, `/discover/adult`; the scope row renders Adult only for granted callers. The Adult section loses its Scenes tab; old links land on `/discover/adult`.
5. **Filter UI:** a shared filter-rail kit — dropdown popovers, typeahead multi-select with removable applied chips, range and sort controls, the hide-in-library toggle — with filter state in the URL so every filtered view is shareable and survives reload. Scene results render as 16:9 duration-badged cards per the Paper boards; movies/series keep DiscoverCard.
6. **Actions on results:** the same add/request affordances the discover rows have today, same member/admin split, same modals.

### Acceptance criteria

- Every filter offered in the UI round-trips to a provider query proven by fixture tests per dialect; nothing renders a control the provider cannot answer.
- "Movies with this actor," "this whole network's scenes with these two tags (all)," and "series from this network under 45 minutes" each work end to end and survive a page reload via URL state.
- The Scenes tab is gone; `/discover/adult` is 404-invisible with the module off or the caller ungranted, and no adult filter surface leaks into ⌘K, Featured, or the movie/series scopes.
- Members can request and admins can add from any scope, with the existing flows.

---

## Risks and long poles

- **The release parser is the long pole.** It gates phase 1 matching quality and phase 3 automation quality. Mitigation: the corpus starts in phase 1 and grows forever; the interactive picker and unmatched queue are the designed graceful-degradation paths.
- **anacrolix/torrent quirks** (resume data, exFAT-friendly file handling, seeding lifecycle) deserve an early spike in phase 2 before UI work builds on it.
- **DLNA client variance** is unbounded; scope phase 4 to browse+serve with documented reference clients, not per-TV workarounds.
- **exFAT integrity** can't be fully simulated in CI; phase 5 needs a manual test matrix with a physical drive and at least one real TV.
- **par2 and yEnc correctness are phase 7's long pole.** Repair math that is subtly wrong corrupts media silently; the fixture corpus (including deliberately damaged sets cross-checked against a reference par2 implementation) must exist before the repair code, and CRC failures always prefer "fail loudly" over "best effort".
- **stash-box dialect variance.** TPDB, StashDB, and FansDB all speak "stash-box," but field coverage and rate limits differ; the phase 9 client is fixture-tested per endpoint, and endpoint quirks live in config presets, not code branches.
- **The scene parser corpus starts near-empty.** Adult release naming is more chaotic than TV/movie naming; the interactive picker and stuck-import queue are again the designed degradation path, and the corpus-only-grows rule applies from the first scene grab.
- **Exposure regressions are phase-blocking.** Any change that lets adult content reach DLNA, Jellyfin, a prepared drive, or an ungranted user by default is treated like an absolute-path violation: a bug that blocks the phase, not a polish item.
