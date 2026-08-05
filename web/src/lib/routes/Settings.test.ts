import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from './Settings.svelte';
import { system } from '../state/system.svelte';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const ENGINE_SETTINGS = {
  engine_listen_port: '51413',
  engine_max_connections: '80',
  engine_max_down_kbps: '1024',
  engine_max_up_kbps: '256',
  engine_seed_ratio: '2.5',
  engine_seed_days: '14',
};

const SYSTEM_STATUS = {
  version: 'test',
  mode: 'server',
  storage_root: '/data',
  schema_version: 4,
  scanning: false,
  counts: { movies: 0, series: 0, media_files: 0, unmatched: 0 },
  disk_free_bytes: 0,
  disk_total_bytes: 0,
  engine_health: 'ok',
};

let host: HTMLElement;
let app: Record<string, unknown>;

beforeEach(() => {
  host = document.createElement('div');
  document.body.appendChild(host);
  vi.useFakeTimers();
});

afterEach(() => {
  // A module singleton: a status one test seeded must not decide the next.
  system.status = null;
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

function button(label: string) {
  const found = [...host.querySelectorAll('button')].find((candidate) => candidate.textContent?.includes(label));
  expect(found, `button labelled ${label}`).toBeDefined();
  return found!;
}

/**
 * The button with this label inside the settings card that owns `selector`.
 *
 * The Downloads pane holds several cards and each has its own Save, so a bare
 * label is ambiguous there — it used to be unique only by accident of there
 * being one card.
 */
function cardButton(selector: string, label: string) {
  const card = host.querySelector(selector)?.closest('section');
  expect(card, `card containing ${selector}`).not.toBeNull();
  const found = [...card!.querySelectorAll('button')].find((b) => b.textContent?.includes(label));
  expect(found, `button labelled ${label} in ${selector}'s card`).toBeDefined();
  return found!;
}

/** Everything the merged Downloads and Playback panes ask for on mount. */
function stubFetch() {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.endsWith('/settings')) return jsonResponse(ENGINE_SETTINGS);
    if (url.endsWith('/system/status')) return jsonResponse(SYSTEM_STATUS);
    if (url.endsWith('/indexers')) return jsonResponse({ indexers: [] });
    if (url.endsWith('/usenet-servers')) return jsonResponse({ usenet_servers: [] });
    if (url.endsWith('/download-clients/types')) return jsonResponse({ types: [] });
    if (url.endsWith('/download-clients')) return jsonResponse({ download_clients: [] });
    if (url.endsWith('/dlna')) {
      return jsonResponse({ enabled: true, friendly_name: 'Caravan', advertising: true, uuid: 'u' });
    }
    if (url.endsWith('/handoff/jellyfin')) {
      return jsonResponse({ url: '', api_key: '', enabled: false });
    }
    if (url.endsWith('/tv-profiles')) return jsonResponse({ tv_profiles: [] });
    // The DLNA card asks for the libraries to find out whether the adult
    // module is on: no adult row means no "share the Adult library" sub-toggle.
    if (url.endsWith('/libraries')) return jsonResponse({ libraries: [] });
    throw new Error(`unexpected fetch: ${url}`);
  }));
}

describe('Settings rail', () => {
  it('groups the ten sections and links each as a route', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'playback' } });
    await settle();

    for (const group of ['Library', 'Acquisition', 'Playback', 'System']) {
      expect(host.textContent).toContain(group);
    }
    const hrefs = [...host.querySelectorAll('nav a')].map((a) => a.getAttribute('href'));
    expect(hrefs).toEqual([
      '/settings/libraries',
      '/settings/metadata',
      '/settings/quality-profiles',
      '/settings/storage',
      '/settings/adult',
      '/settings/indexers',
      '/settings/downloads',
      '/settings/playback',
      '/settings/users',
      '/settings/tasks',
      '/settings/security',
    ]);
    const active = host.querySelector('nav a[aria-current="page"]');
    expect(active?.getAttribute('href')).toBe('/settings/playback');
  });

  it('lands unknown and absent sections on Metadata', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'not-a-section' } });
    await settle();

    expect(host.querySelector('#tmdb-key')).not.toBeNull();
    expect(host.querySelector('nav a[aria-current="page"]')?.getAttribute('href')).toBe(
      '/settings/metadata',
    );
  });

  // The eleven-section rail is gone, but its links are in browser histories and
  // bookmarks. Each one still has to land on the pane that absorbed it.
  it('resolves the retired slugs onto the pane that absorbed them', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'engine' } });
    await settle();

    expect(host.querySelector('#engine-listen-port')).not.toBeNull();
    expect(host.querySelector('nav a[aria-current="page"]')?.getAttribute('href')).toBe(
      '/settings/downloads',
    );
  });

  it('resolves a retired playback slug onto the Playback pane', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'dlna' } });
    await settle();

    expect(host.querySelector('#dlna-friendly-name')).not.toBeNull();
    expect(host.querySelector('#jellyfin-url')).not.toBeNull();
    expect(host.querySelector('nav a[aria-current="page"]')?.getAttribute('href')).toBe(
      '/settings/playback',
    );
  });
});

describe('Settings downloads pane', () => {
  // The order is the product's message: the built-ins work before anything
  // external is configured, so they are read first.
  it('renders the engine, news server and external client cards in that order', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'downloads' } });
    await settle();

    const titles = [...host.querySelectorAll('h3')].map((h) => h.textContent?.trim());
    expect(titles).toContain('Torrent engine');
    expect(titles).toContain('Usenet servers');
    expect(titles).toContain('External clients');
    expect(titles.indexOf('Torrent engine')).toBeLessThan(titles.indexOf('Usenet servers'));
    expect(titles.indexOf('Usenet servers')).toBeLessThan(titles.indexOf('External clients'));
  });
});

