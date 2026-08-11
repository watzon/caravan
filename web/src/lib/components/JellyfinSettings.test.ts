import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import JellyfinSettings from './JellyfinSettings.svelte';
import type { JellyfinConfig } from '../api/types';

interface Call {
  url: string;
  method: string;
  body: unknown;
}

const STORED: JellyfinConfig = {
  url: 'http://jellyfin.lan:8096',
  has_api_key: true,
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
/** What POST /handoff/jellyfin/test answers, per test. */
let testResponse: () => Response;

beforeEach(() => {
  calls = [];
  testResponse = () => jsonResponse({ server_name: 'basement', version: '10.9.11' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      if (url.endsWith('/handoff/jellyfin/test')) return testResponse();
      if (url.endsWith('/handoff/jellyfin')) {
        if ((init?.method ?? 'GET') === 'POST') {
          const body = JSON.parse(String(init?.body));
          return jsonResponse({
            url: body.url,
            has_api_key: body.api_key !== undefined ? body.api_key !== '' : STORED.has_api_key,
            enabled: body.enabled,
          });
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
  app = mount(JellyfinSettings, { target: host, props: {} });
  await settle();
}

describe('JellyfinSettings', () => {
  it('loads a blank write-only key and shows the stored hint', async () => {
    await mountLoaded();

    expect(input('jellyfin-url').value).toBe('http://jellyfin.lan:8096');
    expect(input('jellyfin-api-key').value).toBe('');
    expect(host.textContent).toContain('A key is stored. Leave this blank to keep it.');
    expect(host.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  });

  it('omits a blank key when saving an ordinary edit', async () => {
    await mountLoaded();

    type(input('jellyfin-url'), 'http://media-box:8096  ');
    button('Save').click();
    await settle();

    const saved = calls.filter((c) => c.method === 'POST' && c.url.endsWith('/handoff/jellyfin'));
    expect(saved).toHaveLength(1);
    expect(saved[0]!.body).toEqual({
      url: 'http://media-box:8096',
      enabled: true,
    });
  });

  it('sends an empty key only when Clear is explicit', async () => {
    await mountLoaded();

    button('Clear API key').click();
    button('Save').click();
    await settle();

    const saved = calls.filter((c) => c.method === 'POST' && c.url.endsWith('/handoff/jellyfin'));
    expect(saved[0]!.body).toEqual({
      url: 'http://jellyfin.lan:8096',
      api_key: '',
      enabled: true,
    });
  });

  it('tests the values in the form rather than the saved ones', async () => {
    await mountLoaded();

    type(input('jellyfin-api-key'), 'freshly-typed');
    button('Test connection').click();
    await settle();

    const tested = calls.filter((c) => c.url.endsWith('/handoff/jellyfin/test'));
    expect(tested).toHaveLength(1);
    expect(tested[0]!.body).toEqual({
      url: 'http://jellyfin.lan:8096',
      api_key: 'freshly-typed',
    });
    // The server identifying itself is what makes a green test believable.
    expect(host.textContent).toContain('basement');
    expect(host.textContent).toContain('10.9.11');
  });

  it("surfaces the server's reason when a test fails", async () => {
    testResponse = () => jsonResponse({ error: 'jellyfin test failed: http 401' }, 502);
    await mountLoaded();

    button('Test connection').click();
    await settle();

    expect(host.textContent).toContain('jellyfin test failed: http 401');
  });

  it('refuses to enable the handoff without a server URL', async () => {
    await mountLoaded();

    type(input('jellyfin-url'), '   ');
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
