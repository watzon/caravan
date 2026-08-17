# Changelog

All notable changes to Caravan will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and Caravan uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Discover Featured now follows Overseerr's shape more closely: after
  trending, movie shelves come first (popular, upcoming, now in theatres,
  movie genres, browse by studio) and series shelves follow (popular,
  upcoming, currently airing, series genres, browse by network). Movies
  and Series landings reuse those editorial shelves — studio tiles on
  Movies, network tiles on Series — and only switch to the filter grid
  once a filter, sort, or "view all" is applied. Adult gets the same
  treatment: a just-released billboard, recently released and newly added
  rows, and browse-by-site tiles from the provider's default site list.
- Discover's Browse by network and Browse by studio tiles now show each
  shelf's logo. One-color marks silhouette to bone; filled lockups (Marvel)
  keep a graphite / warm-gray / bone tri-tone so the lettering stays
  visible. The curated lists cover more of the catalogues people actually
  look for (Prime Video, Hulu, HBO Max, Paramount+, Peacock, AMC, Showtime,
  Crunchyroll, Marvel, Pixar, Disney, and others).
- The search box speaks a query language: bare keywords, `"quoted phrases"`,
  field filters (`title:`, `site:`, `date:`, `year:`, `season:`, `episode:`,
  `quality:`, `source:`, `codec:`, `audio:`, `group:`, `edition:`,
  `indexer:`, `is:proper/repack/seasonpack`), `OR`/`AND`/`NOT`, `-term`
  negation, and parentheses. Keywords go to the indexers; field filters and
  negations apply locally, with a visible count of hidden rows. Item pages
  seed the box with their search spelled in the language — a scene seeds
  `site:"…" date:…`, a movie `title:"…" year:…` — so what used to be hidden
  query generation is now the editable query itself, and the response lists
  the exact text sent to every indexer. A syntax cheatsheet lives next to
  the box.
- Interactive searches now fan out multiple query forms: movies search with
  and without the year, and an episode search also asks for the season pack.
  Locally scraped torrent sites receive sanitized keywords ("Marvel's Agents
  of S.H.I.E.L.D." searches as "Marvels Agents of SHIELD"), because they match
  literal text where a Newznab server tokenizes.
- The release picker verifies titles: a result whose parsed name is not the
  item searched for is badged "WRONG TITLE" and sorts below every real match,
  which keeps torrent meta-search noise out of the top of the list. The
  server's wrong-year, wrong-season, wrong-episode, wrong-date, and
  season-pack flags now render as picker badges too.
- Settings → Metadata always shows the Stash-box card to admins, so an
  endpoint can be added before the first adult library exists.
- Adding an indexer now talks to it first. A site homepage is refused instead
  of stalling every search. A background health job flags unreachable
  indexers out of fan-out and disables them after three failed probes.
- Torrent catalog rows no longer prefill the site homepage as the API URL.
  The form starts from the Jackett Torznab feed; published hosts are shown
  as site info. A Forbidden homepage is explained instead of looking like
  a down indexer.
- Settings → Indexers now starts from a catalog: choose torrent, Usenet, or a
  generic Jackett/Torznab/Newznab source, pick a named indexer, then fill in
  the details. Usenet presets carry published API hosts; torrent sites offer
  a Jackett-shaped Torznab placeholder. The picker filters by public/private,
  language, and content type (movies, TV, anime, and so on). Site rows prefill
  a published base URL, offer a mirror dropdown when several exist, and only
  require an API key when the definition actually needs one.
- `just dev` runs Vite HMR beside Air and reverse-proxies the SPA from
  `:8677`, so frontend edits no longer need a Go restart.
- Bare installs now keep `caravan.yaml` under the XDG config directory and
  persistent application state under the XDG data directory by default;
  `--data-dir`, `CARAVAN_DATA_DIR`, and `data_dir` provide explicit overrides.
- Portable preparation accepts drive-relative `--data-dir` and `--storage-root`
  choices and records them in the generated config.
- Initial public-release documentation and release checklist.

### Fixed

- Discover's poster shelves no longer stretch the page sideways. The row
  stays inside a capped scroller instead of growing the document to the
  uncapped width of every card.
- Managed indexer definitions that pass a setting to a template function
  (TorrentDownload and others using `re_replace .Config.…`) no longer fail
  every search with a template arity error.
- Relative dates without a clock ("Yesterday", "Today") now parse, and a
  field that fails to parse only drops its own row instead of failing the
  whole search — matching Cardigann behavior for meta-search sites.
- Multi-word category ids such as "TV shows" now map to Torznab categories.
- A definition whose search API lives on a host declared by a URL setting
  (YTS) is an approved request origin again.
- Indexer error messages no longer redact one-letter setting values, which
  shredded unrelated words into `[REDACTED]` fragments.
- A malicious definition pack archive can no longer crash the process with a
  deeply nested manifest; JSON nesting is bounded like the XML and torrent
  parsers.
- The portable backup ZIP downloaded from Settings → Database can be uploaded
  back through the same page: the restore upload is routed by payload type,
  and generic uploads are detected by their magic bytes.
- Large Torznab responses (500+ items) no longer fail with "XML document
  exceeds parsing limits" and knock the indexer out of search.
- An installed definition pack entry for a site outside the research catalog
  no longer breaks the whole Add Indexer catalog with a 500.
- A database upgraded through the v8 trust migration now opens and completes
  the v9 repair instead of being rejected as unrecognized, and a pack source
  namespace left keyless by that upgrade can be reclaimed by its original
  signer key.
- Catalog sites that require an API key and run through a local definition can
  be saved again; the credential flows through the definition settings form.
- Editing a definition-backed indexer now shows its stored setting names as
  write-only fields, so a rotated passkey can be entered without deleting and
  re-adding the indexer, and the meaningless Torznab/Newznab switch is hidden.
- "Load categories" works for pack-installed definitions and for already
  saved indexers, which now probe with their stored credentials.
- Creating a backup no longer holds two extra in-memory copies of the
  database just to size the download.
- Session cookies survive a process restart (Air, Docker, portable remount)
  instead of forcing a new login.
- Scan Review no longer re-lists a file the user already matched: a parked
  grab is a finished decision, so a restart does not import it again.

## [0.1.0] - TBD

### Added

- A single self-hosted service for media discovery, requests, acquisition,
  import, library management, and playback handoff.
- Embedded torrent and Usenet download engines, with optional qBittorrent,
  SABnzbd, and NZBGet integrations.
- Movie, series, and access-controlled adult libraries with per-library
  metadata providers, indexers, quality profiles, routing, and DLNA visibility.
- Svelte web UI with first-run administrator setup, role-based access,
  discovery, requests, wanted items, queue, history, calendar, and settings.
- Jellyfin, Stash, DLNA, webhook, and iCalendar handoff surfaces.
- Bare binaries for Linux, macOS, and Windows; a Docker Compose deployment; and
  a portable-drive preparation flow.
- Reproducible GitHub Release archives and SHA-256 checksums for five OS/CPU
  targets.
- MIT license for the project source.

### Known limitations

- Portable-drive behavior still requires a recorded real exFAT, multi-OS, and
  television hardware verification run.
- External-client completion and failure behavior still requires a recorded
  run against real qBittorrent, SABnzbd, and NZBGet instances.
- Release binaries are not code-signed or notarized.
- The Docker image is built from a checkout; no registry image is published by
  the initial release workflow.

[Unreleased]: https://github.com/watzon/caravan/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/watzon/caravan/releases/tag/v0.1.0
