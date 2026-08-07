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
  /** The library that owns the movie; 0 on rows from before libraries were plural. */
  library_id: number;
  release_date: string;
  /** The release stage the movie's automatic search waits for. */
  min_availability: MinAvailability;
  added_at: string;
  updated_at: string;
  /** Present when the movie has an imported file. */
  file?: MediaFile | null;
}

/**
 * Minimum availability: how far into its release a movie must be before the
 * automatic search goes after it (internal/wanted). Movies only — episodes
 * gate on their air date instead.
 */
export type MinAvailability = 'announced' | 'in_cinemas' | 'released';

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
  /** The library that owns the series; 0 on rows from before libraries were plural. */
  library_id: number;
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
  /**
   * The library the manual match is scoped to. An untied universal-search grab
   * already chose one, and the review screen names it; 0 — every scan-parked
   * file — means unscoped.
   */
  library_id: number;
  seen_at: string;
}

/** internal/core.MovieMeta — a provider search hit, not yet a library item. */
export interface MovieMeta {
  /**
   * The TMDB id, and zero for a hit from any other provider — which is honest
   * rather than missing: an AniList title has no TMDB id. It stays because it
   * is the compatibility spelling every add still accepts; `provider` and
   * `provider_ref` are what actually identify a row in a chain.
   */
  tmdb_id: number;
  /** The provider that answered: 'tmdb', 'anilist'. */
  provider: string;
  /**
   * This title's id in that provider's own numbering. With `provider` it is
   * the only thing that tells two chain hits apart — two providers' ids are
   * different numbers for different things.
   */
  provider_ref: string;
  imdb_id: string;
  title: string;
  original_title: string;
  year: number;
  overview: string;
  release_date: string;
  vote_average: number;
  /** Number of provider votes behind `vote_average`. */
  vote_count: number;
  /** Absolute provider URL. */
  poster_url: string;
}

/** internal/core.SeriesMeta — a provider search hit, not yet a library item. */
export interface SeriesMeta {
  /** Reads exactly as MovieMeta.tmdb_id does. */
  tmdb_id: number;
  /** The provider that answered. */
  provider: string;
  /** This title's id in that provider's own numbering. */
  provider_ref: string;
  tvdb_id: number;
  imdb_id: string;
  title: string;
  original_title: string;
  year: number;
  overview: string;
  status: string;
  first_air_date: string;
  vote_average: number;
  /** Number of provider votes behind `vote_average`. */
  vote_count: number;
  /** Absolute provider URL. */
  poster_url: string;
}

export interface SearchResults {
  movies: MovieMeta[];
  series: SeriesMeta[];
  /**
   * The provider chain that actually ran, in order and deduplicated.
   *
   * The LENGTH matters as much as the contents: a per-row provider badge is
   * noise on the overwhelmingly common single-provider install, and is the
   * only way to tell two hits apart once the chain is longer than one.
   */
  providers: string[];
  /**
   * The library whose chain answered, echoed so the add lands where the user
   * searched. Zero means the request named none and the kind's default
   * answered.
   */
  library_id: number;
  /**
   * The providers that ran and failed while others succeeded. They arrive on a
   * 200: one provider being down must not hide what the others returned. A
   * chain where every provider failed is a 502/503 instead.
   */
  errors?: SearchProviderError[];
}

