import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Wanted from './Wanted.svelte';
import { tasks } from '../state/tasks.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
let calls: { url: string; method: string }[];
let searchReplies: Record<string, { queued?: number; status: number }>;
let holdSearch: boolean;
let searchReleases: Array<() => void>;

beforeEach(() => {
  calls = [];
  searchReplies = {};
  holdSearch = false;
  searchReleases = [];
  clearToasts();
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, method: init?.method ?? 'GET' });
    if (url.endsWith('/wanted/search')) {
      return jsonResponse({ queued: 3 }, 202);
    }
    if (/\/library\/(?:movies|episodes)\/\d+\/search$/.test(url)) {
      if (holdSearch) {
        await new Promise<void>((resolve) => searchReleases.push(resolve));
      }
      const reply = searchReplies[url] ?? { queued: 1, status: 202 };
      return jsonResponse(
        reply.status >= 400 ? { error: { message: 'search failed' } } : { queued: reply.queued ?? 0 },
        reply.status,
      );
    }
    if (url.includes('/system/tasks')) return jsonResponse({ tasks: [] });
    if (url.includes('/jobs')) return jsonResponse({ jobs: [] });
    if (url.endsWith('/wanted')) {
      return jsonResponse({
        movies: [
          { id: 7, title: 'Arrival', year: 2016, poster_path: '', poster_url: '', reason: 'missing', file_quality: '' },
          { id: 8, title: 'Blade Runner', year: 1982, poster_path: '', poster_url: '', reason: 'below_cutoff', file_quality: '720p' },
        ],
        episodes: [
          { id: 10, series_id: 3, series_title: 'Severance', series_kind: 'tv', season_number: 1, episode_number: 2, title: 'Half Loop', air_date: '2026-07-14', poster_path: 'TV/Severance/poster.jpg', poster_url: '', reason: 'missing', file_quality: '' },
          { id: 11, series_id: 3, series_title: 'Severance', series_kind: 'tv', season_number: 1, episode_number: 3, title: 'In Perpetuity', air_date: '', poster_path: 'TV/Severance/poster.jpg', poster_url: '', reason: 'missing', file_quality: '' },
          { id: 12, series_id: 3, series_title: 'Severance', series_kind: 'tv', season_number: 1, episode_number: 4, title: 'The You You Are', air_date: '2026-07-28', poster_path: 'TV/Severance/poster.jpg', poster_url: '', reason: 'below_cutoff', file_quality: '720p' },
          { id: 13, series_id: 9, series_title: 'Transfixed', series_kind: 'adult', season_number: 2026, episode_number: 24, title: 'A Lesson', air_date: '2026-05-20', poster_path: 'Adult/Transfixed/poster.jpg', poster_url: '', reason: 'missing', file_quality: '' },
        ],
      });
    }
    throw new Error(`unexpected fetch: ${url}`);
  }));
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  clearToasts();
  tasks.stopSoon();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

function buttonLabeled(text: string): HTMLButtonElement | undefined {
  return [...host.querySelectorAll<HTMLButtonElement>('button[aria-label]')].find((button) =>
    button.getAttribute('aria-label')?.includes(text),
  );
}

function buttonWith(text: string): HTMLButtonElement | undefined {
  return [...host.querySelectorAll<HTMLButtonElement>('button')].find((button) =>
    button.textContent?.includes(text),
  );
}

function selectionBar(): HTMLElement | null {
  return host.querySelector('[role="group"][aria-label="Selection actions"]');
}

function searchCalls(): { url: string; method: string }[] {
  return calls.filter((call) => call.method === 'POST' && call.url.includes('/library/'));
}

