/**
 * Wire types for /api/v1 (SPEC §11, phase-1 subset).
 *
 * JSON convention: snake_case, matching the sqlite column names in SPEC §7 and
 * the Go `json:"..."` tags on internal/core types. Timestamps are RFC3339 UTC
 * strings; the empty string means "unset" (same convention the schema uses).
 * Paths are relative to the storage root (SPEC §1.2 pillar 3).
 */

/** internal/core.ParsedRelease */
export interface ParsedRelease {
  title: string;
  year: number;
  season: number;
  episodes: number[];
  quality: string;
  source: string;
  codec: string;
  audio: string;
  /** Video bit depth the name claimed (8 or 10); 0 when it claimed none. */
  bit_depth: number;
  group: string;
  proper: boolean;
  repack: boolean;
  edition: string;
  /** Parser self-assessment in [0,1]. */
  confidence: number;
}

/**
 * internal/core.TVCompatibility — the active TV profile's verdict on a release
 * or an imported file (SPEC §8).
 *
 * Advisory only: nothing is hidden, refused or reordered because of it. An
 * "unknown" verdict means the tags carried nothing to judge, never that the
 * media is bad.
 */
export interface TVCompatibility {
  verdict: 'unknown' | 'compatible' | 'needs-remux' | 'incompatible';
  /** Human-readable, worst first; empty unless something is off. */
  reasons: string[];
}

/** internal/api.tvProfileJSON — one built-in target-set description. */
export interface TVProfile {
  id: string;
  name: string;
  description: string;
  video_codecs: string[];
  max_bit_depth: number;
  audio_codecs: string[];
  containers: string[];
  max_quality: string;
  /** True for the profile the compatibility fields were computed against. */
  active: boolean;
}

/** internal/core.MediaFile */
export interface MediaFile {
  id: number;
  path: string;
  size: number;
  movie_id: number;
  quality: string;
  source: string;
  codec: string;
  audio: string;
  release_group: string;
  added_at: string;
  modified_at: string;
  compatibility: TVCompatibility;
}

/** internal/core.Movie plus the file summary the list/detail endpoints attach. */
export interface Movie {
  id: number;
  tmdb_id: number;
  imdb_id: string;
  title: string;
  sort_title: string;
  year: number;
  overview: string;
  path: string;
  poster_path: string;
  /** Provider poster URL — the artwork before a local poster exists. */
  poster_url: string;
  monitored: boolean;
  quality_profile_id: number;
  release_date: string;
  added_at: string;
  updated_at: string;
  /** Present when the movie has an imported file. */
  file?: MediaFile | null;
}

/** internal/core.Episode plus its imported file, when there is one. */
export interface Episode {
  id: number;
  series_id: number;
  season_number: number;
  episode_number: number;
  tmdb_id: number;
  title: string;
  overview: string;
  air_date: string;
  monitored: boolean;
  file?: MediaFile | null;
}

/** internal/core.Season; `episodes` is populated by the series detail endpoint. */
export interface Season {
  id: number;
  series_id: number;
  season_number: number;
  title: string;
  overview: string;
  poster_path: string;
  air_date: string;
  monitored: boolean;
  episodes?: Episode[];
}

/** internal/core.Series plus the counts the list endpoint attaches. */
export interface Series {
  id: number;
  tmdb_id: number;
  tvdb_id: number;
  imdb_id: string;
  title: string;
  sort_title: string;
  year: number;
  overview: string;
  status: string;
  path: string;
  poster_path: string;
  /** Provider poster URL — the artwork before a local poster exists. */
  poster_url: string;
  monitored: boolean;
  quality_profile_id: number;
  first_aired: string;
  added_at: string;
  updated_at: string;
  /** Total episodes known to the library (specials included). */
  episode_count?: number;
  /** Episodes that have an imported file. */
  episode_file_count?: number;
  /** Populated by GET /library/series/{id} only. */
  seasons?: Season[];
}

/** internal/core.UnmatchedFile — the scan-review queue (SPEC §10.1, §13). */
export interface UnmatchedFile {
  id: number;
  path: string;
  size: number;
  parsed: ParsedRelease;
  reason: string;
  seen_at: string;
}

