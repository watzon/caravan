/**
 * The requests poller. What is asserted here is the part the sidebar badge
 * depends on and the screen cannot show: that polling starts, stops with the
 * last subscriber, and — the case a background tab creates — recovers when the
 * tab becomes visible again.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { requests } from './requests.svelte';
import type { MediaRequest } from '../api/types';

let hidden = false;
let fetches: number;

function stubFetch() {
  fetches = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => {
      fetches++;
      return new Response(JSON.stringify({ requests: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
}

/** jsdom's document.hidden is read-only, so it is replaced for the test. */
function setHidden(next: boolean) {
  hidden = next;
  document.dispatchEvent(new Event('visibilitychange'));
}

/** Let the in-flight refresh settle so the next tick is not dropped. */
async function settle() {
  for (let i = 0; i < 4; i++) await Promise.resolve();
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
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('RequestsState', () => {
  it('polls at the subscribed interval and stops on unsubscribe', async () => {
    const stop = requests.subscribe(1000);
    await settle();
    expect(fetches).toBe(1);

    await vi.advanceTimersByTimeAsync(2000);
    expect(fetches).toBeGreaterThan(1);

    const afterStop = fetches;
    stop();
    await vi.advanceTimersByTimeAsync(5000);
    expect(fetches).toBe(afterStop);
  });

  /**
   * Caravan opened in a background tab: #restart() bails on document.hidden, so
   * without a visibilitychange listener nothing ever creates the timer and the
   * badge is frozen for the whole session.
   */
  it('starts polling when a tab that mounted hidden becomes visible', async () => {
    hidden = true;
    const stop = requests.subscribe(1000);
    await settle();
    // The one-shot refresh still ran; the timer did not start.
    expect(fetches).toBe(1);

    await vi.advanceTimersByTimeAsync(5000);
    expect(fetches).toBe(1);

    setHidden(false);
    await settle();
    await vi.advanceTimersByTimeAsync(3000);
    expect(fetches).toBeGreaterThan(1);

    stop();
  });

  it('remembers a created request and counts it as pending', () => {
    requests.items = [];
    requests.remember({
      id: 7,
      media_type: 'scene',
      tmdb_id: 0,
      stash_id: 'abc',
      title: 'Deep Impact',
      year: 2022,
      poster_path: '',
      poster_url: '',
      seasons: null,
      min_availability: '',
      requested_by_username: '',
      status: 'pending',
      created_at: '',
      updated_at: '',
    } as MediaRequest);
    expect(requests.pendingCount).toBe(1);

    requests.applyStatus(7, 'approved');
    expect(requests.pendingCount).toBe(0);
    expect(requests.items?.[0]?.status).toBe('approved');
  });

  it('runs a refresh that arrived while another was in flight', async () => {
    let release!: () => void;
    const gate = new Promise<void>((resolve) => (release = resolve));
    let fetches = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        fetches++;
        if (fetches === 1) await gate;
        return new Response(JSON.stringify({ requests: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );

    const first = requests.refresh();
    const second = requests.refresh();
    release();
    await first;
    await second;
    await settle();
    expect(fetches).toBe(2);
  });
});
