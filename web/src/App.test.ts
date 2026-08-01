/**
 * Boot smoke test. A build that compiles still white-screens if the shell
 * throws on mount, so this mounts the real App against a stubbed /api/v1 and
 * asserts the two things that prove the shell came up: the sidebar chrome and
 * the routed screen.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import App from './App.svelte';
import type { Movie, SystemStatus } from './lib/api/types';

const STATUS: SystemStatus = {
  version: '0.1.0',
  mode: 'server',
  storage_root: '/data',
  schema_version: 1,
  scanning: false,
  counts: { movies: 1, series: 0, media_files: 1, unmatched: 0 },
};

const MOVIE: Movie = {
  id: 7,
  tmdb_id: 10378,
  imdb_id: '',
  title: 'Big Buck Bunny',
  sort_title: 'big buck bunny',
  year: 2008,
  overview: '',
  path: 'Movies/Big Buck Bunny (2008)',
  poster_path: '',
  monitored: true,
  quality_profile_id: 0,
  release_date: '2008-05-20',
  added_at: '2026-07-31T00:00:00Z',
  updated_at: '2026-07-31T00:00:00Z',
  file: null,
};

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  window.history.replaceState({}, '', '/movies');
  // jsdom has no layout, so scrollTo is unimplemented; the router calls it.
  window.scrollTo = () => {};
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/system/status')) return jsonResponse(STATUS);
      // The list endpoints answer with a named envelope (internal/api).
      if (url.endsWith('/library/movies')) return jsonResponse({ movies: [MOVIE] });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
});

async function settle() {
  // Two macrotask turns: one for /system/status, one for the screen's own load.
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

describe('App shell', () => {
  it('mounts, renders the sidebar and routes to the movie grid', async () => {
    app = mount(App, { target: host });
    await settle();

    expect(host.textContent).toContain('CARAVAN');
    expect(host.querySelector('a[href="/movies"]')).not.toBeNull();
    expect(host.querySelector('a[href="/series"]')).not.toBeNull();

    // The library list rendered its one movie rather than an empty state.
    expect(host.textContent).toContain('Big Buck Bunny');
    expect(host.textContent).not.toContain('No movies yet');
  });

  it('sends the user to first run when there is no storage root', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/system/status')) {
          return jsonResponse({ ...STATUS, storage_root: '' });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    expect(window.location.pathname).toBe('/first-run');
    expect(host.textContent).toContain('Choose a storage root');
  });
});
