/**
 * The tasks poller. What is asserted here is the part the sidebar rail
 * depends on: polling starts, a just-queued search is watched at the fast
 * rate, and a timer cannot outlive the last subscriber.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { tasks } from './tasks.svelte';

let hidden = false;
let fetches: string[];

function stubFetch() {
  fetches = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      fetches.push(String(input));
      const url = String(input);
      const body = url.includes('/jobs') ? { jobs: [] } : { tasks: [] };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

function setHidden(next: boolean) {
  hidden = next;
  document.dispatchEvent(new Event('visibilitychange'));
}

async function settle() {
  for (let i = 0; i < 6; i++) await Promise.resolve();
}

beforeEach(() => {
  hidden = false;
  Object.defineProperty(document, 'hidden', {
    configurable: true,
    get: () => hidden,
  });
  stubFetch();
  vi.useFakeTimers();
});

afterEach(() => {
  tasks.stopSoon();
  tasks.tasks = null;
  tasks.jobs = null;
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('TasksState', () => {
  it('polls at the subscribed interval and stops on unsubscribe', async () => {
    const stop = tasks.subscribe(1000);
    await settle();
    expect(fetches.some((url) => url.includes('/system/tasks'))).toBe(true);
    expect(fetches.some((url) => url.includes('/jobs'))).toBe(true);

    const afterStart = fetches.length;
    await vi.advanceTimersByTimeAsync(2000);
    expect(fetches.length).toBeGreaterThan(afterStart);

    const afterStop = fetches.length;
    stop();
    await vi.advanceTimersByTimeAsync(5000);
    expect(fetches.length).toBe(afterStop);
  });

  it('watches a just-queued search at the fast rate', async () => {
    tasks.watchSoon(10_000);
    await settle();
    const afterStart = fetches.length;

    await vi.advanceTimersByTimeAsync(5000);
    expect(fetches.length).toBeGreaterThan(afterStart);

    tasks.stopSoon();
    const afterBurst = fetches.length;
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetches.length).toBe(afterBurst);
  });

  it('starts polling when a tab that mounted hidden becomes visible', async () => {
    hidden = true;
    const stop = tasks.subscribe(1000);
    await settle();
    expect(fetches.length).toBeGreaterThan(0);

    const afterHidden = fetches.length;
    await vi.advanceTimersByTimeAsync(3000);
    expect(fetches.length).toBe(afterHidden);

    setHidden(false);
    await settle();
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetches.length).toBeGreaterThan(afterHidden);
    stop();
  });
});
