import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from './Settings.svelte';

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
    button('Save changes').click();
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
