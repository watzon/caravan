/**
 * The detail-page action pair (SPEC §9): "Search now" queues the automatic
 * search through the API, while "Interactive search" stays a link to the
 * picker screen. A queued count of zero is reported honestly rather than
 * dressed up as a started search.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import MovieDetail from './MovieDetail.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

const MOVIE = {
  id: 7,
  tmdb_id: 438631,
  imdb_id: '',
  title: 'Dune',
  sort_title: 'dune',
  year: 2021,
  overview: '',
  path: '',
  poster_path: '',
  poster_url: '',
  monitored: true,
  quality_profile_id: 0,
  release_date: '',
  added_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  file: null,
};

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string }[];

function stubFetch(queued: number) {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({ url, method: init?.method ?? 'GET' });
      if (url.endsWith('/search')) {
        return new Response(JSON.stringify({ queued }), {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify(MOVIE), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

beforeEach(() => {
  clearToasts();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  clearToasts();
  vi.unstubAllGlobals();
});

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function searchNowButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find((b) =>
    b.textContent?.includes('Search now'),
  );
  expect(button, 'Search now button').toBeDefined();
  return button as HTMLButtonElement;
}

describe('MovieDetail search actions', () => {
  it('queues the automatic search and keeps the interactive picker a link', async () => {
    stubFetch(1);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    // The picker is still a linkable screen, not a second button.
    const picker = host.querySelector('a[href="/movies/7/search"]');
    expect(picker?.textContent).toContain('Interactive search');

    searchNowButton().click();
    await settle();

    expect(calls.filter((c) => c.method === 'POST')).toEqual([
      { url: '/api/v1/library/movies/7/search', method: 'POST' },
    ]);
    expect(toasts.items.map((t) => t.message)).toEqual(['Search started']);
    expect(toasts.items[0]!.tone).toBe('success');
  });

  it('says nothing was queued when the file already meets the cutoff', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    searchNowButton().click();
    await settle();

    expect(toasts.items).toHaveLength(1);
    expect(toasts.items[0]!.message).toContain('Nothing to search');
    expect(toasts.items[0]!.tone).toBe('info');
  });
});
