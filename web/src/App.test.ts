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
import { shutdown } from './lib/state/shutdown.svelte';
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
  ffmpeg_available: true,
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
  bit_depth: 0,
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
    compatibility: { verdict: 'unknown', reasons: [] },
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
    compatibility: { verdict: 'unknown', reasons: [] },
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
/** What /system/status answers this test; a test may swap it before mounting. */
let statusBody: SystemStatus = STATUS;

beforeEach(() => {
  statusBody = STATUS;
  window.history.replaceState({}, '', '/movies');
  // A module singleton, and one test drives it to its terminal state.
  shutdown.phase = 'idle';
  shutdown.confirming = false;
  shutdown.error = null;
  // jsdom has no layout, so scrollTo is unimplemented; the router calls it.
  window.scrollTo = () => {};
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/system/status')) return jsonResponse(statusBody);
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
    expect(host.querySelector('[aria-label="1 active in Queue"]')).not.toBeNull();
  });

  it('badges the library nav items with their counts', async () => {
    app = mount(App, { target: host });
    await settle();

    // The status fixture reports one movie and zero series: a zero renders
    // nothing rather than a grey 0.
    expect(host.querySelector('[aria-label="1 in Movies"]')).not.toBeNull();
    expect(host.querySelector('[aria-label*="in Series"]')).toBeNull();
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
    // The toggle is icon-only: the label lives in the tooltip and the
    // sr-only text, and it flips between Pause and Resume with the state.
    expect(host.querySelector('[title="Resume download"]')).not.toBeNull();
    expect(host.querySelector('[title="Pause download"]')).not.toBeNull();
    expect(host.textContent).toContain('Resume download');
    expect(host.textContent).toContain('Pause download');
    expect(host.querySelectorAll('[role="progressbar"]').length).toBeGreaterThanOrEqual(2);
    expect(host.textContent).not.toContain('The queue is empty');
  });

  // Killing one download client must say so, name it, and leave the rest of
  // the shell working (SPEC §5.1, PLAN phase 6 task 4).
  it('banners a download client the poller cannot reach', async () => {
    statusBody = {
      ...STATUS,
      unhealthy_download_clients: [
        {
          id: 3,
          name: 'Seedbox',
          type: 'qbittorrent',
          error: 'connection refused',
          since: '2026-08-01T09:30:00Z',
        },
      ],
    };

    app = mount(App, { target: host });
    await settle();

    expect(host.textContent).toContain('Download client Seedbox is unreachable');
    expect(host.textContent).toContain('connection refused');
    // The rest of the shell is unaffected, which is the point of the wording.
    // The rest of the shell is unaffected, which is the point of the wording:
    // the sidebar, the queue badge and the routed screen all still render.
    expect(host.textContent).toContain('CARAVAN');
    expect(host.querySelector('a[href="/queue"]')).not.toBeNull();
    expect(host.textContent).not.toContain('Caravan server unreachable');
  });

  it('shows no client banner when every client is answering', async () => {
    app = mount(App, { target: host });
    await settle();
    expect(host.textContent).not.toContain('unreachable');
  });

  it('renders the release picker with the best release first', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/system/status')) return jsonResponse(statusBody);
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

  it('flags a DTS/HEVC release against the active TV profile in the picker', async () => {
    const clean = RELEASES[1]!;
    const flagged: Release = {
      ...clean,
      guid: 'guid-dts',
      title: 'Big.Buck.Bunny.2008.1080p.BluRay.x265.10bit.DTS-HD.MA.7.1-GRP',
      compatibility: {
        verdict: 'incompatible',
        reasons: ['HEVC video (profile allows H.264)', 'DTS-HD audio (profile allows AAC)'],
      },
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/system/status')) return jsonResponse(statusBody);
        if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [MOVIE] });
        if (url.endsWith('/library/movies/7')) return jsonResponse(MOVIE);
        if (url.endsWith('/library/movies/7/releases')) {
          return jsonResponse({ releases: [flagged, clean] });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();
    navigate('/movies/7/search');
    await settle();

    const badge = [...host.querySelectorAll('span')].find(
      (node) => node.textContent?.trim() === 'NEEDS CONVERT',
    );
    expect(badge, 'the incompatible release carries a NEEDS CONVERT badge').toBeDefined();
    expect(badge?.getAttribute('title')).toContain('DTS-HD audio');
    expect(badge?.getAttribute('title')).toContain('HEVC video');
    // The clean release is not flagged, so the badge appears exactly once.
    expect(
      [...host.querySelectorAll('span')].filter((n) => n.textContent?.trim() === 'NEEDS CONVERT'),
    ).toHaveLength(1);
  });

  it('offers recovery and safe shutdown after a portable dirty eject', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/system/status')) {
          return jsonResponse({ ...STATUS, mode: 'portable', dirty: true });
        }
        if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [MOVIE] });
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    // The recovery banner is up, with the way out on it (SPEC §13).
    expect(host.textContent).toContain('Last shutdown was not clean');
    expect(host.textContent).toContain('Verify & rescan');
    // And portable mode gets the eject control the drive needs (SPEC §2.3).
    expect(host.textContent).toContain('Shut down safely');
  });

  it('replaces the shell with "safe to eject" once the server has stopped', async () => {
    // The 202 only means the teardown started; the process keeps answering
    // while it flushes the engine and checkpoints the WAL, and the eject
    // promise waits for the listener to actually go away.
    let listening = true;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/system/shutdown')) {
          return new Response(JSON.stringify({ status: 'shutting down' }), {
            status: 202,
            headers: { 'Content-Type': 'application/json' },
          });
        }
        if (url.endsWith('/system/status')) {
          if (!listening) throw new TypeError('Failed to fetch');
          return jsonResponse({ ...STATUS, mode: 'portable' });
        }
        if (url.endsWith('/downloads')) return jsonResponse({ downloads: [] });
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [MOVIE] });
        throw new Error(`unexpected fetch: ${url} ${init?.method ?? 'GET'}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    const label = (text: string) =>
      [...host.querySelectorAll('button')].find((b) => b.textContent?.trim() === text);

    label('Shut down safely')!.click();
    await settle();
    label('Shut down')!.click();
    await settle();

    // Still writing: the shell stays up rather than inviting an eject.
    expect(host.textContent).not.toContain('Safe to eject');

    listening = false;
    await new Promise((resolve) => setTimeout(resolve, 700));
    await settle();

    expect(host.textContent).toContain('Safe to eject');
    // The shell is gone with it, which is what stops the polls that would
    // otherwise hammer a server that no longer answers.
    expect(host.querySelector('a[href="/movies"]')).toBeNull();
    expect(host.textContent).not.toContain('CARAVAN');
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
