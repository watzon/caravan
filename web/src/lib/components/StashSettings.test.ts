/**
 * The Stash card.
 *
 * It is the Jellyfin card again, so these are the Jellyfin card's tests again —
 * plus the one thing that is not shared: the scope promise. A user is handing
 * this card an API key for an adult server, and "it only ever scans
 * library/Adult" has to be on screen, not only in the package comment.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import StashSettings from './StashSettings.svelte';
import type { StashConfig } from '../api/types';
import { clearToasts } from '../state/toast.svelte';

interface Call {
  url: string;
  method: string;
  body: unknown;
}

const STORED: StashConfig = {
  url: 'http://stash.lan:9999',
  api_key: 'stored-key',
  enabled: true,
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
let calls: Call[];
/** What POST /adult/stash/test answers, per test. */
let testResponse: () => Response;

beforeEach(() => {
  calls = [];
  clearToasts();
  testResponse = () => jsonResponse({ version: 'v0.28.1', hash: 'abc1234' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      if (url.endsWith('/adult/stash/test')) return testResponse();
      if (url.endsWith('/adult/stash')) {
        if ((init?.method ?? 'GET') === 'POST') {
          return jsonResponse(JSON.parse(String(init?.body)));
        }
        return jsonResponse(STORED);
      }
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
  host = document.createElement('div');
  vi.useFakeTimers();
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

function input(id: string): HTMLInputElement {
  const found = host.querySelector(`#${id}`) as HTMLInputElement | null;
  expect(found, `input #${id}`).not.toBeNull();
  return found!;
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

function type(el: HTMLInputElement, value: string) {
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

async function mountLoaded() {
  app = mount(StashSettings, { target: host, props: {} });
  await settle();
}

describe('StashSettings', () => {
  it('loads the stored configuration into the form', async () => {
    await mountLoaded();

    expect(input('stash-url').value).toBe('http://stash.lan:9999');
    expect(input('stash-api-key').value).toBe('stored-key');
    expect(host.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  });

  it('says on the card that the handoff is scoped to the Adult library', async () => {
    await mountLoaded();

    expect(host.textContent).toContain('Scans only library/Adult. Movies and Series are never sent to Stash.');
    expect(host.textContent).toContain('library/Adult');
  });

  it('saves the form to POST /adult/stash', async () => {
    await mountLoaded();

    type(input('stash-url'), 'http://media-box:9999  ');
    button('Save').click();
    await settle();

    const saved = calls.filter((c) => c.method === 'POST' && c.url.endsWith('/adult/stash'));
    expect(saved).toHaveLength(1);
    expect(saved[0]!.body).toEqual({
      url: 'http://media-box:9999',
      api_key: 'stored-key',
      enabled: true,
    });
  });

  it('tests the values in the form rather than the saved ones', async () => {
    await mountLoaded();

    type(input('stash-api-key'), 'freshly-typed');
    button('Test connection').click();
    await settle();

    const tested = calls.filter((c) => c.url.endsWith('/adult/stash/test'));
    expect(tested).toHaveLength(1);
    expect(tested[0]!.body).toEqual({
      url: 'http://stash.lan:9999',
      api_key: 'freshly-typed',
    });
    // The server naming its own build is what makes a green test believable.
    expect(host.textContent).toContain('v0.28.1');
  });

  it("surfaces the server's reason when a test fails", async () => {
    testResponse = () => jsonResponse({ error: 'stash test failed: http 401' }, 502);
    await mountLoaded();

    button('Test connection').click();
    await settle();

    expect(host.textContent).toContain('stash test failed: http 401');
    // Whatever went wrong, the key that was tried is not echoed into the page.
    expect(host.textContent).not.toContain('stored-key');
  });

  it('refuses to enable the handoff without a server URL', async () => {
    await mountLoaded();

    type(input('stash-url'), '   ');
    expect(button('Save').disabled).toBe(true);
    // Testing needs an address too, so that button goes with it.
    expect(button('Test connection').disabled).toBe(true);

    // Turning the handoff off is always allowed, address or not: it must not
    // take re-typing the server to switch the integration off.
    (host.querySelector('[role="switch"]') as HTMLButtonElement).click();
    flushSync();
    expect(button('Save').disabled).toBe(false);
  });
});