/** internal/core.MovieMeta — a TMDB search hit, not yet a library item. */
export interface MovieMeta {
  tmdb_id: number;
  imdb_id: string;
  title: string;
  original_title: string;
  year: number;
  overview: string;
  release_date: string;
  /** Absolute provider URL. */
  poster_url: string;
}

/** internal/core.SeriesMeta — a TMDB search hit, not yet a library item. */
export interface SeriesMeta {
  tmdb_id: number;
  tvdb_id: number;
  imdb_id: string;
  title: string;
  original_title: string;
  year: number;
  overview: string;
  status: string;
  first_air_date: string;
  /** Absolute provider URL. */
  poster_url: string;
}

export interface SearchResults {
  movies: MovieMeta[];
  series: SeriesMeta[];
}

/** How much is in the library right now, from GET /system/status. */
export interface StatusCounts {
  movies: number;
  series: number;
  media_files: number;
  unmatched: number;
  /** Monitored-but-missing backlog — the GET /wanted list's size. */
  wanted?: number;
  /** Open convert-for-TV queue: queued plus running. */
  converting?: number;
}

/**
 * GET /system/status (SPEC §11).
 *
 * Phase 1 has no download engine and no dirty-shutdown flag yet, so neither is
 * reported: the fields arrive with the phases that make them mean something
 * (SPEC §2.3, §14).
 */
/**
 * One download client the poller cannot reach. Never carries a credential —
 * `error` is the poll's own failure, the same message the client's test button
 * shows (SPEC §12).
 */
export interface UnhealthyDownloadClient {
  id: number;
  name: string;
  type: DownloadClientType;
  error: string;
  /** RFC3339 timestamp of when it was first seen as unreachable. */
  since: string;
}

export interface SystemStatus {
  version: string;
  /** "server" | "portable" (SPEC §2). */
  mode: string;
  storage_root: string;
  /** Applied migration version — the DB is a rebuildable cache (SPEC §7). */
  schema_version: number;
  /** True while a library scan is running. */
  scanning: boolean;
  counts: StatusCounts;
  /** Filesystem holding the storage root; both 0 when unknown or no root. */
  disk_free_bytes: number;
  disk_total_bytes: number;
  /** "ok" | "unconfigured" | "error". Describes the embedded engine only. */
  engine_health: string;
  /**
   * External download clients the queue poller cannot reach (SPEC §5.1).
   * Empty is the normal case; a non-empty list raises the "client X
   * unreachable" banner. Optional so an older server, or a test fixture,
   * reads as "everything is fine" rather than as a crash.
   */
  unhealthy_download_clients?: UnhealthyDownloadClient[];
  /**
   * Whether ffmpeg and ffprobe are both on the server's PATH. False hides the
   * whole convert-for-TV affordance and degrades the TV-incompatible warning
   * to informational (SPEC §8).
   */
  ffmpeg_available: boolean;
  /**
   * Portable mode: the previous session did not shut down cleanly (SPEC §2.3).
   * While it is true the recovery banner is up and downloads refuse to resume.
   */
  dirty?: boolean;
  /**
   * Whether a login password is configured (SPEC §11). Optional so an older
   * server, or a test fixture, reads as "no password" rather than as a crash.
   */
  password_set?: boolean;
  /**
   * Whether Caravan is bound to an address other machines can reach. Together
   * with `password_set` this is the nag: reachable, and nothing protecting it.
   */
  listening_publicly?: boolean;
}

/** POST /auth/login, POST /settings/password — never carries a credential. */
export interface AuthState {
  password_set: boolean;
}

/**
 * POST /system/verify — the dirty-eject recovery action (SPEC §13).
 *
 * A response at all means the database passed sqlite's own consistency check;
 * a failure throws instead, and leaves `dirty` set on the server.
 */
export interface VerifyResult {
  /** Always "ok" — a bad database answers with an error status. */
  integrity: string;
  /** What GET /system/status reports from here on. */
  dirty: boolean;
  /** Whether a library scan is now running (false when one already was). */
  scanning: boolean;
}

/**
 * POST /system/storage-root/repoint — change where Caravan looks (SPEC §10).
 *
 * No media moves: every stored path is relative to the root, which is what
 * makes this instant. `warnings` is advisory and never a reason the operation
 * did not happen — a root with no library in it is a fresh drive.
 */
