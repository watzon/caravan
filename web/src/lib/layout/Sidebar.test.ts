/**
 * The sidebar's task rail and the Settings badge that points at it.
 *
 * Background work used to toast; it now lives here. These assert on rendered
 * text on purpose: `npm run check` type-checks the script blocks and not the
 * templates, so a mistyped prop or a row that never renders is a silent pass
 * everywhere else.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Sidebar from './Sidebar.svelte';
import type { Job, SessionLibrary, SystemStatus, SystemTask } from '../api/types';
import { saveDisplayPreferences } from '../displayPreferences';
import { navigate } from '../router.svelte';
import { session } from '../state/session.svelte';
import { system } from '../state/system.svelte';
import { tasks } from '../state/tasks.svelte';

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function seedStatus(fields: Partial<SystemStatus> = {}): void {
  system.status = {
    version: '0.1.0',
    mode: 'server',
    storage_root: '/data',
    schema_version: 1,
    scanning: false,
    counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
    disk_free_bytes: 500 * 1024 ** 3,
    disk_total_bytes: 1024 ** 4,
    engine_health: 'ok',
    ffmpeg_available: true,
    ...fields,
  } as SystemStatus;
  system.loading = false;
}

function task(extra: Partial<SystemTask> = {}): SystemTask {
  return {
    kind: 'rss_sync',
    name: 'RSS sync',
    description: 'Checks indexer feeds for newly posted releases.',
    interval_minutes: 15,
    last_run: '',
    last_result: 'ok',
    last_error: '',
    next_run: '',
    running: false,
    queued: true,
    ...extra,
  };
}

function job(extra: Partial<Job> = {}): Job {
  return {
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
    ...extra,
  };
}

let host: HTMLElement;
let app: Record<string, unknown>;
let taskRows: SystemTask[];
let jobRows: Job[];

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  window.scrollTo = () => {};
  seedStatus();
  taskRows = [];
  jobRows = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes('/downloads')) return jsonResponse({ downloads: [] });
      if (url.endsWith('/requests')) return jsonResponse({ requests: [] });
      if (url.includes('/system/tasks')) return jsonResponse({ tasks: taskRows });
      if (url.includes('/jobs')) return jsonResponse({ jobs: jobRows });
      if (url.endsWith('/system/status')) return jsonResponse(system.status ?? {});
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(async () => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  localStorage.clear();
  document.documentElement.removeAttribute('data-theme');
  system.status = null;
  system.loading = true;
  tasks.stopSoon();
  tasks.jobs = null;
  tasks.tasks = null;
  session.forget();
  // The router is a module singleton, so a shelf-row test that navigated has to
  // hand the next one a clean path or it inherits an active row.
  navigate('/', { replace: true });
});

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

async function render(props: { settingsSection?: string } = {}) {
  app = mount(Sidebar, { target: host, props: { open: true, onclose: () => {}, ...props } });
  await settle();
}

function activity(): HTMLAnchorElement[] {
  return [...host.querySelectorAll<HTMLAnchorElement>('[data-sidebar-activity-row]')];
}

/**
 * A distinctive fragment of each glyph's markup, copied from Icon.svelte's
 * table. The icon set is inline SVG with no name on the element, so this is how
 * a test says WHICH mark a row is wearing rather than merely that it has one.
 */
const GLYPH = {
  film: 'M7 2v20M17 2v20',
  tv: 'm17 2-5 5-5-5',
  sparkles: 'M12 3.5',
  flame: 'M12 21a6 6 0 0 0 6-6',
  star: 'm12 3 2.7 5.6',
} as const;

function row(href: string): HTMLAnchorElement | null {
  return host.querySelector<HTMLAnchorElement>(`a[href="${href}"]`);
}

/** Every Library-group row's href, in the order they are drawn. */
function libraryHrefs(): string[] {
  const group = [...host.querySelectorAll('nav > div')].find((div) =>
    div.querySelector('p')?.textContent?.trim() === 'Library',
  );
  return [...(group?.querySelectorAll('a') ?? [])].map((a) => a.getAttribute('href') ?? '');
}

function library(over: Partial<SessionLibrary> & { id: number }): SessionLibrary {
  const name = over.name ?? `Library ${over.id}`;
  return {
    kind: 'movie',
    name,
    icon: '',
    slug: name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || `lib-${over.id}`,
    ...over,
  };
}

