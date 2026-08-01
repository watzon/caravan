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
    if (url.includes('/events?')) return jsonResponse({ events: [] });
    if (url.includes('/jobs?')) return jsonResponse({ jobs: [
      {
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
      },
    ] });
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
      (tab) => tab.textContent === 'Jobs',
    ) as HTMLButtonElement | undefined;
    expect(jobsTab).toBeDefined();
    jobsTab!.click();
    await settle();

    expect(host.textContent).toContain('Movie search');
    expect(host.textContent).toContain('Failed');
    expect(host.textContent).toContain('3/5');
    expect(host.textContent).toContain('Indexer timed out');
    expect(host.querySelector('[aria-label="Acquisition jobs"]')).not.toBeNull();
  });
});