export interface RepointResult {
  root: string;
  warnings: string[];
  /**
   * The download engine captured the old root when it was built and cannot be
   * re-pointed under a running process, so the queue follows on the next start.
   */
  restart_required: boolean;
}

/** A storage migration's status. */
export type StorageMigrationStatusName =
  | 'queued'
  | 'running'
  | 'done'
  /** The move broke and undid itself: the old root still has everything. */
  | 'rolled_back'
  /** The one that needs a human: part of the library is under each root. */
  | 'failed';

/** POST /system/storage-root/migrate — one move of the library's files. */
export interface StorageMigration {
  id: number;
  source_root: string;
  target_root: string;
  status: StorageMigrationStatusName;
  files_total: number;
  files_done: number;
  bytes_total: number;
  bytes_done: number;
  error: string;
  created_at: string;
  updated_at: string;
}

/** GET /system/storage-root/migration — what the storage screen polls. */
export interface StorageMigrationStatus {
  /** The most recent migration whatever its status, or null when none has run. */
  migration: StorageMigration | null;
  /** A completed move still needs a restart before downloads follow the root. */
  restart_required: boolean;
}

/** GET/PUT /settings — the settings table is a flat key/value store (SPEC §7). */
export type Settings = Record<string, string>;

/** Known settings keys (internal/store/settings.go). */
export const SETTING_STORAGE_ROOT = 'storage_root';
export const SETTING_TMDB_API_KEY = 'tmdb_api_key';
/**
 * Caravan's own API key, for external tools and the iCal feed. Read-only here:
 * it is generated by POST /settings/apikey, never typed in.
 *
 * There is deliberately no `password_hash` constant — GET /settings never
 * returns it (internal/api/settings.go).
 */
export const SETTING_API_KEY = 'api_key';

export const SETTING_ENGINE_LISTEN_PORT = 'engine_listen_port';
export const SETTING_ENGINE_MAX_CONNECTIONS = 'engine_max_connections';
export const SETTING_ENGINE_MAX_DOWN_KBPS = 'engine_max_down_kbps';
export const SETTING_ENGINE_MAX_UP_KBPS = 'engine_max_up_kbps';
export const SETTING_ENGINE_SEED_RATIO = 'engine_seed_ratio';
export const SETTING_ENGINE_SEED_DAYS = 'engine_seed_days';

/**
 * The default download engine per release protocol (SPEC §5.1). A grab is
 * routed on the release's protocol, never on a per-grab choice, so these two
 * keys are the whole routing configuration.
 *
 * The value is a `DownloadClient.id` as a decimal string, or ROUTE_EMBEDDED
 * for Caravan's built-in torrent engine. Empty means nothing is configured —
 * legal for usenet, where there is no built-in engine and a grab becomes a
 * recorded rejection instead.
 */
export const SETTING_ROUTE_TORRENT = 'route_torrent';
export const SETTING_ROUTE_USENET = 'route_usenet';

/** The SETTING_ROUTE_TORRENT value that selects the built-in torrent engine. */
export const ROUTE_EMBEDDED = 'embedded';

/** Active core.TVProfile id (SPEC §8). Unset resolves to the safe default. */
export const SETTING_TV_PROFILE = 'tv_profile';

/**
 * The built-in DLNA media server (SPEC §5.1). Unlike the Jellyfin handoff these
 * are plain settings keys: there is nothing to validate across them and nothing
 * to test against, so they ride on PUT /settings with everything else.
 *
 * `dlna_enabled` is absent on a fresh install and reads as ON there — the spec's
 * promise is that the library is advertised whenever the server runs.
 */
export const SETTING_DLNA_ENABLED = 'dlna_enabled';
export const SETTING_DLNA_FRIENDLY_NAME = 'dlna_friendly_name';

/**
 * GET /dlna — what the media server is actually doing.
 *
 * `enabled` is the toggle; `advertising` is whether SSDP came up. They differ on
 * a host with no usable multicast, which is a real and common state (a
 * container on a bridge network, a VPN-only interface) and the reason this is an
 * endpoint rather than two settings keys the UI could read directly.
 */
