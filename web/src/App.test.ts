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
import { discover } from './lib/state/discover.svelte';
import { session } from './lib/state/session.svelte';
import { system } from './lib/state/system.svelte';
import { shutdown } from './lib/state/shutdown.svelte';
import {
  SETTINGS_CATALOG,
  SETTINGS_CATEGORIES,
  settingsCategoryLabel,
  settingsHref,
} from './lib/settings/catalog';
import type {
  DiscoverHome,
  DownloadStatus,
  Job,
  MediaRequest,
  Movie,
  Release,
  SystemStatus,
  SystemTask,
} from './lib/api/types';
import { tasks } from './lib/state/tasks.svelte';

const STATUS: SystemStatus = {
  version: '0.1.0',
  mode: 'server',
  storage_root: '/data',
  schema_version: 1,
  scanning: false,
  counts: {
    movies: 1,
    series: 0,
    media_files: 1,
    unmatched: 0,
    // The sidebar badges each shelf from here, not from the whole-install
    // movies/series totals beside it. One entry per visible library.
    libraries: [
      { id: 1, items: 1 },
      { id: 2, items: 0 },
    ],
  },
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
  library_id: 1,
  release_date: '2008-05-20',
  min_availability: 'released',
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
    created_at: '2026-07-31T00:00:00Z',
    updated_at: '2026-07-31T00:00:00Z',
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
    created_at: '2026-07-30T00:00:00Z',
    updated_at: '2026-07-30T00:00:00Z',
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

/** One trending series is enough to prove the discover screen came up. */
const DISCOVER: DiscoverHome = {
  trending: [
    {
      media_type: 'series',
      tmdb_id: 95396,
      title: 'Severance',
      year: 2022,
      overview: 'Work-life balance, surgically.',
      poster_path: '/p.jpg',
      poster_url: '',
      backdrop_url: '',
      vote_average: 8.4,
      vote_count: 1,
      date: '2022-02-18',
      in_library: false,
      library_id: 0,
      requested: false,
    },
  ],
  popular_movies: [],
  upcoming_movies: [],
  now_playing: [],
  popular_series: [],
  upcoming_series: [],
  airing_series: [],
  movie_genres: [],
  series_genres: [],
  networks: [{ id: 213, name: 'Netflix', type: 'network', logo_url: '' }],
  studios: [],
};

let host: HTMLElement;
let app: Record<string, unknown>;

function stubViewport(matches: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn(() => ({
      matches,
      media: '(max-width: 767px)',
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    })),
  );
}

/** What /system/status answers this test; a test may swap it before mounting. */
let statusBody: SystemStatus = STATUS;
/**
 * What /auth/me answers this test; a test may swap it before mounting.
 *
 * `libraries` is what the sidebar's shelf rows are built from — the two seeded
 * defaults of a fresh install — so a session without it renders a Library group
 * holding only Wanted and Calendar.
 */
const SEEDED_LIBRARIES = [
  { id: 1, kind: 'movie', name: 'Movies', slug: 'movies', icon: '' },
  { id: 2, kind: 'tv', name: 'Series', slug: 'series', icon: '' },
];
let sessionBody: Record<string, unknown> = {
  username: '',
  role: 'admin',
  open: true,
  libraries: SEEDED_LIBRARIES,
};
/** What GET /requests answers; the sidebar badge counts the pending ones. */
let requestRows: MediaRequest[] = [];
/** What GET /system/tasks and GET /jobs answer for the footer rail. */
let taskRows: SystemTask[] = [];
let jobRows: Job[] = [];

