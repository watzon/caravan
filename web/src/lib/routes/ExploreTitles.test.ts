/**
 * The filtered title scopes, mounted for real.
 *
 * explore.test.ts proves the filter model in isolation; what is left, and what
 * only a mounted screen can show, is that the URL and the wire are actually
 * joined up: an address somebody was sent restores the rail AND becomes the
 * request, a chip removed rewrites the address, and the scope's own filter
 * never reaches the endpoint that refuses it.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ExploreTitles from './ExploreTitles.svelte';
import type { DiscoverItem } from '../api/types';
import { navigate, router } from '../router.svelte';
import { discover } from '../state/discover.svelte';

function item(tmdbID: number, inLibrary = false): DiscoverItem {
  return {
    media_type: 'movie',
    tmdb_id: tmdbID,
    title: `Title ${tmdbID}`,
    year: 2024,
    overview: '',
    poster_path: '',
    poster_url: '',
    backdrop_url: '',
    vote_average: 8,
    vote_count: 1,
    date: '2024-01-01',
    in_library: inLibrary,
    library_id: inLibrary ? 3 : 0,
    requested: false,
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
/** Every URL the screen asked for, in order. */
let requested: string[];
/** The rows the scope endpoint answers with. */
let served: DiscoverItem[];
let totalPages: number;

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** The last request to the filtered scope, as parsed query parameters. */
function lastScopeQuery(): URLSearchParams {
  const url = [...requested].reverse().find((u) => /\/discover\/(movies|series)\b/.test(u));
  return new URL(String(url), 'http://x').searchParams;
}

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

/** Mount the scope at `url`, query string and all. */
async function open(mediaType: 'movie' | 'series', url: string) {
  window.history.replaceState({}, '', url);
  navigate(url, { replace: true });
  app = mount(ExploreTitles, { target: host, props: { mediaType } }) as Record<string, unknown>;
  await settle();
}

function buttonWithText(text: string): HTMLButtonElement | undefined {
  return [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === text,
  );
}