export interface DlnaStatus {
  enabled: boolean;
  friendly_name: string;
  /** Device identity clients see; stable across restarts. */
  uuid: string;
  advertising: boolean;
  /** Why advertising is off despite being enabled. Empty otherwise. */
  error: string;
}

/**
 * GET/POST /handoff/jellyfin — the playback handoff (SPEC §5.2).
 *
 * It has its own endpoints rather than riding on /settings because the three
 * values are edited, validated and tested as one thing: enabling a handoff with
 * no server address is a rejected form, not three independent keys.
 */
export interface JellyfinConfig {
  url: string;
  api_key: string;
  enabled: boolean;
}

/** POST /handoff/jellyfin/test — what the server said about itself. */
export interface JellyfinTestResult {
  server_name: string;
  version: string;
}

/**
 * What the library holds once a scan has finished (see `api.awaitScan`).
 *
 * The scan runs detached from the request that started it, so these are live
 * library counts rather than a per-scan diff.
 */
export interface ScanSummary {
  media_files: number;
  unmatched: number;
}

/** Body for POST /library/movies and POST /library/series. */
export interface AddItemRequest {
  tmdb_id: number;
  monitored: boolean;
  quality_profile_id?: number;
}

/** Body for POST /import/queue/{id}/match. */
export interface MatchRequest {
  type: 'movie' | 'series';
  tmdb_id: number;
}

/* ---------------------------------------------------------------------------
 * Phase 2 — search & download (SPEC §5.1, §9, §11).
 * ------------------------------------------------------------------------- */

/** internal/core.IndexerType* — both dialects, one client. */
export type IndexerType = 'torznab' | 'newznab';

/**
 * internal/core.IndexerConfig. `api_key` round-trips: the server returns what
 * it stores, so an edit that leaves the field untouched keeps the credential.
 */
export interface Indexer {
  id: number;
  name: string;
  url: string;
  api_key: string;
  type: IndexerType;
  /** Indexer-side category ids; exactly these are searched, empty = unfiltered. */
  categories: number[];
  enabled: boolean;
}

/** Body for POST /indexers and PUT /indexers/{id} — everything but the id. */
export type IndexerInput = Omit<Indexer, 'id'>;

/**
 * One node of the category tree an indexer advertises in its capabilities
 * document (POST /indexers/categories). Mirrors internal/core.IndexerCategory.
 */
export interface IndexerCategory {
  id: number;
  name: string;
  subcats: IndexerCategory[];
}

/** internal/core.Protocol* — decides which engine a grab is routed to. */
export type Protocol = 'torrent' | 'usenet';

/* ---------------------------------------------------------------------------
 * Phase 6 — external download clients (SPEC §5.1, §7, §11).
 * ------------------------------------------------------------------------- */

/** internal/core.DownloadClient* — the backends Caravan can be pointed at. */
export type DownloadClientType = 'qbittorrent' | 'sabnzbd' | 'nzbget';

/**
 * One entry of GET /download-clients/types: what a backend is called, which
 * protocol it carries, which credentials it needs, and whether this build can
 * actually talk to it yet.
 */
export interface DownloadClientTypeInfo {
  type: DownloadClientType;
  label: string;
  protocol: Protocol;
  uses_login: boolean;
  uses_api_key: boolean;
  supported: boolean;
}

/**
 * internal/core.DownloadClientConfig, redacted.
 *
 * Unlike `Indexer.api_key`, the password and API key do NOT round-trip: the
 * server never hands a download-client credential back (SPEC §12), and reports
 * only whether one is stored. An edit that leaves the field blank therefore
 * means "keep what is stored", which is what DownloadClientInput encodes.
 */
export interface DownloadClient {
  id: number;
  type: DownloadClientType;
  name: string;
  url: string;
  username: string;
  has_password: boolean;
  has_api_key: boolean;
  category: string;
  /** Lowest wins when more than one enabled client can take a release. */
  priority: number;
  enabled: boolean;
}

/**
 * Body for POST/PUT /download-clients and POST /download-clients/test.
 *
 * `password` and `api_key` are optional on purpose: omitting one keeps the
 * stored credential, and sending "" clears it. `id` is read only by the test
 * endpoint, where it names the row a blank credential falls back to.
 */