describe('Sidebar library shelves', () => {
  it('draws one row per session library, with its name, glyph, id and count', async () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      libraries: [
        library({ id: 1, kind: 'movie', name: 'Movies' }),
        library({ id: 4, kind: 'movie', name: 'Kids', icon: 'star' }),
        library({ id: 2, kind: 'tv', name: 'Series' }),
        library({ id: 3, kind: 'anime', name: 'Anime' }),
      ],
    };
    seedStatus({
      counts: {
        movies: 0,
        series: 0,
        media_files: 0,
        unmatched: 0,
        libraries: [
          { id: 4, items: 7 },
          { id: 1, items: 0 },
        ],
      },
    });
    await render();

    // Grouped movie, tv, anime; inside a kind, the order /auth/me sent.
    expect(libraryHrefs()).toEqual([
      '/l/movies',
      '/l/kids',
      '/l/series',
      '/l/anime',
      '/wanted',
      '/calendar',
    ]);
    expect(row('/l/kids')?.textContent).toContain('Kids');
    // A chosen glyph is drawn; an empty one falls back to the kind's default.
    expect(row('/l/kids')?.innerHTML).toContain(GLYPH.star);
    expect(row('/l/movies')?.innerHTML).toContain(GLYPH.film);
    expect(row('/l/series')?.innerHTML).toContain(GLYPH.tv);
    expect(row('/l/anime')?.innerHTML).toContain(GLYPH.sparkles);
    // The badge is this shelf's own inventory, not the install's, and a zero
    // renders nothing.
    expect(
      row('/l/kids')?.querySelector('span[title="7 items in this library"]'),
    ).not.toBeNull();
    expect(row('/l/movies')?.querySelector('span.tabular-nums')).toBeNull();
  });

  it('falls back to the kind glyph for an icon name it cannot draw', async () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      // The server stores any well-formed name without checking it against a
      // list, so the SPA has to survive one it has never heard of.
      libraries: [library({ id: 3, kind: 'anime', name: 'Anime', icon: 'nonesuch' })],
    };
    await render();

    expect(row('/l/anime')?.innerHTML).toContain(GLYPH.sparkles);
  });

  it('lights the row the library slug names, and no row on the plain kind path', async () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      libraries: [
        library({ id: 1, kind: 'movie', name: 'Movies' }),
        library({ id: 4, kind: 'movie', name: 'Kids' }),
      ],
    };
    navigate('/l/kids', { replace: true });
    await render();

    expect(row('/l/kids')?.getAttribute('aria-current')).toBe('page');
    expect(row('/l/movies')?.getAttribute('aria-current')).toBeNull();

    unmount(app);
    // The older ?library= spelling still lights the same shelf.
    navigate('/movies?library=4', { replace: true });
    await render();

    expect(row('/l/kids')?.getAttribute('aria-current')).toBe('page');
    expect(row('/l/movies')?.getAttribute('aria-current')).toBeNull();

    unmount(app);
    navigate('/movies', { replace: true });
    await render();

    expect(row('/l/movies')?.getAttribute('aria-current')).toBeNull();
    expect(row('/l/kids')?.getAttribute('aria-current')).toBeNull();
  });

  it('gives a granted member the adult row and no other shelf', async () => {
    session.user = {
      username: 'ada',
      role: 'member',
      open: false,
      adult: true,
      // The server sends a member their libraries too; the shelf SCREENS are
      // still an admin's, so only the adult row may be drawn from them.
      libraries: [
        library({ id: 1, kind: 'movie', name: 'Movies' }),
        library({ id: 3, kind: 'anime', name: 'Anime' }),
        library({ id: 9, kind: 'adult', name: 'Adult', icon: 'star' }),
      ],
    };
    await render();

    expect(libraryHrefs()).toEqual(['/adult']);
    // The adult row wears the adult library's own glyph when one was chosen.
    expect(row('/adult')?.innerHTML).toContain(GLYPH.star);
  });

  it('keeps the adult row a single entry however many adult libraries exist', async () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: true,
      libraries: [
        library({ id: 9, kind: 'adult', name: 'Adult' }),
        library({ id: 10, kind: 'adult', name: 'Vintage' }),
      ],
    };
    await render();

    expect(libraryHrefs()).toEqual(['/adult', '/wanted', '/calendar']);
    expect(row('/adult')?.innerHTML).toContain(GLYPH.flame);
  });
});