describe('Wanted', () => {
  it('renders grouped results and filters them by reason', async () => {
    app = mount(Wanted, { target: host });
    await settle();

    expect(host.textContent).toContain('Movies');
    expect(host.textContent).toContain('Episodes');
    expect(host.textContent).toContain('Arrival (2016)');
    expect(host.textContent).toContain('Severance · S01E02 · Half Loop');
    expect(host.textContent).toContain('Severance · S01E03 · In Perpetuity');
    expect(host.textContent).toContain('Air date unknown');
    expect(host.querySelector('a[href="/series/3/search/1/2"]')).not.toBeNull();
    expect(host.querySelector('a[href="/adult/sites/9/search/2026/24"]')).not.toBeNull();
    expect(host.textContent).toContain('Transfixed · 2026 · #024 · A Lesson');
    expect(host.querySelector('[title="Arrival (2016)"]')).not.toBeNull();
    expect(host.querySelector('[title="Severance · S01E02 · Half Loop"]')).not.toBeNull();
    expect(host.querySelector('[title="Transfixed · 2026 · #024 · A Lesson"]')).not.toBeNull();
    expect(host.querySelector('[title="Air date unknown"]')).not.toBeNull();
    expect(host.textContent).not.toContain('Blade Runner');

    const belowCutoffName = 'Below quality cutoff 2';
    const belowCutoff = [...host.querySelectorAll<HTMLButtonElement>('[role="group"][aria-label="Wanted filter"] button')].find(
      (button) => button.textContent?.replace(/\s+/g, ' ').trim() === belowCutoffName,
    );
    expect(belowCutoff?.textContent?.replace(/\s+/g, ' ').trim()).toBe(belowCutoffName);
    expect(belowCutoff?.getAttribute('aria-pressed')).toBe('false');
    belowCutoff!.click();
    flushSync();
    expect(belowCutoff?.getAttribute('aria-pressed')).toBe('true');

    expect(host.textContent).toContain('Blade Runner (1982)');
    expect(host.textContent).toContain('720p on disk, cutoff 1080p');
    const movieRow = host.querySelector('a[href="/movies/8/search"]')?.closest('li');
    const episodeRow = host.querySelector('a[href="/series/3/search/1/4"]')?.closest('li');
    expect(movieRow?.textContent).toContain('Below quality cutoff');
    expect(episodeRow?.textContent).toContain('Below quality cutoff');
    expect(host.textContent).not.toContain('Below cutoff');
    expect(host.querySelector('[title="720p on disk, cutoff 1080p"]')).not.toBeNull();
    expect(host.textContent).not.toContain('Arrival (2016)');
    expect(host.querySelector('a[href="/movies/8/search"]')).not.toBeNull();
  });

  // The sweep covers both tabs, so it must not be scoped to the active filter,
  // and the count comes from the server: it deduplicates against searches
  // already on the queue.
  it('queues the whole wanted list from Search all', async () => {
    app = mount(Wanted, { target: host });
    await settle();

    const button = [...host.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Search all'),
    ) as HTMLButtonElement | undefined;
    expect(button).toBeDefined();

    button!.click();
    await settle();

    expect(calls.filter((c) => c.method === 'POST')).toEqual([
      { url: '/api/v1/wanted/search', method: 'POST' },
    ]);
    expect(toasts.items).toHaveLength(0);
  });

  it('searches mixed movie and episode selections sequentially with exact endpoints', async () => {
    searchReplies['/api/v1/library/episodes/10/search'] = { queued: 2, status: 202 };
    holdSearch = true;
    app = mount(Wanted, { target: host });
    await settle();

    buttonLabeled('Select Arrival (2016)')!.click();
    flushSync();
    buttonLabeled('Select Severance S01E02')!.click();
    flushSync();
    expect(selectionBar()?.textContent).toContain('2 selected');

    buttonWith('Search selected')!.click();
    await Promise.resolve();
    expect(searchCalls()).toEqual([
      { url: '/api/v1/library/movies/7/search', method: 'POST' },
    ]);

    searchReleases.shift()!();
    await settle();
    expect(searchCalls()).toEqual([
      { url: '/api/v1/library/movies/7/search', method: 'POST' },
      { url: '/api/v1/library/episodes/10/search', method: 'POST' },
    ]);
    expect(searchCalls().some((call) => call.url.includes('/library/series/'))).toBe(false);

    searchReleases.shift()!();
    await settle();
    await settle();

    expect(calls.filter((call) => call.method === 'GET' && call.url.endsWith('/wanted'))).toHaveLength(2);
    expect(toasts.items).toHaveLength(0);
    expect(selectionBar()).toBeNull();
  });

  it('clears an exact episode selection when the server reports it already handled', async () => {
    searchReplies['/api/v1/library/episodes/11/search'] = { queued: 0, status: 202 };
    app = mount(Wanted, { target: host });
    await settle();

    buttonLabeled('Select Severance S01E03')!.click();
    flushSync();
    buttonWith('Search selected')!.click();
    await settle();
    await settle();

    expect(searchCalls()).toEqual([
      { url: '/api/v1/library/episodes/11/search', method: 'POST' },
    ]);
    expect(toasts.items).toEqual([
      expect.objectContaining({ message: 'Queued 0 searches', tone: 'info' }),
    ]);
    expect(selectionBar()).toBeNull();
  });

  it('retains only failed ids after one reload and one aggregate toast', async () => {
    searchReplies['/api/v1/library/episodes/10/search'] = { status: 500 };
    app = mount(Wanted, { target: host });
    await settle();

    buttonLabeled('Select Arrival (2016)')!.click();
    flushSync();
    buttonLabeled('Select Severance S01E02')!.click();
    flushSync();
    buttonWith('Search selected')!.click();
    await settle();
    await settle();

    expect(calls.filter((call) => call.method === 'GET' && call.url.endsWith('/wanted'))).toHaveLength(2);
    expect(toasts.items).toEqual([
      expect.objectContaining({ message: 'Queued 1 search; 1 failed', tone: 'danger' }),
    ]);
    expect(selectionBar()?.textContent).toContain('1 selected');
    expect(buttonLabeled('Deselect Severance S01E02')?.getAttribute('aria-pressed')).toBe('true');
    expect(buttonLabeled('Select Arrival (2016)')?.getAttribute('aria-pressed')).toBe('false');
  });

  it('clears selections with Escape and the bulk bar control', async () => {
    app = mount(Wanted, { target: host });
    await settle();

    buttonLabeled('Select Arrival (2016)')!.click();
    flushSync();
    expect(selectionBar()?.textContent).toContain('1 selected');

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    flushSync();
    expect(selectionBar()).toBeNull();
    expect(host.querySelector('a[href="/movies/7/search"]')).not.toBeNull();

    buttonLabeled('Select Severance S01E02')!.click();
    flushSync();
    host.querySelector<HTMLButtonElement>('button[title="Clear selection"]')!.click();
    flushSync();
    expect(selectionBar()).toBeNull();
  });
});
