/**
 * The editable per-item picker.
 *
 * The screen makes two promises that pull in opposite directions, and both are
 * pinned here: the query is the user's to change, and the target is not. An
 * edited query goes to the universal endpoint; the grab that follows still
 * posts to this item's own grab endpoint, because the route — not the query box
 * — is what says where the file lands.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ReleaseSearch from './ReleaseSearch.svelte';
import type { Movie, ParsedRelease, Release, Series } from '../api/types';
import { session } from '../state/session.svelte';
import { downloads } from '../state/downloads.svelte';
import { clearToasts } from '../state/toast.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function parsed(overrides: Partial<ParsedRelease> = {}): ParsedRelease {
  return {
    title: 'Blade Runner 2049',
    year: 2017,
    season: 0,
    episodes: [],
    quality: '1080p',
    source: 'bluray',
    codec: 'x264',
    audio: 'AC3',
    bit_depth: 0,
    group: 'GROUP',
    proper: false,
    repack: false,
    edition: '',
    confidence: 0.9,
    ...overrides,
  };
}

function release(overrides: Partial<Release> = {}): Release {
  return {
    id: 7,
    indexer_id: 1,
    indexer: 'Test Indexer',
    title: 'Blade.Runner.2049.2017.1080p.BluRay.x264-GROUP',
    guid: 'guid-1',
    download_url: 'magnet:?xt=urn:btih:abc',
    info_hash: 'abc',
    protocol: 'torrent',
    size: 4 * 1024 * 1024 * 1024,
    seeders: 20,
    leechers: 3,
    published_at: '2026-07-01T00:00:00Z',
    parsed: parsed(),
    compatibility: { verdict: 'unknown', reasons: [] },
    ...overrides,
  };
}

const MOVIE = {
  id: 3,
  tmdb_id: 335984,
  title: 'Blade Runner 2049',
  year: 2017,
  library_id: 4,
  monitored: true,
} as Movie;

const SITE = {
  id: 9,
  title: 'Transfixed',
  kind: 'adult',
  library_id: 5,
  seasons: [
    {
      id: 1,
      series_id: 9,
      season_number: 2026,
      title: '',
      overview: '',
      poster_path: '',
      air_date: '2026-05-20',
      monitored: true,
      episodes: [
        {
          id: 24,
          series_id: 9,
          season_number: 2026,
          episode_number: 24,
          tmdb_id: 0,
          title: 'A Lesson',
          overview: '',
          air_date: '2026-05-20',
          monitored: true,
        },
      ],
    },
  ],
} as Series;

const SITE_EXPRESSION = '(site:"Transfixed" date:2026-05-20) OR "Transfixed A Lesson"';

/** When set, the per-item search endpoint waits on it: the item is fast and
 * the fan-out is slow, and one test needs to look at the screen in between. */
let releaseGate: Promise<void> | null = null;

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: unknown }[];

/** The two answers a test bends: the per-item seed, and the free-text reply. */
let itemAnswer: { body: unknown; status: number };
let searchAnswer: { body: unknown; status: number };

beforeEach(() => {
  calls = [];
  releaseGate = null;
  itemAnswer = {
    status: 200,
    body: {
      query: 'Blade Runner 2049 2017',
      queries: ['Blade Runner 2049 2017'],
      releases: [release()],
      errors: [{ indexer_id: 2, indexer: 'Down Indexer', error: 'dial tcp: refused' }],
    },
  };
  searchAnswer = {
    status: 200,
    body: {
      query: 'br2049 remux',
      queries: ['br2049 remux'],
      releases: [release({ id: 99, guid: 'guid-2', title: 'BR2049.2017.REMUX-GROUP' })],
      errors: [],
    },
  };
  host = document.createElement('div');
  document.body.appendChild(host);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      });
      if (url.includes('/indexers')) {
        return jsonResponse({
          indexers: [
            {
              id: 1,
              name: 'Test Indexer',
              url: 'http://localhost',
              has_api_key: true,
              type: 'torznab',
              categories: [2000],
              priority: 0,
              enabled: true,
            },
          ],
        });
      }
      if (url.includes('/movies/3/releases') || url.includes('/series/9/releases')) {
        if (releaseGate) await releaseGate;
        return jsonResponse(itemAnswer.body, itemAnswer.status);
      }
      if (url.includes('/search/releases')) {
        return jsonResponse(searchAnswer.body, searchAnswer.status);
      }
      if (url.includes('/movies/3/grab')) return jsonResponse({}, 204);
      if (url.includes('/movies/3')) return jsonResponse(MOVIE);
      if (url.includes('/series/9')) return jsonResponse(SITE);
      if (url.includes('/system/status')) {
        return jsonResponse({
          version: '0.1.0',
          mode: 'server',
          storage_root: '/data',
          schema_version: 12,
          scanning: false,
          counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
        });
      }
      if (url.includes('/downloads')) return jsonResponse({ downloads: [] });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  session.user = null;
  downloads.stopSoon();
  downloads.items = null;
  clearToasts();
  vi.unstubAllGlobals();
});

