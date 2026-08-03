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

// Two seasons holding three episode files between them: the confirm names the
// count it would delete, so "Also delete 3 files" has to come from the data.
const SERIES_WITH_FILES = {
  ...SERIES,
  seasons: [
    {
      id: 1,
      series_id: 3,
      season_number: 1,
      title: '',
      overview: '',
      poster_path: '',
      air_date: '',
      monitored: true,
      episodes: [1, 2].map((n) => episode(n, true)),
    },
    {
      id: 2,
      series_id: 3,
      season_number: 2,
      title: '',
      overview: '',
      poster_path: '',
      air_date: '',
      monitored: true,
      episodes: [episode(1, true), episode(2, false)],
    },
  ],
};

function episode(number: number, withFile: boolean) {
  return {
    id: number * 10,
    series_id: 3,
    season_number: 1,
    episode_number: number,
    tmdb_id: 0,
    title: '',
    overview: '',
    air_date: '',
    monitored: true,
    file: withFile
      ? {
          id: number,
          path: `library/TV/Severance (2022)/Season 01/e${number}.mkv`,
          size: 1,
          quality: '1080p',
          source: 'webdl',
          codec: '',
          audio: '',
          release_group: '',
          added_at: '2026-01-01T00:00:00Z',
          modified_at: '2026-01-01T00:00:00Z',
          compatibility: { verdict: 'ok', reasons: [] },
        }
      : null,
  };
}

function stubFetch(queued: number, series: unknown = SERIES) {
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
      return new Response(JSON.stringify(series), {
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

function removeTrigger(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find((b) =>
    b.textContent?.includes('Remove Severance'),
  );
  expect(button, 'Remove trigger').toBeDefined();
  return button as HTMLButtonElement;
}

function confirmButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === 'Remove',
  );
  expect(button, 'confirm Remove button').toBeDefined();
  return button as HTMLButtonElement;
}

function deletes(): { url: string; method: string }[] {
  return calls.filter((c) => c.method === 'DELETE');
}

describe('SeriesDetail remove', () => {
  it('untracks without touching files by default', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    removeTrigger().click();
    await settle();
    expect(deletes(), 'opening the confirm must not delete anything').toEqual([]);

    confirmButton().click();
    await settle();

    expect(deletes()).toEqual([{ url: '/api/v1/library/series/3', method: 'DELETE' }]);
    expect(toasts.items[0]!.message).toContain('Removed Severance');
  });

  it('counts the episode files the checkbox would delete', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    removeTrigger().click();
    await settle();

    const dialog = host.querySelector('[role="dialog"]');
    expect(dialog?.textContent).toContain('Also delete 3 files from disk');

    const checkbox = host.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(checkbox, 'delete-files checkbox').toBeTruthy();
    checkbox!.checked = true;
    checkbox!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    confirmButton().click();
    await settle();

    expect(deletes()).toEqual([
      { url: '/api/v1/library/series/3?files=true', method: 'DELETE' },
    ]);
  });

  it('sends nothing when the confirm is cancelled', async () => {
    stubFetch(0, SERIES_WITH_FILES);
    app = mount(SeriesDetail, { target: host, props: { id: 3 } });
    await settle();

    removeTrigger().click();
    await settle();

    const cancel = [...host.querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Cancel',
    );
    expect(cancel, 'Cancel button').toBeDefined();
    cancel!.click();
    await settle();

    expect(deletes()).toEqual([]);
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });
});

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
