import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import FlareSolverrSettings from './FlareSolverrSettings.svelte';
import type { Settings } from '../api/types';

interface Call {
  url: string;
  method: string;
  body: unknown;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
let calls: Call[];
let testResponse: () => Response;
let saved: Settings[];

beforeEach(() => {
  calls = [];
  saved = [];
  testResponse = () => jsonResponse({ status: 'ok', version: '3.5.0' });
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      if (url.endsWith('/settings/flaresolverr/test')) return testResponse();
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

function input(): HTMLInputElement {
  const found = host.querySelector('#flaresolverr-url') as HTMLInputElement | null;
  expect(found).not.toBeNull();
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

function mountWith(settings: Settings) {
  app = mount(FlareSolverrSettings, {
    target: host,
    props: {
      settings,
      onsave: async (patch: Settings) => {
        saved.push(patch);
        return true;
      },
    },
  });
  flushSync();
}

describe('FlareSolverrSettings', () => {
  it('shows the stored URL and keeps Save disabled until it changes', () => {
    mountWith({ flaresolverr_url: 'http://flaresolverr:8191' });

    expect(input().value).toBe('http://flaresolverr:8191');
    expect(button('Save').disabled).toBe(true);

    type(input(), 'http://solver.lan:8191');
    expect(button('Save').disabled).toBe(false);
  });

  it('saves the trimmed URL under the settings key', async () => {
    mountWith({});

    type(input(), '  http://solver.lan:8191/ ');
    button('Save').click();
    await settle();

    expect(saved).toEqual([{ flaresolverr_url: 'http://solver.lan:8191/' }]);
  });

  it('tests the typed URL and reports the solver version', async () => {
    mountWith({});

    expect(button('Test connection').disabled).toBe(true);
    type(input(), 'http://solver.lan:8191');
    button('Test connection').click();
    await settle();

    const tests = calls.filter((c) => c.url.endsWith('/settings/flaresolverr/test'));
    expect(tests).toHaveLength(1);
    expect(tests[0]!.body).toEqual({ url: 'http://solver.lan:8191' });
    expect(host.textContent).toContain('FlareSolverr 3.5.0 answered.');
  });

  it('shows the server error when the test fails', async () => {
    testResponse = () => jsonResponse({ error: 'FlareSolverr test failed: unreachable' }, 502);
    mountWith({ flaresolverr_url: 'http://down:8191' });

    button('Test connection').click();
    await settle();

    expect(host.textContent).toContain('FlareSolverr test failed: unreachable');
  });
});