beforeEach(() => {
  statusBody = STATUS;
  sessionBody = { username: '', role: 'admin', open: true, libraries: SEEDED_LIBRARIES };
  requestRows = [];
  taskRows = [];
  jobRows = [];
  // Most shell tests deliberately run without matchMedia, like SSR/jsdom.
  // Responsive cases install their own media-query result before mounting.
  discover.reset();
  // jsdom has no layout, so scrollTo is unimplemented; the router calls it.
  window.scrollTo = () => {};
  window.history.replaceState({}, '', '/movies');
  // The router is a module singleton seeded from location at import time, and
  // `/` is Discover now rather than a redirect to the library — so the shared
  // path state has to be told, not just the History API.
  navigate('/movies', { replace: true });
  // A module singleton, and one test drives it to its terminal state.
  shutdown.phase = 'idle';
  shutdown.confirming = false;
  shutdown.error = null;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      // The shell asks who it is talking as before anything else.
      if (url.endsWith('/auth/me')) return jsonResponse(sessionBody);
      if (url.endsWith('/system/status')) return jsonResponse(statusBody);
      // The list endpoints answer with a named envelope (internal/api).
      if (url.endsWith('/library/movies')) return jsonResponse({ movies: [MOVIE] });
      if (url.endsWith('/settings')) return jsonResponse({});
      if (url.endsWith('/libraries')) return jsonResponse({ libraries: [] });
      if (url.endsWith('/indexers')) return jsonResponse({ indexers: [] });
      if (url.endsWith('/usenet-servers')) return jsonResponse({ usenet_servers: [] });
      if (url.endsWith('/download-clients')) return jsonResponse({ download_clients: [] });
      // The sidebar badge reads the queue as soon as the shell mounts.
      if (url.includes('/downloads')) return jsonResponse({ downloads: DOWNLOADS });
      // …and the requests badge alongside it.
      if (url.endsWith('/requests')) return jsonResponse({ requests: requestRows });
      if (url.includes('/system/tasks')) return jsonResponse({ tasks: taskRows });
      if (url.includes('/jobs')) return jsonResponse({ jobs: jobRows });
      if (url.endsWith('/discover')) return jsonResponse(DISCOVER);
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
  // A module singleton: these tests all run as the open-server admin, and the
  // next file must not inherit that.
  session.forget();
  tasks.tasks = null;
  tasks.jobs = null;
  tasks.stopSoon();
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
    // One row per session library, each linking with its own id: the plain
    // /movies URL is "every visible movie", which is not what a shelf row means.
    expect(host.querySelector('a[href="/l/movies"]')).not.toBeNull();
    expect(host.querySelector('a[href="/l/series"]')).not.toBeNull();

    // The library list rendered its one movie rather than an empty state.
    expect(host.textContent).toContain('Big Buck Bunny');
    expect(host.textContent).not.toContain('No movies yet');
  });

  it('routes the secondary global add action by click and keyboard', async () => {
    app = mount(App, { target: host });
    await settle();

    const add = host.querySelector<HTMLButtonElement>('header button[title="Add movie or series"]')!;
    expect(add).not.toBeNull();
    expect(add.classList).toContain('bg-raised');
    expect(add.classList).toContain('border-border-strong');
    expect(add.classList).not.toContain('bg-accent');
    expect(add.textContent).toContain('Add movie or series');
    expect(add.querySelector('path')?.getAttribute('d')).toBe('M12 5v14M5 12h14');
    // The rail no longer carries a page-level primary that duplicates this
    // one; the empty state owns the only accent add button, and the library
    // here is not empty.
    expect(host.querySelectorAll('button.bg-accent')).toHaveLength(0);

    add.click();
    flushSync();
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain('Add to library');

    host.querySelector<HTMLButtonElement>('[role="dialog"] button[aria-label="Close"]')!.click();
    flushSync();
    expect(host.querySelector('[role="dialog"]')).toBeNull();

    const shortcut = new KeyboardEvent('keydown', {
      key: 'k',
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    });
    window.dispatchEvent(shortcut);
    flushSync();

    expect(shortcut.defaultPrevented).toBe(true);
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain('Add to library');

    host.querySelector<HTMLButtonElement>('[role="dialog"] button[aria-label="Close"]')!.click();
    flushSync();

    const commandShortcut = new KeyboardEvent('keydown', {
      key: 'k',
      metaKey: true,
      bubbles: true,
      cancelable: true,
    });
    window.dispatchEvent(commandShortcut);
    flushSync();

    expect(commandShortcut.defaultPrevented).toBe(true);
    expect(host.querySelector('[role="dialog"]')?.textContent).toContain('Add to library');
  });

  it('keeps the global add action unavailable to members', async () => {
    sessionBody = { username: 'reader', role: 'member', open: false };
    app = mount(App, { target: host });
    await settle();

    expect(window.location.pathname).toBe('/discover');
    expect(host.querySelector('header button[title="Add movie or series"]')).toBeNull();

    const shortcut = new KeyboardEvent('keydown', {
      key: 'k',
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    });
    window.dispatchEvent(shortcut);
    flushSync();

    expect(shortcut.defaultPrevented).toBe(false);
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });

  it('uses responsive classes for the desktop rail and narrow drawer', async () => {
    app = mount(App, { target: host });
    await settle();

    const navigation = host.querySelector<HTMLElement>('#primary-navigation-drawer')!;
    const menu = host.querySelector<HTMLButtonElement>('[aria-controls="primary-navigation-drawer"]')!;

    expect(navigation.classList).toContain('fixed');
    expect(navigation.classList).toContain('md:static');
    expect(navigation.classList).toContain('w-60');
    expect(menu.classList).toContain('md:hidden');
  });

  it('opens from the menu button and closes on its close button or overlay', async () => {
    stubViewport(true);
    app = mount(App, { target: host });
    await settle();

    const navigation = host.querySelector<HTMLElement>('#primary-navigation-drawer')!;
    const menu = host.querySelector<HTMLButtonElement>('[aria-controls="primary-navigation-drawer"]')!;

    expect(navigation.getAttribute('aria-hidden')).toBe('true');
    expect(navigation.inert).toBe(true);
    expect(navigation.classList).toContain('-translate-x-full');
    expect(navigation.classList).toContain('md:translate-x-0');

    menu.click();
    flushSync();

    expect(menu.getAttribute('aria-expanded')).toBe('true');
    expect(navigation.getAttribute('aria-hidden')).toBeNull();
    expect(navigation.inert).toBe(false);
    expect(host.querySelector('[data-sidebar-overlay]')).not.toBeNull();

    const close = navigation.querySelector<HTMLButtonElement>('[aria-label="Close navigation"]')!;
    expect(document.activeElement).toBe(close);
    close.click();
    flushSync();
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(menu);

    menu.click();
    flushSync();
    host.querySelector<HTMLElement>('[data-sidebar-overlay]')!.click();
    flushSync();

    expect(menu.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(menu);
  });

  it('closes the narrow drawer when a navigation link changes routes', async () => {
    stubViewport(true);
    app = mount(App, { target: host });
    await settle();

    const menu = host.querySelector<HTMLButtonElement>('[aria-controls="primary-navigation-drawer"]')!;
    menu.click();
    flushSync();

    host
      .querySelector<HTMLAnchorElement>('#primary-navigation-drawer a[href="/discover"]')!
      .dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
    await settle();

    expect(window.location.pathname).toBe('/discover');
    expect(menu.getAttribute('aria-expanded')).toBe('false');
  });

  it('replaces primary rows with one settings sidebar and restores them after returning', async () => {
    navigate('/settings/metadata', { replace: true });
    app = mount(App, { target: host });
    await settle();

    const sidebar = host.querySelector<HTMLElement>('#primary-navigation-drawer')!;
    const settingsNavigation = sidebar.querySelector<HTMLElement>('[data-settings-sidebar-navigation]')!;
    const back = sidebar.querySelector<HTMLAnchorElement>('[data-settings-back]')!;
    const categories = [...settingsNavigation.querySelectorAll<HTMLElement>('[data-settings-category]')];

    expect(host.querySelectorAll('aside')).toHaveLength(1);
    expect(host.querySelectorAll('[data-settings-sidebar]')).toHaveLength(0);
    expect(sidebar.dataset.sidebarMode).toBe('settings');
    expect(back.tagName).toBe('A');
    expect(back.getAttribute('href')).toBe('/');
    expect(back.textContent).toContain('Back to Caravan');
    expect(categories.map((group) => group.querySelector('p')?.textContent?.trim())).toEqual(
      SETTINGS_CATEGORIES.map((category) => settingsCategoryLabel(category)),
    );
    for (const entry of SETTINGS_CATALOG) {
      expect(settingsNavigation.querySelector(`a[href="${settingsHref(entry)}"]`)).not.toBeNull();
    }
    for (const href of [
      '/discover',
      '/requests',
      '/series',
      '/adult',
      '/wanted',
      '/calendar',
      '/queue',
      '/convert',
      '/history',
      '/scan-review',
    ]) {
      expect(sidebar.querySelector(`a[href="${href}"]`)).toBeNull();
    }
    expect(
      [...sidebar.querySelectorAll<HTMLAnchorElement>('a[href="/"]')].map((link) =>
        link.textContent?.trim(),
      ),
    ).toEqual(['CARAVAN', 'Back to Caravan']);
    expect(sidebar.querySelector('a[href="/settings/metadata#metadata"]')?.getAttribute('aria-current')).toBe(
      'page',
    );

    navigate('/settings/general', { replace: true });
    await settle();
    expect(sidebar.querySelector('a[href="/settings/metadata#metadata"]')?.getAttribute('aria-current')).toBe(
      'page',
    );

    back.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
    await settle();

    expect(window.location.pathname).toBe('/discover');
    expect(sidebar.dataset.sidebarMode).toBe('primary');
    expect(sidebar.querySelector('[data-settings-sidebar-navigation]')).toBeNull();
    expect(sidebar.textContent).toContain('Explore');
    expect(sidebar.querySelector('a[href="/queue"]')).not.toBeNull();
  });

  it('moves settings search into the top bar and routes matching results', async () => {
    navigate('/settings', { replace: true });
    app = mount(App, { target: host });
    await settle();

    const searchContainer = host.querySelector<HTMLElement>('[data-settings-top-search]')!;
    const search = searchContainer.querySelector<HTMLInputElement>('#settings-search')!;

    expect(search).not.toBeNull();
    expect(search.closest('header')?.classList).toContain('sticky');
    expect(host.querySelector('main #settings-search')).toBeNull();
    expect(host.textContent).not.toContain('Find a setting');
    expect(
      [...host.querySelectorAll('header button')].some((control) =>
        control.textContent?.includes('Add movie or series'),
      ),
    ).toBe(false);

    const cases = [
      ['port', '/settings/downloads#downloads'],
      ['schedule', '/settings/tasks#tasks'],
      ['API token', '/settings/security#security'],
      ['profile', '/settings/quality-profiles#quality-profiles'],
      ['Jellyfin', '/settings/playback#playback'],
    ] as const;

    for (const [query, href] of cases) {
      search.value = query;
      search.dispatchEvent(new Event('input', { bubbles: true }));
      flushSync();
      expect(searchContainer.querySelector(`[aria-live] a[href="${href}"]`), query).not.toBeNull();
    }

    search.blur();
    const shortcut = new KeyboardEvent('keydown', {
      key: 'k',
      ctrlKey: true,
      bubbles: true,
      cancelable: true,
    });
    window.dispatchEvent(shortcut);
    flushSync();

    expect(shortcut.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(search);
    expect(host.querySelector('[role="dialog"]')).toBeNull();

    searchContainer
      .querySelector<HTMLAnchorElement>('a[href="/settings/playback#playback"]')!
      .dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
    await settle();

    expect(window.location.pathname).toBe('/settings/playback');
    expect(window.location.hash).toBe('#playback');
    expect(host.querySelector('[data-settings-top-search]')).not.toBeNull();
    expect(host.querySelector('#settings-search-results')).toBeNull();
  });

  it('closes the narrow drawer after choosing a settings page', async () => {
    stubViewport(true);
    navigate('/settings', { replace: true });
    app = mount(App, { target: host });
    await settle();

    const menu = host.querySelector<HTMLButtonElement>('[aria-controls="primary-navigation-drawer"]')!;
    menu.click();
    flushSync();

    host
      .querySelector<HTMLAnchorElement>(
        '#primary-navigation-drawer [data-settings-sidebar-navigation] a[href="/settings/metadata#metadata"]',
      )!
      .dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
    await settle();

    expect(window.location.pathname).toBe('/settings/metadata');
    expect(menu.getAttribute('aria-expanded')).toBe('false');
  });

  it('closes the narrow drawer on Escape and restores focus to its opener', async () => {
    stubViewport(true);
    app = mount(App, { target: host });
    await settle();

    const menu = host.querySelector<HTMLButtonElement>('[aria-controls="primary-navigation-drawer"]')!;
    menu.focus();
    menu.click();
    flushSync();

    const escape = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    window.dispatchEvent(escape);
    flushSync();

    expect(escape.defaultPrevented).toBe(true);
    expect(menu.getAttribute('aria-expanded')).toBe('false');
    expect(document.activeElement).toBe(menu);
  });

  /**
   * `/` forwards to Discover, not to the library: the first question on
   * opening Caravan is what to watch, not what is already downloaded.
   */
  it('renders Discover at the index and leaves the library where it was', async () => {
    navigate('/', { replace: true });
    app = mount(App, { target: host });
    await settle();

    expect(window.location.pathname).toBe('/discover');
    expect(host.querySelector('a[href="/discover"]')).not.toBeNull();
    expect(host.querySelector('a[href="/requests"]')).not.toBeNull();
    // The trending billboard, from the stubbed /discover payload.
    expect(host.textContent).toContain('Trending #1 · SERIES');
    expect(host.textContent).toContain('Severance');
    expect(host.textContent).toContain('Browse by network');
    // The movie grid did not come along for the ride.
    expect(host.textContent).not.toContain('Big Buck Bunny');
    // A wide Discover shelf must not raise <main>'s automatic minimum and
    // turn the shell column into the horizontal scroller.
    const main = host.querySelector('main');
    expect(main?.classList.contains('min-w-0')).toBe(true);
    expect(main?.parentElement?.classList.contains('overflow-x-hidden')).toBe(true);
  });

  it('sends the brand mark through the index to Discover', async () => {
    app = mount(App, { target: host });
    await settle();

    expect(host.textContent).toContain('Big Buck Bunny');

    const brand = [...host.querySelectorAll<HTMLAnchorElement>('a')].find((link) =>
      link.textContent?.includes('CARAVAN'),
    );
    expect(brand?.getAttribute('href')).toBe('/');
    brand!.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }));
    await settle();

    expect(window.location.pathname).toBe('/discover');
    expect(host.textContent).toContain('Severance');
    expect(host.textContent).not.toContain('Big Buck Bunny');
  });

  it('says nothing about the metadata key in the sidebar', async () => {
    statusBody = { ...STATUS, metadata_credential: 'absent' };
    app = mount(App, { target: host });
    await settle();

    expect(host.textContent).not.toContain('No TMDB key');
    expect(host.textContent).not.toContain('TMDB key rejected');
    expect(host.querySelector('a[href="/settings/metadata"]')).toBeNull();
  });

  it('uses uniform semantic badges for every nonzero sidebar count', async () => {
    statusBody = {
      ...STATUS,
      counts: {
        ...STATUS.counts,
        movies: 2,
        series: 3,
        wanted: 4,
        converting: 5,
        unmatched: 6,
        libraries: [
          { id: 1, items: 2 },
          { id: 2, items: 3 },
        ],
      },
    };
    requestRows = [
      {
        id: 1,
        media_type: 'series',
        tmdb_id: 1396,
        stash_id: '',
        title: 'Severance',
        year: 2022,
        poster_path: '',
        poster_url: '',
        seasons: null,
        min_availability: '',
        requested_by_username: 'chris',
        status: 'pending',
        created_at: '2026-08-01T00:00:00Z',
        updated_at: '2026-08-01T00:00:00Z',
      },
    ];
    app = mount(App, { target: host });
    await settle();

    const cases = [
      ['/requests', '1 pending request', 'bg-warning-tint'],
      // Per shelf now, and counted per library rather than per kind.
      ['/l/movies', '2 items in this library', 'bg-raised'],
      ['/l/series', '3 items in this library', 'bg-raised'],
      ['/wanted', '4 movies and episodes waiting', 'bg-warning-tint'],
      ['/queue', '1 active download', 'bg-accent-tint'],
      ['/convert', '5 open conversions', 'bg-raised'],
      ['/scan-review', '6 unmatched media files', 'bg-warning-tint'],
    ] as const;

    for (const [href, title, tone] of cases) {
      const badge = host.querySelector(`a[href="${href}"] > span[title="${title}"]`);
      expect(badge, href).not.toBeNull();
      expect(badge?.classList, href).toContain('h-5');
      expect(badge?.classList, href).toContain('rounded-sm');
      expect(badge?.classList, href).toContain(tone);
    }
  });

  it('badges the queue nav with the active download count', async () => {
    app = mount(App, { target: host });
    await settle();

    const queueLink = host.querySelector('a[href="/queue"]');
    expect(queueLink).not.toBeNull();
    // One downloading, one paused: a paused download waits on the user, so the
    // badge counts one (see isActiveDownload).
    expect(queueLink?.textContent).toContain('1');
    expect(queueLink?.querySelector('[title="1 active download"]')).not.toBeNull();
  });

  it('badges each library shelf row with its own item count', async () => {
    app = mount(App, { target: host });
    await settle();

    // The status fixture gives library 1 one item and library 2 none: a zero
    // renders nothing rather than an inactive badge.
    expect(
      host.querySelector('a[href="/l/movies"] > span[title="1 item in this library"]'),
    ).not.toBeNull();
    expect(host.querySelector('a[href="/l/series"] > span.tabular-nums')).toBeNull();
  });

  it('shows live search progress in the sidebar footer instead of a toast', async () => {
    jobRows = [
      {
        id: 1,
        kind: 'search_episode',
        payload: '',
        state: 'running',
        attempts: 1,
        run_after: '',
        lease_expires_at: '',
        last_error: '',
        created_at: '',
        updated_at: '',
      },
    ];
    app = mount(App, { target: host });
    await settle();

    const rail = host.querySelector('[data-sidebar-activity]');
    const row = host.querySelector<HTMLAnchorElement>('[data-sidebar-activity-row]');
    expect(rail?.textContent).toContain('Searching');
    expect(row?.getAttribute('href')).toBe('/wanted');
    expect(host.textContent).not.toContain('500 GB free');
    expect(host.querySelector('[role="progressbar"][aria-label="Disk used"]')).toBeNull();
  });

  it('badges Settings when a background task failed', async () => {
    taskRows = [
      {
        kind: 'rss_sync',
        name: 'RSS sync',
        description: 'Checks indexer feeds for newly posted releases.',
        interval_minutes: 15,
        last_run: '',
        last_result: 'failed',
        last_error: 'indexer timed out',
        next_run: '',
        running: false,
        queued: true,
      },
    ];
    app = mount(App, { target: host });
    await settle();

    expect(host.querySelector('a[href="/settings"] > span[title="1 task needs attention"]')).not.toBeNull();
    expect(host.querySelector('[data-sidebar-activity]')?.textContent).toContain('RSS sync failed');
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

  // Killing one download client must say so, name it, and leave the rest of the
  // shell working (SPEC §5.1).
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

  /**
   * Stash being down is a banner, never a blocker. The field is only on the
   * payload for a caller the module is visible to, so its presence is what
   * raises this. There is no second adult check to keep in step with the
   * server's.
   */
  it('banners the adult library handoff when Stash is unreachable', async () => {
    statusBody = {
      ...STATUS,
      stash_unreachable: { error: 'connection refused', since: '2026-08-01T09:30:00Z' },
    };

    app = mount(App, { target: host });
    await settle();

    expect(host.textContent).toContain('Stash is unreachable');
    expect(host.textContent).toContain('connection refused');
    // The promise the banner exists to make: imports are unaffected.
    expect(host.textContent).toContain('Adult imports continue');
    // And the shell around it is untouched.
    expect(host.textContent).toContain('CARAVAN');
    expect(host.textContent).not.toContain('Caravan server unreachable');
  });

  it('says nothing about Stash when the status carries no outage', async () => {
    app = mount(App, { target: host });
    await settle();
    expect(host.textContent).not.toContain('Stash');
  });

  it('renders the release picker with the best release first', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/system/status')) return jsonResponse(statusBody);
        if (url.includes('/downloads')) return jsonResponse({ downloads: [] });
        if (url.includes('/system/tasks')) return jsonResponse({ tasks: [] });
        if (url.includes('/jobs')) return jsonResponse({ jobs: [] });
        if (url.endsWith('/requests')) return jsonResponse({ requests: [] });
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

  it("flags a DTS/HEVC release against the title's playback target in the picker", async () => {
    const clean = RELEASES[1]!;
    const flagged: Release = {
      ...clean,
      guid: 'guid-dts',
      title: 'Big.Buck.Bunny.2008.1080p.BluRay.x265.10bit.DTS-HD.MA.7.1-GRP',
      compatibility: {
        verdict: 'incompatible',
        reasons: ['HEVC video (target allows H.264)', 'DTS-HD audio (target allows AAC)'],
      },
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/system/status')) return jsonResponse(statusBody);
        if (url.includes('/downloads')) return jsonResponse({ downloads: [] });
        if (url.includes('/system/tasks')) return jsonResponse({ tasks: [] });
        if (url.includes('/jobs')) return jsonResponse({ jobs: [] });
        if (url.endsWith('/requests')) return jsonResponse({ requests: [] });
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
        if (url.includes('/downloads')) return jsonResponse({ downloads: [] });
        if (url.includes('/system/tasks')) return jsonResponse({ tasks: [] });
        if (url.includes('/jobs')) return jsonResponse({ jobs: [] });
        if (url.endsWith('/requests')) return jsonResponse({ requests: [] });
        if (url.endsWith('/library/movies')) return jsonResponse({ movies: [MOVIE] });
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    // The recovery banner is up, with the way out on it (SPEC §13).
    expect(host.textContent).toContain('Last shutdown was not clean');
    expect(host.textContent).toContain('Verify and rescan');
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
        if (url.includes('/downloads')) return jsonResponse({ downloads: [] });
        if (url.includes('/system/tasks')) return jsonResponse({ tasks: [] });
        if (url.includes('/jobs')) return jsonResponse({ jobs: [] });
        if (url.endsWith('/requests')) return jsonResponse({ requests: [] });
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

  it('requires account creation while a fresh open server still needs setup', async () => {
    statusBody = { ...STATUS, storage_root: '', needs_setup: true, password_set: false };

    app = mount(App, { target: host });
    await settle();

    expect(window.location.pathname).toBe('/first-run');
    expect(host.textContent).toContain('Create your administrator account');
    expect(host.querySelector('#admin-username')).not.toBeNull();
    expect(host.textContent).not.toContain('Sign in');
  });

  it('resumes first run after reload for a signed-in administrator whose account already exists', async () => {
    sessionBody = { username: 'admin', role: 'admin', open: false, libraries: SEEDED_LIBRARIES };
    statusBody = { ...STATUS, storage_root: '', needs_setup: true, password_set: true };
    system.status = null;
    system.loading = true;
    navigate('/first-run', { replace: true });

    app = mount(App, { target: host });
    await settle();

    expect(window.location.pathname).toBe('/first-run');
    expect(host.textContent).toContain('Administrator account created');
    expect(host.querySelector('#admin-username')).toBeNull();
    expect(host.textContent).not.toContain('Sign in');
  });

  it('links the public-bind warning to account settings', async () => {
    statusBody = { ...STATUS, listening_publicly: true, password_set: false };

    app = mount(App, { target: host });
    await settle();

    expect(host.querySelector('a[href="/settings/users"]')).not.toBeNull();
    expect(host.textContent).toContain('Settings / Users');
    const warning = [...host.querySelectorAll<HTMLElement>('[role="alert"]')].find((alert) =>
      alert.textContent?.includes('Anyone on this network can open Caravan'),
    );
    expect(warning?.classList).toContain('bg-warning-tint');
    expect(warning?.querySelector('.bg-warning')).not.toBeNull();
  });

  it('sends an existing administrator to first-run configuration when there is no storage root', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith('/system/status')) {
          return jsonResponse({ ...STATUS, storage_root: '', password_set: true });
        }
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );

    app = mount(App, { target: host });
    await settle();

    expect(window.location.pathname).toBe('/first-run');
    expect(host.textContent).toContain('Where does your media live?');
  });
});
