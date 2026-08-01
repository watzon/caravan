/**
 * Boot smoke test. A build that compiles still white-screens if the shell
 * throws on mount, so this mounts the real App against a stubbed /api/v1 and
 * asserts the two things that prove the shell came up: the sidebar chrome and
 * the routed screen.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import App from './App.svelte';
import { navigate } from './lib/router.svelte';
import type { DownloadStatus, Movie, Release, SystemStatus } from './lib/api/types';

const STATUS: SystemStatus = {
  version: '0.1.0',
  mode: 'server',
  storage_root: '/data',
  schema_version: 1,
  scanning: false,
  counts: { movies: 1, series: 0, media_files: 1, unmatched: 0 },
  disk_free_bytes: 500 * 1024 ** 3,
  disk_total_bytes: 1024 ** 4,
  engine_health: 'ok',
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
  poster_url: '',
  monitored: true,
  quality_profile_id: 0,
  release_date: '2008-05-20',
  added_at: '2026-07-31T00:00:00Z',
  updated_at: '2026-07-31T00:00:00Z',
  file: null,
};

/** One active download and one that is waiting on the user. */
const DOWNLOADS: DownloadStatus[] = [
  {
    id: 'hash-a',
    state: 'downloading',
    name: 'Big.Buck.Bunny.2008.1080p.BluRay.x264-GROUP',
    progress: 0.42,
    bytes_done: 1024,
    size: 2048,
    down_rate: 512,
    up_rate: 0,
    eta_seconds: 60,
    ratio: 0,
    save_path: 'incomplete/bbb',
    error: '',
  },
  {
    id: 'hash-b',
    state: 'paused',
    name: 'Something.Else.2020.720p-GRP',
    progress: 0.1,
    bytes_done: 100,
    size: 1000,
    down_rate: 0,
    up_rate: 0,
    eta_seconds: -1,
    ratio: 0,
    save_path: 'incomplete/else',
    error: '',
  },
];

const PARSED = {
  title: 'Big Buck Bunny',
  year: 2008,
  season: 0,
  episodes: [] as number[],
  quality: '720p',
  source: 'webdl',
  codec: 'x264',
  audio: 'AAC',
  group: 'GRP',
  proper: false,
  repack: false,
  edition: '',
  confidence: 0.9,
};

const RELEASES: Release[] = [
  {
    id: 2,
    indexer_id: 1,
    indexer: 'Test Indexer',
    title: 'Big.Buck.Bunny.2008.2160p.CAM.x264-BAD',
    guid: 'guid-cam',
    download_url: 'magnet:?xt=urn:btih:bad',
    info_hash: 'bad',
    protocol: 'torrent',
    size: 1024,
    seeders: 5,
    leechers: 1,
    published_at: '2026-07-01T00:00:00Z',
    parsed: { ...PARSED, quality: '2160p', source: 'cam' },
  },
  {
    id: 1,
    indexer_id: 1,
    indexer: 'Test Indexer',
    title: 'Big.Buck.Bunny.2008.720p.WEB-DL.x264-GRP',
    guid: 'guid-ok',
    download_url: 'magnet:?xt=urn:btih:ok',
    info_hash: 'ok',
    protocol: 'torrent',
    size: 2048,
    seeders: 30,
    leechers: 2,
    published_at: '2026-07-20T00:00:00Z',
    parsed: PARSED,
  },
];

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
      // The sidebar badge polls the queue as soon as the shell mounts.
      if (url.endsWith('/downloads')) return jsonResponse({ downloads: DOWNLOADS });
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

  it('badges the queue nav with the active download count', async () => {
    app = mount(App, { target: host });
    await settle();

    const queueLink = host.querySelector('a[href="/queue"]');
    expect(queueLink).not.toBeNull();
    // One downloading, one paused: a paused download waits on the user, so the
    // badge counts one (see isActiveDownload).
    expect(queueLink?.textContent).toContain('1');
    expect(host.querySelector('[aria-label="1 active downloads"]')).not.toBeNull();
  });

  it('renders the queue screen with its rows and controls', async () => {
    app = mount(App, { target: host });
    await settle();
    // The router reads window.location once, at module load, so later screens
    // are reached the way a user reaches them: by navigating.
    navigate('/queue');
    await settle();

    expect(host.textContent).toContain('Big.Buck.Bunny');
    expect(host.textContent).toContain('Downloading');
    expect(host.textContent).toContain('Paused');
    // A paused row offers Resume, an active one offers Pause.
    expect(host.textContent).toContain('Resume');
    expect(host.textContent).toContain('Pause');
    expect(host.querySelectorAll('[role="progressbar"]').length).toBeGreaterThanOrEqual(2);
    expect(host.textContent).not.toContain('The queue is empty');
  });

  it('renders the release picker with the best release first', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/system/status')) return jsonResponse(STATUS);
        if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [MOVIE] });
        if (url.endsWith('/library/movies/7')) return jsonResponse(MOVIE);
        if (url.endsWith('/library/movies/7/releases')) {
          return jsonResponse({ releases: RELEASES });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();
    navigate('/movies/7/search');
    await settle();

    const rows = host.querySelectorAll('tbody tr');
    expect(rows).toHaveLength(2);
    // The CAM is flagged, so it sorts below the clean 720p despite claiming 2160p.
    expect(rows[0]?.textContent).toContain('720p');
    expect(rows[1]?.textContent).toContain('CAM');
    expect(host.textContent).toContain('Grab');
    expect(host.textContent).not.toContain('No releases found');
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