beforeEach(() => {
  requested = [];
  served = [item(1), item(2)];
  totalPages = 3;
  discover.reset();
  window.scrollTo = () => {};
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      requested.push(url);
      if (url.includes('/discover/genres')) {
        return jsonResponse({
          media_type: 'movie',
          genres: [{ tmdb_id: 878, name: 'Science Fiction' }],
        });
      }
      if (url.includes('/discover/people')) {
        return jsonResponse({ people: [{ tmdb_id: 1245, name: 'Pedro Pascal', department: 'Acting', profile_url: '' }] });
      }
      if (/\/discover\/(movies|series)\b/.test(url)) {
        const page = Number(new URL(url, 'http://x').searchParams.get('page') ?? '1');
        return jsonResponse({ media_type: 'movie', page, total_pages: totalPages, items: served });
      }
      if (url.endsWith('/discover')) {
        return jsonResponse({
          trending: [],
          popular_movies: [],
          popular_series: [],
          networks: [{ id: 213, name: 'Netflix', type: 'network' }],
          studios: [],
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.unstubAllGlobals();
  discover.reset();
  navigate('/', { replace: true });
});

describe('a filtered movie scope', () => {
  it('names every filter trigger, toggle, and sort control', async () => {
    await open('movie', '/discover/movies');

    const filterNames = [...host.querySelectorAll<HTMLButtonElement>('button[aria-expanded]')].map(
      (button) => button.textContent?.trim(),
    );
    expect(filterNames).toEqual([
      'Genre',
      'Cast & crew',
      'Studio',
      'Keyword',
      'Year',
      'Runtime',
      'Rating',
      'Language',
    ]);
    expect(host.querySelector('[role="switch"]')?.textContent?.trim()).toBe('Hide in library');
    expect(host.querySelector('select')?.getAttribute('aria-label')).toBe('Sort results');
  });

  /**
   * The whole point of putting the filter in the URL: this address is what
   * somebody shares or reloads into, and every part of it has to survive the
   * trip to the provider.
   */
  it('turns a shared URL into one request carrying every filter', async () => {
    await open(
      'movie',
      '/discover/movies?genres=878:Science+Fiction&people=1245:Pedro+Pascal' +
        '&companies=41077:A24&keywords=9715:superhero&from=2019-01-01&to=2024-12-31' +
        '&runtime_min=60&runtime_max=120&rating_min=7.5&language=ja&sort=rating&order=desc',
    );

    const query = lastScopeQuery();
    expect(query.get('genres')).toBe('878');
    expect(query.get('people')).toBe('1245');
    expect(query.get('companies')).toBe('41077');
    expect(query.get('keywords')).toBe('9715');
    expect(query.get('from')).toBe('2019-01-01');
    expect(query.get('to')).toBe('2024-12-31');
    expect(query.get('runtime_min')).toBe('60');
    expect(query.get('runtime_max')).toBe('120');
    expect(query.get('rating_min')).toBe('7.5');
    expect(query.get('language')).toBe('ja');
    expect(query.get('sort')).toBe('rating');
    expect(query.get('order')).toBe('desc');
    expect(query.get('page')).toBe('1');
  });

  /** The names came from the URL, so the chips are readable without a lookup. */
  it('restores the applied chips from the URL', async () => {
    await open('movie', '/discover/movies?genres=878:Science+Fiction&people=1245:Pedro+Pascal');

    expect(host.textContent).toContain('Genre: Science Fiction');
    expect(host.textContent).toContain('Cast & crew: Pedro Pascal');
    expect(host.textContent).toContain('Clear all');
  });

  it('rewrites the URL and re-asks when a chip is removed', async () => {
    await open('movie', '/discover/movies?genres=878:Science+Fiction&people=1245:Pedro+Pascal');
    const before = requested.length;

    host.querySelector<HTMLButtonElement>('button[aria-label="Remove filter Genre: Science Fiction"]')?.click();
    await settle();

    expect(router.search).toBe('people=1245%3APedro+Pascal');
    expect(requested.length).toBeGreaterThan(before);
    expect(lastScopeQuery().get('genres')).toBeNull();
    expect(lastScopeQuery().get('people')).toBe('1245');
    expect(host.textContent).not.toContain('Genre: Science Fiction');
  });

  it('clears every chip at once, and asks the unfiltered question', async () => {
    await open('movie', '/discover/movies?genres=878:Science+Fiction&rating_min=7');

    buttonWithText('Clear all')?.click();
    await settle();

    expect(router.path).toBe('/discover/movies');
    expect(router.search).toBe('');
    expect(lastScopeQuery().get('genres')).toBeNull();
    expect(lastScopeQuery().get('rating_min')).toBeNull();
  });

  /**
   * Hiding what the library holds is a view over the answer, not a question for
   * the provider — there is no server parameter for it, and paging with one
   * would page differently from every other discover shelf.
   */
  it('hides owned rows in the browser without re-asking the provider', async () => {
    served = [item(1, true), item(2)];
    await open('movie', '/discover/movies?hide=1');

    const query = lastScopeQuery();
    expect(query.get('hide')).toBeNull();
    expect(host.textContent).toContain('Title 2');
    expect(host.textContent).not.toContain('Title 1');
  });

  it('appends the next page rather than replacing the grid', async () => {
    await open('movie', '/discover/movies');
    served = [item(3), item(4)];

    buttonWithText('Load more')?.click();
    await settle();

    expect(lastScopeQuery().get('page')).toBe('2');
    for (const title of ['Title 1', 'Title 2', 'Title 3', 'Title 4']) {
      expect(host.textContent, title).toContain(title);
    }
  });

  /**
   * A page handed back twice — TMDB does this at its own ceiling and either
   * side of a retry — must not produce two rows with the same key, which tears
   * a keyed {#each} down.
   */
  it('survives the same page being served twice', async () => {
    await open('movie', '/discover/movies');

    buttonWithText('Load more')?.click();
    await settle();

    expect(host.querySelectorAll('a[href="/discover/movie/1"]').length).toBe(1);
  });

  /** The rail must not offer a filter the scope's endpoint answers 400 to. */
  it('offers the cast & crew pill and no network pill', async () => {
    await open('movie', '/discover/movies');

    const labels = [...host.querySelectorAll('button')].map((b) => b.textContent?.trim());
    expect(labels).toContain('Cast & crew');
    expect(labels).not.toContain('Network');
  });
});

describe('a filtered series scope', () => {
  it('sends the network filter and never a person one', async () => {
    await open('series', '/discover/series?networks=213:Netflix&people=1245:Pedro+Pascal');

    const query = lastScopeQuery();
    expect(query.get('networks')).toBe('213');
    // `people` is a 400 on /discover/series — TMDB's /discover/tv has no
    // with_cast/with_crew/with_people at all — so a value that rode in on the
    // URL is dropped rather than forwarded.
    expect(query.get('people')).toBeNull();
  });

  it('offers the network pill and no cast & crew pill', async () => {
    await open('series', '/discover/series');

    const labels = [...host.querySelectorAll('button')].map((b) => b.textContent?.trim());
    expect(labels).toContain('Network');
    expect(labels).not.toContain('Cast & crew');
  });

  it('shows no chip for a person filter it will not apply', async () => {
    await open('series', '/discover/series?people=1245:Pedro+Pascal');

    expect(host.textContent).not.toContain('Pedro Pascal');
  });
});

describe('failures', () => {
  it('offers a retry rather than an empty grid when the provider is unhappy', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        requested.push(url);
        if (url.includes('/discover/genres')) {
          return jsonResponse({ media_type: 'movie', genres: [] });
        }
        return jsonResponse({ error: 'upstream said no' }, 502);
      }),
    );

    await open('movie', '/discover/movies');

    expect(host.textContent).toContain('upstream said no');
    expect(buttonWithText('Retry')).toBeDefined();
  });

  /**
   * A genre list that failed to load must not take the results down with it:
   * the pill is one control, and the grid is the screen.
   */
  it('keeps the grid when only the genre list fails', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        requested.push(url);
        if (url.includes('/discover/genres')) return jsonResponse({ error: 'nope' }, 502);
        return jsonResponse({ media_type: 'movie', page: 1, total_pages: 1, items: served });
      }),
    );

    await open('movie', '/discover/movies');

    expect(host.textContent).toContain('Title 1');
  });
});
