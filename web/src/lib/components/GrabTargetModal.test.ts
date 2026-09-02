/**
 * Where a universal-search grab lands.
 *
 * The dialog exists because a free-text result has no item behind it, so the
 * request it builds is the whole contract: an untied grab names a library and
 * nothing else, and a tied one names the item as well. Both shapes are pinned
 * here, along with the reason the library question is asked at all — the server
 * requires it in both modes.
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
      // The add the "add new from metadata" dialog makes, before the list
      // branches below: it is a POST to the same path the list is a GET of.
      if (url.includes('/library/movies') && init?.method === 'POST') {
        movies = [...movies, { id: 12, title: 'Ponyo', library_id: 3 }];
        return jsonResponse({ id: 12, title: 'Ponyo' }, 201);
      }
      if (url.includes('/library/movies')) return jsonResponse({ movies });
      if (url.includes('/library/series')) return jsonResponse({ series: seriesRows });
      if (url.includes('/search/grab')) return new Response(null, { status: 204 });
      if (url.includes('/api/v1/search')) {
        return jsonResponse({
          movies: [
            {
              tmdb_id: 4,
              provider: 'tmdb',
              provider_ref: '4',
              title: 'Ponyo',
              year: 2008,
              overview: '',
              release_date: '2008-07-19',
              vote_average: 7.7,
              vote_count: 4_000,
              poster_url: '',
            },
          ],
          series: [],
          providers: ['tmdb'],
          library_id: 3,
          errors: [],
        });
      }
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

  /**
   * A dormant shelf refuses everyone, admins included (core.LibraryVisible), so
   * every route this dialog uses answers 404 for one — the grab included. The
   * admin list still carries it, because that is where the toggle reviving it
   * lives, so the filter has to be here rather than in the response.
   */
  it('offers no dormant shelf, which every grab would 404 on', () => {
    libraries.all = [
      library(),
      library({ id: 4, name: 'Archive', root_path: 'library/Archive', is_default: false, active: false }),
    ];
    mountModal();

    const options = [...host.querySelectorAll('option')].map((o) => o.textContent?.trim());
    expect(options).toEqual(['Movies']);
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

  /**
   * "Add this title, then tie this release to it" is a sentence about ONE
   * library. The add dialog is told which, so the new row lands on the shelf
   * the tie names — before this it targeted the kind's default, and a tie to a
   * row that had gone somewhere else is what `itemInLibrary` refuses
   * (internal/api/searchreleases.go).
   */
  it('adds a new title into the shelf the tie names, then ties it', async () => {
    libraries.all = [
      library(),
      library({ id: 2, kind: 'tv', name: 'TV', is_default: true }),
      library({ id: 3, name: 'Kids', root_path: 'library/Kids', is_default: false }),
    ];
    mountModal();

    const select = host.querySelector<HTMLSelectElement>(
      'select[aria-label="Target library"]',
    )!;
    select.selectedIndex = 2;
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    chooseMode('tie');
    await settle();

    button('Add new from metadata...').click();
    flushSync();

    // The nested dialog has no tab row: one shelf is not a choice.
    const added = [...host.querySelectorAll('[role="dialog"]')].at(-1)!;
    expect(added.querySelector('[role="tab"]')).toBeNull();

    const input = added.querySelector<HTMLInputElement>('input[type="search"]')!;
    input.value = 'ponyo';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    await settle();
    (added.querySelector('ul button') as HTMLButtonElement).click();
    await settle();

    const add = calls.find((c) => c.method === 'POST' && c.url.includes('/library/movies'));
    expect(add?.body).toMatchObject({ tmdb_id: 4, library_id: 3 });
    // And the search that found it asked the same shelf's chain.
    expect(calls.find((c) => c.url.includes('/api/v1/search'))?.url).toContain('library_id=3');

    // The row came back owned by that shelf, so the tie is made rather than
    // refused with "it went to another library".
    button('Grab').click();
    await settle();
    expect(calls.find((c) => c.url.includes('/search/grab'))?.body).toEqual({
      release_id: 42,
      library_id: 3,
      tie: { media_type: 'movie', media_id: 12 },
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
