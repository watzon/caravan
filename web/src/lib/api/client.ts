/**
 * Typed fetch wrappers for /api/v1 (SPEC §11, phase-1 subset).
 *
 * Every URL the SPA talks to is built from `endpoints` below, so a server-side
 * path change is a one-object edit here rather than a hunt through components.
 */

import type {
  ActivityEvent,
  AddItemRequest,
  AuthState,
  CalendarEntry,
  Conversion,
  DownloadInsight,
  DlnaStatus,
  DownloadStatus,
  GrabRequest,
  Indexer,
  IndexerCategory,
  IndexerInput,
  JellyfinConfig,
  JellyfinTestResult,
  Job,
  MatchRequest,
  Movie,
  QualityProfile,
  QualityProfileInput,
  Release,
  RepointResult,
  ScanSummary,
  SearchResults,
  Series,
  Settings,
  StorageMigration,
  StorageMigrationStatus,
  SystemStatus,
  TVProfile,
  UnmatchedFile,
  VerifyResult,
  WantedLists,
} from './types';

export const API_BASE = '/api/v1';

/** A non-2xx response, or a transport failure. */
export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(message: string, status: number, body: unknown = null) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

/** Every endpoint the phase-1 SPA uses, in one place. */
export const endpoints = {
  systemStatus: () => `${API_BASE}/system/status`,
  // Phase 5 — the portable integrity flow (SPEC §2.3, §13).
  systemShutdown: () => `${API_BASE}/system/shutdown`,
  systemVerify: () => `${API_BASE}/system/verify`,
  // Phase 5 — moving the storage root (SPEC §10). Re-pointing answers
  // synchronously; migrating answers 202 and the progress endpoint is polled.
  storageRootRepoint: () => `${API_BASE}/system/storage-root/repoint`,
  storageRootMigrate: () => `${API_BASE}/system/storage-root/migrate`,
  storageMigration: () => `${API_BASE}/system/storage-root/migration`,
  settings: () => `${API_BASE}/settings`,
  movies: () => `${API_BASE}/library/movies`,
  movie: (id: number) => `${API_BASE}/library/movies/${id}`,
  seriesList: () => `${API_BASE}/library/series`,
  series: (id: number) => `${API_BASE}/library/series/${id}`,
  season: (seriesID: number, seasonNumber: number) =>
    `${API_BASE}/library/series/${seriesID}/seasons/${seasonNumber}`,
  episode: (id: number) => `${API_BASE}/library/episodes/${id}`,
  rescan: () => `${API_BASE}/library/rescan`,
  // SPEC §11 names the scan-review queue /import/queue: it is the same queue
  // that will hold stuck downloads from phase 2 onwards.
  unmatched: () => `${API_BASE}/import/queue`,
  unmatchedItem: (id: number) => `${API_BASE}/import/queue/${id}`,
  unmatchedMatch: (id: number) => `${API_BASE}/import/queue/${id}/match`,
  search: () => `${API_BASE}/search`,
  image: (relPath: string) =>
    `${API_BASE}/images/${relPath.split('/').map(encodeURIComponent).join('/')}`,

  // Phase 2 — search & download (SPEC §5.1, §9, §11).
  indexers: () => `${API_BASE}/indexers`,
  indexer: (id: number) => `${API_BASE}/indexers/${id}`,
  indexerTest: (id: number) => `${API_BASE}/indexers/${id}/test`,
  indexerCategories: () => `${API_BASE}/indexers/categories`,
  movieReleases: (id: number) => `${API_BASE}/library/movies/${id}/releases`,
  movieGrab: (id: number) => `${API_BASE}/library/movies/${id}/grab`,
  seriesReleases: (id: number) => `${API_BASE}/library/series/${id}/releases`,
  seriesGrab: (id: number) => `${API_BASE}/library/series/${id}/grab`,
  downloads: () => `${API_BASE}/downloads`,
  downloadPause: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/pause`,
  downloadResume: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/resume`,
  download: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}`,
  downloadInsight: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/insight`,
  downloadLimits: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/limits`,
  wanted: () => `${API_BASE}/wanted`,
  events: () => `${API_BASE}/events`,
  jobs: () => `${API_BASE}/jobs`,
  calendar: () => `${API_BASE}/calendar`,
  calendarFeed: (apiKey: string) => `${API_BASE}/calendar.ics?apikey=${encodeURIComponent(apiKey)}`,
  regenerateAPIKey: () => `${API_BASE}/settings/apikey`,

  // Phase 5 — the optional single-user password (SPEC §11). The session lives
  // in an HttpOnly cookie, so no token is ever handled here.
  login: () => `${API_BASE}/auth/login`,
  logout: () => `${API_BASE}/auth/logout`,
  password: () => `${API_BASE}/settings/password`,
  tvProfiles: () => `${API_BASE}/tv-profiles`,

  // Phase 4 — the built-in DLNA media server (SPEC §5.1). Read-only: the
  // toggle and the friendly name are settings keys, saved through PUT /settings.
  dlna: () => `${API_BASE}/dlna`,

  // Phase 4 — the Jellyfin playback handoff (SPEC §5.2).
  jellyfin: () => `${API_BASE}/handoff/jellyfin`,
  jellyfinTest: () => `${API_BASE}/handoff/jellyfin/test`,

  // Phase 4 — the convert-for-TV queue (SPEC §8).
  conversions: () => `${API_BASE}/convert`,
  conversionCancel: (id: number) => `${API_BASE}/convert/${id}/cancel`,
  conversionRetry: (id: number) => `${API_BASE}/convert/${id}/retry`,
  qualityProfiles: () => `${API_BASE}/quality-profiles`,
  qualityProfile: (id: number) => `${API_BASE}/quality-profiles/${id}`,
} as const;

/**
 * Told when the server answers 401 to anything other than the login itself.
 *
 * The auth state registers here rather than being imported, so this module
 * keeps its one-way dependency: state imports the client, never the reverse.
 */
let unauthorizedHandler: (() => void) | null = null;

/** Register the 401 handler. Passing null clears it (used by tests). */
export function onUnauthorized(handler: (() => void) | null): void {
  unauthorizedHandler = handler;
}

interface RequestOptions {
  method?: string;
  body?: unknown;
  query?: Record<string, string | number | undefined>;
  signal?: AbortSignal;
}

function withQuery(path: string, query: RequestOptions['query']): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== '') params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, query, signal } = options;

  let res: Response;
  try {
    res = await fetch(withQuery(path, query), {
      method,
      signal,
      headers: body === undefined ? { Accept: 'application/json' } : {
        Accept: 'application/json',
        'Content-Type': 'application/json',
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err;
    // SPEC §13: failures are visible. Surface the transport error as an
    // ApiError with status 0 so callers have one error type to render.
    throw new ApiError(
      err instanceof Error ? err.message : 'network request failed',
      0,
    );
  }

  const payload = await readBody(res);

  if (!res.ok) {
    // A 401 means the session is missing or expired: the whole SPA has to go
    // back to the login screen, so it is handled once here rather than by
    // every caller. The login endpoint is excluded — a rejected password is a
    // form error, not a lost session.
    if (res.status === 401 && !path.startsWith(endpoints.login())) {
      unauthorizedHandler?.();
    }
    throw new ApiError(errorMessage(payload, res), res.status, payload);
  }
  return payload as T;
}

async function readBody(res: Response): Promise<unknown> {
  if (res.status === 204) return null;
  const text = await res.text();
  if (text === '') return null;
  const type = res.headers.get('content-type') ?? '';
  if (!type.includes('json')) return text;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function errorMessage(payload: unknown, res: Response): string {
  if (payload && typeof payload === 'object') {
    const rec = payload as Record<string, unknown>;
    for (const key of ['error', 'message', 'detail']) {
      const value = rec[key];
      if (typeof value === 'string' && value !== '') return value;
    }
  }
  if (typeof payload === 'string' && payload !== '') return payload;
  return `${res.status} ${res.statusText || 'request failed'}`;
}

/** Human-readable text for anything thrown by this module. */
export function errorText(err: unknown): string {
  if (err instanceof ApiError) {
    return err.status === 0 ? `Cannot reach Caravan: ${err.message}` : err.message;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * Resolve a poster for display. Provider metadata carries absolute URLs;
 * library items carry storage-root-relative paths served by the API.
 */
export function posterSrc(path: string | null | undefined): string | null {
  if (!path) return null;
  if (/^https?:\/\//i.test(path)) return path;
  return endpoints.image(path);
}

/**
 * List endpoints answer with a named envelope ({"movies": [...]}) so the API
 * can grow paging fields without breaking clients. The SPA only ever wants the
 * rows, so the envelope is unwrapped here rather than in every component.
 */
async function listOf<T>(
  path: string,
  key: string,
  signal?: AbortSignal,
): Promise<T[]> {
  const payload = await request<Record<string, T[]>>(path, { signal });
  return payload?.[key] ?? [];
}

/** How often awaitScan re-checks whether a running scan has finished. */
const SCAN_POLL_MS = 750;

/** Give up waiting on a scan after this long, so a stuck scan cannot hang the UI forever. */
const SCAN_TIMEOUT_MS = 20 * 60 * 1000;

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

export const api = {
  systemStatus: (signal?: AbortSignal) =>
    request<SystemStatus>(endpoints.systemStatus(), { signal }),

  /**
   * Stop the server so the drive can be ejected (SPEC §2.3). It answers 202 and
   * then tears itself down, so this call succeeding means the shutdown started
   * — and the very next request to this origin is expected to fail.
   */
  shutdown: () => request<{ status: string }>(endpoints.systemShutdown(), { method: 'POST' }),

  /**
   * The dirty-eject recovery action (SPEC §13): check the database, rescan the
   * library, and clear the dirty flag that is holding downloads paused. It
   * throws when the database fails its check, in which case the flag stays set.
   */
  verifyIntegrity: () => request<VerifyResult>(endpoints.systemVerify(), { method: 'POST' }),

  /**
   * Re-point the storage root (SPEC §10): change where Caravan looks without
   * moving a byte. Every stored path is relative, so this is one settings
   * write. `warnings` is advisory — a root with no library in it is a fresh
   * drive, which is allowed.
   */
  repointStorageRoot: (root: string) =>
    request<RepointResult>(endpoints.storageRootRepoint(), { method: 'POST', body: { root } }),

  /**
   * Move the library and incomplete folders to a new root. It answers with the
   * queued migration; the work happens on a durable job, so the browser can be
   * closed and the progress polled again later.
   */
  migrateStorageRoot: (root: string) =>
    request<StorageMigration>(endpoints.storageRootMigrate(), { method: 'POST', body: { root } }),

  /** The most recent storage migration, or null when none has ever run. */
  storageMigration: (signal?: AbortSignal) =>
    request<StorageMigrationStatus>(endpoints.storageMigration(), { signal }),

  getSettings: (signal?: AbortSignal) =>
    request<Settings>(endpoints.settings(), { signal }),

  putSettings: (patch: Settings) =>
    request<Settings>(endpoints.settings(), { method: 'PUT', body: patch }),

  /**
   * The built-in TV profiles (SPEC §8). Read-only: the active choice is the
   * `tv_profile` settings key, saved through putSettings like every other one.
   */
  listTVProfiles: (signal?: AbortSignal) =>
    listOf<TVProfile>(endpoints.tvProfiles(), 'profiles', signal),

  /**
   * What the built-in DLNA media server is doing (SPEC §5.1).
   *
   * `enabled` comes back from the same settings the UI writes; `advertising` is
   * the part that cannot be read from the settings table, because whether SSDP
   * came up is a fact about the host's network rather than about configuration.
   */
  dlnaStatus: (signal?: AbortSignal) => request<DlnaStatus>(endpoints.dlna(), { signal }),

  /* ------------------------------------------------------------------------
   * Jellyfin playback handoff (SPEC §5.2). The scan itself is never triggered
   * from here: the import pipeline queues it and the job queue runs it, so the
   * UI only configures the connection and proves it works.
   * --------------------------------------------------------------------- */

  jellyfinConfig: (signal?: AbortSignal) =>
    request<JellyfinConfig>(endpoints.jellyfin(), { signal }),

  saveJellyfinConfig: (body: JellyfinConfig) =>
    request<JellyfinConfig>(endpoints.jellyfin(), { method: 'POST', body }),

  /**
   * Ask the server to talk to Jellyfin with the values currently in the form,
   * before they are saved. Blank fields fall back to what is stored, so `{}`
   * tests the saved configuration.
   */
  testJellyfin: (body: Partial<Pick<JellyfinConfig, 'url' | 'api_key'>> = {}) =>
    request<JellyfinTestResult>(endpoints.jellyfinTest(), { method: 'POST', body }),

  /* ------------------------------------------------------------------------
   * Convert-for-TV queue (SPEC §8). Listing works on a server without ffmpeg;
   * every mutation answers 503 there, which is what the route's banner says.
   * --------------------------------------------------------------------- */

  listConversions: (limit = 100, signal?: AbortSignal) =>
    request<{ conversions: Conversion[] }>(endpoints.conversions(), { query: { limit }, signal })
      .then((payload) => payload?.conversions ?? []),

  /** Queue one library file. 409 means it is already in the queue. */
  convertMediaFile: (mediaFileID: number) =>
    request<Conversion>(endpoints.conversions(), {
      method: 'POST',
      body: { media_file_id: mediaFileID },
    }),

  cancelConversion: (id: number) =>
    request<Conversion>(endpoints.conversionCancel(id), { method: 'POST' }),

  retryConversion: (id: number) =>
    request<Conversion>(endpoints.conversionRetry(id), { method: 'POST' }),

  listMovies: (signal?: AbortSignal) =>
    listOf<Movie>(endpoints.movies(), 'movies', signal),

  getMovie: (id: number, signal?: AbortSignal) =>
    request<Movie>(endpoints.movie(id), { signal }),

  addMovie: (body: AddItemRequest) =>
    request<Movie>(endpoints.movies(), { method: 'POST', body }),

  setMovieMonitored: (id: number, monitored: boolean) =>
    request<Movie>(endpoints.movie(id), { method: 'PATCH', body: { monitored } }),

  listSeries: (signal?: AbortSignal) =>
    listOf<Series>(endpoints.seriesList(), 'series', signal),

  getSeries: (id: number, signal?: AbortSignal) =>
    request<Series>(endpoints.series(id), { signal }),

  addSeries: (body: AddItemRequest) =>
    request<Series>(endpoints.seriesList(), { method: 'POST', body }),

  setSeriesMonitored: (id: number, monitored: boolean) =>
    request<Series>(endpoints.series(id), { method: 'PATCH', body: { monitored } }),

  setSeasonMonitored: (seriesID: number, seasonNumber: number, monitored: boolean) =>
    request<void>(endpoints.season(seriesID, seasonNumber), {
      method: 'PATCH',
      body: { monitored },
    }),

  setEpisodeMonitored: (id: number, monitored: boolean) =>
    request<void>(endpoints.episode(id), { method: 'PATCH', body: { monitored } }),

  /**
   * Start a library scan. The server answers 202 as soon as the scan is
   * running, because walking the storage root can take minutes; use
   * `awaitScan` to wait for it to finish. A 409 means one is already running.
   */
  rescan: () => request<void>(endpoints.rescan(), { method: 'POST' }),

  /**
   * Poll until no scan is running, then report what the library holds.
   *
   * The counts come from GET /system/status rather than from the scan itself:
   * the scan runs detached from the request that started it, so its summary is
   * not addressable, and "what is in the library now" is the honest answer to
   * "did the scan do anything".
   */
  async awaitScan(signal?: AbortSignal): Promise<ScanSummary> {
    const deadline = Date.now() + SCAN_TIMEOUT_MS;
    for (;;) {
      const status = await api.systemStatus(signal);
      if (!status.scanning) {
        return {
          media_files: status.counts.media_files,
          unmatched: status.counts.unmatched,
        };
      }
      if (Date.now() > deadline) {
        throw new ApiError('the library scan is still running', 0);
      }
      await sleep(SCAN_POLL_MS);
    }
  },

  listUnmatched: (signal?: AbortSignal) =>
    listOf<UnmatchedFile>(endpoints.unmatched(), 'items', signal),

  matchUnmatched: (id: number, body: MatchRequest) =>
    request<void>(endpoints.unmatchedMatch(id), { method: 'POST', body }),

  dismissUnmatched: (id: number) =>
    request<void>(endpoints.unmatchedItem(id), { method: 'DELETE' }),

  search: (q: string, kind: 'movie' | 'series' | 'all' = 'all', signal?: AbortSignal) =>
    request<SearchResults>(endpoints.search(), { query: { q, type: kind }, signal }),

  /* ------------------------------------------------------------------------
   * Phase 2 — search & download.
   * --------------------------------------------------------------------- */

  listIndexers: (signal?: AbortSignal) =>
    listOf<Indexer>(endpoints.indexers(), 'indexers', signal),

  addIndexer: (body: IndexerInput) =>
    request<Indexer>(endpoints.indexers(), { method: 'POST', body }),

  updateIndexer: (id: number, body: IndexerInput) =>
    request<Indexer>(endpoints.indexer(id), { method: 'PUT', body }),

  deleteIndexer: (id: number) =>
    request<void>(endpoints.indexer(id), { method: 'DELETE' }),

  /**
   * Ask the server to talk to the indexer. Success is any 2xx — the body is
   * whatever the handler feels like saying — and a failure arrives as an
   * ApiError carrying the indexer's own complaint.
   */
  testIndexer: (id: number) =>
    request<void>(endpoints.indexerTest(id), { method: 'POST' }),

  /**
   * Fetch the category tree an indexer advertises. Takes the form's current
   * values rather than a stored id so the picker works while the indexer is
   * still being typed in, before it is saved.
   */
  indexerCategories: (body: Pick<IndexerInput, 'url' | 'api_key' | 'type'>, signal?: AbortSignal) =>
    request<{ categories: IndexerCategory[] }>(endpoints.indexerCategories(), {
      method: 'POST',
      body,
      signal,
    }).then((payload) => payload?.categories ?? []),

  /**
   * Interactive search (SPEC §9 step 4). The server fans out across enabled
   * indexers, so this is slow by nature; callers pass a signal and show
   * skeleton rows.
   */
  movieReleases: (id: number, signal?: AbortSignal) =>
    listOf<Release>(endpoints.movieReleases(id), 'releases', signal),

  /**
   * Episode and season search. `season`/`episode` are numbers as they appear on
   * screen, because they are what the query sent to the indexer is built from;
   * the grab that follows targets episodes by id.
   */
  seriesReleases: (
    id: number,
    opts: { season?: number; episode?: number } = {},
    signal?: AbortSignal,
  ) =>
    listOf<Release>(
      withQuery(endpoints.seriesReleases(id), {
        season: opts.season,
        episode: opts.episode,
      }),
      'releases',
      signal,
    ),

  grabForMovie: (id: number, body: GrabRequest) =>
    request<void>(endpoints.movieGrab(id), { method: 'POST', body }),

  grabForSeries: (id: number, body: GrabRequest) =>
    request<void>(endpoints.seriesGrab(id), { method: 'POST', body }),

  listDownloads: (signal?: AbortSignal) =>
    listOf<DownloadStatus>(endpoints.downloads(), 'downloads', signal),

  pauseDownload: (id: string) =>
    request<void>(endpoints.downloadPause(id), { method: 'POST' }),

  resumeDownload: (id: string) =>
    request<void>(endpoints.downloadResume(id), { method: 'POST' }),

  /**
   * Remove a download. `deleteData` false leaves the payload on disk, which is
   * the safe default everywhere in the UI: an imported file is a hardlink away
   * from this data (SPEC §13).
   */
  removeDownload: (id: string, deleteData: boolean) =>
    request<void>(endpoints.download(id), {
      method: 'DELETE',
      query: { deleteData: deleteData ? 'true' : 'false' },
    }),

  downloadInsight: (id: string, signal?: AbortSignal) =>
    request<{ insight: DownloadInsight }>(endpoints.downloadInsight(id), { signal }).then(
      (payload) => payload.insight,
    ),

  setDownloadLimits: (id: string, maxDownKbps: number, maxUpKbps: number) =>
    request<void>(endpoints.downloadLimits(id), {
      method: 'PUT',
      body: { max_down_kbps: maxDownKbps, max_up_kbps: maxUpKbps },
    }),
  wanted: (signal?: AbortSignal) =>
    request<WantedLists>(endpoints.wanted(), { signal }).then((payload) => ({
      movies: payload?.movies ?? [],
      episodes: payload?.episodes ?? [],
    })),

  listEvents: (limit = 100, signal?: AbortSignal) =>
    request<{ events: ActivityEvent[] }>(endpoints.events(), { query: { limit }, signal })
      .then((payload) => payload?.events ?? []),

  listJobs: (limit = 100, signal?: AbortSignal) =>
    request<{ jobs: Job[] }>(endpoints.jobs(), { query: { limit }, signal })
      .then((payload) => payload?.jobs ?? []),

  calendar: (start: string, end: string, signal?: AbortSignal) =>
    request<{ entries: CalendarEntry[] }>(endpoints.calendar(), {
      query: { start, end },
      signal,
    }).then((payload) => payload?.entries ?? []),

  regenerateAPIKey: () =>
    request<{ api_key: string }>(endpoints.regenerateAPIKey(), { method: 'POST' }),

  /* ------------------------------------------------------------------------
   * Phase 5 — the optional single-user password (SPEC §11).
   *
   * The session is an HttpOnly cookie the browser attaches on its own, so
   * nothing here reads or stores a token; a 401 from any other call is what
   * tells the SPA the session is gone.
   * --------------------------------------------------------------------- */

  login: (password: string) =>
    request<AuthState>(endpoints.login(), { method: 'POST', body: { password } }),

  logout: () => request<void>(endpoints.logout(), { method: 'POST' }),

  /**
   * Set, change or clear the password. An empty `newPassword` clears it;
   * `currentPassword` is required whenever one is already set.
   */
  setPassword: (currentPassword: string, newPassword: string) =>
    request<AuthState>(endpoints.password(), {
      method: 'POST',
      body: { current_password: currentPassword, new_password: newPassword },
    }),

  listQualityProfiles: (signal?: AbortSignal) =>
    listOf<QualityProfile>(endpoints.qualityProfiles(), 'profiles', signal),

  addQualityProfile: (body: QualityProfileInput) =>
    request<QualityProfile>(endpoints.qualityProfiles(), { method: 'POST', body }),

  updateQualityProfile: (id: number, body: QualityProfileInput) =>
    request<QualityProfile>(endpoints.qualityProfile(id), { method: 'PUT', body }),

  deleteQualityProfile: (id: number) =>
    request<void>(endpoints.qualityProfile(id), { method: 'DELETE' }),
};
