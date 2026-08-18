/**
 * The phase-2 half of the API client (SPEC §11). These assert the wire: the
 * exact URL, method and body the SPA sends, because that is the contract the Go
 * handlers are written against and a typo here is invisible until runtime.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api, endpoints } from './client';

interface Call {
  url: string;
  method: string;
  body: unknown;
}

let calls: Call[];

/** Stub fetch with a fixed JSON reply and record what was sent. */
function stubFetch(body: unknown, status = 200) {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({
        url: String(input),
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      return new Response(body === null ? '' : JSON.stringify(body), {
        status,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

function only(): Call {
  expect(calls).toHaveLength(1);
  return calls[0] as Call;
}

beforeEach(() => {
  calls = [];
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('endpoints', () => {
  it('builds the phase-2 paths under /api/v1', () => {
    expect(endpoints.indexers()).toBe('/api/v1/indexers');
    expect(endpoints.indexer(3)).toBe('/api/v1/indexers/3');
    expect(endpoints.indexerCatalog()).toBe('/api/v1/indexers/catalog');
    expect(endpoints.indexerTest(3)).toBe('/api/v1/indexers/3/test');
    expect(endpoints.indexerTestConfig()).toBe('/api/v1/indexers/test');
    expect(endpoints.indexerStoredCategories(3)).toBe('/api/v1/indexers/3/categories');
    expect(endpoints.movieReleases(7)).toBe('/api/v1/library/movies/7/releases');
    expect(endpoints.movieGrab(7)).toBe('/api/v1/library/movies/7/grab');
    expect(endpoints.seriesReleases(9)).toBe('/api/v1/library/series/9/releases');
    expect(endpoints.seriesGrab(9)).toBe('/api/v1/library/series/9/grab');
    expect(endpoints.episodeSearchNow(11)).toBe('/api/v1/library/episodes/11/search');
    expect(endpoints.downloads()).toBe('/api/v1/downloads');
    expect(endpoints.conversions()).toBe('/api/v1/convert');
    expect(endpoints.conversionCancel(4)).toBe('/api/v1/convert/4/cancel');
    expect(endpoints.conversionRetry(4)).toBe('/api/v1/convert/4/retry');
    expect(endpoints.definitionPacks()).toBe('/api/v1/definition-packs');
    expect(endpoints.definitionPacksPreview()).toBe('/api/v1/definition-packs/preview');
    expect(endpoints.definitionPacksInstall()).toBe('/api/v1/definition-packs/install');
    expect(endpoints.definitionPacksActivate()).toBe('/api/v1/definition-packs/activate');
    expect(endpoints.definitionPacksRollback()).toBe('/api/v1/definition-packs/rollback');
  });

  it('builds remote path mapping and editable task paths', () => {
    expect(endpoints.remotePathMappings()).toBe('/api/v1/remote-path-mappings');
    expect(endpoints.remotePathMapping(6)).toBe('/api/v1/remote-path-mappings/6');
    expect(endpoints.task('rss/sync')).toBe('/api/v1/system/tasks/rss%2Fsync');
  });

  it('escapes the engine-native download id, which is not a number', () => {
    // A DownloadID is whatever the engine calls it; SABnzbd ids contain
    // characters that would otherwise change the path.
    expect(endpoints.download('a/b?c')).toBe('/api/v1/downloads/a%2Fb%3Fc');
    expect(endpoints.downloadPause('a/b')).toBe('/api/v1/downloads/a%2Fb/pause');
    expect(endpoints.downloadResume('a/b')).toBe('/api/v1/downloads/a%2Fb/resume');
  });

  it('builds a provider scene URL with an encoded id and provider query', () => {
    expect(endpoints.adultScene('stashbox:tpdb', 'scene/a')).toBe(
      '/api/v1/adult/scenes/scene%2Fa?provider=stashbox%3Atpdb',
    );
  });
});

describe('playback targets', () => {
  it('unwraps the profiles envelope', async () => {
    stubFetch({ profiles: [{ id: 'safe' }] });
    const list = await api.listTVProfiles();
    expect(list).toHaveLength(1);
    expect(only().url).toBe('/api/v1/tv-profiles');
  });
});

describe('adult provider scenes', () => {
  it('reads one scene through the provider-qualified wire contract', async () => {
    stubFetch({ stash_id: 'scene/a', provider: 'stashbox:tpdb', code: 'ABC-123' });
    const scene = await api.adultScene('stashbox:tpdb', 'scene/a');

    expect(scene.code).toBe('ABC-123');
    expect(only()).toMatchObject({
      method: 'GET',
      url: '/api/v1/adult/scenes/scene%2Fa?provider=stashbox%3Atpdb',
    });
  });
});

describe('dlna media server', () => {
  it('reads its status from a read-only endpoint', async () => {
    stubFetch({
      enabled: true,
      friendly_name: 'Caravan',
      uuid: 'abc',
      advertising: true,
      error: '',
    });
    const status = await api.dlnaStatus();
    expect(status.advertising).toBe(true);
    expect(only()).toMatchObject({ method: 'GET', url: '/api/v1/dlna' });
  });

  it('saves the toggle through the ordinary settings flow', async () => {
    stubFetch({ dlna_enabled: 'false', dlna_friendly_name: 'Den TV' });
    await api.putSettings({ dlna_enabled: 'false', dlna_friendly_name: 'Den TV' });
    expect(only()).toMatchObject({
      method: 'PUT',
      url: '/api/v1/settings',
      body: { dlna_enabled: 'false', dlna_friendly_name: 'Den TV' },
    });
  });
});

describe('jellyfin handoff', () => {
  it('reads the configuration from its own endpoint', async () => {
    stubFetch({ url: 'http://jellyfin.lan:8096', api_key: 'k', enabled: true });
    const cfg = await api.jellyfinConfig();
    expect(cfg.enabled).toBe(true);
    expect(only()).toMatchObject({ method: 'GET', url: '/api/v1/handoff/jellyfin' });
  });

  it('saves the configuration as one POST rather than three settings keys', async () => {
    stubFetch({ url: 'http://jellyfin.lan:8096', api_key: 'k', enabled: true });
    await api.saveJellyfinConfig({ url: 'http://jellyfin.lan:8096', api_key: 'k', enabled: true });
    expect(only()).toMatchObject({
      method: 'POST',
      url: '/api/v1/handoff/jellyfin',
      body: { url: 'http://jellyfin.lan:8096', api_key: 'k', enabled: true },
    });
  });

  it('tests unsaved credentials and reports what the server said', async () => {
    stubFetch({ server_name: 'basement', version: '10.9.11' });
    const info = await api.testJellyfin({ url: 'http://media-box:8096', api_key: 'typed' });
    expect(info.server_name).toBe('basement');
    expect(only()).toMatchObject({
      method: 'POST',
      url: '/api/v1/handoff/jellyfin/test',
      body: { url: 'http://media-box:8096', api_key: 'typed' },
    });
  });

  it('tests the stored configuration when given nothing', async () => {
    stubFetch({ server_name: 'basement', version: '10.9.11' });
    await api.testJellyfin();
    expect(only().body).toEqual({});
  });
});

describe('convert queue', () => {
  it('unwraps pending files and conversions and passes the limit', async () => {
    stubFetch({
      pending: [{ id: 2, path: 'library/Movies/B.mkv' }],
      conversions: [{ id: 1, status: 'queued' }],
    });
    const queue = await api.listConversionQueue(25);
    expect(queue.pending).toHaveLength(1);
    expect(queue.conversions).toHaveLength(1);
    expect(only().url).toBe('/api/v1/convert?limit=25');
  });

  it('reads an empty envelope as empty pending and conversion lists', async () => {
    stubFetch({});
    await expect(api.listConversionQueue()).resolves.toEqual({ pending: [], conversions: [] });
  });

  it('queues a file by media file id', async () => {
    stubFetch({ id: 4, status: 'queued' });
    await api.convertMediaFile(42);
    expect(only()).toMatchObject({
      method: 'POST',
      url: '/api/v1/convert',
      body: { media_file_id: 42 },
    });
  });

  it('cancels and retries by conversion id', async () => {
    stubFetch({ id: 4, status: 'cancelled' });
    await api.cancelConversion(4);
    expect(only()).toMatchObject({ method: 'POST', url: '/api/v1/convert/4/cancel' });

    stubFetch({ id: 4, status: 'queued' });
    await api.retryConversion(4);
    expect(only()).toMatchObject({ method: 'POST', url: '/api/v1/convert/4/retry' });
  });

  it('surfaces the 503 an ffmpeg-less server answers a queue attempt with', async () => {
    stubFetch({ error: 'ffmpeg is not installed' }, 503);
    await expect(api.convertMediaFile(42)).rejects.toBeInstanceOf(ApiError);
  });
});

describe('quality profiles', () => {
  it('builds the default and test paths under the selected profile', () => {
    expect(endpoints.qualityProfileDefault(3)).toBe('/api/v1/quality-profiles/3/default');
    expect(endpoints.qualityProfileTest(3)).toBe('/api/v1/quality-profiles/3/test');
  });

  it('sets a profile as default and receives the updated profile', async () => {
    stubFetch({ id: 3, name: '4K', is_default: true });
    const profile = await api.setDefaultQualityProfile(3);

    expect(profile.is_default).toBe(true);
    expect(only()).toMatchObject({
      method: 'PUT',
      url: '/api/v1/quality-profiles/3/default',
      body: null,
    });
  });

  it('posts pasted titles to the server-owned profile scorer', async () => {
    stubFetch({
      results: [{
        title: 'Film.2026.1080p.WEB-DL',
        parsed: { quality: '1080p', source: 'WEB-DL' },
        decision: {
          accepted: true,
          profile_id: 3,
          profile_name: 'HD',
          score: 42,
          reason: 'Accepted at the cutoff.',
          contributions: { quality: 20, source: 10, proper: 0, repack: 0, seeders: 12 },
        },
      }],
    });
    const result = await api.testQualityProfile(3, { titles: ['Film.2026.1080p.WEB-DL'] });

    expect(result.results[0]?.decision.score).toBe(42);
    expect(only()).toMatchObject({
      method: 'POST',
      url: '/api/v1/quality-profiles/3/test',
      body: { titles: ['Film.2026.1080p.WEB-DL'] },
    });
  });
});

describe('indexers', () => {
  it('unwraps the list envelope', async () => {
    stubFetch({ indexers: [{ id: 1, name: 'Jackett' }] });
    const list = await api.listIndexers();
    expect(list).toHaveLength(1);
    expect(only().url).toBe('/api/v1/indexers');
  });

  it('reads a missing envelope key as an empty list', async () => {
    stubFetch({});
    expect(await api.listIndexers()).toEqual([]);
  });

  it('POSTs a new indexer', async () => {
    stubFetch({ id: 1 });
    await api.addIndexer({
      name: 'Jackett',
      type: 'torznab',
      url: 'http://127.0.0.1:9117',
      api_key: 'k',
      categories: [2000],
      enabled: true,
    });
    const call = only();
    expect(call.method).toBe('POST');
    expect(call.url).toBe('/api/v1/indexers');
    expect(call.body).toMatchObject({ name: 'Jackett', type: 'torznab', categories: [2000] });
  });

  it('PUTs an edit to the id path', async () => {
    stubFetch({ id: 4 });
    await api.updateIndexer(4, {
      name: 'Jackett',
      type: 'newznab',
      url: 'https://example.test',
      api_key: '',
      categories: [],
      enabled: false,
    });
    const call = only();
    expect(call.method).toBe('PUT');
    expect(call.url).toBe('/api/v1/indexers/4');
  });

  it('DELETEs by id', async () => {
    stubFetch(null);
    await api.deleteIndexer(4);
    expect(only()).toMatchObject({ method: 'DELETE', url: '/api/v1/indexers/4' });
  });

  it('POSTs a test and resolves when the server is happy', async () => {
    stubFetch({ ok: true });
    await api.testIndexer(4);
    expect(only()).toMatchObject({ method: 'POST', url: '/api/v1/indexers/4/test' });
  });

  it('surfaces a failed test as the indexer own complaint', async () => {
    stubFetch({ error: 'indexer returned 401' }, 502);
    await expect(api.testIndexer(4)).rejects.toThrow('indexer returned 401');
    await expect(api.testIndexer(4)).rejects.toBeInstanceOf(ApiError);
  });

  it('GETs categories for a stored indexer and unwraps the tree', async () => {
    stubFetch({ categories: [{ id: 2000, name: 'Movies', subcats: [] }] });
    const categories = await api.indexerStoredCategories(4);

    expect(categories).toEqual([{ id: 2000, name: 'Movies', subcats: [] }]);
    expect(only()).toMatchObject({ method: 'GET', url: '/api/v1/indexers/4/categories' });
  });
});

describe('remote path mappings', () => {
  const mapping = { remote_path: '/downloads', local_path: '/mnt/downloads' };

  it('unwraps the remote path mapping envelope', async () => {
    stubFetch({ remote_path_mappings: [{ id: 6, ...mapping }] });

    await expect(api.listRemotePathMappings()).resolves.toEqual([{ id: 6, ...mapping }]);
    expect(only()).toMatchObject({ method: 'GET', url: '/api/v1/remote-path-mappings' });
  });

  it('POSTs, PUTs and DELETEs a mapping at the collection and item paths', async () => {
    stubFetch({ id: 6, ...mapping });
    await api.addRemotePathMapping(mapping);
    expect(only()).toMatchObject({
      method: 'POST',
      url: '/api/v1/remote-path-mappings',
      body: mapping,
    });

    stubFetch({ id: 6, ...mapping });
    await api.updateRemotePathMapping(6, mapping);
    expect(only()).toMatchObject({
      method: 'PUT',
      url: '/api/v1/remote-path-mappings/6',
      body: mapping,
    });

    stubFetch(null);
    await api.deleteRemotePathMapping(6);
    expect(only()).toMatchObject({ method: 'DELETE', url: '/api/v1/remote-path-mappings/6' });
  });
});

describe('request approvals', () => {
  it('carries the edited monitored state independently from availability', async () => {
    stubFetch({ request: { id: 12, monitored: false } });

    await api.approveRequest(12, false, undefined, 'announced', 4, false);

    expect(only()).toMatchObject({
      method: 'POST',
      url: '/api/v1/requests/12/approve',
      body: {
        search_now: false,
        min_availability: 'announced',
        quality_profile_id: 4,
        monitored: false,
      },
    });
  });
});

describe('task intervals', () => {
  it('PUTs a whole-minute interval to the task item path', async () => {
    stubFetch({ kind: 'rss_sync', interval_minutes: 30 });

    await expect(api.updateTaskInterval('rss_sync', { interval_minutes: 30 })).resolves.toEqual({
      kind: 'rss_sync',
      interval_minutes: 30,
    });
    expect(only()).toMatchObject({
      method: 'PUT',
      url: '/api/v1/system/tasks/rss_sync',
      body: { interval_minutes: 30 },
    });
  });
});

describe('interactive search', () => {
  it('fetches movie releases with the whole envelope', async () => {
    // The envelope, not bare rows: the picker seeds its editable query box
    // from `query` and surfaces the per-indexer `errors`.
    stubFetch({ query: 'dune 2021', releases: [{ guid: 'a' }, { guid: 'b' }], errors: [] });
    const found = await api.movieReleases(7);
    expect(found.releases).toHaveLength(2);
    expect(found.query).toBe('dune 2021');
    expect(only().url).toBe('/api/v1/library/movies/7/releases');
  });

  it('encodes the universal search params comma-joined and omits empties', async () => {
    stubFetch({ query: 'x', releases: [], errors: [] });
    await api.searchReleases({ q: 'ubuntu iso', cats: [2000, 5000], indexer_ids: [] });
    expect(only().url).toBe('/api/v1/search/releases?q=ubuntu+iso&cats=2000%2C5000');
  });

  it('sends no season or episode for a bare series search', async () => {
    stubFetch({ releases: [] });
    await api.seriesReleases(9);
    expect(only().url).toBe('/api/v1/library/series/9/releases');
  });

  it('sends the season for a season-pack search', async () => {
    stubFetch({ releases: [] });
    await api.seriesReleases(9, { season: 2 });
    expect(only().url).toBe('/api/v1/library/series/9/releases?season=2');
  });

  it('keeps season 0, which is Specials rather than "unset"', async () => {
    stubFetch({ releases: [] });
    await api.seriesReleases(9, { season: 0, episode: 1 });
    expect(only().url).toBe('/api/v1/library/series/9/releases?season=0&episode=1');
  });
});

/**
 * Metadata search. The library is what picks the provider chain, so it is part
 * of the question: the same query asked of two libraries has two answers.
 */
describe('metadata search', () => {
  it('names the library whose chain should answer', async () => {
    stubFetch({ movies: [], series: [], providers: ['tmdb'], library_id: 9, errors: [] });
    await api.search('frieren', 'series', 9);
    expect(only().url).toBe('/api/v1/search?q=frieren&type=series&library_id=9');
  });

  it.each([
    { name: 'omitted', libraryID: undefined },
    // Zero is "the kind's default library", which is what an absent parameter
    // already means — sending it would be a filter on library 0, which is not
    // a library.
    { name: 'zero', libraryID: 0 },
  ])('sends no library_id when it is $name', async ({ libraryID }) => {
    stubFetch({ movies: [], series: [], providers: ['tmdb'], library_id: 1, errors: [] });
    await api.search('dune', 'movie', libraryID);
    expect(only().url).toBe('/api/v1/search?q=dune&type=movie');
  });
});

describe('automatic search', () => {
  it('POSTs an exact episode search with no body and returns the queued count', async () => {
    stubFetch({ queued: 1 }, 202);

    await expect(api.searchEpisodeNow(11)).resolves.toEqual({ queued: 1 });
    expect(only()).toEqual({
      url: '/api/v1/library/episodes/11/search',
      method: 'POST',
      body: null,
    });
  });
});

describe('grab', () => {
  it('POSTs a movie grab by cached release id', async () => {
    stubFetch(null, 202);
    await api.grabForMovie(7, { release_id: 55 });
    const call = only();
    expect(call.method).toBe('POST');
    expect(call.url).toBe('/api/v1/library/movies/7/grab');
    expect(call.body).toEqual({ release_id: 55 });
  });

  it('POSTs a series grab with its exact scope in the query', async () => {
    stubFetch(null, 202);
    await api.grabForSeries(9, { release_id: 55, season: 2, episode: 4 });
    expect(only()).toEqual({
      url: '/api/v1/library/series/9/grab?season=2&episode=4',
      method: 'POST',
      body: { release_id: 55 },
    });
  });
});

describe('history pagination', () => {
  it('passes cursor and preserves page metadata for events and jobs', async () => {
    stubFetch({ events: [{ id: 3 }], next_cursor: '2' });
    await expect(api.listEventsPage(1, '2')).resolves.toEqual({
      events: [{ id: 3 }],
      next_cursor: '2',
    });
    expect(only().url).toBe('/api/v1/events?limit=1&cursor=2');

    calls = [];
    stubFetch({ jobs: [{ id: 4 }], next_cursor: '' });
    await expect(api.listJobsPage(1, '3')).resolves.toEqual({
      jobs: [{ id: 4 }],
      next_cursor: '',
    });
    expect(only().url).toBe('/api/v1/jobs?limit=1&cursor=3');
  });
});

describe('downloads', () => {
  it('fetches the first cursor page and preserves metadata', async () => {
    stubFetch({ downloads: [{ id: 'abc' }], next_cursor: 'stored:7' });
    await expect(api.listDownloadsPage(1)).resolves.toEqual({
      downloads: [{ id: 'abc' }],
      next_cursor: 'stored:7',
    });
    expect(only().url).toBe('/api/v1/downloads?limit=1');
  });

  it('unwraps the queue envelope', async () => {
    stubFetch({ downloads: [{ id: 'abc' }] });
    expect(await api.listDownloads()).toHaveLength(1);
    expect(only().url).toBe('/api/v1/downloads?limit=100');
  });

  it('pauses and resumes by engine id', async () => {
    stubFetch(null);
    await api.pauseDownload('abc');
    expect(only()).toMatchObject({ method: 'POST', url: '/api/v1/downloads/abc/pause' });

    calls = [];
    await api.resumeDownload('abc');
    expect(only()).toMatchObject({ method: 'POST', url: '/api/v1/downloads/abc/resume' });
  });

  it('says deleteData=false explicitly, so the default is never inferred', async () => {
    // A dropped query param must not be read as "delete the data" (SPEC §13).
    stubFetch(null);
    await api.removeDownload('abc', false);
    expect(only()).toMatchObject({
      method: 'DELETE',
      url: '/api/v1/downloads/abc?deleteData=false',
    });
  });

  it('asks for the data to be deleted when the user opted in', async () => {
    stubFetch(null);
    await api.removeDownload('abc', true);
    expect(only().url).toBe('/api/v1/downloads/abc?deleteData=true');
  });
});

describe('definition packs', () => {
  const REVISION = {
    source: 'community',
    revision: '2026.08.14',
    install_state: 'installed',
    pending: false,
    active: true,
    last_known_good: true,
    definition_count: 12,
    runnable_count: 10,
    archive_digest: 'sha256:archive',
    manifest_digest: 'sha256:manifest',
    license_digest: 'sha256:license',
    signature_fingerprint: 'aa:bb',
    license_expression: 'MIT',
    provenance: 'signed',
    minimum_caravan_version: '0.1.0',
    installed_at: '2026-08-14T00:00:00Z',
    accepted_at: '2026-08-14T00:00:00Z',
    accepted_by_user_id: 1,
  };
  const PREVIEW = {
    source: 'community',
    revision: '2026.08.14',
    archive_digest: 'sha256:archive',
    manifest_digest: 'sha256:manifest',
    license_digest: 'sha256:license',
    signature_fingerprint: 'aa:bb',
    license: 'MIT license text',
    notice: 'Notice text',
    token: 'preview-token',
    expires_at: '2026-08-15T12:00:00Z',
  };

  function stubPackFetch(body: unknown, status = 200) {
    calls = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const headers = (init?.headers ?? {}) as Record<string, string>;
        calls.push({
          url: String(input),
          method: init?.method ?? 'GET',
          body: init?.body ?? null,
          headers,
        } as Call & { headers: Record<string, string> });
        return new Response(JSON.stringify(body), {
          status,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );
  }

  function packCall() {
    expect(calls).toHaveLength(1);
    return calls[0] as Call & { headers: Record<string, string>; body: FormData | string | null };
  }

  it('unwraps GET /definition-packs revisions', async () => {
    stubPackFetch({ revisions: [REVISION] });
    await expect(api.listDefinitionPacks()).resolves.toEqual([REVISION]);
    expect(packCall()).toMatchObject({ method: 'GET', url: '/api/v1/definition-packs' });
  });

  it('sends preview multipart fields and omits Content-Type', async () => {
    stubPackFetch(PREVIEW);
    const archive = new File(['zip'], 'community.zip', { type: 'application/zip' });
    await expect(
      api.previewDefinitionPack({
        archive,
        signer_key_id: 'owner-1',
        public_key: 'cHVibGljLWtleQ==',
      }),
    ).resolves.toEqual(PREVIEW);

    const call = packCall();
    expect(call.method).toBe('POST');
    expect(call.url).toBe('/api/v1/definition-packs/preview');
    expect(call.headers.Accept).toBe('application/json');
    expect(call.headers['Content-Type']).toBeUndefined();
    expect(call.body).toBeInstanceOf(FormData);
    const fields = call.body as FormData;
    expect(fields.get('archive')).toBe(archive);
    expect(fields.get('signer_key_id')).toBe('owner-1');
    expect(fields.get('public_key')).toBe('cHVibGljLWtleQ==');
    expect(fields.has('token')).toBe(false);
  });

  it('resends the same archive plus source and token on install', async () => {
    stubPackFetch(REVISION, 201);
    const archive = new File(['zip'], 'community.zip', { type: 'application/zip' });
    await expect(
      api.installDefinitionPack({
        archive,
        signer_key_id: 'owner-1',
        public_key: 'cHVibGljLWtleQ==',
        source: 'community',
        token: 'preview-token',
      }),
    ).resolves.toEqual(REVISION);

    const call = packCall();
    expect(call.method).toBe('POST');
    expect(call.url).toBe('/api/v1/definition-packs/install');
    expect(call.headers.Accept).toBe('application/json');
    expect(call.headers['Content-Type']).toBeUndefined();
    const fields = call.body as FormData;
    expect(fields.get('archive')).toBe(archive);
    expect(fields.get('signer_key_id')).toBe('owner-1');
    expect(fields.get('public_key')).toBe('cHVibGljLWtleQ==');
    expect(fields.get('source')).toBe('community');
    expect(fields.get('token')).toBe('preview-token');
  });

  it('POSTs activate and rollback as exact JSON refs', async () => {
    stubPackFetch({ restart_required: true }, 202);
    await expect(api.activateDefinitionPack('community', '2026.08.14')).resolves.toEqual({
      restart_required: true,
    });
    expect(packCall()).toMatchObject({
      method: 'POST',
      url: '/api/v1/definition-packs/activate',
      body: JSON.stringify({ source: 'community', revision: '2026.08.14' }),
    });

    calls = [];
    stubPackFetch(REVISION);
    await expect(api.rollbackDefinitionPack('community', '2026.08.14')).resolves.toEqual(REVISION);
    expect(packCall()).toMatchObject({
      method: 'POST',
      url: '/api/v1/definition-packs/rollback',
      body: JSON.stringify({ source: 'community', revision: '2026.08.14' }),
    });
  });

  it('rejects missing pack inputs and files larger than 80 MiB without fetching', async () => {
    stubPackFetch(PREVIEW);
    const archive = new File(['zip'], 'community.zip');
    await expect(
      api.previewDefinitionPack({ archive, signer_key_id: '', public_key: 'cHVibGljLWtleQ==' }),
    ).rejects.toThrow(/signer key/i);
    await expect(
      api.previewDefinitionPack({ archive, signer_key_id: 'owner-1', public_key: '' }),
    ).rejects.toThrow(/public key/i);
    await expect(
      api.installDefinitionPack({
        archive,
        signer_key_id: 'owner-1',
        public_key: 'cHVibGljLWtleQ==',
        source: '',
        token: 'preview-token',
      }),
    ).rejects.toThrow(/source/i);

    const oversized = new File(['x'], 'huge.zip');
    Object.defineProperty(oversized, 'size', { value: 80 * 1024 * 1024 + 1 });
    await expect(
      api.previewDefinitionPack({
        archive: oversized,
        signer_key_id: 'owner-1',
        public_key: 'cHVibGljLWtleQ==',
      }),
    ).rejects.toThrow(/80 MiB/i);
    expect(calls).toHaveLength(0);
  });
});
