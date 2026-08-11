/**
 * The requests screen, which is two screens sharing one list.
 *
 * For an admin: what it lists, and the two answers a pending row can get.
 * Approve opens the shared modal prefilled with what was asked for; Dismiss is
 * a DELETE that leaves the row behind as history.
 *
 * For a member: their own rows in every status, and the one thing they may do
 * to one — cancel it while it is still pending.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Requests from './Requests.svelte';
import type { MediaRequest } from '../api/types';
import { requests } from '../state/requests.svelte';
import { session } from '../state/session.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

const ROWS: MediaRequest[] = [
  {
    id: 11,
    media_type: 'series',
    tmdb_id: 1396,
    stash_id: '',
    title: 'Severance',
    year: 2022,
    poster_path: '/p.jpg',
    poster_url: 'https://image.tmdb.org/t/p/w500/p.jpg',
    monitored: false,
    seasons: [2],
    min_availability: '',
    requested_by_username: 'ada',
    status: 'pending',
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
  },
  {
    id: 12,
    media_type: 'movie',
    tmdb_id: 78,
    stash_id: '',
    title: 'Blade Runner',
    year: 1982,
    poster_path: '',
    poster_url: '',
    seasons: null,
    min_availability: '',
    requested_by_username: '',
    status: 'pending',
    created_at: '2026-07-30T00:00:00Z',
    updated_at: '2026-07-30T00:00:00Z',
  },
  // Already answered: it must not be listed, and must not reach the badge.
  {
    id: 13,
    media_type: 'movie',
    tmdb_id: 79,
    stash_id: '',
    title: 'Dismissed Movie',
    year: 1990,
    poster_path: '',
    poster_url: '',
    seasons: null,
    min_availability: '',
    requested_by_username: 'ada',
    status: 'dismissed',
    created_at: '2026-07-29T00:00:00Z',
    updated_at: '2026-07-29T00:00:00Z',
  },
];

/**
 * A pending scene row, as a granted caller's list carries one: named by its
 * stash id, `tmdb_id` 0, no seasons. The server withholds these entirely from a
 * caller the adult module is not visible to, so a row reaching this screen at
 * all already means the grant is in place.
 *
 * `poster_path` and `poster_url` are the SAME absolute url, which is the pair
 * the server actually produces: a scene's cover comes from the stash-box
 * provider already absolute (stashbox.coverURL), so unlike a movie or a series
 * there is no CDN prefix to add. A fixture that paired a TMDB-shaped
 * "/scene.jpg" with some other rendered url described a row no server can send.
 */
const SCENE_ROW: MediaRequest = {
  id: 14,
  media_type: 'scene',
  tmdb_id: 0,
  stash_id: 'a1b2c3',
  title: 'Deep Impact',
  year: 2022,
  poster_path: 'https://cdn.example/scene.jpg',
  poster_url: 'https://cdn.example/scene.jpg',
  seasons: null,
  min_availability: '',
  requested_by_username: 'ada',
  status: 'pending',
  created_at: '2026-08-02T00:00:00Z',
  updated_at: '2026-08-02T00:00:00Z',
};

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body?: unknown }[];
/** What GET /requests answers; a test sets it before mounting. */
let served: MediaRequest[] = ROWS;

function json(body: unknown, status = 200): Response {
  if (status === 204) return new Response(null, { status });
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

async function settle() {
  for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function rowButtons(label: string): HTMLButtonElement[] {
  return [...host.querySelectorAll<HTMLButtonElement>('li button')].filter(
    (b) => b.textContent?.trim() === label,
  );
}

beforeEach(() => {
  clearToasts();
  calls = [];
  served = ROWS;
  requests.items = null;
  requests.error = null;
  requests.loading = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
      calls.push({ url, method, ...(body === null ? {} : { body }) });
      if (url.endsWith('/requests') && method === 'GET') return json({ requests: served });
      if (method === 'DELETE') return json(null, 204);
      if (url.endsWith('/quality-profiles')) return json({ profiles: [] });
      if (url.endsWith('/approve')) {
        return json({
          request: { id: 11, monitored: false },
          series: { id: 42, title: 'Severance' },
        });
      }
      if (url.endsWith('/library/series/42/search')) return json(null, 204);
      return json(null, 204);
    }),
  );
  window.scrollTo = () => {};
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  requests.items = null;
  session.forget();
  clearToasts();
  vi.unstubAllGlobals();
});

