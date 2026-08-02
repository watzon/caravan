import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DlnaSettings from './DlnaSettings.svelte';
import type { DlnaStatus, Settings } from '../api/types';

const ADVERTISING: DlnaStatus = {
  enabled: true,
  friendly_name: 'Caravan',
  uuid: '1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed',
  advertising: true,
  error: '',
};

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
let saved: Settings[];
/** What GET /dlna answers, per test. */
let status: () => Response;

beforeEach(() => {
  saved = [];
  status = () => jsonResponse(ADVERTISING);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.endsWith('/dlna')) return status();
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

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

function nameInput(): HTMLInputElement {
  const found = host.querySelector('#dlna-friendly-name') as HTMLInputElement | null;
  expect(found).not.toBeNull();
  return found!;
}

function type(el: HTMLInputElement, value: string) {
  el.value = value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

async function mountLoaded(settings: Settings = {}) {
  app = mount(DlnaSettings, {
    target: host,
    props: {
      settings,
      onsave: async (patch: Settings) => {
        saved.push(patch);
        return true;
      },
    },
  });
  await settle();
}

describe('DlnaSettings', () => {
  it('seeds the form from the server rather than from the raw settings', async () => {
    // The settings table has neither key on a fresh install; the server's
    // defaults are the truth about what is running.
    await mountLoaded({});

    expect(nameInput().value).toBe('Caravan');
    expect(host.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
    expect(host.textContent).toContain('Advertising');
    expect(host.textContent).toContain('1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed');
  });

  it('saves the toggle and the trimmed name through the settings flow', async () => {
    await mountLoaded();

    type(nameInput(), '  Den TV  ');
    (host.querySelector('[role="switch"]') as HTMLButtonElement).click();
    flushSync();
    button('Save').click();
    await settle();

    expect(saved).toEqual([{ dlna_enabled: 'false', dlna_friendly_name: 'Den TV' }]);
  });

  it('refuses to save a blank device name', async () => {
    await mountLoaded();

    type(nameInput(), '   ');
    expect(button('Save').disabled).toBe(true);
    expect(saved).toHaveLength(0);
  });

  // Enabled-but-silent is the failure a user actually hits, and it is invisible
  // unless the UI distinguishes it from "off".
  it('explains an enabled server that is not on the network', async () => {
    status = () =>
      jsonResponse({
        ...ADVERTISING,
        advertising: false,
        error: 'dlna: join ssdp group: no such device',
      });
    await mountLoaded();

    expect(host.textContent).toContain('Not on the network');
    expect(host.textContent).toContain('no such device');
    expect(host.textContent).not.toContain('Advertising');
  });

  it('renders a switched-off server as off, with no warning', async () => {
    status = () => jsonResponse({ ...ADVERTISING, enabled: false, advertising: false });
    await mountLoaded();

    expect(host.textContent).toContain('Off');
    expect(host.textContent).not.toContain('Not on the network');
    expect(host.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('false');
  });

  it('surfaces a failed load with a retry', async () => {
    status = () => jsonResponse({ error: 'boom' }, 500);
    await mountLoaded();

    expect(host.textContent).toContain('boom');
    expect(button('Retry')).toBeDefined();
  });
});
