/**
 * The editable per-item picker (plan part B7).
 *
 * The screen makes two promises that pull in opposite directions, and both are
 * pinned here: the query is the user's to change, and the target is not. An
 * edited query goes to the universal endpoint; the grab that follows still
 * posts to this item's own grab endpoint, because the route — not the query
 * box — is what says where the file lands.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ReleaseSearch from './ReleaseSearch.svelte';
import type { Movie, ParsedRelease, Release } from '../api/types';
import { session } from '../state/session.svelte';
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
  library_id: 4,
  monitored: true,
} as Movie;

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: unknown }[];

beforeEach(() => {
  calls = [];
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
      if (url.includes('/movies/3/releases')) {
        return jsonResponse({
          query: 'Blade Runner 2049 2017',
          queries: ['Blade Runner 2049 2017'],
          releases: [release()],
          errors: [{ indexer_id: 2, indexer: 'Down Indexer', error: 'dial tcp: refused' }],
        });
      }
      if (url.includes('/search/releases')) {
        return jsonResponse({
          query: 'br2049 remux',
          queries: ['br2049 remux'],
          releases: [release({ id: 99, guid: 'guid-2', title: 'BR2049.2017.REMUX-GROUP' })],
          errors: [],
        });
      }
      if (url.includes('/movies/3/grab')) return jsonResponse({}, 204);
      if (url.includes('/movies/3')) return jsonResponse(MOVIE);
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  session.user = null;
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