describe('Requests', () => {
  it('lists pending rows only, with what was actually asked for', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(host.querySelectorAll('li')).toHaveLength(2);
    expect(host.textContent).toContain('Severance (2022)');
    expect(host.textContent).toContain('Season 02');
    expect(host.textContent).toContain('Blade Runner (1982)');
    expect(host.textContent).toContain('Movie');
    expect(host.textContent).not.toContain('Dismissed Movie');
    // The badge counts the same two rows.
    expect(requests.pendingCount).toBe(2);
  });


  it('gives an admin awaiting-approval and approved tabs with their counts', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    const tabs = host.querySelector<HTMLElement>('[aria-label="Requests filter"]');
    expect(tabs).not.toBeNull();
    expect(tabs?.textContent).toContain('Awaiting approval');
    expect(tabs?.textContent).toContain('2');
    expect(tabs?.textContent).toContain('Approved');
    expect(tabs?.textContent).toContain('0');
  });

  it('shows approved rows only on the approved tab, without decision actions', async () => {
    served = [
      ...ROWS,
      { ...ROWS[0]!, id: 15, title: 'Approved Series', status: 'approved' },
    ];
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(host.textContent).not.toContain('Approved Series');
    const approvedTab = [...host.querySelectorAll<HTMLButtonElement>(
      '[aria-label="Requests filter"] button',
    )].find((button) => button.textContent?.includes('Approved'));
    approvedTab!.click();
    flushSync();

    const approvedRow = [...host.querySelectorAll('li')].find((row) =>
      row.textContent?.includes('Approved Series'),
    );
    expect(approvedRow).toBeDefined();
    expect(approvedRow?.querySelectorAll('button')).toHaveLength(0);
    expect(host.textContent).not.toContain('Blade Runner (1982)');
  });
  it('links each row at the discover screen, keyed by TMDB id', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(host.querySelector('a[href="/discover/series/1396"]')).not.toBeNull();
    expect(host.querySelector('a[href="/discover/movie/78"]')).not.toBeNull();
    expect(host.querySelector('a[title="Severance (2022)"]')).not.toBeNull();
    expect(host.querySelector('a[title="Blade Runner (1982)"]')).not.toBeNull();
  });

  it('dismisses a row, drops it locally and says so', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    rowButtons('Dismiss')[0]!.click();
    await settle();

    expect(calls).toContainEqual({ url: '/api/v1/requests/11', method: 'DELETE' });
    expect(host.textContent).not.toContain('Severance');
    expect(toasts.items.map((t) => t.message)).toEqual(['Dismissed Severance']);
  });

  /**
   * Approve is the shared modal in add mode, prefilled with the requested
   * seasons — it does not add behind the user's back.
   */
  it('opens the add modal prefilled instead of approving straight away', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    rowButtons('Approve')[0]!.click();
    await settle();

    const dialog = document.querySelector('[role="dialog"]');
    expect(dialog).not.toBeNull();
    expect(dialog?.textContent).toContain('Add to library');
    expect(dialog?.textContent).toContain('Severance');
    // Nothing was written yet.
    expect(calls.filter((c) => c.method === 'POST')).toEqual([]);
  });

  it('passes the request monitoring state into the approval editor and approval write', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    rowButtons('Approve')[0]!.click();
    await settle();
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
    const monitored = dialog?.querySelector<HTMLInputElement>('#add-monitored');
    expect(monitored).not.toBeNull();
    expect(monitored!.checked).toBe(false);

    monitored!.click();
    flushSync();
    [...dialog!.querySelectorAll<HTMLButtonElement>('footer button')].at(-1)!.click();
    await settle();

    expect(calls).toContainEqual({
      url: '/api/v1/requests/11/approve',
      method: 'POST',
      body: { search_now: false, monitored: true },
    });
  });

  /**
   * The asker is a fact about somebody else's wish, so it is only on the admin
   * list. "" covers a row from before accounts existed, one made while the
   * server ran open, and an asker since deleted — all of which render as
   * nothing rather than as a guess.
   */
  it('names who asked, and says nothing at all for an ownerless row', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    const rows = [...host.querySelectorAll('li')].map((li) => li.textContent ?? '');
    expect(rows[0]).toContain('by ada');
    expect(rows[1]).not.toContain('by ');
  });

  /**
   * The ungranted half of the visibility matrix, and the module-off half too:
   * both are the same list on the wire, because the server strips scene rows
   * from a caller the module is not visible to. What must be true on this side
   * is that the screen adds nothing back — no adult endpoint, and no adult
   * vocabulary anywhere in the rendered output.
   */
  it('leaves no adult trace at all on a list with no scene rows', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(calls.some((c) => c.url.includes('/adult'))).toBe(false);
    for (const word of ['SCENE', 'Adult', 'adult', 'Site', 'flame']) {
      expect(host.innerHTML, word).not.toContain(word);
    }
    expect(host.querySelectorAll('li')).toHaveLength(2);
  });

  it('shows the empty state when nothing is pending', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ requests: [] })),
    );
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(host.textContent).toContain('No pending requests');
    expect(host.querySelector('a[href="/discover"]')).not.toBeNull();
  });
});

/**
 * Scene rows on the shared requests screen (PLAN phase 9 task 7d).
 *
 * They only ever reach a caller the adult module is visible to — the server
 * strips them from everyone else's list — so what is proved here is that a row
 * which HAS arrived renders as the thing it is, rather than as a television
 * series with a dead link on it.
 */
