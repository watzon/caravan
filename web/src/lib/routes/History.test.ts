import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import History from './History.svelte';

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.includes('/events?')) {
      return jsonResponse(url.includes('cursor=events-next')
        ? { events: [{ id: 1, level: 'info', category: 'scan', message: 'Older event', detail: '', movie_id: 0, series_id: 0, created_at: '2026-08-01T10:00:00Z' }], next_cursor: '' }
        : { events: [{ id: 2, level: 'info', category: 'scan', message: 'Newest event', detail: '', movie_id: 0, series_id: 0, created_at: '2026-08-01T10:01:00Z' }], next_cursor: 'events-next' });
    }
    if (url.includes('/jobs?')) {
      return jsonResponse(url.includes('cursor=jobs-next')
        ? { jobs: [{
            id: 8,
            kind: 'rss_sync',
            payload: '{}',
            state: 'done',
            attempts: 1,
            run_after: '',
            lease_expires_at: '',
            last_error: '',
            created_at: '2026-08-01T09:00:00Z',
            updated_at: '2026-08-01T09:01:00Z',
          }], next_cursor: '' }
        : { jobs: [{
            id: 9,
            kind: 'search_movie',
            payload: '{"movie_id":3}',
            state: 'failed',
            attempts: 3,
            run_after: '2026-08-01T10:00:00Z',
            lease_expires_at: '',
            last_error: 'Indexer timed out',
            created_at: '2026-08-01T10:00:00Z',
            updated_at: '2026-08-01T10:03:00Z',
          }], next_cursor: 'jobs-next' });
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
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function settle() {
  await vi.advanceTimersByTimeAsync(0);
  await Promise.resolve();
  flushSync();
}

describe('History', () => {
  it('renders the jobs feed with job state, attempts and failure detail', async () => {
    app = mount(History, { target: host });
    await settle();

    const jobsTab = [...host.querySelectorAll('[role="tab"]')].find(
      (tab) => tab.textContent?.trim() === 'Jobs',
    ) as HTMLButtonElement | undefined;
    expect(jobsTab).toBeDefined();
    jobsTab!.click();
    await settle();

    expect(host.textContent).toContain('Movie search');
    expect(host.textContent).toContain('Failed');
    expect(host.textContent).toContain('3/5');
    expect(host.textContent).toContain('Indexer timed out');
    expect(host.querySelector('[aria-label="Acquisition jobs"]')).not.toBeNull();
    const loadOlder = [...host.querySelectorAll('button')].find(
      (button) => button.textContent?.includes('Load older'),
    ) as HTMLButtonElement | undefined;
    expect(loadOlder?.textContent).toContain('Load older');
    loadOlder?.click();
    await settle();
    expect(host.textContent).toContain('RSS sync');
    expect(host.querySelectorAll('[aria-label="Acquisition jobs"] li')).toHaveLength(2);
  });
});