describe('Settings engine tab', () => {
  it('loads and saves all six engine settings', async () => {
    let savedPatch: Record<string, string> | null = null;
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith('/settings') && (!init?.method || init.method === 'GET')) {
        return jsonResponse(ENGINE_SETTINGS);
      }
      if (url.endsWith('/settings') && init?.method === 'PUT') {
        savedPatch = JSON.parse(String(init.body));
        return jsonResponse({ ...ENGINE_SETTINGS, ...savedPatch });
      }
      if (url.endsWith('/system/status')) return jsonResponse(SYSTEM_STATUS);
      if (url.endsWith('/indexers')) return jsonResponse({ indexers: [] });
      if (url.endsWith('/usenet-servers')) return jsonResponse({ usenet_servers: [] });
      if (url.endsWith('/download-clients/types')) return jsonResponse({ types: [] });
      if (url.endsWith('/download-clients')) return jsonResponse({ download_clients: [] });
      throw new Error(`unexpected fetch: ${url}`);
    }));

    app = mount(Settings, { target: host, props: { section: 'engine' } });
    await settle();

    expect((host.querySelector('#engine-listen-port') as HTMLInputElement).value).toBe('51413');
    expect((host.querySelector('#engine-max-connections') as HTMLInputElement).value).toBe('80');
    expect((host.querySelector('#engine-seed-ratio') as HTMLInputElement).value).toBe('2.5');

    const down = host.querySelector('#engine-max-down-kbps') as HTMLInputElement;
    down.value = '2048';
    down.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    cardButton('#engine-listen-port', 'Save changes').click();
    await settle();

    expect(savedPatch).toEqual({
      engine_listen_port: '51413',
      engine_max_connections: '80',
      engine_max_down_kbps: '2048',
      engine_max_up_kbps: '256',
      engine_seed_ratio: '2.5',
      engine_seed_days: '14',
    });
  });
});

/**
 * Settings → Metadata (PLAN phase 10 task 4).
 *
 * The key entered here is the one every metadata surface runs on, so it gets
 * the indexer card's idiom: ask the provider, report what it said, inline —
 * and prove the key in the field rather than the one on disk, so a typo never
 * has to be saved to find out it was one.
 */
describe('Settings metadata pane', () => {
  function stubMetadata(testReply: () => Response): { url: string; method: string; body: unknown }[] {
    const calls: { url: string; method: string; body: unknown }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      if (url.includes('/settings/metadata/test')) return testReply();
      if (url.endsWith('/settings')) return jsonResponse({ tmdb_api_key: 'stored-key' });
      if (url.endsWith('/system/status')) return jsonResponse(SYSTEM_STATUS);
      if (url.endsWith('/indexers')) return jsonResponse({ indexers: [] });
      if (url.endsWith('/usenet-servers')) return jsonResponse({ usenet_servers: [] });
      if (url.endsWith('/download-clients/types')) return jsonResponse({ types: [] });
      if (url.endsWith('/download-clients')) return jsonResponse({ download_clients: [] });
      throw new Error(`unexpected fetch: ${url}`);
    }));
    return calls;
  }

  it('tests the key in the field, not the one on disk', async () => {
    const calls = stubMetadata(() => jsonResponse({ status: 'ok' }));
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    const field = host.querySelector('#tmdb-key') as HTMLInputElement;
    field.value = '  typed-key  ';
    field.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    button('Test').click();
    await settle();

    expect(calls.find((c) => c.url.includes('/settings/metadata/test'))).toMatchObject({
      method: 'POST',
      body: { api_key: 'typed-key' },
    });
    expect(host.textContent).toContain('TMDB accepted this key');
    // Testing is not saving: a key is only stored when Save says so.
    expect(calls.some((c) => c.method === 'PUT')).toBe(false);
  });

  it('reports the provider’s own complaint inline when the key is refused', async () => {
    stubMetadata(() =>
      jsonResponse(
        { error: 'metadata test failed: Invalid API key', code: 'metadata_credential_invalid' },
        502,
      ),
    );
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    button('Test').click();
    await settle();

    expect(host.textContent).toContain('Invalid API key');
  });

  // A verdict is about the string that was tested. FirstRun already treats that
  // as a correctness rule and clears on input; this field outlived the value it
  // was about, so a green ✓ could sit under a completely different key and a red
  // ✕ under one the user had just corrected.
  it('forgets the verdict once the key is edited', async () => {
    stubMetadata(() => jsonResponse({ status: 'ok' }));
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    const field = host.querySelector('#tmdb-key') as HTMLInputElement;
    field.value = 'proven-key';
    field.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button('Test').click();
    await settle();
    expect(host.textContent).toContain('TMDB accepted this key');

    field.value = 'proven-key-typo';
    field.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    expect(host.textContent).not.toContain('TMDB accepted this key');
  });

  // The pane that fixes it says so: every metadata surface is degraded while
  // this is true, and inferring that from empty states elsewhere is not a plan.
  it('names the credential state it is there to fix', async () => {
    stubMetadata(() => jsonResponse({ status: 'ok' }));
    // The banner reads the shared status store, which the shell keeps current.
    system.status = { ...SYSTEM_STATUS, metadata_credential: 'absent' } as never;
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    expect(host.textContent).toContain('No TMDB API key yet');
  });

  it('stays quiet about a key that works', async () => {
    stubMetadata(() => jsonResponse({ status: 'ok' }));
    system.status = { ...SYSTEM_STATUS, metadata_credential: 'ok' } as never;
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    expect(host.textContent).not.toContain('No TMDB API key yet');
    expect(host.textContent).not.toContain('TMDB rejected this key');
  });
});