describe('Requests — a scene row', () => {
  beforeEach(() => {
    served = [SCENE_ROW, ...ROWS];
  });

  it('renders as a scene, with its own badge and poster', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    const row = [...host.querySelectorAll('li')].find((li) =>
      li.textContent?.includes('Deep Impact'),
    );
    expect(row).toBeDefined();
    expect(row!.textContent).toContain('Deep Impact (2022)');
    expect(row!.textContent).toContain('SCENE');
    // The two-way movie/series branch this replaced labelled it SERIES and gave
    // it the television placeholder.
    expect(row!.textContent).not.toContain('SERIES');
    expect(row!.textContent).not.toContain('All seasons');
    expect(row!.textContent).toContain('Scene');
    expect(row!.querySelector('img')?.getAttribute('src')).toBe('https://cdn.example/scene.jpg');
  });

  /**
   * A scene's tmdb id is 0 and there is no per-scene route, so the shared
   * discover link built `/discover/scene/0` — and put it on both the poster and
   * the title. The row is text now.
   */
  it('links nowhere rather than at a route that does not exist', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(host.querySelector('a[href="/discover/scene/0"]')).toBeNull();
    const row = [...host.querySelectorAll('li')].find((li) =>
      li.textContent?.includes('Deep Impact'),
    );
    expect(row!.querySelectorAll('a')).toHaveLength(0);
    // The rows that DO have a destination keep it.
    expect(host.querySelector('a[href="/discover/series/1396"]')).not.toBeNull();
  });

  /**
   * Approving a scene adds the site the provider files it under, which the
   * server resolves on its own. There is nothing for the TMDB-shaped modal to
   * ask, so it must not open — it would fetch seasons for tmdb id 0.
   */
  it('approves straight through the API, queues a scene search, and removes the row immediately', async () => {
    let approvalComplete = false;
    let resolveRefresh: ((response: Response) => void) | null = null;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? 'GET';
        const body = typeof init?.body === 'string' ? JSON.parse(init.body) : null;
        calls.push({ url, method, ...(body === null ? {} : { body }) });
        if (url.endsWith('/requests') && method === 'GET') {
          return approvalComplete
            ? new Promise<Response>((resolve) => (resolveRefresh = resolve))
            : json({ requests: served });
        }
        if (url.endsWith('/approve')) {
          approvalComplete = true;
          return json({
            request: { id: 14, status: 'approved' },
            site: { id: 44, title: 'Example Site' },
            search_queued: true,
          });
        }
        return json(null, 204);
      }),
    );
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    const row = [...host.querySelectorAll('li')].find((li) =>
      li.textContent?.includes('Deep Impact'),
    );
    const approve = [...row!.querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Approve',
    );
    approve!.click();
    await settle();

    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(calls).toContainEqual({
      url: '/api/v1/requests/14/approve',
      method: 'POST',
      body: { search_now: true },
    });
    expect(host.textContent).not.toContain('Deep Impact');
    expect(toasts.items.map((t) => t.message)).toContain('Approved Deep Impact. Search queued.');
    resolveRefresh!(json({ requests: served.filter((request) => request.id !== SCENE_ROW.id) }));
    await settle();
  });
});

describe('Requests — as a member', () => {
  beforeEach(() => {
    session.user = { username: 'ada', role: 'member', open: false, adult: false };
  });

  /**
   * The server hands a member only their own rows, in every status, so the
   * screen shows what it was given rather than filtering to pending: watching a
   * wish go from pending to approved is the whole point of the screen for them.
   */
  it('lists every status of their own rows, with the status on each', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(host.querySelectorAll('li')).toHaveLength(3);
    expect(host.textContent).toContain('Dismissed Movie');
    expect(host.textContent).toContain('Pending');
    expect(host.textContent).toContain('Dismissed');
    // Whose row it is is not news to the person whose rows these are.
    expect(host.textContent).not.toContain('by ada');
  });

  it('offers Cancel on a pending row and nothing on a decided one', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(rowButtons('Cancel')).toHaveLength(2);
    expect(rowButtons('Approve')).toHaveLength(0);
    expect(rowButtons('Dismiss')).toHaveLength(0);

    const decided = [...host.querySelectorAll('li')][2] as HTMLElement;
    expect(decided.textContent).toContain('Dismissed Movie');
    expect(decided.querySelectorAll('button')).toHaveLength(0);
  });

  it('cancels its own pending row through the same DELETE', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    rowButtons('Cancel')[0]!.click();
    await settle();

    expect(calls).toContainEqual({ url: '/api/v1/requests/11', method: 'DELETE' });
    expect(toasts.items.map((t) => t.message)).toEqual(['Cancelled Severance']);
    // The row belongs back on screen as dismissed, so the list is refetched
    // rather than left with a hole in it.
    expect(calls.filter((c) => c.url.endsWith('/requests') && c.method === 'GET').length)
      .toBeGreaterThan(1);
  });
});
