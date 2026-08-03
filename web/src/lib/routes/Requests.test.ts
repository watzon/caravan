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
    title: 'Severance',
    year: 2022,
    poster_path: '/p.jpg',
    poster_url: 'https://image.tmdb.org/t/p/w500/p.jpg',
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

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string }[];

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
  requests.items = null;
  requests.error = null;
  requests.loading = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      calls.push({ url, method });
      if (url.endsWith('/requests') && method === 'GET') return json({ requests: ROWS });
      if (method === 'DELETE') return json(null, 204);
      if (url.endsWith('/quality-profiles')) return json({ profiles: [] });
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

  it('links each row at the discover screen, keyed by TMDB id', async () => {
    app = mount(Requests, { target: host }) as Record<string, unknown>;
    await settle();

    expect(host.querySelector('a[href="/discover/series/1396"]')).not.toBeNull();
    expect(host.querySelector('a[href="/discover/movie/78"]')).not.toBeNull();
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

describe('Requests — as a member', () => {
  beforeEach(() => {
    session.user = { username: 'ada', role: 'member', open: false };
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