export interface DownloadClientInput {
  id?: number;
  type: DownloadClientType;
  name: string;
  url: string;
  username: string;
  password?: string;
  api_key?: string;
  category: string;
  priority: number;
  enabled: boolean;
}

/**
 * internal/core.UsenetServerConfig, redacted (SPEC §5.1, §11 `/usenet-servers`).
 *
 * A news server is an article SOURCE for Caravan's built-in engine, not a
 * download client: the engine reads article bodies from it directly. That is
 * why `port` and `max_connections` always arrive resolved — the server fills in
 * the protocol default before storing, so the form shows the number that will
 * be dialled rather than a blank the user has to know the default for.
 *
 * `password` does not round-trip, exactly as a download-client credential does
 * not (SPEC §12): only `has_password` is reported.
 */
export interface UsenetServer {
  id: number;
  name: string;
  host: string;
  port: number;
  /** Implicit TLS (NNTPS). There is no STARTTLS. */
  tls: boolean;
  username: string;
  has_password: boolean;
  /** Hard per-server connection cap, not a target. */
  max_connections: number;
  /** Lowest wins; higher numbers are backup servers. */
  priority: number;
  enabled: boolean;
}

/**
 * Body for POST/PUT /usenet-servers and POST /usenet-servers/test.
 *
 * `password` is optional on purpose: omitting it keeps the stored credential,
 * and sending "" clears it. `id` is read only by the test endpoint, where it
 * names the row a blank password falls back to — and the server only honours
 * that fallback when host, port and TLS still match the stored row, so a
 * credential can never be pointed at a different machine.
 */
export interface UsenetServerInput {
  id?: number;
  name: string;
  host: string;
  port: number;
  tls: boolean;
  username: string;
  password?: string;
  max_connections: number;
  priority: number;
  enabled: boolean;
}

/**
 * internal/core.Release — one indexer search result, already parsed.
 *
 * Everything except `id`/`indexer_id` is the indexer's claim, not a fact, which
 * is why the picker shows the parse next to the raw title rather than instead
 * of it.
 */
export interface Release {
  /** `releases` row id; 0 for a result that was not cached. */
  id: number;
  indexer_id: number;
  /** Display name of the indexer, denormalized so deletions do not blank it. */
  indexer: string;
  title: string;
  guid: string;
  download_url: string;
  info_hash: string;
  protocol: Protocol;
  size: number;
  seeders: number;
  leechers: number;
  published_at: string;
  parsed: ParsedRelease;
  compatibility: TVCompatibility;
}

/**
 * Body for POST /library/movies/{id}/grab and /library/series/{id}/grab.
 *
 * The release is identified by its cached row id: the search that produced it
 * wrote it to the `releases` seen-cache, so the server does not have to trust a
 * client-supplied download URL. Season/episode targeting mirrors
 * internal/core.AddOpts.
 */
export interface GrabRequest {
  release_id: number;
  /** Season number for a season-pack grab. */
  season?: number;
  /** `episodes.id` values the grab is expected to satisfy. */
  episode_ids?: number[];
}

/** internal/core.DownloadState — the vocabulary the queue colours by. */
export type DownloadState =
  | 'queued'
  | 'downloading'
  | 'seeding'
  | 'completed'
  | 'failed'
  | 'paused';

/**
 * internal/core.DownloadPhase — the sub-step of a multi-stage download.
 *
 * Only the built-in Usenet engine has one: fetching articles, repairing holes
 * with par2, unpacking archives. All three are "downloading" as far as the
 * state machine is concerned, so the phase is shown beside the state rather
 * than instead of it. Every other engine reports "".
 */
export type DownloadPhase = 'downloading' | 'repairing' | 'extracting';

/** internal/core.DownloadStatus — a live snapshot, not a persisted row. */
export interface DownloadStatus {
  /** Engine-native handle (an info hash for the embedded engine). */
  id: string;
  state: DownloadState;
  /**
   * Sub-step within the state, "" or absent when the engine has none. Live
   * only: a row the engine is not currently reporting on has no phase.
   */
  phase?: DownloadPhase | '';
  name: string;
  /** Completion in [0,1]. */
  progress: number;
  bytes_done: number;
  /** 0 until a magnet's metadata arrives. */
  size: number;
  down_rate: number;
  up_rate: number;
  /** -1 when unknown. */
  eta_seconds: number;
  ratio: number;
  save_path: string;
  error: string;
  max_down_rate?: number;
  max_up_rate?: number;
  /**
   * Which backend holds this download (internal/core.Download.Engine). Phase 2
   * ships one engine, so the server may omit it; the queue falls back to
   * "embedded" rather than showing a blank badge.
   */
  engine?: string;
}

