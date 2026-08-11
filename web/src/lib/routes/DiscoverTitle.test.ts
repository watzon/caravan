/**
 * The acquisition screen and its shared discover rating surfaces. The mounted
 * regressions cover presentation that pure helpers cannot: rating provenance,
 * release-date gating, season actions for owned titles, and provider facts.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DiscoverTitle from './DiscoverTitle.svelte';
import DiscoverCard from '../components/DiscoverCard.svelte';
import DiscoverRoute from './Discover.svelte';
import type { DiscoverSeason, DiscoverTitle as DiscoverTitlePayload } from '../api/types';
import { discover } from '../state/discover.svelte';
import { session } from '../state/session.svelte';

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
    vote_count: 1_234,
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

type RatingSurface = 'card' | 'hero' | 'title';

async function mountRatingSurface(surface: RatingSurface, body: DiscoverTitlePayload) {
  if (surface === 'card') {
    app = mount(DiscoverCard, {
      target: host,
      props: { item: body },
    }) as Record<string, unknown>;
    flushSync();
    return;
  }

  if (surface === 'hero') {
    discover.home = {
      trending: [body],
      popular_movies: [],
      popular_series: [],
      networks: [],
      studios: [],
    };
    app = mount(DiscoverRoute, { target: host }) as Record<string, unknown>;
    flushSync();
    return;
  }

  await mountTitle(body);
}

function ratingElement(): HTMLElement | null {
  return host.querySelector('[title^="Rated "], [title="Not yet rated"]');
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
  session.forget();
});

function buttonLabels(): (string | undefined)[] {
  return [...host.querySelectorAll('button')].map((b) => b.textContent?.trim());
}

describe('Discover rating surfaces', () => {
  for (const surface of ['card', 'hero', 'title'] as const) {
    it(`shows a released, voted rating on the ${surface}`, async () => {
      await mountRatingSurface(surface, payload());

      expect(ratingElement()?.getAttribute('title')).toBe('Rated 8.9/10');
      expect(ratingElement()?.textContent).toContain('8.9/10');
    });

    const hiddenRatings: Array<[string, Partial<DiscoverTitlePayload>]> = [
      ['a nonzero average with zero votes', { vote_count: 0 }],
      ['a future release date', { date: '2999-01-01' }],
      ['an unknown release date', { date: '' }],
      ['an invalid release date', { date: '2025-02-30' }],
    ];

    for (const [condition, extra] of hiddenRatings) {
      it(`shows Not yet rated for ${condition} on the ${surface}`, async () => {
        await mountRatingSurface(surface, payload(extra));

        expect(ratingElement()?.getAttribute('title')).toBe('Not yet rated');
        expect(ratingElement()?.textContent).not.toContain('8.9/10');
      });
    }
  }
});

describe('Discover text fallbacks', () => {
  it('exposes complete hero and source text when the visible copy is clamped', () => {
    const heroTitle = 'A Trending Title That Is Longer Than the Billboard Copy';
    const heroOverview =
      'The complete billboard overview remains available when only two visual lines fit in the hero.';
    const sourceName = 'A Network Name That Is Longer Than Its Browse Tile';
    discover.home = {
      trending: [payload({ title: heroTitle, overview: heroOverview })],
      popular_movies: [],
      popular_series: [],
      networks: [{ id: 213, name: sourceName, type: 'network' }],
      studios: [],
    };
    app = mount(DiscoverRoute, { target: host }) as Record<string, unknown>;
    flushSync();

    expect(host.querySelector(`h2[title="${heroTitle}"]`)).not.toBeNull();
    expect(host.querySelector('p.line-clamp-2')?.getAttribute('title')).toBe(heroOverview);
    expect(host.querySelector('a[href="/discover/network/213"]')?.getAttribute('title')).toBe(
      sourceName,
    );
  });

  it('exposes the complete detail hero title', async () => {
    const detailTitle = 'A Complete Detail Hero Title That Can Wrap at Phone Width';
    await mountTitle(payload({ title: detailTitle }));

    expect(host.querySelector('h2')?.getAttribute('title')).toBe(detailTitle);
  });

  it('uses the visible fallback as the title for an unknown cast character', async () => {
    await mountTitle(
      payload({
        cast: [{ tmdb_id: 1, name: 'Known Performer', character: '', profile_url: '' }],
      }),
    );

    const character = host.querySelector('.truncate.text-ink-secondary');
    const visibleFallback = character?.textContent?.trim();
    expect(visibleFallback).toBeTruthy();
    expect(character?.getAttribute('title')).toBe(visibleFallback);
  });
});


describe('DiscoverTitle — season rows', () => {
  it('offers a Request for a missing season of a title nobody owns', async () => {
    await mountTitle(payload({ seasons: [season(1, { requested: true }), season(2)] }));
    expect(seasonSlots()).toEqual(['Requested, pending approval', 'Request']);
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
      'Last aired': '29 Sept 2013',
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

describe('DiscoverTitle — who is asking', () => {
  it('offers an admin both the ask and the direct add', async () => {
    session.user = { username: 'root', role: 'admin', open: false, adult: false };
    await mountTitle(payload());

    expect(buttonLabels()).toContain('Request series');
    expect(buttonLabels()).toContain('Add to library');
    expect(host.textContent).toContain('Direct add is available to admins');
  });

  /**
   * A member's add would be a 403, and the sentence explaining the admin's
   * quality-profile choice is about a button they cannot see.
   */
  it('offers a member only the ask', async () => {
    session.user = { username: 'ada', role: 'member', open: false, adult: false };
    await mountTitle(payload());

    expect(buttonLabels()).toContain('Request series');
    expect(buttonLabels()).not.toContain('Add to library');
    expect(host.textContent).not.toContain('Direct add is available to admins');
    // Per-season Request is theirs either way: it is a request, not an add.
    expect(seasonSlots()).toEqual(['Request', 'Request']);
  });

  /**
   * /series/:id is an admin screen: App.svelte bounces a member off it the
   * instant they land. "Open in library" is this screen's only call to action
   * for a title Caravan already has, so for a member it must state the fact
   * rather than offer a door that closes in their face.
   */
  it('tells a member a title is in the library without linking into it', async () => {
    session.user = { username: 'ada', role: 'member', open: false, adult: false };
    await mountTitle(payload({ in_library: true, library_id: 7 }));

    expect(host.textContent).toContain('In library');
    expect(host.querySelector('a[href="/series/7"]')).toBeNull();
  });

  it('keeps the link into the library for an admin', async () => {
    session.user = { username: 'root', role: 'admin', open: false, adult: false };
    await mountTitle(payload({ in_library: true, library_id: 7 }));

    expect(host.querySelector('a[href="/series/7"]')).not.toBeNull();
  });
});
