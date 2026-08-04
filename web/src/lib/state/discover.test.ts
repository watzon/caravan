/**
 * The discover cache. Two behaviours matter: the landing page costs three
 * sequential TMDB round trips, so it must not be refetched on every visit; and
 * when the user requests or adds a title, every shelf holding it has to say so
 * without another fetch.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { DiscoverHome, DiscoverItem } from '../api/types';
import { discover } from './discover.svelte';

function item(tmdbID: number, extra: Partial<DiscoverItem> = {}): DiscoverItem {
  return {
    media_type: 'movie',
    tmdb_id: tmdbID,
    title: `Title ${tmdbID}`,
    year: 2020,
    overview: '',
    poster_path: '',
    poster_url: '',
    backdrop_url: '',
    vote_average: 7,
    date: '2020-01-01',
    in_library: false,
    library_id: 0,
    requested: false,
    ...extra,
  };
}

function home(): DiscoverHome {
  return {
    // The same title appears on two shelves — that is exactly the case a patch
    // has to cover.
    trending: [item(1), item(2, { media_type: 'series' })],
    popular_movies: [item(1)],
    popular_series: [item(2, { media_type: 'series' })],
    networks: [],
    studios: [],
  };
}

let calls: string[];

function stubFetch(payload: unknown, status = 200) {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      calls.push(String(input));
      return new Response(JSON.stringify(payload), {
        status,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

beforeEach(() => {
  discover.reset();
});

afterEach(() => {
  discover.reset();
  vi.unstubAllGlobals();
});

describe('discover store', () => {
  it('fetches once and serves the cache afterwards', async () => {
    stubFetch(home());

    await discover.load();
    await discover.load();

    expect(calls).toEqual(['/api/v1/discover']);
    expect(discover.home?.trending).toHaveLength(2);
    expect(discover.error).toBeNull();
  });

  it('refetches when forced — that is what the retry button is', async () => {
    stubFetch(home());
    await discover.load();
    await discover.load(true);
    expect(calls).toHaveLength(2);
  });

  // A credential fault sends the user to settings; anything else offers a
  // retry. The two are told apart by the error envelope's code, so the fault
  // has to survive the failure.
  it('keeps a missing-key fault so the screen can send the user to settings', async () => {
    stubFetch(
      { error: 'no metadata provider configured', code: 'metadata_credential_absent' },
      503,
    );
    await discover.load();

    expect(discover.home).toBeNull();
    expect(discover.fault).toBe('absent');
    expect(discover.error).toBe('no metadata provider configured');
  });

  it('tells a rejected key apart from a missing one', async () => {
    stubFetch(
      { error: 'the TMDB API key was rejected', code: 'metadata_credential_invalid' },
      503,
    );
    await discover.load();

    expect(discover.fault).toBe('invalid');
  });

  // A provider that is simply unhappy is not a credential problem: no code,
  // and the screen keeps its retry.
  it('leaves a provider failure without a fault so the retry stays', async () => {
    stubFetch({ error: 'tmdb: http 500' }, 502);
    await discover.load();

    expect(discover.fault).toBeNull();
    expect(discover.error).toBe('tmdb: http 500');
  });

  it('marks a requested title on every shelf that holds it', async () => {
    stubFetch(home());
    await discover.load();

    discover.markRequested('movie', 1);

    expect(discover.home?.trending[0]?.requested).toBe(true);
    expect(discover.home?.popular_movies[0]?.requested).toBe(true);
    // Same TMDB id, different media type: two id spaces, not one.
    expect(discover.home?.popular_series[0]?.requested).toBe(false);
  });

  it('marks an added title owned, and drops the request the add absorbed', async () => {
    stubFetch(home());
    await discover.load();
    discover.markRequested('series', 2);

    discover.markInLibrary('series', 2, 42);

    const added = discover.home?.trending[1];
    expect(added?.in_library).toBe(true);
    expect(added?.library_id).toBe(42);
    expect(added?.requested).toBe(false);
  });

  it('ignores marks for a title it has never seen', async () => {
    stubFetch(home());
    await discover.load();
    expect(() => discover.markRequested('movie', 999)).not.toThrow();
    expect(discover.home?.trending[0]?.requested).toBe(false);
  });
});
