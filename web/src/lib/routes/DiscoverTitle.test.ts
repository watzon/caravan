/**
 * The acquisition screen. Two things are asserted here that the pure helpers
 * cannot: that a season row on an owned series does not offer a Request the
 * server would answer 409 to, and that the facts rail renders the provider
 * facts the payload now carries.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DiscoverTitle from './DiscoverTitle.svelte';
import type { DiscoverSeason, DiscoverTitle as DiscoverTitlePayload } from '../api/types';
import { discover } from '../state/discover.svelte';

function season(number: number, extra: Partial<DiscoverSeason> = {}): DiscoverSeason {
  return {
    season_number: number,
    title: `Season ${number}`,
    overview: '',
    poster_url: '',
    air_date: '2008-01-20',
    episode_count: 7,
    in_library: false,
    requested: false,
    ...extra,
  };
}

function payload(extra: Partial<DiscoverTitlePayload> = {}): DiscoverTitlePayload {
  return {
    media_type: 'series',
    tmdb_id: 1396,
    title: 'Breaking Bad',
    year: 2008,
    overview: 'Chemistry.',
    poster_path: '/p.jpg',
    poster_url: '',
    backdrop_url: '',
    vote_average: 8.9,
    date: '2008-01-20',
    in_library: false,
    library_id: 0,
    requested: false,
    status: 'Ended',
    runtime: 49,
    network: 'AMC',
    last_aired: '2013-09-29',
    language: 'en',
    genres: ['Drama'],
    imdb_id: 'tt0903747',
    tvdb_id: 81189,
    cast: [],
    recommendations: [],
    seasons: [season(1), season(2)],
    ...extra,
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;

function stubFetch(body: DiscoverTitlePayload) {
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(JSON.stringify(body), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    ),
  );
}

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function mountTitle(body: DiscoverTitlePayload) {
  stubFetch(body);
  app = mount(DiscoverTitle, {
    target: host,
    props: { type: 'series' as const, tmdbID: 1396 },
  }) as Record<string, unknown>;
  flushSync();
  await settle();
}

/** The trailing control or badge of each season row. */
function seasonSlots(): string[] {
  return [...host.querySelectorAll('section li')].map((li) =>
    (li.querySelector('.ml-auto')?.textContent ?? '').trim(),
  );
}

/** The facts rail as label → value. */
function facts(): Record<string, string> {
  const out: Record<string, string> = {};
  for (const row of host.querySelectorAll('dl > div')) {
    const label = row.querySelector('dt')?.textContent?.trim() ?? '';
    out[label] = row.querySelector('dd')?.textContent?.trim() ?? '';
  }
  return out;
}

beforeEach(() => {
  discover.reset();
  window.scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.unstubAllGlobals();
});

describe('DiscoverTitle — season rows', () => {
  it('offers a Request for a missing season of a title nobody owns', async () => {
    await mountTitle(payload({ seasons: [season(1, { requested: true }), season(2)] }));
    expect(seasonSlots()).toEqual(['Requested · pending approval', 'Request']);
  });

  /**
   * POST /requests refuses anything already tracked with 409 "already in the
   * library" — it checks the title, not the season — so a Request button here
   * could only ever produce a danger toast.
   */
  it('never offers a Request when the series itself is in the library', async () => {
    await mountTitle(
      payload({
        in_library: true,
        library_id: 7,
        seasons: [season(1, { in_library: true }), season(2)],
      }),
    );

    expect(seasonSlots()).toEqual(['In library', 'Not in library']);
    const labels = [...host.querySelectorAll('button')].map((b) => b.textContent?.trim());
    expect(labels).not.toContain('Request');
  });
});

describe('DiscoverTitle — facts', () => {
  it('names the network, the last air date and the language', async () => {
    await mountTitle(payload());

    expect(facts()).toMatchObject({
      Status: 'Ended',
      'First aired': '20 Jan 2008',
      'Last aired': '29 Sep 2013',
      Network: 'AMC',
      Language: 'English',
    });
    // The provider fact replaced a TMDB id nobody could act on.
    expect(facts()).not.toHaveProperty('TMDB id');
    // And the hero meta line credits it too.
    expect(host.textContent).toContain('AMC');
  });

  it('labels a movie’s production company a studio and has no last aired row', async () => {
    await mountTitle(
      payload({
        media_type: 'movie',
        tmdb_id: 78,
        title: 'Blade Runner',
        network: 'The Ladd Company',
        last_aired: '',
        status: 'Released',
        seasons: [],
      }),
    );

    const rows = facts();
    expect(rows.Studio).toBe('The Ladd Company');
    expect(rows).not.toHaveProperty('Network');
    expect(rows).not.toHaveProperty('Last aired');
    expect(rows.Released).toBe('20 Jan 2008');
  });
});