async function settle() {
  for (let i = 0; i < 6; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function mountPicker() {
  app = mount(ReleaseSearch, { target: host, props: { kind: 'movie' as const, id: 3 } });
}

function mountSitePicker(kind: 'site' | 'series' = 'site') {
  app = mount(ReleaseSearch, {
    target: host,
    props: { kind, id: 9, season: 2026, episode: 24 },
  });
}

function queryBox(): HTMLInputElement {
  return host.querySelector<HTMLInputElement>('input[type="search"]')!;
}

/** Type into the query box the way a user does, so the binding sees it. */
function type(value: string) {
  const input = queryBox();
  input.value = value;
  input.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function clickSearch() {
  const button = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === 'Search',
  );
  button!.click();
}

describe('ReleaseSearch', () => {
  it('opens pre-filled with the query the server derived', async () => {
    mountPicker();
    await settle();

    expect(queryBox().value).toBe('Blade Runner 2049 2017');
    expect(host.textContent).toContain('Blade Runner 2049');
  });

  it('keeps the locked grab target visible beside the editable query', async () => {
    mountPicker();
    await settle();

    expect(host.querySelector('[data-search-context]')?.textContent).toContain(
      'Movie · Blade Runner 2049',
    );
  });

  it('names the indexers that did not answer', async () => {
    mountPicker();
    await settle();

    expect(host.textContent).toContain('Down Indexer');
    expect(host.textContent).toContain('dial tcp: refused');
  });

  it('re-runs the per-item search when the query is untouched', async () => {
    mountPicker();
    await settle();
    calls.length = 0;

    clickSearch();
    await settle();

    // The per-item builders send several queries at once; routing an unedited
    // re-search through the free-text endpoint would lose half the results.
    expect(calls.some((c) => c.url.includes('/movies/3/releases'))).toBe(true);
    expect(calls.some((c) => c.url.includes('/search/releases'))).toBe(false);
  });

  it('sends an edited query to the universal endpoint, scored by the item’s library', async () => {
    mountPicker();
    await settle();
    calls.length = 0;

    type('br2049 remux');
    clickSearch();
    await settle();

    const search = calls.find((c) => c.url.includes('/search/releases'));
    expect(search).toBeDefined();
    expect(search!.url).toContain('q=br2049+remux');
    expect(search!.url).toContain('library_id=4');
    expect(host.textContent).toContain('BR2049.2017.REMUX-GROUP');
  });

  it('still grabs through the item’s own endpoint after an edited search', async () => {
    mountPicker();
    await settle();
    type('br2049 remux');
    clickSearch();
    await settle();
    calls.length = 0;

    host.querySelector<HTMLButtonElement>('tbody button')!.click();
    await settle();

    const grab = calls.find((c) => c.method === 'POST');
    expect(grab?.url).toContain('/library/movies/3/grab');
    expect(grab?.body).toEqual({ release_id: 99 });
    // Nothing is posted to the universal grab: the item context never leaves.
    expect(calls.some((c) => c.url.includes('/search/grab'))).toBe(false);
    expect(window.location.pathname).not.toBe('/queue');
  });

  it('seeds the box with the query-language spelling the server sent, not the raw query', async () => {
    // The raw `query` is only the first string the fan-out sent; the expression
    // is the whole search written in the language the box speaks, so widening
    // it is an edit rather than a retype.
    (itemAnswer.body as Record<string, unknown>).search_expression =
      'title:"Blade Runner 2049" year:2017';
    mountPicker();
    await settle();

    expect(queryBox().value).toBe('title:"Blade Runner 2049" year:2017');
  });

  it('seeds the box from the item while the search is still in flight', async () => {
    (itemAnswer.body as Record<string, unknown>).search_expression =
      'title:"Blade Runner 2049" year:2017';
    let releaseSearch!: () => void;
    releaseGate = new Promise((resolve) => (releaseSearch = resolve));
    mountPicker();
    await settle();

    // The fan-out has not answered, but the box already speaks: the item
    // arrived, and the client twin wrote the same seed the server will send.
    expect(queryBox().value).toBe('title:"Blade Runner 2049" year:2017');

    releaseSearch();
    await settle();
    // The authoritative seed replaced the twin without visible change.
    expect(queryBox().value).toBe('title:"Blade Runner 2049" year:2017');
  });

  it('puts the syntax help inside the search box as an icon button', async () => {
    mountPicker();
    await settle();

    const toggle = host.querySelector<HTMLButtonElement>('[data-syntax-toggle]')!;
    expect(toggle).not.toBeNull();
    // Icon-only, so the name lives in the accessible label.
    expect(toggle.getAttribute('aria-label')).toBe('Query syntax');
    // It rides inside the query box's wrapper, not in a row of its own.
    expect(toggle.closest('.relative')?.querySelector('input')).not.toBeNull();
  });

  it('counts the rows the expression hid, and says nothing when it hid none', async () => {
    mountPicker();
    await settle();
    // The seed answer hid nothing, so there is no note to read.
    expect(host.querySelector('[data-filtered-note]')).toBeNull();

    (searchAnswer.body as Record<string, unknown>).filtered = 3;
    type('br2049 remux -hdtv');
    clickSearch();
    await settle();

    expect(host.querySelector('[data-filtered-note]')?.textContent).toContain(
      '3 results hidden by your filters',
    );
  });

  it('shows the parser’s own message when the expression will not read', async () => {
    mountPicker();
    await settle();

    searchAnswer.status = 400;
    searchAnswer.body = { error: 'unclosed quote in query' };
    type('title:"Dune');
    clickSearch();
    await settle();

    expect(host.textContent).toContain('unclosed quote in query');
  });

  it('opens the syntax cheatsheet in a modal naming the fields the language understands', async () => {
    mountPicker();
    await settle();
    expect(host.querySelector('[data-syntax-help]')).toBeNull();

    const toggle = host.querySelector<HTMLButtonElement>('[data-syntax-toggle]')!;
    toggle.click();
    flushSync();

    // The cheatsheet lives in a dialog now, closed by the modal's own
    // affordances rather than a second press of the opener.
    expect(host.querySelector('[data-syntax-help]')?.closest('[role="dialog"]')).not.toBeNull();
    const panel = host.querySelector('[data-syntax-help]')?.textContent ?? '';
    for (const field of ['title', 'site', 'year', 'season', 'episode', 'date', 'quality', 'codec']) {
      expect(panel).toContain(field);
    }
    expect(panel).toContain('site:"Vixen" date:2026-01-19');
    expect(panel).toContain('foo OR bar');

    const close = [...host.querySelectorAll<HTMLButtonElement>('button')].find(
      (candidate) => candidate.getAttribute('aria-label') === 'Close',
    )!;
    close.click();
    flushSync();
    expect(host.querySelector('[data-syntax-help]')).toBeNull();
  });

  it('seeds a site with the scene spelling before the search lands', async () => {
    (itemAnswer.body as Record<string, unknown>).search_expression = SITE_EXPRESSION;
    let releaseSearch!: () => void;
    releaseGate = new Promise((resolve) => (releaseSearch = resolve));
    mountSitePicker();
    await settle();

    expect(queryBox().value).toBe(SITE_EXPRESSION);
    expect(host.textContent).toContain('Transfixed · 2026 · #024');
    expect(host.textContent).not.toContain('S2026E24');

    releaseSearch();
    await settle();
    expect(queryBox().value).toBe(SITE_EXPRESSION);
  });

  it('grabs only the scene named by the site route', async () => {
    (itemAnswer.body as Record<string, unknown>).search_expression = SITE_EXPRESSION;
    mountSitePicker();
    await settle();
    calls.length = 0;

    host.querySelector<HTMLButtonElement>('tbody button')!.click();
    await settle();

    const grab = calls.find((call) => call.method === 'POST');
    expect(grab?.url).toBe('/api/v1/library/series/9/grab?season=2026&episode=24');
    expect(grab?.body).toEqual({ release_id: 7 });
  });

  it('treats an adult series row as a site even when the route said series', async () => {
    // Wanted used to open /series/:id/search/:season/:episode for a scene.
    // The item is a site; the television seed must never reach the box.
    (itemAnswer.body as Record<string, unknown>).search_expression = SITE_EXPRESSION;
    let releaseSearch!: () => void;
    releaseGate = new Promise((resolve) => (releaseSearch = resolve));
    mountSitePicker('series');
    await settle();

    expect(queryBox().value).toBe(SITE_EXPRESSION);
    expect(host.textContent).toContain('Transfixed · 2026 · #024');
    expect(host.textContent).not.toContain('S2026E24');
    expect(host.querySelector('a[href="/adult/sites/9"]')).not.toBeNull();

    releaseSearch();
    await settle();
    expect(queryBox().value).toBe(SITE_EXPRESSION);
  });

  it('omits the adult category block when the module is not visible', async () => {
    session.user = { username: 'admin', role: 'admin', open: false, adult: false } as never;
    mountPicker();
    await settle();

    const trigger = [...host.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
      b.textContent?.includes('All categories'),
    );
    trigger!.click();
    flushSync();

    const options = [...host.querySelectorAll<HTMLElement>('[role="dialog"] li button')].map(
      (b) => b.textContent?.trim(),
    );
    expect(options).not.toContain('6000 XXX');
    expect(options).toContain('2000 Movies');
  });
});