/** One provider's refusal inside an otherwise successful search. */
export interface SearchProviderError {
  provider: string;
  message: string;
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
  /** Adult site count — present only when the module is visible to the caller. */
  sites?: number;
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

/**
 * The adult library's Stash handoff, when the last scan or identity push could
 * not reach it (PLAN phase 11 task 4). Never carries a credential: `error` is
 * the attempt's own failure, the same string the card's test button shows.
 */
export interface StashHealth {
  error: string;
  /** RFC3339 timestamp of when the outage started. */
  since: string;
}

export interface RuntimeDiagnostics {
  started_at: string;
  uptime_seconds: number;
  go_version: string;
  os: string;
  arch: string;
  config_dir: string;
  config_file: string;
  database_path: string;
  database_size_bytes: number;
  log_level: string;
  listen_address: string;
  goroutines: number;
  memory_alloc_bytes: number;
}

/**
 * One provider's cached credential verdict, as `GET /system/status` reports it
 * under `metadata_credentials` (internal/api/credentials.go credentialStateJSON).
 *
 * A provider that needs no key is absent from that map rather than present as
 * "ok": what a provider requires is a fact read off the provider list, not a
 * verdict the server reached about anything.
 */
export interface ProviderCredential {
  /** "absent" | "invalid" | "ok". Read through `credentials.providerStateOf`. */
  state: string;
  /** The provider's own words for a rejection. Absent unless invalid. */
  reason?: string;
  /** RFC3339 timestamp of the verdict. Absent when nothing has checked yet. */
  checked_at?: string;
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
   * The adult library's Stash handoff, when it is down (PLAN phase 11 task 4).
   *
   * Absent is healthy, and absent is also what a caller the adult module is not
   * visible to always gets — the server strips it exactly as it strips
   * `counts.sites`. So the field's presence IS permission to render the banner;
   * there is no second adult check for this one to disagree with.
   */
  stash_unreachable?: StashHealth;
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
   * True while the administrator account or storage root is still missing.
   * Optional for compatibility with older servers and test fixtures.
   */
  needs_setup?: boolean;
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
  /**
   * The TMDB key's cached verdict — "absent" | "invalid" | "ok" (PLAN phase 10
   * task 2). Answered from a cache, so polling this endpoint never costs an
   * upstream call. Optional so an older server, or a test fixture, reads as the
   * optimistic "ok" rather than as a problem; read it through
   * `credentials.metadataStateOf` rather than comparing here.
   */
  metadata_credential?: string;
  /** The provider's own words for a rejection. Absent unless invalid. Never
   * contains the key. */
  metadata_credential_reason?: string;
  /** RFC3339 timestamp of the verdict. Absent when nothing has checked yet. */
  metadata_credential_checked_at?: string;
  /**
   * Every credentialed provider's verdict, keyed by provider id, since "the
   * metadata provider" stopped being singular once libraries gained chains.
   *
   * The three flat fields above are this map's TMDB entry — the server fills
   * them from it, so the two cannot disagree. Optional so an older server, or a
   * fixture written before the map existed, still answers for TMDB through the
   * flat fields; read it through `credentials.providerStateOf`, which knows
   * that fallback.
   */
  metadata_credentials?: Record<string, ProviderCredential>;
  /** Admin-only process and path diagnostics supplied by the serving command. */
  runtime?: RuntimeDiagnostics;
}

/** POST /auth/login, POST /settings/password — never carries a credential. */
export interface AuthState {
  password_set: boolean;
}

/**
 * What an account may do (SPEC §11). An admin runs the server; a member can
 * only find something and ask for it.
 */
export type UserRole = 'admin' | 'member';

/**
 * GET /auth/me — who this browser is talking as.
 *
 * A server with no accounts answers with an empty username, the admin role and
 * `open: true`: there is nobody to name, and whoever reached the API may do
 * anything, which is exactly how a passwordless Caravan always behaved.
 */
export interface SessionUser {
  username: string;
  role: UserRole;
  open: boolean;
  /**
   * Whether the adult module is visible to THIS caller: the server-wide switch
   * is on and this account reaches it (an admin always does, a member needs the
   * grant). It is the only field outside /adult that reports anything about the
   * module, and it is false — never absent — for everybody else, so "off" and
   * "not granted" are the same answer here as they are on the 404 from /adult.
   *
   * The SPA reads the nav item, the settings pill and the scene surfaces from
   * this and nothing else. GET /settings cannot stand in for it: that route is
   * admin-only, and a granted member has to be able to decide too.
   */
  adult: boolean;
  /**
   * Which controls the Explore rail's Adult scope may draw. Absent for a caller
   * the module is invisible to, for a server with no stash-box credential, and
   * for a server too old to send it — see `sceneFiltersOf`, which is the only
   * thing that should read it.
   */
  scene_filters?: SceneFilterSupport;
}

/**
 * Which scene filters the configured stash-box endpoint can actually express
 * (internal/api.sceneFiltersJSON).
 *
 * "stash-box" is a protocol with dialects: TPDB serves a release year, a
 * runtime, a widened site scope and two extra orderings, and a StashDB or
 * FansDB install refuses every one of them with a 400 that blanks the grid. The
 * rail draws each of these pills only when the endpoint behind it says yes,
 * which is PLAN phase 12's "nothing renders a control the provider cannot
 * answer".
 *
 * Positive: true is "this works". So an absent block is not "nothing works" —
 * see `sceneFiltersOf`.
 */
export interface SceneFilterSupport {
  year: boolean;
  duration: boolean;
  site_scope: boolean;
  date_op: boolean;
  sort_duration: boolean;
  sort_relevance: boolean;
  any_of: boolean;
}

/**
 * internal/api.userJSON — one account in the Users settings list. There is
 * deliberately no password field in either direction: the hash never leaves
 * the server, and a new password only travels on the way in.
 */
export interface User {
  id: number;
  username: string;
  role: UserRole;
  created_at: string;
  updated_at: string;
}

/** Body for POST /users. On a server with no accounts this is what closes it. */
export interface CreateUserBody {
  username: string;
  password: string;
  role: UserRole;
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
export const SETTING_TMDB_API_KEY_SET = 'tmdb_api_key_set';
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
 * Concurrency ceilings. Zero is unlimited everywhere, which is what Caravan did
 * before these existed: every grab started at once and they starved each other.
 *
 * A download over a ceiling is not refused — it waits in the queue's existing
 * 'queued' state until a slot frees, oldest first.
 */
export const SETTING_MAX_CONCURRENT_DOWNLOADS = 'max_concurrent_downloads';
export const SETTING_EMBEDDED_TORRENT_MAX_CONCURRENT = 'embedded_torrent_max_concurrent';
export const SETTING_EMBEDDED_USENET_MAX_CONCURRENT = 'embedded_usenet_max_concurrent';

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

/** Defaults for required video/audio re-encoding (internal/store/settings.go). */
export const SETTING_CONVERT_VIDEO_PRESET = 'convert_video_preset';
export const SETTING_CONVERT_VIDEO_CRF = 'convert_video_crf';
export const SETTING_CONVERT_AUDIO_BITRATE_KBPS = 'convert_audio_bitrate_kbps';

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
 * The stash-box metadata source for the adult module (internal/store).
 *
 * These two ride on PUT /settings like every other key. The master switch does
 * NOT — flipping `adult_enabled` creates the Adult library row on its first
 * enable, which a key/value PUT cannot carry out, so it has an endpoint of its
 * own (POST /settings/adult) and the server refuses it here.
 *
 * A blank endpoint is legal and means the TPDB preset below.
 */
export const SETTING_STASHBOX_ENDPOINT = 'stashbox_endpoint';
export const SETTING_STASHBOX_API_KEY = 'stashbox_api_key';

/** internal/stashbox.DefaultEndpoint — what a blank endpoint resolves to. */
export const STASHBOX_TPDB_ENDPOINT = 'https://theporndb.net/graphql';

/**
 * The adult module's master switch, as GET /settings reports it.
 *
 * Readable here but NOT writable: PUT /settings rejects it, because the first
 * enable also creates the Adult library row. `api.setAdultEnabled` is the only
 * way to change it. Absent means off, exactly as the server reads it, so the
 * Settings screen can seed its toggle without a second request.
 */
export const SETTING_ADULT_ENABLED = 'adult_enabled';

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
 * GET /handoff/jellyfin — the playback handoff (SPEC §5.2).
 *
 * The API key is write-only. `has_api_key` reports whether one is stored.
 */
export interface JellyfinConfig {
  url: string;
  has_api_key: boolean;
  enabled: boolean;
}

/** POST /handoff/jellyfin — optional write-only credential input. */
export interface JellyfinConfigInput {
  url: string;
  api_key?: string;
  enabled: boolean;
}

/** POST /handoff/jellyfin/test — what the server said about itself. */
export interface JellyfinTestResult {
  server_name: string;
  version: string;
}

/**
 * GET/POST /adult/stash — the adult library's handoff (PLAN phase 11).
 *
 * Field for field the Jellyfin card's shape, because it is the same card for
 * the other library. What differs is where it lives: this rides the /adult
 * mux, so with the module off — or for an account that was not granted it —
 * the route 404s like any unrouted path rather than answering with an empty
 * configuration. That is why these are not three keys on PUT /settings.
 */
export interface StashConfig {
  url: string;
  api_key: string;
  enabled: boolean;
}

/** POST /adult/stash/test — the build Stash identified itself as. */
export interface StashTestResult {
  version: string;
  /** Git hash of that build; Stash reports it beside the version. */
  hash: string;
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
  /**
   * The compatibility spelling of "which title". Send it for a TMDB hit; send
   * `provider`/`provider_ref` for a hit from anywhere else. Sending both is
   * harmless — the pair wins.
   */
  tmdb_id: number;
  /**
   * The general spelling: the provider that identified the title and its id in
   * that provider's numbering, straight off a search hit. They travel as a
   * pair — half of one is rejected, because a ref read in the wrong vocabulary
   * is a different title rather than a failed lookup.
   */
  provider?: string;
  provider_ref?: string;
  /**
   * Whether Caravan should keep searching for missing releases after the add.
   * Omitting it preserves the endpoint's historical monitored default.
   */
  monitored?: boolean;
  /** Optional explicit profile; omitting it (or sending 0) uses the library default. */
  quality_profile_id?: number;
  /** Movies only: queue the automatic search as soon as the add succeeds. */
  search_now?: boolean;
  /** Series only: queue a search for every wanted episode after the add. */
  search_missing?: boolean;
  /**
   * Series only: the seasons this add is going after. Omitting it adds the
   * whole series; naming a subset leaves every other season unmonitored, and
   * is what stops a partial add from closing a request for seasons it never
   * acquired.
   */
  seasons?: number[];
  /**
   * Movies only: the release stage the automatic search waits for. Omitting
   * it defaults a new movie to 'released' and leaves a re-add's choice alone.
   */
  min_availability?: MinAvailability;
  /**
   * The library a NEW item lands in. Omitting it (or 0) targets the kind's
   * default library; a re-added title stays where it already lives.
   */
  library_id?: number;
}

/**
 * Body for POST /adult/sites. A site is named by its stash-box id and nothing
 * else — there is no tmdb_id counterpart.
 */
export interface AddSiteRequest {
  stash_id: string;
  /** Reads exactly as AddItemRequest.monitored does. */
  monitored?: boolean;
  /**
   * Queue a search for every wanted scene. It happens after the catalogue walk
   * the add defers to a background job, because before the walk the site has no
   * scenes to search for.
   */
  search_now?: boolean;
  /** Reads exactly as AddItemRequest.library_id does, over adult libraries. */
  library_id?: number;
}

/**
 * Reply from the on-demand search endpoints. `queued` is how many search jobs
 * were actually added: zero means there was nothing to search for, or the same
 * search was already on the queue.
 */
export interface SearchQueued {
  queued: number;
}

/** Body for POST /import/queue/{id}/match. */
export interface MatchRequest {
  type: 'movie' | 'series';
  /** Reads exactly as AddItemRequest.tmdb_id does. */
  tmdb_id: number;
  /** Reads exactly as AddItemRequest's pair does; both or neither. */
  provider?: string;
  provider_ref?: string;
}

/* ---------------------------------------------------------------------------
 * Phase 2 — search & download (SPEC §5.1, §9, §11).
 * ------------------------------------------------------------------------- */

/** internal/core.IndexerType* — both dialects, one client. */
export type IndexerType = 'torznab' | 'newznab';

/**
 * internal/core.IndexerConfig. The API key is write-only; `has_api_key` reports
 * whether one is stored.
 */
export interface Indexer {
  id: number;
  name: string;
  url: string;
  has_api_key: boolean;
  type: IndexerType;
  /** Indexer-side category ids; exactly these are searched, empty = unfiltered. */
  categories: number[];
  /** Lower values run first and break otherwise equal release scores. */
  priority: number;
  enabled: boolean;
}

/** Body for POST /indexers and PUT /indexers/{id}. */
export interface IndexerInput {
  name: string;
  url: string;
  api_key?: string;
  type: IndexerType;
  categories: number[];
  priority?: number;
  enabled: boolean;
}

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
  /** Null means no explicit per-client cap; zero is also an unlimited stored cap. */
  max_concurrent: number | null;
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
  /** Null clears the explicit per-client cap. */
  max_concurrent: number | null;
  enabled: boolean;
}

/**
 * A translation from a download client's reported filesystem root to the
 * corresponding root on the host running Caravan.
 *
 * Caravan chooses the longest matching remote prefix when more than one mapping
 * applies. Both paths are deliberately opaque strings: their syntax belongs to
 * the remote client and Caravan host respectively.
 */
export interface RemotePathMapping {
  id: number;
  remote_path: string;
  local_path: string;
  /** Number of imports or events this root translation has resolved. */
  match_count: number;
  /** Empty until the mapping resolves a path for the first time. */
  last_matched_at: string;
  created_at: string;
  updated_at: string;
}

/** Body for POST/PUT /remote-path-mappings. */
export interface RemotePathMappingInput {
  remote_path: string;
  local_path: string;
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
  /** Active quality profile's score and accept/reject rationale when evaluated. */
  profile_decision?: ProfileDecision;
}

/** One indexer that failed during a fan-out; partial results still come back. */
export interface IndexerError {
  indexer_id: number;
  indexer: string;
  error: string;
}

/**
 * The release-search envelope every picker endpoint answers with: the exact
 * queries the server sent, the rows, and the indexers that failed. The
 * universal search adds `truncated` and echoes the scoring library.
 */
export interface ReleasesResponse {
  query: string;
  queries: string[];
  truncated?: boolean;
  library_id?: number;
  releases: Release[];
  errors: IndexerError[];
}

/** Query for GET /search/releases — the Prowlarr-style universal search. */
export interface SearchReleasesParams {
  q: string;
  /** Indexer category ids; empty searches genuinely unfiltered. */
  cats?: number[];
  /** Restrict to these indexers; empty asks every enabled one. */
  indexer_ids?: number[];
  /** Score rows against this library's profile instead of the default. */
  library_id?: number;
  limit?: number;
}

/**
 * Body for POST /search/grab. Without `tie` the grab is untied: the finished
 * download parks in scan review scoped to `library_id`.
 */
export interface SearchGrabRequest {
  release_id: number;
  library_id: number;
  tie?: {
    media_type: 'movie' | 'series';
    media_id: number;
    /** Season/episode narrowing for a series tie; absent grabs the whole series. */
    season?: number;
    episode?: number;
  };
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

/**
 * internal/core's release protocols, as the queue tags each row (derived
 * server-side from the engine holding it, internal/clients.ProtocolForEngine).
 *
 * It is what makes the detail drawer protocol-specific: a torrent has peers,
 * trackers, a share ratio and an upload limit; a Usenet download has none of
 * those and a list of files being assembled instead.
 */
export type DownloadProtocol = 'torrent' | 'usenet';

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
  /**
   * Which protocol this download speaks. Absent from a server older than the
   * field; the UI falls back to "torrent", which is what it assumed before it
   * existed.
   */
  protocol?: DownloadProtocol;
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

export interface DownloadPage {
  downloads: DownloadStatus[];
  next_cursor: string;
}

/**
 * Protocol-specific diagnostics from GET /downloads/{id}/insight.
 *
 * The two halves are disjoint: a torrent engine fills peers/trackers/
 * availability, a Usenet engine fills the file and repair fields, and every
 * Usenet field is omitted from a torrent's response rather than sent as zero.
 */
export interface DownloadInsight {
  peers: DownloadPeer[];
  trackers: DownloadTracker[];
  availability: number;