/** Torrent-specific diagnostics from GET /downloads/{id}/insight. */
export interface DownloadInsight {
  peers: DownloadPeer[];
  trackers: DownloadTracker[];
  availability: number;
}

export interface DownloadPeer {
  addr: string;
  client: string;
  progress: number;
  down_rate: number;
  up_rate: number;
}

export interface DownloadTracker {
  url: string;
  status: string;
  seeders: number;
  leechers: number;
}

/* ---------------------------------------------------------------------------
 * Phase 3 - acquisition visibility, calendar and quality profiles.
 * ------------------------------------------------------------------------- */

export type WantedReason = 'missing' | 'below_cutoff';

export interface WantedMovie {
  id: number;
  title: string;
  year: number;
  poster_path: string;
  poster_url: string;
  reason: WantedReason;
  file_quality: string;
}

export interface WantedEpisode {
  id: number;
  series_id: number;
  series_title: string;
  season_number: number;
  episode_number: number;
  title: string;
  air_date: string;
  reason: WantedReason;
  file_quality: string;
}

export interface WantedLists {
  movies: WantedMovie[];
  episodes: WantedEpisode[];
}

export type EventLevel = 'info' | 'warn' | 'error';

export interface ActivityEvent {
  id: number;
  level: EventLevel;
  category: string;
  message: string;
  detail: string;
  movie_id: number;
  series_id: number;
  created_at: string;
}

export type JobState = 'pending' | 'running' | 'done' | 'failed';

export interface Job {
  id: number;
  kind: 'rss_sync' | 'backlog_sweep' | 'search_movie' | 'search_episode';
  payload: string;
  state: JobState;
  attempts: number;
  run_after: string;
  lease_expires_at: string;
  last_error: string;
  created_at: string;
  updated_at: string;
}

export type CalendarStatus = 'downloaded' | 'downloading' | 'missing' | 'unaired';

export interface CalendarEntry {
  kind: 'episode' | 'movie';
  date: string;
  title: string;
  series_id?: number;
  movie_id?: number;
  season_number?: number;
  episode_number?: number;
  episode_title?: string;
  monitored: boolean;
  has_file: boolean;
  status: CalendarStatus;
}

export const QUALITY_LADDER = ['2160p', '1080p', '720p', '480p'] as const;
export type Quality = (typeof QUALITY_LADDER)[number];

export interface QualityProfile {
  id: number;
  name: string;
  cutoff: Quality;
  items: Quality[];
  upgrade_allowed: boolean;
  created_at: string;
  updated_at: string;
}

export interface QualityProfileInput {
  name: string;
  cutoff: Quality;
  items: Quality[];
  upgrade_allowed: boolean;
}

/* ---------------------------------------------------------------------------
 * Convert-for-TV queue (SPEC §8, GET/POST /convert).
 * ------------------------------------------------------------------------ */

/** internal/core conversion statuses. */
export type ConversionStatus = 'queued' | 'running' | 'done' | 'failed' | 'cancelled';

/**
 * How a file was (or will be) converted. Empty until the job has probed it,
 * because the choice belongs to the probe and not to whoever pressed Convert.
 */
export type ConversionStrategy = '' | 'none' | 'remux' | 'transcode';

/** internal/core.Conversion — one row of the convert queue. */
export interface Conversion {
  id: number;
  media_file_id: number;
  /** Storage-root-relative path as it was when the conversion was queued. */
  source_path: string;
  /** Storage-root-relative path the library now points at; empty until done. */
  output_path: string;
  strategy: ConversionStrategy;
  /** The TV profile this conversion targets, recorded at queue time. */
  profile_id: string;
  status: ConversionStatus;
  error: string;
  created_at: string;
  updated_at: string;
}
