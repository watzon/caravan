/**
 * The universal indexer search screen.
 *
 * Three promises are pinned here. The URL is the search, so a shared link
 * reproduces its own results. A grab always goes through the target dialog,
 * because a free-text result has no item behind it. And the two answers that
 * dialog can give — untied and tied — reach the server as two different request
 * shapes.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Search from './Search.svelte';
import type { Library, ParsedRelease, Release } from '../api/types';
import { libraries } from '../state/libraries.svelte';
import { downloads } from '../state/downloads.svelte';
import { navigate } from '../router.svelte';
import { clearToasts } from '../state/toast.svelte';
import { DEBOUNCE_MS } from '../typeahead';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

function parsed(): ParsedRelease {
  return {
    title: 'Dune',
    year: 2021,
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
  };
}

const RELEASE: Release = {
  id: 42,
  indexer_id: 1,
  indexer: 'Test Indexer',
  title: 'Dune.2021.1080p.BluRay.x264-GROUP',
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
};

const MOVIE_LIBRARY: Library = {
  id: 1,
  kind: 'movie',
  name: 'Movies',
  icon: '',
  root_path: 'library/Movies',
  provider: 'tmdb',
  providers: ['tmdb'],
  is_default: true,
  item_count: 0,
  active: true,
  restricted: false,
  dlna_visible: true,
  route_torrent: '',
  route_usenet: '',
  quality_profile_id: 0,
  indexers: [],
};

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: unknown }[];

beforeEach(() => {
  calls = [];
  host = document.createElement('div');
  document.body.appendChild(host);
  navigate('/search', { replace: true });
  libraries.all = [MOVIE_LIBRARY];
  libraries.loaded = true;
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
      if (url.includes('/search/releases')) {
        return jsonResponse({
          query: 'dune',
          queries: ['dune'],
          releases: [RELEASE],
          errors: [],
        });
      }
      if (url.includes('/search/grab')) return new Response(null, { status: 204 });
      if (url.includes('/library/movies')) return jsonResponse({ movies: [{ id: 11, title: 'Dune', library_id: 1 }] });
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
  navigate('/search', { replace: true });
  libraries.all = [];
  libraries.loaded = false;
  downloads.stopSoon();
  downloads.items = null;
  clearToasts();
  vi.unstubAllGlobals();
});

async function settle() {
  for (let i = 0; i < 4; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, DEBOUNCE_MS + 20));
  for (let i = 0; i < 4; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function mountScreen() {
  app = mount(Search, { target: host });
  flushSync();
}

function button(label: string): HTMLButtonElement {
  return [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label,
  )!;
}

/** Search, then open the target dialog on the one result. */
async function searchAndOpenTarget() {
  const input = host.querySelector<HTMLInputElement>('input[type="search"]')!;
  input.value = 'dune';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
  button('Search').click();
  await settle();
  host.querySelector<HTMLButtonElement>('tbody button')!.click();
  await settle();
}

describe('Search', () => {
  it('says nothing has been searched for yet, rather than "nothing found"', () => {
    mountScreen();
    expect(host.textContent).toContain('Search every enabled indexer');
    expect(host.textContent).not.toContain('No releases found');
  });

  it('seeds the query box from the URL and searches it', async () => {
    navigate('/search?q=dune&cats=2000&indexers=1', { replace: true });
    mountScreen();
    await settle();

    expect(host.querySelector<HTMLInputElement>('input[type="search"]')!.value).toBe('dune');
    const search = calls.find((c) => c.url.includes('/search/releases'));
    expect(search?.url).toContain('q=dune');
    expect(search?.url).toContain('cats=2000');
    expect(search?.url).toContain('indexer_ids=1');
  });

  it('writes the search into the URL so the result is shareable', async () => {
    mountScreen();
    await settle();
    const input = host.querySelector<HTMLInputElement>('input[type="search"]')!;
    input.value = 'dune';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button('Search').click();
    await settle();

    expect(window.location.search).toBe('?q=dune');
  });

  it('grabs through the target dialog rather than straight off the row', async () => {
    mountScreen();
    await settle();
    await searchAndOpenTarget();

    expect(host.textContent).toContain('Download only');
    // Nothing was posted by opening the dialog.
    expect(calls.some((c) => c.method === 'POST')).toBe(false);
  });

  it('posts an untied grab as library only, with no tie', async () => {
    mountScreen();
    await settle();
    await searchAndOpenTarget();

    button('Grab').click();
    await settle();

    const grab = calls.find((c) => c.url.includes('/search/grab'));
    expect(grab?.body).toEqual({ release_id: 42, library_id: 1 });
  });

  it('posts a tied grab with the item the user named', async () => {
    mountScreen();
    await settle();
    await searchAndOpenTarget();

    const tie = host.querySelector<HTMLInputElement>('input[type="radio"][value="tie"]')!;
    tie.checked = true;
    tie.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    button('Dune').click();
    flushSync();
    button('Grab').click();
    await settle();

    const grab = calls.find((c) => c.url.includes('/search/grab'));
    expect(grab?.body).toEqual({
      release_id: 42,
      library_id: 1,
      tie: { media_type: 'movie', media_id: 11 },
    });
  });

  it('stays on the search page once a grab is accepted', async () => {
    mountScreen();
    await settle();
    await searchAndOpenTarget();

    button('Grab').click();
    await settle();

    expect(window.location.pathname).toBe('/search');
    expect(host.textContent).toContain('Downloading');
  });
});
