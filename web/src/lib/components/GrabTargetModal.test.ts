/**
 * Where a universal-search grab lands (plan part B8).
 *
 * The dialog exists because a free-text result has no item behind it, so the
 * request it builds is the whole contract: an untied grab names a library and
 * nothing else, and a tied one names the item as well. Both shapes are pinned
 * here, along with the reason the library question is asked at all — the
 * server requires it in both modes.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import GrabTargetModal from './GrabTargetModal.svelte';
import type { Library, ParsedRelease, Release } from '../api/types';
import { libraries } from '../state/libraries.svelte';
import { DEBOUNCE_MS } from '../typeahead';
import { clearToasts } from '../state/toast.svelte';

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

function library(overrides: Partial<Library> = {}): Library {
  return {
    id: 1,
    kind: 'movie',
    name: 'Movies',
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
    ...overrides,
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: unknown }[];
let movies: unknown[];
let seriesRows: unknown[];

beforeEach(() => {
  calls = [];
  movies = [{ id: 11, title: 'Dune', library_id: 1 }];
  seriesRows = [{ id: 21, title: 'Severance', library_id: 2 }];
  host = document.createElement('div');
  document.body.appendChild(host);
  libraries.all = [library(), library({ id: 2, kind: 'tv', name: 'TV', is_default: true })];
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
      if (url.includes('/library/movies')) return jsonResponse({ movies });
      if (url.includes('/library/series')) return jsonResponse({ series: seriesRows });
      if (url.includes('/search/grab')) return new Response(null, { status: 204 });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  libraries.all = [];
  libraries.loaded = false;
  clearToasts();
  vi.unstubAllGlobals();
});

/**
 * The tie picker runs through the shared typeahead, which debounces; a settle
 * that only drains microtasks would look at the list before it exists.
 */
async function settle() {
  for (let i = 0; i < 4; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, DEBOUNCE_MS + 20));
  for (let i = 0; i < 4; i += 1) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function mountModal(ongrabbed?: () => void) {
  app = mount(GrabTargetModal, {
    target: host,
    props: { release: RELEASE, onclose: () => {}, ongrabbed },
  }) as Record<string, unknown>;
  flushSync();
}

function button(label: string): HTMLButtonElement {
  return [...host.querySelectorAll<HTMLButtonElement>('button')].find(
    (b) => b.textContent?.trim() === label,
  )!;
}

function chooseMode(value: 'park' | 'tie') {
  const radio = host.querySelector<HTMLInputElement>(`input[type="radio"][value="${value}"]`)!;
  radio.checked = true;
  radio.dispatchEvent(new Event('change', { bubbles: true }));
  flushSync();
}

describe('GrabTargetModal', () => {
  it('offers every library the store knows about', () => {
    mountModal();
    const options = [...host.querySelectorAll('option')].map((o) => o.textContent?.trim());
    expect(options).toEqual(['Movies', 'TV']);
  });

  it('opens on download-only and says where the file will wait', () => {
    mountModal();
    expect(host.querySelector<HTMLInputElement>('input[value="park"]')?.checked).toBe(true);
    expect(host.textContent).toContain(
      'Send the finished download to Scan Review for Movies. You will match it there.',
    );
    // The tie picker does not exist until the user asks for it.
    expect(host.querySelector('[data-tie-picker]')).toBeNull();
  });

  it('posts an untied grab as library only, with no tie at all', async () => {
    mountModal();
    button('Grab').click();
    await settle();

    const grab = calls.find((c) => c.url.includes('/search/grab'));
    expect(grab?.method).toBe('POST');
    expect(grab?.body).toEqual({ release_id: 42, library_id: 1 });
  });

  it('tells the screen the grab landed, so it can move on', async () => {
    let done = 0;
    mountModal(() => (done += 1));
    button('Grab').click();
    await settle();
    expect(done).toBe(1);
  });

  it('shows the chosen library’s own items when tying', async () => {
    mountModal();
    chooseMode('tie');
    await settle();

    expect(host.querySelector('[data-tie-picker]')).not.toBeNull();
    expect(host.textContent).toContain('Dune');
    // The movie library was asked for movies, not series.
    expect(calls.some((c) => c.url.includes('/library/movies'))).toBe(true);
  });

  it('refuses to confirm a tie until an item is named', async () => {
    mountModal();
    chooseMode('tie');
    await settle();
    expect(button('Grab').disabled).toBe(true);
  });

  it('posts a movie tie with the item the user picked', async () => {
    mountModal();
    chooseMode('tie');
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

  it('leaves a series tie unscoped when the season and episode are blank', async () => {
    mountModal();
    // Switch to the TV library: an adult site and a series are both series
    // rows, which is why the tie's media_type follows the library's kind.
    const select = host.querySelector<HTMLSelectElement>('select')!;
    select.value = '2';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    chooseMode('tie');
    await settle();
    button('Severance').click();
    flushSync();
    button('Grab').click();
    await settle();

    const grab = calls.find((c) => c.url.includes('/search/grab'));
    expect(grab?.body).toEqual({
      release_id: 42,
      library_id: 2,
      tie: { media_type: 'series', media_id: 21 },
    });
  });

  it('narrows a series tie to the season and episode that were typed', async () => {
    mountModal();
    const select = host.querySelector<HTMLSelectElement>('select')!;
    select.value = '2';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    chooseMode('tie');
    await settle();
    button('Severance').click();
    flushSync();

    const season = host.querySelector<HTMLInputElement>('input[aria-label="Season number"]')!;
    season.value = '2';
    season.dispatchEvent(new Event('input', { bubbles: true }));
    const episode = host.querySelector<HTMLInputElement>('input[aria-label="Episode number"]')!;
    episode.value = '4';
    episode.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    button('Grab').click();
    await settle();

    const grab = calls.find((c) => c.url.includes('/search/grab'));
    expect(grab?.body).toEqual({
      release_id: 42,
      library_id: 2,
      tie: { media_type: 'series', media_id: 21, season: 2, episode: 4 },
    });
  });

  it('hides items another library owns, because the server would refuse the tie', async () => {
    // The series row belongs to library 2; the movie library must not offer it.
    movies = [{ id: 11, title: 'Dune', library_id: 9 }];
    mountModal();
    chooseMode('tie');
    await settle();
    expect(host.textContent).toContain('Nothing in Movies matches');
  });
});
