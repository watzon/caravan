/**
 * Typed fetch wrappers for /api/v1 (SPEC §11, phase-1 subset).
 *
 * Every URL the SPA talks to is built from `endpoints` below, so a server-side
 * path change is a one-object edit here rather than a hunt through components.
 */

import type {
  ActivityEvent,
  AddItemRequest,
  EventPage,
  AddSiteRequest,
  AdultDiscoverPage,
  AdultUser,
  ApproveRequestBody,
  ApproveRequestResult,
  AuthState,
  CalendarEntry,
  Conversion,
  ConversionQueue,
  CreateRequestBody,
  CreateUserBody,
  DiscoverBrowse,
  DiscoverCompany,
  DiscoverGenres,
  DiscoverHome,
  DiscoverNamed,
  DiscoverPerson,
  DiscoverScopePage,
  DiscoverSourceType,
  DiscoverTitle,
  DownloadClient,
  DownloadClientInput,
  DownloadClientTypeInfo,
  DownloadInsight,
  DownloadPage,
  DlnaStatus,
  DownloadStatus,
  GrabRequest,
  Indexer,
  IndexerCategory,
  IndexerInput,
  JellyfinConfig,
  JellyfinConfigInput,
  JellyfinTestResult,
  Job,
  Library,
  JobPage,
  LibraryIndexerOverride,
  LibraryPatch,
  MatchRequest,
  MediaRequest,
  MinAvailability,
  MediaType,
  Movie,
  QualityProfile,
  QualityProfileInput,
  QualityProfileTestRequest,
  QualityProfileTestResponse,
  Release,
  NotificationWebhook,
  NotificationWebhookInput,
  RemotePathMapping,
  RemotePathMappingInput,
  RepointResult,
  RequestStatus,
  RunTaskResult,
  ScanSummary,
  SearchQueued,
  SearchResults,
  SceneFilterRef,
  ScenePerformerMeta,
  Series,
  SessionUser,
  Settings,
  Site,
  SiteDetail,
  SiteMeta,
  StashConfig,
  StashTestResult,
  StorageMigration,
  StorageMigrationStatus,
  SystemStatus,
  SystemTask,
  TaskIntervalInput,
  TaskIntervalUpdate,
  TVProfile,
  UnmatchedFile,
  UsenetServer,
  UsenetServerInput,
  User,
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
  systemBackup: () => `${API_BASE}/system/backup`,
  systemRestore: () => `${API_BASE}/system/restore`,
  // Phase 5 — moving the storage root (SPEC §10). Re-pointing answers
  // synchronously; migrating answers 202 and the progress endpoint is polled.
  storageRootRepoint: () => `${API_BASE}/system/storage-root/repoint`,
  storageRootMigrate: () => `${API_BASE}/system/storage-root/migrate`,
  storageMigration: () => `${API_BASE}/system/storage-root/migration`,
  settings: () => `${API_BASE}/settings`,
  // Phase 10 — proving the TMDB key where it is typed, the same idiom as
  // POST /indexers/{id}/test. It takes the key in the body so the first-run
  // wizard can prove one that has not been saved yet.
  metadataTest: () => `${API_BASE}/settings/metadata/test`,
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

  // Phase 6 — external download clients (SPEC §5.1). downloadClientTestConfig
  // takes the form's current values, so Test works before a client is saved.
  downloadClients: () => `${API_BASE}/download-clients`,
  downloadClient: (id: number) => `${API_BASE}/download-clients/${id}`,
  downloadClientTypes: () => `${API_BASE}/download-clients/types`,
  downloadClientTest: (id: number) => `${API_BASE}/download-clients/${id}/test`,
  downloadClientTestConfig: () => `${API_BASE}/download-clients/test`,

  // A client may report a path rooted somewhere other than Caravan's host.
  remotePathMappings: () => `${API_BASE}/remote-path-mappings`,
  remotePathMapping: (id: number) => `${API_BASE}/remote-path-mappings/${id}`,

  // Phase 7 — news servers the built-in engine fetches articles from
  // (SPEC §5.1). Same shape as the download-client endpoints, including the
  // unsaved-config probe.
  usenetServers: () => `${API_BASE}/usenet-servers`,
  usenetServer: (id: number) => `${API_BASE}/usenet-servers/${id}`,
  usenetServerTest: (id: number) => `${API_BASE}/usenet-servers/${id}/test`,
  usenetServerTestConfig: () => `${API_BASE}/usenet-servers/test`,
  movieReleases: (id: number) => `${API_BASE}/library/movies/${id}/releases`,
  movieGrab: (id: number) => `${API_BASE}/library/movies/${id}/grab`,
  seriesReleases: (id: number) => `${API_BASE}/library/series/${id}/releases`,
  seriesGrab: (id: number) => `${API_BASE}/library/series/${id}/grab`,
  downloads: () => `${API_BASE}/downloads`,
  downloadPause: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/pause`,
  downloadResume: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/resume`,
  downloadRetry: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/retry`,
  download: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}`,
  downloadInsight: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/insight`,
  downloadLimits: (id: string) => `${API_BASE}/downloads/${encodeURIComponent(id)}/limits`,
  wanted: () => `${API_BASE}/wanted`,
  // On-demand automatic search (SPEC §9): the same jobs the backlog sweep
  // queues, asked for now. Distinct from the /releases pair above, which is
  // the interactive picker.
  movieSearchNow: (id: number) => `${API_BASE}/library/movies/${id}/search`,
  seriesSearchNow: (id: number) => `${API_BASE}/library/series/${id}/search`,
  episodeSearchNow: (id: number) => `${API_BASE}/library/episodes/${id}/search`,
  wantedSearch: () => `${API_BASE}/wanted/search`,
  events: () => `${API_BASE}/events`,
  jobs: () => `${API_BASE}/jobs`,
  // The recurring background tasks, their editable cadence, and the button
  // that brings the queued successor forward.
  tasks: () => `${API_BASE}/system/tasks`,
  task: (kind: string) => `${API_BASE}/system/tasks/${encodeURIComponent(kind)}`,
  runTask: (kind: string) => `${API_BASE}/system/tasks/${encodeURIComponent(kind)}/run`,
  notificationWebhooks: () => `${API_BASE}/notification-webhooks`,
  notificationWebhook: (id: number) => `${API_BASE}/notification-webhooks/${id}`,
  notificationWebhookTest: (id: number) => `${API_BASE}/notification-webhooks/${id}/test`,
  calendar: () => `${API_BASE}/calendar`,
  calendarFeed: (apiKey: string) => `${API_BASE}/calendar.ics?apikey=${encodeURIComponent(apiKey)}`,
  regenerateAPIKey: () => `${API_BASE}/settings/apikey`,

  // The first-run administrator is the one unauthenticated account write. The
  // response sets the HttpOnly session cookie used by all later setup calls.
  setupAdmin: () => `${API_BASE}/setup/admin`,
  // The optional login (SPEC §11). The session lives in an HttpOnly cookie, so
  // no token is ever handled here.
  login: () => `${API_BASE}/auth/login`,
  logout: () => `${API_BASE}/auth/logout`,
  me: () => `${API_BASE}/auth/me`,
  // Changing your own password lives under /settings for historical reasons;
  // it is the one settings route a member may reach.
  password: () => `${API_BASE}/settings/password`,

  // Accounts (SPEC §11). Admin-only, except that on a server with no accounts
  // everyone is an implicit admin — which is what makes POST /users the door
  // that closes an open Caravan.
  users: () => `${API_BASE}/users`,
  user: (id: number) => `${API_BASE}/users/${id}`,
  userPassword: (id: number) => `${API_BASE}/users/${id}/password`,

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
  qualityProfileDefault: (id: number) => `${API_BASE}/quality-profiles/${id}/default`,
  qualityProfileTest: (id: number) => `${API_BASE}/quality-profiles/${id}/test`,
  qualityProfilesExport: () => `${API_BASE}/quality-profiles/export`,
  qualityProfilesImport: () => `${API_BASE}/quality-profiles/import`,

  // Phase 8 — libraries as first-class objects (SPEC §7). Admin-only: a member
  // gets 403 from the allowlist, so nothing here is offered to one.
  libraries: () => `${API_BASE}/libraries`,
  library: (id: number) => `${API_BASE}/libraries/${id}`,
  libraryIndexer: (id: number, indexerID: number) =>
    `${API_BASE}/libraries/${id}/indexers/${indexerID}`,

  // Discover — browse the provider rather than search it. Every id in this
  // block is a TMDB id; library ids only appear in the decorated payloads.
  discover: () => `${API_BASE}/discover`,
  discoverBrowse: () => `${API_BASE}/discover/browse`,
  // The filtered scopes and the typeaheads that fill their rails (phase 12).
  // Plural, so they cannot collide with /discover/{type}/{id} below.
  discoverScope: (mediaType: MediaType) =>
    `${API_BASE}/discover/${mediaType === 'movie' ? 'movies' : 'series'}`,
  discoverPeople: () => `${API_BASE}/discover/people`,
  discoverCompanies: () => `${API_BASE}/discover/companies`,
  discoverKeywords: () => `${API_BASE}/discover/keywords`,
  discoverGenres: () => `${API_BASE}/discover/genres`,
  discoverTitle: (type: MediaType, tmdbID: number) => `${API_BASE}/discover/${type}/${tmdbID}`,

  // Requests — a wish for a title that is not in the library yet. Adding the
  // title (from anywhere) absorbs its pending request server-side.
  requests: () => `${API_BASE}/requests`,
  request: (id: number) => `${API_BASE}/requests/${id}`,
  requestApprove: (id: number) => `${API_BASE}/requests/${id}/approve`,

  // Phase 9 — the adult module. Everything under /adult sits behind the
  // server's router-level gate: with the module off, or for an account that
  // was not granted it, each of these answers 404 with the body an unrouted
  // path gets. The SPA is expected not to ask (SessionUser.adult says so), and
  // a 404 from one of them is that answer arriving late, not an error to show.
  adultSites: () => `${API_BASE}/adult/sites`,
  adultSite: (id: number) => `${API_BASE}/adult/sites/${id}`,
  adultSearch: () => `${API_BASE}/adult/search`,
  adultDiscover: () => `${API_BASE}/adult/discover`,
  adultPerformers: () => `${API_BASE}/adult/performers`,
  adultTags: () => `${API_BASE}/adult/tags`,
  adultUsers: () => `${API_BASE}/adult/users`,
  adultUserAccess: (id: number) => `${API_BASE}/adult/users/${id}/access`,
  // Phase 11 — the Stash handoff. Inside the gated subtree with the rest of
  // /adult rather than beside /handoff/jellyfin, so an adult-module setting is
  // absent for an ungranted caller rather than merely switched off.
  adultStash: () => `${API_BASE}/adult/stash`,
  adultStashTest: () => `${API_BASE}/adult/stash/test`,
  // The master switch, and the one adult route outside the gated subtree: it
  // has to be reachable while the module is off, because turning it on is what
  // it is for.
  settingsAdult: () => `${API_BASE}/settings/adult`,
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
  query?: Record<string, string | number | string[] | undefined>;
  signal?: AbortSignal;
  rawBody?: BodyInit;
  contentType?: string;
}

/**
 * Build a query string. An array value becomes one REPEATED parameter rather
 * than a comma-joined list — the scene filters are spelled that way because a
 * value there carries a name and a name may contain a comma.
 */
function withQuery(path: string, query: RequestOptions['query']): string {
  if (!query) return path;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === '') continue;
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item !== '') params.append(key, item);
      }
      continue;
    }
    params.set(key, String(value));
  }
  const qs = params.toString();
  return qs ? `${path}?${qs}` : path;
}

async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, rawBody, contentType, query, signal } = options;

  let res: Response;
  try {
    const hasBody = body !== undefined || rawBody !== undefined;
    res = await fetch(withQuery(path, query), {
      method,
      signal,
      headers: hasBody
        ? {
            Accept: 'application/json',
            'Content-Type': contentType ?? 'application/json',
          }
        : { Accept: 'application/json' },
      body: rawBody ?? (body === undefined ? undefined : JSON.stringify(body)),
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

/**
 * The stable `code` on an error envelope, or '' when the server sent none.
 *
 * The envelope grew the field in phase 10 (internal/api.errorResponse): a coded
 * error is one the SPA is expected to branch on — a missing credential has a
 * destination, not a message — while an uncoded one keeps the old contract of
 * "render what it says". Everything that reads a code lives in credentials.ts;
 * this is only the accessor, so no caller has to know the body's shape.
 */
export function errorCode(err: unknown): string {
  if (!(err instanceof ApiError)) return '';
  const body = err.body;
  if (body && typeof body === 'object') {
    const code = (body as Record<string, unknown>).code;
    if (typeof code === 'string') return code;
  }
  return '';
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

  /** Stage a Caravan SQLite backup for replacement after the next restart. */
  restoreBackup: (file: Blob) =>
    request<{ restart_required: boolean }>(endpoints.systemRestore(), {
      method: 'POST',
      rawBody: file,
      contentType: 'application/vnd.sqlite3',
    }),

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
   * Prove a TMDB API key against TMDB (PLAN phase 10 task 4).
   *
   * Passing the key tests that exact string without storing it, which is what
   * the first-run wizard and the settings field both do — so a wrong key is
   * caught before it is saved. Passing nothing tests the stored one.
   *
   * The server caches the verdict against the key's value, so testing and then
   * saving the same key costs one upstream call, not two: prefer test-then-save
   * over saving blind.
   */
  testMetadataKey: (apiKey = '') =>
    request<{ status: string }>(endpoints.metadataTest(), {
      method: 'POST',
      body: { api_key: apiKey },
    }),

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

  saveJellyfinConfig: (body: JellyfinConfigInput) =>
    request<JellyfinConfig>(endpoints.jellyfin(), { method: 'POST', body }),

  /**
   * Ask the server to talk to Jellyfin with the values currently in the form,
   * before they are saved. Blank fields fall back to what is stored, so `{}`
   * tests the saved configuration.
   */
  testJellyfin: (body: Partial<Pick<JellyfinConfigInput, 'url' | 'api_key'>> = {}) =>
    request<JellyfinTestResult>(endpoints.jellyfinTest(), { method: 'POST', body }),

  /* ------------------------------------------------------------------------
   * Convert-for-TV queue (SPEC §8). Listing works on a server without ffmpeg;
   * every mutation answers 503 there, which is what the route's banner says.
   * --------------------------------------------------------------------- */

  listConversionQueue: (limit = 100, signal?: AbortSignal) =>
    request<ConversionQueue>(endpoints.conversions(), { query: { limit }, signal }).then(
      (payload) => ({
        pending: payload?.pending ?? [],
        conversions: payload?.conversions ?? [],
      }),
    ),

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

  /**
   * Stop tracking a movie. With `files` the movie's files are deleted from
   * disk as well; without it the filesystem is untouched and a rescan re-adds
   * the movie (SPEC §1.2). The switch is a query parameter because a DELETE
   * body is not reliably sent.
   */
  deleteMovie: (id: number, files = false) =>
    request<void>(endpoints.movie(id), {
      method: 'DELETE',
      query: { files: files ? 'true' : undefined },
    }),

  listSeries: (signal?: AbortSignal) =>
    listOf<Series>(endpoints.seriesList(), 'series', signal),

  getSeries: (id: number, signal?: AbortSignal) =>
    request<Series>(endpoints.series(id), { signal }),

  addSeries: (body: AddItemRequest) =>
    request<Series>(endpoints.seriesList(), { method: 'POST', body }),

  setSeriesMonitored: (id: number, monitored: boolean) =>
    request<Series>(endpoints.series(id), { method: 'PATCH', body: { monitored } }),

  /** deleteMovie's series twin; `files` deletes every episode file too. */
  deleteSeries: (id: number, files = false) =>
    request<void>(endpoints.series(id), {
      method: 'DELETE',
      query: { files: files ? 'true' : undefined },
    }),

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
  indexerCategories: (
    body: Pick<IndexerInput, 'url' | 'type'> & { api_key: string },
    signal?: AbortSignal,
  ) =>
    request<{ categories: IndexerCategory[] }>(endpoints.indexerCategories(), {
      method: 'POST',
      body,
      signal,
    }).then((payload) => payload?.categories ?? []),

  /* ---------------------------------------------------------------------
   * Phase 6 — external download clients (SPEC §5.1, §11).
   * ------------------------------------------------------------------- */

  listDownloadClients: (signal?: AbortSignal) =>
    listOf<DownloadClient>(endpoints.downloadClients(), 'download_clients', signal),

  /**
   * Which backends this build can be configured with, and which of them it can
   * actually probe. Served rather than hard-coded so a build without a backend
   * says so instead of offering it.
   */
  downloadClientTypes: (signal?: AbortSignal) =>
    listOf<DownloadClientTypeInfo>(endpoints.downloadClientTypes(), 'types', signal),

  addDownloadClient: (body: DownloadClientInput) =>
    request<DownloadClient>(endpoints.downloadClients(), { method: 'POST', body }),

  updateDownloadClient: (id: number, body: DownloadClientInput) =>
    request<DownloadClient>(endpoints.downloadClient(id), { method: 'PUT', body }),

  deleteDownloadClient: (id: number) =>
    request<void>(endpoints.downloadClient(id), { method: 'DELETE' }),

  /** Probe a stored client with its stored credentials. */
  testDownloadClient: (id: number) =>
    request<void>(endpoints.downloadClientTest(id), { method: 'POST' }),

  /**
   * Probe what is on screen, before it is saved. Include `id` when editing a
   * stored client: the credential was never handed back, so a blank one falls
   * back to that row's.
   */
  testDownloadClientConfig: (body: DownloadClientInput) =>
    request<void>(endpoints.downloadClientTestConfig(), { method: 'POST', body }),

  /* ---------------------------------------------------------------------
   * Remote path mappings (SPEC §11).
   * ------------------------------------------------------------------- */

  listRemotePathMappings: (signal?: AbortSignal) =>
    listOf<RemotePathMapping>(endpoints.remotePathMappings(), 'remote_path_mappings', signal),

  addRemotePathMapping: (body: RemotePathMappingInput) =>
    request<RemotePathMapping>(endpoints.remotePathMappings(), { method: 'POST', body }),

  updateRemotePathMapping: (id: number, body: RemotePathMappingInput) =>
    request<RemotePathMapping>(endpoints.remotePathMapping(id), { method: 'PUT', body }),

  deleteRemotePathMapping: (id: number) =>
    request<void>(endpoints.remotePathMapping(id), { method: 'DELETE' }),

  /* ---------------------------------------------------------------------
   * Notification webhooks.
   * ------------------------------------------------------------------- */

  listNotificationWebhooks: (signal?: AbortSignal) =>
    listOf<NotificationWebhook>(endpoints.notificationWebhooks(), 'notification_webhooks', signal),

  addNotificationWebhook: (body: NotificationWebhookInput) =>
    request<NotificationWebhook>(endpoints.notificationWebhooks(), { method: 'POST', body }),

  updateNotificationWebhook: (id: number, body: NotificationWebhookInput) =>
    request<NotificationWebhook>(endpoints.notificationWebhook(id), { method: 'PUT', body }),

  deleteNotificationWebhook: (id: number) =>
    request<void>(endpoints.notificationWebhook(id), { method: 'DELETE' }),

  testNotificationWebhook: (id: number) =>
    request<void>(endpoints.notificationWebhookTest(id), { method: 'POST' }),

  /* ---------------------------------------------------------------------
   * Phase 7 — news servers for the built-in engine (SPEC §5.1, §11).
   * ------------------------------------------------------------------- */

  listUsenetServers: (signal?: AbortSignal) =>
    listOf<UsenetServer>(endpoints.usenetServers(), 'usenet_servers', signal),

  addUsenetServer: (body: UsenetServerInput) =>
    request<UsenetServer>(endpoints.usenetServers(), { method: 'POST', body }),

  updateUsenetServer: (id: number, body: UsenetServerInput) =>
    request<UsenetServer>(endpoints.usenetServer(id), { method: 'PUT', body }),

  deleteUsenetServer: (id: number) =>
    request<void>(endpoints.usenetServer(id), { method: 'DELETE' }),

  /** Connect and authenticate against a stored server with its stored password. */
  testUsenetServer: (id: number) =>
    request<void>(endpoints.usenetServerTest(id), { method: 'POST' }),

  /**
   * Probe what is on screen, before it is saved. Include `id` when editing a
   * stored server: the password was never handed back, so a blank one falls
   * back to that row's — but only while host, port and TLS still match it.
   */
  testUsenetServerConfig: (body: UsenetServerInput) =>
    request<void>(endpoints.usenetServerTestConfig(), { method: 'POST', body }),

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

  listDownloadsPage: (limit = 100, cursor?: string, signal?: AbortSignal) =>
    request<DownloadPage>(endpoints.downloads(), { query: { limit, cursor }, signal }).then((payload) => ({
      downloads: payload?.downloads ?? [],
      next_cursor: payload?.next_cursor ?? '',
    })),

  listDownloads: async (signal?: AbortSignal) => {
    const downloads: DownloadStatus[] = [];
    let cursor: string | undefined;
    do {
      const page = await api.listDownloadsPage(100, cursor, signal);
      downloads.push(...page.downloads);
      cursor = page.next_cursor || undefined;
    } while (cursor);
    return downloads;
  },

  pauseDownload: (id: string) =>
    request<void>(endpoints.downloadPause(id), { method: 'POST' }),

  resumeDownload: (id: string) =>
    request<void>(endpoints.downloadResume(id), { method: 'POST' }),

  /**
   * Put a failed download back to work, picking up from whatever stage it got
   * to. Only the built-in Usenet engine can: a Usenet download is several
   * stages and a failure belongs to one of them, where a torrent's failures are
   * about the swarm and resume already covers them. An engine that cannot
   * answers 400, and a download that has not failed answers 409.
   */
  retryDownload: (id: string) =>
    request<void>(endpoints.downloadRetry(id), { method: 'POST' }),

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

  /**
   * Queue the automatic search for one movie. Answers how many jobs it added:
   * 0 when the movie already meets its cutoff, or when the same search is
   * still on the queue.
   */
  searchMovieNow: (id: number) =>
    request<SearchQueued>(endpoints.movieSearchNow(id), { method: 'POST' }),

  /** Queue a search for every wanted episode of one series. */
  searchSeriesNow: (id: number) =>
    request<SearchQueued>(endpoints.seriesSearchNow(id), { method: 'POST' }),

  /** Queue an automatic search for exactly one wanted episode. */
  searchEpisodeNow: (id: number) =>
    request<SearchQueued>(endpoints.episodeSearchNow(id), { method: 'POST' }),

  /** Queue a search for the whole wanted list — the backlog sweep on demand. */
  searchWanted: () =>
    request<SearchQueued>(endpoints.wantedSearch(), { method: 'POST' }),
  listEventsPage: (limit = 100, cursor?: string, signal?: AbortSignal) =>
    request<EventPage>(endpoints.events(), { query: { limit, cursor }, signal }).then((payload) => ({
      events: payload?.events ?? [],
      next_cursor: payload?.next_cursor ?? '',
    })),

  listEvents: (limit = 100, signal?: AbortSignal) =>
    request<{ events: ActivityEvent[] }>(endpoints.events(), { query: { limit }, signal })
      .then((payload) => payload?.events ?? []),

  listJobsPage: (limit = 100, cursor?: string, signal?: AbortSignal) =>
    request<JobPage>(endpoints.jobs(), { query: { limit, cursor }, signal }).then((payload) => ({
      jobs: payload?.jobs ?? [],
      next_cursor: payload?.next_cursor ?? '',
    })),

  listJobs: (limit = 100, signal?: AbortSignal) =>
    request<{ jobs: Job[] }>(endpoints.jobs(), { query: { limit }, signal })
      .then((payload) => payload?.jobs ?? []),

  /** The recurring background tasks and where each one is in its cycle. */
  listTasks: (signal?: AbortSignal) =>
    request<{ tasks: SystemTask[] }>(endpoints.tasks(), { signal })
      .then((payload) => payload?.tasks ?? []),

  /** Bring a recurring task's next run forward to now. */
  runTask: (kind: string) =>
    request<RunTaskResult>(endpoints.runTask(kind), { method: 'POST' }),

  /** Change a recurring task's cadence in whole minutes. */
  updateTaskInterval: (kind: string, body: TaskIntervalInput) =>
    request<TaskIntervalUpdate>(endpoints.task(kind), {
      method: 'PUT',
      body,
    }),

  calendar: (start: string, end: string, signal?: AbortSignal) =>
    request<{ entries: CalendarEntry[] }>(endpoints.calendar(), {
      query: { start, end },
      signal,
    }).then((payload) => payload?.entries ?? []),

  regenerateAPIKey: () =>
    request<{ api_key: string }>(endpoints.regenerateAPIKey(), { method: 'POST' }),

  /* ------------------------------------------------------------------------
   * Accounts and the optional login (SPEC §11).
   *
   * The session is an HttpOnly cookie the browser attaches on its own, so
   * nothing here reads or stores a token; a 401 from any other call is what
   * tells the SPA the session is gone.
   * --------------------------------------------------------------------- */
  setupAdmin: (username: string, password: string) =>
    request<AuthState>(endpoints.setupAdmin(), { method: 'POST', body: { username, password } }),

  login: (username: string, password: string) =>
    request<AuthState>(endpoints.login(), { method: 'POST', body: { username, password } }),

  logout: () => request<void>(endpoints.logout(), { method: 'POST' }),

  /** Who this browser is talking as, and whether the server has any accounts. */
  me: (signal?: AbortSignal) => request<SessionUser>(endpoints.me(), { signal }),

  /**
   * Change the calling account's own password. It can only ever touch the
   * caller — resetting somebody else's is `resetUserPassword` — and it needs a
   * signed-in account, so it fails on a server that is still open.
   */
  setPassword: (currentPassword: string, newPassword: string) =>
    request<AuthState>(endpoints.password(), {
      method: 'POST',
      body: { current_password: currentPassword, new_password: newPassword },
    }),

  /** Every account, without a hash between them. */
  listUsers: (signal?: AbortSignal) => listOf<User>(endpoints.users(), 'users', signal),

  /** 409 means the username is taken — case-insensitively, as logins are. */
  createUser: (body: CreateUserBody) =>
    request<User>(endpoints.users(), { method: 'POST', body }),

  /**
   * Delete an account and sign out every browser holding it. 409 means it was
   * the last admin: a server with no admin can never be administered again.
   */
  deleteUser: (id: number) => request<void>(endpoints.user(id), { method: 'DELETE' }),

  /** Set someone else's password without proving the old one, and sign them out. */
  resetUserPassword: (id: number, newPassword: string) =>
    request<void>(endpoints.userPassword(id), {
      method: 'POST',
      body: { new_password: newPassword },
    }),

  listQualityProfiles: (signal?: AbortSignal) =>
    listOf<QualityProfile>(endpoints.qualityProfiles(), 'profiles', signal),

  addQualityProfile: (body: QualityProfileInput) =>
    request<QualityProfile>(endpoints.qualityProfiles(), { method: 'POST', body }),

  updateQualityProfile: (id: number, body: QualityProfileInput) =>
    request<QualityProfile>(endpoints.qualityProfile(id), { method: 'PUT', body }),

  setDefaultQualityProfile: (id: number) =>
    request<QualityProfile>(endpoints.qualityProfileDefault(id), { method: 'PUT' }),

  testQualityProfile: (id: number, body: QualityProfileTestRequest) =>
    request<QualityProfileTestResponse>(endpoints.qualityProfileTest(id), {
      method: 'POST',
      body,
    }),

  deleteQualityProfile: (id: number) =>
    request<void>(endpoints.qualityProfile(id), { method: 'DELETE' }),

  exportQualityProfilesURL: () => endpoints.qualityProfilesExport(),

  importQualityProfiles: (body: {
    version: 1;
    default_profile: string;
    profiles: QualityProfileInput[];
  }) =>
    request<{ profiles: number }>(endpoints.qualityProfilesImport(), { method: 'POST', body }),

  /* ------------------------------------------------------------------------
   * Phase 8 — libraries.
   *
   * Both writes answer with the library's whole state, so one response
   * re-renders every card rather than the screen guessing what a write did to
   * the rest of them.
   * --------------------------------------------------------------------- */

  listLibraries: (signal?: AbortSignal) =>
    listOf<Library>(endpoints.libraries(), 'libraries', signal),

  updateLibrary: (id: number, body: LibraryPatch) =>
    request<Library>(endpoints.library(id), { method: 'PATCH', body }),

  setLibraryIndexer: (id: number, indexerID: number, body: LibraryIndexerOverride) =>
    request<Library>(endpoints.libraryIndexer(id, indexerID), { method: 'PUT', body }),

  /* ------------------------------------------------------------------------
   * Discover & requests.
   *
   * 503 means no metadata provider is configured (send the user to settings);
   * 502 means the provider is unhappy (offer a retry). Both arrive as an
   * ApiError, so the routes branch on `status` rather than on the message.
   * --------------------------------------------------------------------- */

  /** The discover landing page. Three sequential provider calls — show a skeleton. */
  discoverHome: (signal?: AbortSignal) =>
    request<DiscoverHome>(endpoints.discover(), { signal }),

  /**
   * One page of a curated shelf. The media type follows the shelf — a network
   * browses series, a studio browses movies — so it is not a separate param.
   */
  discoverBrowse: (
    type: DiscoverSourceType,
    id: number,
    page = 1,
    signal?: AbortSignal,
  ) =>
    request<DiscoverBrowse>(endpoints.discoverBrowse(), {
      query: { type, id, page },
      signal,
    }),

  /**
   * One page of a filtered scope (phase 12).
   *
   * The query is built by lib/explore.ts and passed through: the endpoint
   * ALLOWLISTS its parameters and answers 400 to anything else, including a
   * filter the other scope serves, so nothing may be added here that the
   * filter model did not decide to send.
   */
  discoverScope: (
    mediaType: MediaType,
    query: Record<string, string | number | string[] | undefined>,
    signal?: AbortSignal,
  ) => request<DiscoverScopePage>(endpoints.discoverScope(mediaType), { query, signal }),

  /** The filter rail's typeaheads. Pure passthroughs — nothing is decorated. */
  discoverPeople: (query: string, signal?: AbortSignal) =>
    listOf<DiscoverPerson>(withQuery(endpoints.discoverPeople(), { q: query }), 'people', signal),

  discoverCompanies: (query: string, signal?: AbortSignal) =>
    listOf<DiscoverCompany>(
      withQuery(endpoints.discoverCompanies(), { q: query }),
      'companies',
      signal,
    ),

  discoverKeywords: (query: string, signal?: AbortSignal) =>
    listOf<DiscoverNamed>(
      withQuery(endpoints.discoverKeywords(), { q: query }),
      'keywords',
      signal,
    ),

  /**
   * One media type's genre vocabulary. `type` is required by the server: the
   * two lists differ and neither is a subset of the other, so a rail showing
   * the movie genres over a series scope would offer filters matching nothing.
   */
  discoverGenres: (mediaType: MediaType, signal?: AbortSignal) =>
    request<DiscoverGenres>(endpoints.discoverGenres(), {
      query: { type: mediaType === 'movie' ? 'movie' : 'series' },
      signal,
    }),

  /** One title's acquisition screen, addressed by TMDB id (never a library id). */
  discoverTitle: (type: MediaType, tmdbID: number, signal?: AbortSignal) =>
    request<DiscoverTitle>(endpoints.discoverTitle(type, tmdbID), { signal }),

  /** Every request, newest first. Pass a status to narrow it. */
  listRequests: (status?: RequestStatus, signal?: AbortSignal) =>
    listOf<MediaRequest>(withQuery(endpoints.requests(), { status }), 'requests', signal),

  /**
   * Record a request. A second one for the same title merges into the pending
   * row and answers 201 with *that* row — replace it locally, do not append.
   * 409 means the title is already in the library: the view is stale.
   */
  createRequest: (body: CreateRequestBody) =>
    request<MediaRequest>(endpoints.requests(), { method: 'POST', body }),

  /**
   * Grant a request by adding its title, the same path the add button takes.
   * An explicit profile is persisted by this add, before any series search is
   * queued by the caller.
   */
  approveRequest: (
    id: number,
    searchNow: boolean,
    seasons?: number[],
    minAvailability?: MinAvailability,
    qualityProfileID?: number,
    monitored?: boolean,
  ) => {
    const body: ApproveRequestBody = {
      search_now: searchNow,
      ...(seasons ? { seasons } : {}),
      ...(minAvailability ? { min_availability: minAvailability } : {}),
      ...(qualityProfileID !== undefined && qualityProfileID > 0
        ? { quality_profile_id: qualityProfileID }
        : {}),
      ...(monitored !== undefined ? { monitored } : {}),
    };
    return request<ApproveRequestResult>(endpoints.requestApprove(id), { method: 'POST', body });
  },

  /** Turn a request down. The row survives as dismissed history. */
  dismissRequest: (id: number) =>
    request<void>(endpoints.request(id), { method: 'DELETE' }),

  /** Re-assign an existing library item's quality profile. */
  setMovieQualityProfile: (id: number, profileID: number) =>
    request<Movie>(endpoints.movie(id), {
      method: 'PATCH',
      body: { quality_profile_id: profileID },
    }),

  setSeriesQualityProfile: (id: number, profileID: number) =>
    request<Series>(endpoints.series(id), {
      method: 'PATCH',
      body: { quality_profile_id: profileID },
    }),

  /**
   * Change the release stage a movie's automatic search waits for. The choice
   * made at add time is not final: the movie detail screen edits it here.
   */
  setMovieMinAvailability: (id: number, minAvailability: MinAvailability) =>
    request<Movie>(endpoints.movie(id), {
      method: 'PATCH',
      body: { min_availability: minAvailability },
    }),

  /* ------------------------------------------------------------------------
   * Phase 9 — the adult module.
   *
   * 503 means no stash-box credential is configured (send the user to
   * Settings → Adult content); 502 means the provider is unhappy. Both arrive
   * as an ApiError, same as the TMDB-backed screens, so callers branch on
   * `status`. A 404 means the module is not visible to this caller — which is
   * indistinguishable from the route not existing, deliberately.
   * --------------------------------------------------------------------- */

  /**
   * Turn the module on or off. The first enable creates the Adult library row
   * (hidden from DLNA); a disable deletes nothing, so turning it back on finds
   * the sites, the scenes and the files exactly as they were.
   *
   * An enable carries the credential it is made with (PLAN phase 10 task 5):
   * the server proves the stash-box endpoint and key BEFORE it writes anything
   * and commits `adult_enabled` last, so a credential that does not work leaves
   * the endpoint, the key and the switch byte-identical. Omitting either field
   * means "use what is stored", which is what re-enabling a module that was
   * configured once and switched off should send.
   *
   * A failure arrives as an ApiError coded `adult_credential_absent` (nothing
   * to authenticate with) or `adult_credential_invalid` (the endpoint refused
   * it) — see credentials.ts.
   */
  setAdultEnabled: (
    enabled: boolean,
    credential?: { endpoint?: string; apiKey?: string },
  ) =>
    request<{ enabled: boolean }>(endpoints.settingsAdult(), {
      method: 'POST',
      body: {
        enabled,
        ...(credential?.endpoint === undefined ? {} : { stashbox_endpoint: credential.endpoint }),
        ...(credential?.apiKey === undefined ? {} : { stashbox_api_key: credential.apiKey }),
      },
    }),

  /** Every site in the library, with the scene counts the grid badges. */
  listSites: (signal?: AbortSignal) => listOf<Site>(endpoints.adultSites(), 'sites', signal),

  /** One site's page: release years as seasons, each holding its scenes. */
  getSite: (id: number, signal?: AbortSignal) =>
    request<SiteDetail>(endpoints.adultSite(id), { signal }),

  /**
   * Add a site by stash-box id.
   *
   * It answers as soon as the site row exists; the catalogue walk that fills in
   * its scenes is a background job, so the caller's success message should say
   * so rather than implying the site is complete.
   */
  addSite: (body: AddSiteRequest) =>
    request<Site>(endpoints.adultSites(), { method: 'POST', body }),

  /** Ask the provider for sites to add — the adult twin of GET /search. */
  searchSites: (query: string, signal?: AbortSignal) =>
    listOf<SiteMeta>(withQuery(endpoints.adultSearch(), { q: query }), 'sites', signal),

  /**
   * One page of provider scenes, decorated with owned/requested state.
   *
   * The query is built by lib/explore.ts. As on the title scopes it is an
   * allowlist server-side, and there is a second refusal beyond that one: a
   * filter the CONFIGURED endpoint cannot express (a widened site scope off
   * TPDB, say) answers 400 naming the filter rather than a quietly unfiltered
   * page. Callers render that message.
   */
  adultDiscover: (
    query: Record<string, string | number | string[] | undefined>,
    signal?: AbortSignal,
  ) => request<AdultDiscoverPage>(endpoints.adultDiscover(), { query, signal }),

  /** The scene rail's typeaheads. Ids are opaque strings — echo them back. */
  adultPerformers: (query: string, signal?: AbortSignal) =>
    listOf<ScenePerformerMeta>(
      withQuery(endpoints.adultPerformers(), { q: query }),
      'performers',
      signal,
    ),

  adultTags: (query: string, signal?: AbortSignal) =>
    listOf<SceneFilterRef>(withQuery(endpoints.adultTags(), { q: query }), 'tags', signal),

  /** The member-access card: every account and whether it reaches the module. */
  listAdultUsers: (signal?: AbortSignal) =>
    listOf<AdultUser>(endpoints.adultUsers(), 'users', signal),

  /**
   * Grant or revoke one account's access. It takes effect on that account's
   * very next request — the grant is not a credential, so there is no session
   * to invalidate and nobody gets signed out.
   */
  setAdultAccess: (id: number, granted: boolean) =>
    request<AdultUser>(endpoints.adultUserAccess(id), { method: 'PUT', body: { granted } }),

  /* ------------------------------------------------------------------------
   * Phase 11 — the Stash handoff (SPEC §5.2's adult twin). Like Jellyfin's,
   * the scan is never triggered from here: an adult import queues it and the
   * job queue runs it, so the UI only configures the connection and proves it.
   * --------------------------------------------------------------------- */

  stashConfig: (signal?: AbortSignal) => request<StashConfig>(endpoints.adultStash(), { signal }),

  saveStashConfig: (body: StashConfig) =>
    request<StashConfig>(endpoints.adultStash(), { method: 'POST', body }),

  /**
   * Talk to Stash with the values currently in the form, before they are
   * saved. Blank fields fall back to what is stored, so `{}` tests the saved
   * configuration.
   */
  testStash: (body: Partial<Pick<StashConfig, 'url' | 'api_key'>> = {}) =>
    request<StashTestResult>(endpoints.adultStashTest(), { method: 'POST', body }),
};
