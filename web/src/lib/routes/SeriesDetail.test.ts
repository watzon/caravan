/**
 * The series-level action pair (SPEC §9). "Search now" queues one job per
 * wanted episode and reports the count the server actually queued; the
 * per-season and per-episode links stay the interactive picker.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import SeriesDetail from './SeriesDetail.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

const SERIES = {
  id: 3,
  tmdb_id: 95396,
  tvdb_id: 0,
  imdb_id: '',
  title: 'Severance',
  sort_title: 'severance',
  year: 2022,
  overview: '',
  status: 'Continuing',
  path: '',
  poster_path: '',
  poster_url: '',
  monitored: true,
  quality_profile_id: 0,
  first_aired: '',
  added_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  seasons: [],
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
      return new Response(JSON.stringify(SERIES), {
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

describe('SeriesDetail search actions', () => {
  it('queues every wanted episode and reports the count', async () => {
    stubFetch(4);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    expect(host.querySelector('a[href="/series/3/search"]')?.textContent).toContain(
      'Interactive search',
    );

    searchNowButton().click();
    await settle();

    expect(calls.filter((c) => c.method === 'POST')).toEqual([
      { url: '/api/v1/library/series/3/search', method: 'POST' },
    ]);
    expect(toasts.items.map((t) => t.message)).toEqual(['4 searches started']);
  });

  it('says nothing was queued when the series is already covered', async () => {
    stubFetch(0);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    searchNowButton().click();
    await settle();

    expect(toasts.items).toHaveLength(1);
    expect(toasts.items[0]!.message).toContain('Nothing to search');
  });

  it('singularizes a lone queued search', async () => {
    stubFetch(1);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    searchNowButton().click();
    await settle();

    expect(toasts.items.map((t) => t.message)).toEqual(['1 search started']);
  });
});
