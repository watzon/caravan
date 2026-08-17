import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DlnaSettings from './DlnaSettings.svelte';
import type { DlnaStatus, Library, Settings } from '../api/types';

/** The Adult library row, as GET /libraries reports it once the module is on. */
const ADULT_LIBRARY: Library = {
  id: 3,
  kind: 'adult',
  name: 'Adult',
  icon: '',
  root_path: 'library/Adult',
  provider: 'stashbox',
  providers: ['stashbox'],
  is_default: true,
  item_count: 0,
  active: true,
  restricted: true,
  dlna_visible: false,
  route_torrent: '',
  route_usenet: '',
  quality_profile_id: 0,
  indexers: [],
};

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
/** What GET /libraries answers: no adult row means the module is off. */
let libraries: Library[];
/** Every PATCH /libraries/{id} body the sub-toggle sent. */
let patched: { id: number; body: unknown }[];

beforeEach(() => {
  saved = [];
  libraries = [];
  patched = [];
  status = () => jsonResponse(ADVERTISING);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/dlna')) return status();
      if (url.endsWith('/libraries')) return jsonResponse({ libraries });
      const patch = /\/libraries\/(\d+)$/.exec(url);
      if (patch && init?.method === 'PATCH') {
        const body = JSON.parse(String(init.body));
        patched.push({ id: Number(patch[1]), body });
        const updated = { ...ADULT_LIBRARY, ...body };
        libraries = [updated];
        return jsonResponse(updated);
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
    const advertisingStatuses = [...host.querySelectorAll('span')].filter(
      (element) => element.textContent?.trim() === 'Advertising',
    );
    expect(advertisingStatuses).toHaveLength(1);
    expect(advertisingStatuses[0]?.closest('dd')).not.toBeNull();
    expect(advertisingStatuses[0]?.classList.contains('sr-only')).toBe(false);
    expect(host.textContent).toContain('1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed');
    const deviceID = [...host.querySelectorAll('dd')].find(
      (element) => element.textContent?.trim() === ADVERTISING.uuid,
    );
    expect(deviceID?.getAttribute('title')).toBe(ADVERTISING.uuid);
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

  // PLAN phase 9 task 7b. The sub-toggle is the one discoverable control for
  // putting adult content on the LAN, so its absence while the module is off is
  // as much of the contract as its behaviour while the module is on.
  describe('the Adult sub-toggle', () => {
    function switches(): HTMLButtonElement[] {
      return [...host.querySelectorAll('[role="switch"]')] as HTMLButtonElement[];
    }

    function toggle(index: number): HTMLButtonElement {
      const found = switches()[index];
      expect(found, `switch #${index}`).toBeDefined();
      return found as HTMLButtonElement;
    }

    it('is absent while the adult module is off', async () => {
      // GET /libraries omits the adult row entirely on a server with the module
      // switched off, which is exactly what "no sub-toggle" is derived from.
      await mountLoaded();

      expect(switches()).toHaveLength(1);
      expect(host.textContent).not.toContain('Adult');
    });

    it('writes the adult library dlna_visible and warns once it is on', async () => {
      libraries = [ADULT_LIBRARY];
      await mountLoaded();

      const sub = toggle(1);
      expect(sub.getAttribute('aria-checked')).toBe('false');
      // Off is the state a fresh enable leaves it in, and the copy says why it
      // matters before it is flipped, not only after.
      expect(host.textContent).toContain(
        'DLNA has no accounts. Every device on this network can browse anything shared here.',
      );

      sub.click();
      await settle();

      // The phase-8 libraries API, not a DLNA setting of its own.
      expect(patched).toEqual([{ id: 3, body: { dlna_visible: true } }]);
      expect(toggle(1).getAttribute('aria-checked')).toBe('true');
      // Nothing about the DLNA card's own Save button was touched by it.
      expect(saved).toHaveLength(0);
    });
  });
});