  /** One entry per file the NZB indexes, in NZB order. */
  files?: DownloadFile[];
  files_complete?: number;
  segments?: number;
  segments_done?: number;
  segments_failed?: number;
  /**
   * What verification found wrong, which is what the repairing phase is
   * working on. par2 reports no live progress, so there is no percentage: the
   * stage is shown as indeterminate.
   */
  damaged_segments?: number;
  damaged_files?: string[];
}

/** One file inside an NZB, as the engine's pipeline sees it. */
export interface DownloadFile {
  name: string;
  segments: number;
  segments_done: number;
  segments_failed: number;
  complete: boolean;
  /** A par2 recovery volume rather than payload. */
  par2: boolean;
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
  /** The series' artwork — episodes have none of their own. */
  poster_path: string;
  poster_url: string;
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

export interface EventPage {
  events: ActivityEvent[];
  next_cursor: string;
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

export interface JobPage {
  jobs: Job[];
  next_cursor: string;
}

/**
 * GET /system/tasks — one recurring background job as the Settings screen
 * shows it. The name and description are server-authored, so a task added to
 * the queue appears here without an SPA change.
 */
export interface SystemTask {
  kind: string;
  name: string;
  description: string;
  interval_minutes: number;
  /** When the last run finished; empty when none ever has. */
  last_run: string;
  /** Empty means it has never finished. */
  last_result: 'ok' | 'failed' | '';
  /** Why the last run failed. Empty unless last_result is 'failed'. */
  last_error: string;
  /** When the queued successor comes due. Empty means "as soon as it is polled". */
  next_run: string;
  running: boolean;
  /** False means nothing is queued — the chain broke, and Run now repairs it. */
  queued: boolean;
}

export interface NotificationWebhook {
  id: number;
  name: string;
  /** The endpoint is stored, but never returned because its path/query can contain a token. */
  has_url: boolean;
  on_grab: boolean;
  on_import: boolean;
  on_health: boolean;
  enabled: boolean;
  last_event_id: number;
  created_at: string;
  updated_at: string;
}

export interface NotificationWebhookInput {
  name: string;
  /** Omit on update to preserve the write-only endpoint. */
  url?: string;
  on_grab: boolean;
  on_import: boolean;
  on_health: boolean;
  enabled: boolean;
}

/** Body for PUT /system/tasks/{kind}. */
export interface TaskIntervalInput {
  interval_minutes: number;
}

/** Response from PUT /system/tasks/{kind}. */
export interface TaskIntervalUpdate {
  kind: string;
  interval_minutes: number;
}

/** POST /system/tasks/{kind}/run. */
export interface RunTaskResult {
  kind: string;
  /** The task was already working, so there was nothing to bring forward. */
  already_running: boolean;
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
/** Score components that make a quality-profile decision explainable. */
export interface ProfileScoreContributions {
  quality: number;
  source: number;
  proper: number;
  repack: number;
  seeders: number;
  custom_formats: number;
  tv_compatibility: number;
}

/** Caravan's evaluation of a parsed release against one quality profile. */
export interface ProfileDecision {
  accepted: boolean;
  profile_id: number;
  profile_name: string;
  score: number;
  /** Exact rejection text or a concise explanation of an accepted release. */
  reason: string;
  contributions: ProfileScoreContributions;
}

/** One pasted release title evaluated through POST /quality-profiles/{id}/test. */
export interface ProfileTestResult {
  title: string;
  parsed: ParsedRelease;
  decision: ProfileDecision;
}

/** Body for POST /quality-profiles/{id}/test. */
export interface QualityProfileTestRequest {
  titles: string[];
}

/** Server-owned parsing and scoring response for a profile test. */
export interface QualityProfileTestResponse {
  results: ProfileTestResult[];
}

/** The server-owned, best-first quality ladder exposed by quality profiles. */
export const QUALITY_LADDER = ['2160p', '1080p', '720p', '480p'] as const;
export type Quality = (typeof QUALITY_LADDER)[number];

export interface QualityProfileAssignments {
  libraries: number;
  movies: number;
  series: number;
}
export type ProperRepackPreference = 'prefer' | 'neutral';
export type TVCompatibilityPolicy = 'ignore' | 'prefer' | 'require';

export interface QualityProfileCustomFormat {
  name: string;
  include_terms: string[];
  exclude_terms: string[];
  score: number;
}

export interface QualityProfile {
  id: number;
  name: string;
  cutoff: Quality;
  items: Quality[];
  upgrade_allowed: boolean;
  preferred_sources: string[];
  proper_repack_preference: ProperRepackPreference;
  min_seeders: number;
  min_size_mb: number;
  max_size_mb: number;
  custom_formats: QualityProfileCustomFormat[];
  tv_profile: 'safe' | 'capable';
  tv_compatibility_policy: TVCompatibilityPolicy;
  is_default: boolean;
  assignments: QualityProfileAssignments;
  created_at: string;
  updated_at: string;
}

export interface QualityProfileInput {
  name: string;
  cutoff: Quality;
  items: Quality[];
  upgrade_allowed: boolean;
  preferred_sources?: string[];
  proper_repack_preference?: ProperRepackPreference;
  min_seeders?: number;
  min_size_mb?: number;
  max_size_mb?: number;
  custom_formats?: QualityProfileCustomFormat[];
  tv_profile?: 'safe' | 'capable';
  tv_compatibility_policy?: TVCompatibilityPolicy;
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

/** Process-local stage reported while this Caravan process runs the job. */
export type ConversionStage = 'probing' | 'converting' | 'verifying' | 'installing';

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
  /** Optional live ffmpeg detail. Missing after restart and after completion. */
  stage?: ConversionStage;
  started_at?: string;
  /** Completed media time as a fraction from 0 through 1. */
  progress?: number;
  processed_seconds?: number;
  duration_seconds?: number;
  /** Relative encoding speed, where 1 is real time. */
  speed?: number;
  eta_seconds?: number;
}

/** GET /convert: current candidates plus queued and terminal jobs. */
export interface ConversionQueue {
  pending: MediaFile[];
  conversions: Conversion[];
}

/* ---------------------------------------------------------------------------
 * Discover & requests (SPEC §11 `/discover`, `/requests`).
 *
 * Two id spaces meet here: `tmdb_id` addresses everything under /discover,
 * `library_id` addresses /library/*. Nothing cross-references two calls to work
 * out owned/requested state — every discover payload arrives pre-decorated.
 * ------------------------------------------------------------------------- */

/** internal/api MediaTypeMovie/MediaTypeSeries. Never TMDB's "tv". */
export type MediaType = 'movie' | 'series';

/**
 * What a request row can be about. It is deliberately NOT `MediaType` widened:
 * `MediaType` addresses the TMDB half of the app — /discover/{type}/{tmdbID},
 * /movies/:id, /series/:id — and a scene has none of those. Keeping them apart
 * is what makes the compiler point at every place a scene row needs its own
 * answer instead of silently building a link to a screen that does not exist.
 */
export type RequestMediaType = MediaType | 'scene';

/** internal/api.discoverItemJSON — one provider title, decorated. */
export interface DiscoverItem {
  media_type: MediaType;
  tmdb_id: number;
  title: string;
  year: number;
  overview: string;
  /** The provider's raw path; round-trips into POST /requests unchanged. */
  poster_path: string;
  /** Rendered poster URL, read-only (derived server-side). */
  poster_url: string;
  backdrop_url: string;
  /** TMDB's 0-10 vote average; 0 when nobody has voted. */
  vote_average: number;
  /** Number of TMDB votes behind `vote_average`. */
  vote_count: number;
  /** Release date (movie) or first air date (series); "" when unknown. */
  date: string;
  in_library: boolean;
  /** 0 when `in_library` is false. */
  library_id: number;
  /** A *pending* request names this title. Approved/dismissed do not count. */
  requested: boolean;
}

export type DiscoverSourceType = 'network' | 'studio';

/**
 * internal/api.discoverSourceJSON — one curated browse shelf. Deliberately
 * carries no title count: TMDB's is the whole catalogue, not what Caravan
 * holds, and a number nobody can act on is worse than no number.
 */
export interface DiscoverSource {
  id: number;
  name: string;
  type: DiscoverSourceType;
}

/** GET /discover. */
export interface DiscoverHome {
  trending: DiscoverItem[];
  popular_movies: DiscoverItem[];
  popular_series: DiscoverItem[];
  networks: DiscoverSource[];
  studios: DiscoverSource[];
}

/** GET /discover/browse — one page of a curated shelf. */
export interface DiscoverBrowse {
  source: DiscoverSource;
  page: number;
  total_pages: number;
  items: DiscoverItem[];
}

/**
 * GET /discover/movies and /discover/series — one page of a FILTERED scope
 * (phase 12).
 *
 * It carries no `source` block, unlike the curated shelf above: there is no
 * shelf behind it, the filter itself is the description, and the client already
 * holds that in its URL.
 */
export interface DiscoverScopePage {
  media_type: MediaType;
  page: number;
  total_pages: number;
  items: DiscoverItem[];
}

/** internal/api.discoverPersonJSON — one row of the cast & crew typeahead. */
export interface DiscoverPerson {
  tmdb_id: number;
  name: string;
  /** What the provider says they are best known for; "" when it does not say. */
  department: string;
  profile_url: string;
}

/** internal/api.discoverCompanyJSON — one row of the studio typeahead. */
export interface DiscoverCompany {
  tmdb_id: number;
  name: string;
  /** ISO 3166-1 country code; "" when unknown. */
  country: string;
  logo_url: string;
}

/** internal/api.discoverNamedJSON — what a keyword and a genre both are. */
export interface DiscoverNamed {
  tmdb_id: number;
  name: string;
}

/** GET /discover/genres?type= — one media type's genre vocabulary. */
export interface DiscoverGenres {
  media_type: MediaType;
  genres: DiscoverNamed[];
}

/** internal/api.castMemberJSON. */
export interface CastMember {
  tmdb_id: number;
  name: string;
  character: string;
  profile_url: string;
}

/**
 * internal/api.discoverSeasonJSON. `requested` is true when a pending request
 * covers this season — including a whole-title request, which covers them all.
 */
export interface DiscoverSeason {
  season_number: number;
  title: string;
  overview: string;
  poster_url: string;
  air_date: string;
  episode_count: number;
  in_library: boolean;
  requested: boolean;
}

/** GET /discover/{type}/{tmdbID} — the acquisition screen's payload. */
export interface DiscoverTitle extends DiscoverItem {
  status: string;
  /** Feature length (movie) or one episode's run time (series), in minutes. */
  runtime: number;
  /**
   * Originating network (series) or lead production company (movie); "" when
   * the provider names neither. One field because exactly one applies, the
   * same way `date` is a release date or a first air date.
   */
  network: string;
  /** A series' most recent air date; "" for movies and for unaired series. */
  last_aired: string;
  /** ISO 639-1 original-language code ("en"); "" when unknown. */
  language: string;
  genres: string[];
  imdb_id: string;
  tvdb_id: number;
  cast: CastMember[];
  recommendations: DiscoverItem[];
  /** Always empty for movies. */
  seasons: DiscoverSeason[];
}

export type RequestStatus = 'pending' | 'approved' | 'dismissed';

/**
 * The server's password floor (internal/api/auth.go minPasswordLength). One
 * constant for every form that collects a password, counted on the exact
 * string submitted: the server hashes the bytes it is sent, so the client
 * must not trim before counting what the server will not trim before hashing.
 */
export const MIN_PASSWORD_LENGTH = 8;

/** internal/api.requestJSON — one row of the requests screen. */
export interface MediaRequest {
  id: number;
  media_type: RequestMediaType;
  tmdb_id: number;
  /**
   * The stash-box id of a requested scene, and "" on the other two kinds —
   * whose `tmdb_id` is what names them. A scene row's `tmdb_id` is 0: the two
   * ids are separate fields because they are separate id spaces, and the server
   * enforces that exactly one of them is filled.
   */
  stash_id: string;
  title: string;
  year: number;
  poster_path: string;
  /** "" when no metadata provider is configured; the list still works. */
  poster_url: string;
  /** Whether approval adds the title to the library as monitored. */
  monitored?: boolean;
  /** null means the whole title: every movie, and an all-seasons series ask. */
  seasons: number[] | null;
  /** "" when unspecified — every series request. */
  min_availability: MinAvailability | '';
  /**
   * Who asked. "" covers three cases the screen renders identically: the row
   * predates accounts, it was made while the server ran open, or the asker has
   * since been deleted.
   */
  requested_by_username: string;
  status: RequestStatus;
  created_at: string;
  updated_at: string;
}

/**
 * Body for POST /requests. A second request for the same title merges into the
 * pending one and answers 201 with that row's id — update in place, never
 * append.
 */
export interface CreateRequestBody {
  media_type: RequestMediaType;
  /** 0 for a scene, and required (> 0) for the other two. */
  tmdb_id: number;
  /** Scene only, and required there. The server refuses the two mixed. */
  stash_id?: string;
  title: string;
  year: number;
  poster_path?: string;
  /** Series only; omitting it asks for the whole title. */
  seasons?: number[];
  /** Movies only: the release stage the asker wants the movie held for. */
  min_availability?: MinAvailability;
}

/** Body for POST /requests/{id}/approve. */
export interface ApproveRequestBody {
  /** Movies only: queue a search as soon as the add succeeds. */
  search_now: boolean;
  /** Series only; omitting it grants the whole title. */
  seasons?: number[];
  /** Movies only: override the requester's saved release stage. */
  min_availability?: MinAvailability;
  /** Optional explicit profile; omitting it (or sending 0) uses the library default. */
  quality_profile_id?: number;
  /** Whether the approved library item should stay monitored. */
  monitored?: boolean;
}

/** POST /requests/{id}/approve — the add happened, and the row is approved. */
export interface ApproveRequestResult {
  request: MediaRequest;
  movie?: Movie;
  series?: Series;
}

/* ---------------------------------------------------------------------------
 * Phase 8 — libraries as first-class objects (SPEC §7, §11 `/libraries`).
 * ------------------------------------------------------------------------- */

/**
 * internal/core.LibraryKind* — the whole item→library mapping.
 *
 * The `adult` row does not exist until the module is enabled for the first
 * time, and it survives a later disable (nothing is deleted) — so the row
 * outliving the module is exactly why GET /libraries filters rather than
 * trusting the table. The server drops it for any caller the module is not
 * visible to (internal/api.libraryVisible), which means its presence in a
 * payload IS permission to render it, and the switcher needs no adult rule of
 * its own. Enforced by TestLibrariesHideTheAdultRowWhenTheModuleIsOff.
 */
export type LibraryKind = 'movie' | 'tv' | 'adult';

/**
 * One row of a library's indexer matrix (internal/api.libraryIndexerJSON).
 *
 * `enabled` and `indexer_enabled` are separate because they are different
 * problems: `enabled` is the only one this library owns, and an indexer that is
 * off globally can never be switched back on from here.
 */
export interface LibraryIndexer {
  indexer_id: number;
  name: string;
  type: IndexerType;
  /** The indexer's own switch, read-only from the Libraries screen. */
  indexer_enabled: boolean;
  /** Whether this library searches it. True when no override row exists. */
  enabled: boolean;
  /** What a search for this library sends: the override, else the indexer's own. */
  categories: number[];
  categories_overridden: boolean;
  /** The indexer's own list — what clearing the override restores. */
  default_categories: number[];
}

/**
 * internal/api.libraryJSON.
 *
 * The override fields carry the library's OWN answer, never the resolved one:
 * `''` and `0` mean "this library does not answer, the global setting does",
 * which is exactly the distinction the screen draws between an override and a
 * global default.
 */
export interface Library {
  id: number;
  kind: LibraryKind;
  name: string;
  /** Storage-root-relative and read-only: moving it is the Storage screen's job. */
  root_path: string;
  /**
   * The chain's head, READ-ONLY: it is what a client written before chains
   * reads, and the server keeps it in step with `providers`. Write through
   * `providers` instead.
   */
  provider: string;
  /**
   * The ordered chain this library identifies new items through, each one of
   * the ids GET /libraries/providers lists. The first that recognizes a title
   * wins a scan; a search asks all of them.
   */
  providers: string[];
  /** The one library per kind that answers by-kind lookups and untargeted adds. */
  is_default: boolean;
  /** How many movies and series this library owns — what the delete guard counts. */
  item_count: number;
  dlna_visible: boolean;
  route_torrent: string;
  route_usenet: string;
  quality_profile_id: number;
  indexers: LibraryIndexer[];
}

/**
 * Body for PATCH /libraries/{id}. Every field is optional because the screen
 * saves one control at a time, and `''`/`0` clear an override rather than
 * meaning "unset". `is_default` may only be set true — a kind must always
 * have a default, so the flag moves by promoting the successor.
 */
export interface LibraryPatch {
  name?: string;
  /**
   * The pre-chain spelling, still accepted and read as a chain of one. New
   * writes send `providers`, which wins when both are present.
   */
  provider?: string;
  /** The whole ordered chain: non-empty, no duplicates, all serving the kind. */
  providers?: string[];
  is_default?: boolean;
  dlna_visible?: boolean;
  route_torrent?: string;
  route_usenet?: string;
  quality_profile_id?: number;
}

/** Body for POST /libraries. */
export interface LibraryCreate {
  kind: LibraryKind;
  name: string;
  /** Storage-root-relative, must sit under library/. */
  root_path: string;
  /** The pre-chain spelling, read as a chain of one. Empty picks the kind's default. */
  provider?: string;
  /** The ordered chain, validated as LibraryPatch.providers is. */
  providers?: string[];
}

/** One compiled-in metadata provider (GET /libraries/providers). */
export interface MetadataProviderInfo {
  id: string;
  name: string;
  /** The library kinds this provider can serve. */
  kinds: LibraryKind[];
}

/**
 * Body for PUT /libraries/{id}/indexers/{indexerID}.
 *
 * `categories: null` undoes an override — it is what an absent row already
 * means — while `[]` is the override "search unfiltered". The two are not the
 * same answer and the server stores them differently.
 */
export interface LibraryIndexerOverride {
  enabled: boolean;
  categories: number[] | null;
}

/* ---------------------------------------------------------------------------
 * Phase 9 — the adult module (SPEC §11 `/adult`).
 *
 * Every shape below comes off a route behind requireAdult, so a client that is
 * not meant to see any of it never decodes any of it: the routes answer 404,
 * byte-identical to a path that was never registered. Nothing here is filtered
 * on the client — the client is told, once, by `SessionUser.adult`, whether the
 * surface exists at all.
 *
 * A site IS a series row and a scene IS an episode row (release year = season,
 * sequence within the year = episode number). These are separate interfaces
 * anyway, because the DTOs are: a site carries no tmdb/tvdb/imdb id, no status
 * and no first-aired date, and offering those fields would invite a card to
 * render six permanently empty values.
 * ------------------------------------------------------------------------- */

/** internal/api.siteJSON — one card on the Adult grid. */
export interface Site {
  id: number;
  title: string;
  /** The provider id. It is what every /adult route accepts, never `id`. */
  stash_id: string;
  sort_title: string;
  overview: string;
  path: string;
  poster_path: string;
  poster_url: string;
  monitored: boolean;
  quality_profile_id: number;
  /** The adult library that owns the site. */
  library_id: number;
  added_at: string;
  updated_at: string;
  /** The grid's "18 / 240" badge — episode counts under this screen's nouns. */
  scene_count: number;
  scene_file_count: number;
}

/** internal/api.sceneJSON — one row on a site's page. */
export interface Scene {
  id: number;
  series_id: number;
  /** The release year, which is the season number. */
  year: number;
  /** The scene's sequence within its year — what the "#003" prefix renders. */
  number: number;
  stash_id: string;
  title: string;
  overview: string;
  studio: string;
  performers: string[];
  url: string;
  /**
   * The scene's page on the metadata endpoint's own website, "" when there is
   * none. Titles link here rather than to `url`: the provider page is the one
   * that explains what Caravan thinks the scene is.
   */
  provider_url: string;
  /** The air date under the name this screen uses: a scene is published. */
  release_date: string;
  monitored: boolean;
  file?: MediaFile | null;
}

/** internal/api.siteYearJSON — a release year and its scenes. */
export interface SiteYear {
  year: number;
  monitored: boolean;
  scenes: Scene[];
}

/** GET /adult/sites/{id}. Years and the scenes within them arrive newest first. */
export interface SiteDetail extends Site {
  /**
   * The site's page on the metadata endpoint's own website, "" when there is
   * none. The server derives it: where it points depends on which endpoint is
   * configured, and that setting is admin-only while this page is not.
   */
  provider_url: string;
  /**
   * A catalogue walk for this site is queued or running, so `years` is still
   * filling in. The walk publishes a whole release year at a time, so the page
   * polls while this is true and the years appear as they land.
   */
  cataloguing: boolean;
  years: SiteYear[];
}

/** internal/api.siteMetaJSON — a provider search hit, decorated. */
export interface SiteMeta {
  stash_id: string;
  name: string;
  /** The other names the provider knows this site by; [] when it knows none. */
  aliases: string[];
  parent_name: string;
  url: string;
  image_url: string;
  in_library: boolean;
  /** 0 when `in_library` is false. */
  library_id: number;
}

/**
 * internal/api.sceneMetaJSON — one provider scene on the discover screen.
 * `in_library`/`requested` answer the same two questions a title card does.
 */
export interface SceneMeta {
  media_type: 'scene';
  stash_id: string;
  site_stash_id: string;
  site_name: string;
  title: string;
  overview: string;
  /** The release date, "YYYY-MM-DD"; "" when the provider has none. */
  date: string;
  /** Run time in seconds; 0 when unknown. */
  duration: number;
  performers: string[];
  url: string;
  image_url: string;
  in_library: boolean;
  library_id: number;
  requested: boolean;
}

/** GET /adult/discover — one page of provider scenes. */
export interface AdultDiscoverPage {
  page: number;
  per_page: number;
  total: number;
  scenes: SceneMeta[];
}

/**
 * internal/api.sceneFilterRefJSON — one performer or tag, as the typeahead
 * hands it over and as the filter sends it back.
 *
 * `id` is a STRING and opaque: it is a numeric id on a TPDB endpoint and a
 * uuid on a stash-box one, and a client that echoes back exactly what it was
 * given never has to learn which dialect it is talking to.
 */
export interface SceneFilterRef {
  id: string;
  name: string;
}

export interface ScenePerformerMeta extends SceneFilterRef {
  image_url: string;
}

/** GET /adult/performers?q= */
export interface ScenePerformersPage {
  performers: ScenePerformerMeta[];
}

/** GET /adult/tags?q= */
export interface SceneTagsPage {
  tags: SceneFilterRef[];
}

/**
 * internal/api.adultUserJSON — one row of the member-access card.
 *
 * It is a shape of its own rather than a field on `User` because GET /users
 * carries no adult field at all: an `adult_access: false` on every row of an
 * install that never enabled the module is exactly the trace this phase
 * promises not to leave.
 */
export interface AdultUser {
  id: number;
  username: string;
  role: UserRole;
  /** The account's own grant. False and meaningless on an admin row. */
  granted: boolean;
  /** The account reaches the module through its role — every admin. */
  always_granted: boolean;
}