describe('Sidebar task rail', () => {
  it('stays quiet while nothing is running and nothing failed', async () => {
    await render();

    expect(activity()).toEqual([]);
    expect(host.querySelector('[data-sidebar-footer]')).not.toBeNull();
    expect(host.textContent).not.toContain('Ready');
    expect(host.textContent).not.toContain('500 GB free');
    expect(host.textContent).not.toContain('No TMDB key');
    expect(host.querySelector('[role="progressbar"]')).toBeNull();
    // The badge, not "any titled span": every nav row's label now carries its
    // own title so a truncated library name is still readable on hover.
    expect(host.querySelector('a[href="/settings"] > span.tabular-nums')).toBeNull();
  });

  it('stacks two named searches at once', async () => {
    jobRows = [
      job({
        id: 1,
        subject: 'Transfixed',
        subject_kind: 'site',
        subject_id: 9,
      }),
      job({
        id: 2,
        subject: 'Transfixed',
        subject_kind: 'site',
        subject_id: 9,
      }),
      job({
        id: 3,
        subject: 'Severance',
        subject_kind: 'series',
        subject_id: 3,
      }),
    ];
    await render();

    expect(activity().map((row) => ({
      text: row.textContent?.replace(/\s+/g, ' ').trim(),
      href: row.getAttribute('href'),
    }))).toEqual([
      { text: 'Searching 2 scenes from Transfixed', href: '/adult/sites/9' },
      { text: 'Searching Severance', href: '/series/3' },
    ]);
  });

  it('shows a live search and sends the click to the work it names', async () => {
    jobRows = [job()];
    await render();

    expect(activity()).toHaveLength(1);
    expect(activity()[0]?.textContent).toContain('Searching');
    expect(activity()[0]?.getAttribute('href')).toBe('/wanted');
    expect(activity()[0]?.querySelector('.sidebar-task-pulse')).not.toBeNull();
    expect(activity()[0]?.className).toContain('text-accent-text');
  });

  it('warns about a failed last run without a pulse', async () => {
    taskRows = [task({ last_result: 'failed', last_error: 'indexer timed out' })];
    await render();

    expect(activity()[0]?.textContent).toContain('RSS sync failed');
    expect(activity()[0]?.getAttribute('title')).toContain('indexer timed out');
    expect(activity()[0]?.querySelector('.sidebar-task-pulse')).toBeNull();
    expect(activity()[0]?.className).toContain('text-warning');
  });

  it('badges Settings when a recurring task failed', async () => {
    taskRows = [task({ last_result: 'failed' })];
    await render();

    const badge = host.querySelector('a[href="/settings"] > span[title="1 task needs attention"]');
    expect(badge).not.toBeNull();
    expect(badge?.classList).toContain('bg-warning-tint');
    expect(badge?.textContent).toContain('1');
  });

  it('badges the Tasks row once the settings rail is open', async () => {
    taskRows = [task({ last_result: 'failed' })];
    await render({ settingsSection: '' });

    const badge = host.querySelector(
      'a[href="/settings/tasks#tasks"] span[title="1 task needs attention"]',
    );
    expect(badge).not.toBeNull();
    expect(host.querySelector('[data-settings-sidebar-navigation]')).not.toBeNull();
  });

  it('keeps the rail off a member account', async () => {
    session.user = { username: 'ada', role: 'member', open: false, adult: false };
    jobRows = [job()];
    taskRows = [task({ last_result: 'failed' })];
    await render();

    expect(activity()).toEqual([]);
    expect(host.querySelector('a[href="/settings"]')).toBeNull();
    expect(host.textContent).toContain('Sign out ada');
  });

  it('sends the brand mark to the index for an administrator and a member', async () => {
    session.user = { username: 'root', role: 'admin', open: false, adult: false };
    await render();

    const brand = [...host.querySelectorAll('a')].find((link) =>
      link.textContent?.includes('CARAVAN'),
    );
    expect(brand?.getAttribute('href')).toBe('/');

    unmount(app);
    session.user = { username: 'ada', role: 'member', open: false, adult: false };
    await render();

    const memberBrand = [...host.querySelectorAll('a')].find((link) =>
      link.textContent?.includes('CARAVAN'),
    );
    expect(memberBrand?.getAttribute('href')).toBe('/');
  });

  it('sends settings back to the index rather than the movie shelf', async () => {
    session.user = { username: 'root', role: 'admin', open: false, adult: false };
    await render({ settingsSection: '' });

    expect(host.querySelector('[data-settings-back]')?.getAttribute('href')).toBe('/');
  });

  it('keeps sign-out when an administrator is named', async () => {
    session.user = { username: 'root', role: 'admin', open: false, adult: false };
    await render();

    expect(host.textContent).toContain('Sign out root');
    expect(host.querySelector('[data-sidebar-footer]')).not.toBeNull();
  });

  it('puts a theme toggle to the right of sign-out and flips the document theme', async () => {
    saveDisplayPreferences({ theme: 'dark', motion: 'system' });
    session.user = { username: 'ada', role: 'admin', open: false, adult: false };
    await render();

    const toggle = host.querySelector<HTMLButtonElement>('[data-sidebar-theme]');
    expect(toggle).not.toBeNull();
    expect(toggle?.getAttribute('aria-label')).toBe('Use the light theme');

    toggle?.click();
    await settle();

    expect(document.documentElement.dataset.theme).toBe('light');
    expect(host.querySelector('[data-sidebar-theme]')?.getAttribute('aria-label')).toBe(
      'Use the dark theme',
    );
  });
});
