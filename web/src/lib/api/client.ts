/**
 * Typed fetch wrappers for /api/v1 (SPEC §11, phase-1 subset).
 *
 * Every URL the SPA talks to is built from `endpoints` below, so a server-side
 * path change is a one-object edit here rather than a hunt through components.
 */

import type {
  AddItemRequest,
  DownloadStatus,
  GrabRequest,
  Indexer,
  IndexerInput,
  MatchRequest,
  Movie,
  Release,
  ScanSummary,
  SearchResults,
  Series,
  Settings,
  SystemStatus,
  UnmatchedFile,
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
  movieReleases: (id: number) => `${API_BASE}/library/movies/${id}/releases`,
  movieGrab: (id: number) => `${API_BASE}/library/movies/${id}/grab`,
  seriesReleases: (id: number) => `${API_BASE}/library/series/${id}/releases`,
  seriesGrab: (id: number) => `${API_BASE}/library/series/${id}/grab`,
  downloads: () => `${API_BASE}/downloads`,
  downloadPause: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/pause`,
  downloadResume: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/resume`,
  download: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}`,
} as const;

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

  getSettings: (signal?: AbortSignal) =>
    request<Settings>(endpoints.settings(), { signal }),

  putSettings: (patch: Settings) =>
    request<Settings>(endpoints.settings(), { method: 'PUT', body: patch }),

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
};
