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
  min_availability: 'released',
  added_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
  file: null,
};

let host: HTMLElement;
let app: Record<string, unknown> | undefined;
let calls: { url: string; method: string; body: unknown }[];

// A movie with an imported file: the delete-files checkbox is only offered
// when there is something on disk to delete.
const MOVIE_WITH_FILE = {
  ...MOVIE,
  file: {
    id: 1,
    path: 'library/Movies/Dune (2021)/Dune (2021).mkv',
    size: 1,
    quality: '1080p',
    source: 'bluray',
    codec: 'x265',
    audio: '',
    release_group: '',
    added_at: '2026-01-01T00:00:00Z',
    modified_at: '2026-01-01T00:00:00Z',
    compatibility: { verdict: 'ok', reasons: [] },
  },
};

function stubFetch(queued: number, movie: unknown = MOVIE) {
  calls = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      if (url.endsWith('/search')) {
        return new Response(JSON.stringify({ queued }), {
          status: 202,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      return new Response(JSON.stringify(movie), {
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

/** The header's Remove trigger, named for a screen reader by the movie title. */
function removeTrigger(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find((b) =>
    b.textContent?.includes('Remove Dune'),
  );
  expect(button, 'Remove trigger').toBeDefined();
  return button as HTMLButtonElement;
}

/** The confirm dialog's own Remove button, whose label is exactly "Remove". */
function confirmButton(): HTMLButtonElement {
  const button = [...host.querySelectorAll('button')].find(
    (b) => b.textContent?.trim() === 'Remove',
  );
  expect(button, 'confirm Remove button').toBeDefined();
  return button as HTMLButtonElement;
}

function deletes(): { url: string; method: string }[] {
  return calls.filter((c) => c.method === 'DELETE').map(({ url, method }) => ({ url, method }));
}

describe('MovieDetail remove', () => {
  it('untracks without touching files when the checkbox is left alone', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    removeTrigger().click();
    await settle();
    expect(deletes(), 'opening the confirm must not delete anything').toEqual([]);

    confirmButton().click();
    await settle();

    // No ?files=true: the files stay on disk and a rescan would re-add them.
    expect(deletes()).toEqual([{ url: '/api/v1/library/movies/7', method: 'DELETE' }]);
    expect(toasts.items[0]!.message).toContain('Removed Dune');
  });

  it('offers no delete-files checkbox when the movie has no file', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    removeTrigger().click();
    await settle();

    expect(host.querySelector('input[type="checkbox"]')).toBeNull();
  });

  it('deletes the files when the checkbox is ticked', async () => {
    stubFetch(0, MOVIE_WITH_FILE);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    removeTrigger().click();
    await settle();

    const checkbox = host.querySelector<HTMLInputElement>('input[type="checkbox"]');
    expect(checkbox, 'delete-files checkbox').toBeTruthy();
    checkbox!.checked = true;
    checkbox!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    confirmButton().click();
    await settle();

    expect(deletes()).toEqual([
      { url: '/api/v1/library/movies/7?files=true', method: 'DELETE' },
    ]);
  });

  it('sends nothing when the confirm is cancelled', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
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

    expect(calls.filter((c) => c.method === 'POST').map(({ url, method }) => ({ url, method }))).toEqual([
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

describe('minimum availability', () => {
  it('shows the stored stage and PATCHes a new choice', async () => {
    stubFetch(0);
    app = mount(MovieDetail, { target: host, props: { id: 7 } });
    await settle();

    const select = host.querySelector<HTMLSelectElement>(
      'select[aria-label="Minimum availability"]',
    );
    expect(select).not.toBeNull();
    expect(select!.value).toBe('released');

    select!.value = 'in_cinemas';
    select!.dispatchEvent(new Event('change', { bubbles: true }));
    await settle();

    const patch = calls.find((c) => c.method === 'PATCH');
    expect(patch?.url.endsWith('/library/movies/7')).toBe(true);
    expect(patch?.body).toEqual({ min_availability: 'in_cinemas' });
  });
});
