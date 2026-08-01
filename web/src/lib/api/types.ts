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
  group: string;
  proper: boolean;
  repack: boolean;
  edition: string;
  /** Parser self-assessment in [0,1]. */
  confidence: number;
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
}

/**
 * GET /system/status (SPEC §11).
 *
 * Phase 1 has no download engine and no dirty-shutdown flag yet, so neither is
 * reported: the fields arrive with the phases that make them mean something
 * (SPEC §2.3, §14).
 */
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
}

/** GET/PUT /settings — the settings table is a flat key/value store (SPEC §7). */
export type Settings = Record<string, string>;

/** Known settings keys (internal/store/settings.go). */
export const SETTING_STORAGE_ROOT = 'storage_root';
export const SETTING_TMDB_API_KEY = 'tmdb_api_key';

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
  /** Indexer-side category ids; empty means "let the caller decide". */
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

/** internal/core.DownloadStatus — a live snapshot, not a persisted row. */
export interface DownloadStatus {
  /** Engine-native handle (an info hash for the embedded engine). */
  id: string;
  state: DownloadState;
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
  /**
   * Which backend holds this download (internal/core.Download.Engine). Phase 2
   * ships one engine, so the server may omit it; the queue falls back to
   * "embedded" rather than showing a blank badge.
   */
  engine?: string;
}
