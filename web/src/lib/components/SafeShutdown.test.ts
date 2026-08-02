/**
 * Safe shutdown (SPEC §2.3, §11, PLAN phase 5 task 3): the confirm dialog, the
 * POST, and the terminal "safe to eject" state the SPA lands in when the server
 * it was talking to stops existing.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import SafeShutdown from './SafeShutdown.svelte';
import { shutdown } from '../state/shutdown.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** How the server answers POST /system/shutdown in each test. */
type Behavior = 'accepted' | 'connection-dropped' | 'refused';

let host: HTMLElement;
let app: Record<string, unknown>;
let posted: string[] = [];
let behavior: Behavior = 'accepted';
/** True while the process is still tearing down and still answering requests. */
let listening = true;
let statusPolls = 0;

beforeEach(() => {
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
  posted = [];
  behavior = 'accepted';
  listening = true;
  statusPolls = 0;
  shutdown.phase = 'idle';
  shutdown.confirming = false;
  shutdown.error = null;
  shutdown.timedOut = false;

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if ((init?.method ?? 'GET') === 'POST') posted.push(url);
      if (url.endsWith('/system/shutdown')) {
        if (behavior === 'connection-dropped') throw new TypeError('Failed to fetch');
        if (behavior === 'refused') {
          return jsonResponse({ error: 'this process cannot shut itself down' }, 503);
        }
        return jsonResponse({ status: 'shutting down' }, 202);
      }
      if (url.endsWith('/system/status')) {
        statusPolls++;
        // A listener that is still up is a drive that is still being written
        // to, whatever status code it answers with.
        if (listening) return jsonResponse({ version: 'test' }, 200);
        throw new TypeError('Failed to fetch');
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
  vi.useRealTimers();
  shutdown.phase = 'idle';
  shutdown.confirming = false;
  shutdown.error = null;
  shutdown.timedOut = false;
});

/** Let pending microtasks and any timers due within ms run. */
async function settle(ms = 0) {
  await vi.advanceTimersByTimeAsync(ms);
  flushSync();
}

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find(
    (candidate) => candidate.textContent?.trim() === label,
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

describe('safe shutdown', () => {
  it('confirms before stopping the server', async () => {
    app = mount(SafeShutdown, { target: host });
    flushSync();

    // Nothing goes out until the dialog is confirmed.
    button('Shut down safely').click();
    await settle();
    expect(posted).toEqual([]);
    expect(host.textContent).toContain('Shut down Caravan?');
    expect(host.textContent).toContain('safe to eject');

    // Cancelling really cancels.
    button('Cancel').click();
    await settle();
    expect(posted).toEqual([]);
    expect(shutdown.phase).toBe('idle');

    button('Shut down safely').click();
    await settle();
    button('Shut down').click();
    await settle();

    expect(posted).toEqual(['/api/v1/system/shutdown']);
    expect(shutdown.confirming).toBe(false);

    // The 202 is not the ending: the engine flush, the WAL checkpoint, the
    // database close and the clean marker all happen after it.
    expect(shutdown.phase).toBe('stopping');

    listening = false;
    await settle(1000);
    expect(shutdown.phase).toBe('stopped');
    expect(shutdown.timedOut).toBe(false);
  });

  // The regression: "safe to eject" used to appear the moment the 202 landed,
  // roughly 5ms in, while the server was still writing resume data and the WAL
  // was still un-checkpointed. A user who pulled an exFAT drive then got
  // exactly the torn database the feature exists to prevent.
  it('does not promise the drive is safe while the server still answers', async () => {
    app = mount(SafeShutdown, { target: host });
    flushSync();

    button('Shut down safely').click();
    await settle();
    button('Shut down').click();

    // Ten seconds of teardown — srv.Shutdown alone is allowed that long.
    await settle(10_000);
    expect(shutdown.phase).toBe('stopping');
    expect(statusPolls).toBeGreaterThan(1);

    listening = false;
    await settle(1000);
    expect(shutdown.phase).toBe('stopped');
  });

  it('treats a dropped connection as the start of the teardown, not the end', async () => {
    behavior = 'connection-dropped';
    app = mount(SafeShutdown, { target: host });
    flushSync();

    button('Shut down safely').click();
    await settle();
    button('Shut down').click();
    await settle(2000);

    // The browser cannot tell "202 then the listener closed" from a genuine
    // transport failure, so it waits for the origin to go quiet either way.
    expect(shutdown.phase).toBe('stopping');
    expect(shutdown.error).toBeNull();

    listening = false;
    await settle(1000);
    expect(shutdown.phase).toBe('stopped');
  });

  it('says so rather than lying when the server never stops answering', async () => {
    app = mount(SafeShutdown, { target: host });
    flushSync();

    button('Shut down safely').click();
    await settle();
    button('Shut down').click();
    await settle(130_000);

    expect(shutdown.phase).toBe('stopped');
    expect(shutdown.timedOut).toBe(true);
  });

  it('stays usable when the server refuses to stop', async () => {
    behavior = 'refused';
    app = mount(SafeShutdown, { target: host });
    flushSync();

    button('Shut down safely').click();
    await settle();
    button('Shut down').click();
    await settle();

    expect(shutdown.phase).toBe('idle');
    expect(shutdown.error).toContain('cannot shut itself down');
    expect(host.textContent).toContain('cannot shut itself down');
  });
});
