import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Wanted from './Wanted.svelte';

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).endsWith('/wanted')) {
      return jsonResponse({
        movies: [
          { id: 7, title: 'Arrival', year: 2016, poster_path: '', poster_url: '', reason: 'missing', file_quality: '' },
          { id: 8, title: 'Blade Runner', year: 1982, poster_path: '', poster_url: '', reason: 'below_cutoff', file_quality: '720p' },
        ],
        episodes: [
          { id: 10, series_id: 3, series_title: 'Severance', season_number: 1, episode_number: 2, title: 'Half Loop', air_date: '2026-07-14', reason: 'missing', file_quality: '' },
        ],
      });
    }
    throw new Error(`unexpected fetch: ${String(input)}`);
  }));
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

describe('Wanted', () => {
  it('renders grouped results and filters them by reason', async () => {
    app = mount(Wanted, { target: host });
    await settle();

    expect(host.textContent).toContain('Movies');
    expect(host.textContent).toContain('Episodes');
    expect(host.textContent).toContain('Arrival (2016)');
    expect(host.textContent).toContain('Severance - S01E02 - Half Loop');
    expect(host.textContent).not.toContain('Blade Runner');

    const belowCutoff = [...host.querySelectorAll('[role="tab"]')].find((tab) =>
      tab.textContent?.includes('Below cutoff'),
    ) as HTMLButtonElement | undefined;
    expect(belowCutoff).toBeDefined();
    belowCutoff!.click();
    flushSync();

    expect(host.textContent).toContain('Blade Runner (1982)');
    expect(host.textContent).toContain('720p on disk, cutoff 1080p');
    expect(host.textContent).not.toContain('Arrival (2016)');
    expect(host.querySelector('a[href="/movies/8/search"]')).not.toBeNull();
  });
});
