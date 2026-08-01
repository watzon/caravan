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
    expect(endpoints.indexerTest(3)).toBe('/api/v1/indexers/3/test');
    expect(endpoints.movieReleases(7)).toBe('/api/v1/library/movies/7/releases');
    expect(endpoints.movieGrab(7)).toBe('/api/v1/library/movies/7/grab');
    expect(endpoints.seriesReleases(9)).toBe('/api/v1/library/series/9/releases');
    expect(endpoints.seriesGrab(9)).toBe('/api/v1/library/series/9/grab');
    expect(endpoints.downloads()).toBe('/api/v1/downloads');
  });

  it('escapes the engine-native download id, which is not a number', () => {
    // A DownloadID is whatever the engine calls it; SABnzbd ids contain
    // characters that would otherwise change the path.
    expect(endpoints.download('a/b?c')).toBe('/api/v1/downloads/a%2Fb%3Fc');
    expect(endpoints.downloadPause('a/b')).toBe('/api/v1/downloads/a%2Fb/pause');
    expect(endpoints.downloadResume('a/b')).toBe('/api/v1/downloads/a%2Fb/resume');
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
});

describe('interactive search', () => {
  it('fetches movie releases and unwraps them', async () => {
    stubFetch({ releases: [{ guid: 'a' }, { guid: 'b' }] });
    expect(await api.movieReleases(7)).toHaveLength(2);
    expect(only().url).toBe('/api/v1/library/movies/7/releases');
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

describe('grab', () => {
  it('POSTs a movie grab by cached release id', async () => {
    stubFetch(null, 202);
    await api.grabForMovie(7, { release_id: 55 });
    const call = only();
    expect(call.method).toBe('POST');
    expect(call.url).toBe('/api/v1/library/movies/7/grab');
    expect(call.body).toEqual({ release_id: 55 });
  });

  it('POSTs a series grab with the episodes it should satisfy', async () => {
    stubFetch(null, 202);
    await api.grabForSeries(9, { release_id: 55, season: 2, episode_ids: [11, 12] });
    expect(only().body).toEqual({ release_id: 55, season: 2, episode_ids: [11, 12] });
  });
});

describe('downloads', () => {
  it('unwraps the queue envelope', async () => {
    stubFetch({ downloads: [{ id: 'abc' }] });
    expect(await api.listDownloads()).toHaveLength(1);
    expect(only().url).toBe('/api/v1/downloads');
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
