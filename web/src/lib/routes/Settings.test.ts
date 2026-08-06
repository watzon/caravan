import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from './Settings.svelte';
import { reactiveProps } from '../reactiveprops.svelte';
import { system } from '../state/system.svelte';
import { navigate, router } from '../router.svelte';
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
  localStorage.clear();
  vi.useFakeTimers();
});

afterEach(() => {
  // A module singleton: a status one test seeded must not decide the next.
  system.status = null;
  unmount(app);
  host.remove();
  localStorage.clear();
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

describe('Settings overview and route resolution', () => {
  it('renders the bare route as a task-based overview, not Metadata', async () => {
    stubFetch();
    app = mount(Settings, { target: host });
    await settle();

    expect(host.querySelector('#settings-overview')?.textContent).toContain('Settings');
    expect(host.querySelector('#tmdb-key')).toBeNull();
    expect(host.textContent).not.toContain('Find a setting');
    expect(host.textContent).toContain('Set up Caravan');
    expect(host.textContent).toContain('Browse settings');
  });


  it('renders content only because shell settings navigation owns the category links', async () => {
    stubFetch();
    app = mount(Settings, { target: host });
    await settle();

    const layout = host.querySelector<HTMLElement>('[data-settings-layout]');
    const main = host.querySelector<HTMLElement>('[data-settings-main]');

    expect(layout?.className).toContain('flex-1');
    expect(layout?.className).not.toContain('lg:flex-row');
    expect(main?.className).toContain('flex-1');
    expect(host.querySelector('[data-settings-sidebar]')).toBeNull();
    expect(host.querySelector('[data-settings-navigation]')).toBeNull();
    expect(host.querySelector('[data-settings-navigation-toggle]')).toBeNull();
    expect(host.querySelector('aside')).toBeNull();
    expect(host.querySelector('nav[aria-label="Settings pages"]')).toBeNull();
  });

  it('derives setup states from settings and live API results', async () => {
    stubFetch();
    app = mount(Settings, { target: host });
    await settle();

    expect(host.textContent).toContain('Choose a storage location');
    expect(host.textContent).toContain('Needs setup');
    expect(host.textContent).toContain('Create a library');
    expect(host.textContent).toContain('Add a search or download source');
  });

  it('keeps one mounted instance synchronized with section, fragment, and overview routes', async () => {
    stubFetch();
    navigate('/settings', { replace: true });
    const props = reactiveProps({ section: router.match?.params.section ?? '' });
    app = mount(Settings, { target: host, props: props.props });
    await settle();

    expect(host.querySelector('#settings-overview')).not.toBeNull();
    expect(host.querySelector('#settings-search')).toBeNull();

    navigate('/settings/downloads#download-clients');
    props.set({ section: router.match?.params.section ?? '' });
    await settle();

    expect(router.hash).toBe('#download-clients');
    expect(host.querySelector('#settings-overview')).toBeNull();
    expect(host.querySelector('#settings-search')).toBeNull();
    expect(host.querySelector('h1#downloads')?.textContent).toContain('Downloads');
    expect(host.querySelector('#download-clients')).not.toBeNull();

    navigate('/settings');
    props.set({ section: router.match?.params.section ?? '' });
    await settle();

    expect(router.hash).toBe('');
    expect(host.querySelector('#settings-overview')).not.toBeNull();
    expect(host.querySelector('#settings-search')).toBeNull();
    expect(host.querySelector('#download-clients')).toBeNull();
  });

  it('fails closed when loading settings fails', async () => {
    const fetch = vi.fn(async () => jsonResponse({ error: 'settings unavailable' }, 503));
    vi.stubGlobal('fetch', fetch);
    app = mount(Settings, { target: host, props: { section: 'libraries' } });
    await settle();

    expect(host.textContent).toContain('settings unavailable');
    expect(host.querySelector('#settings-search')).toBeNull();
    expect(fetch).toHaveBeenCalledOnce();
  });

  it('keeps direct and retired deep routes compatible', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'engine' } });
    await settle();

    expect(host.querySelector('#engine-listen-port')).not.toBeNull();
    expect(host.querySelector('h1#downloads')?.textContent).toContain('Downloads');
  });

  it('uses the resolved page title without rendering a nested settings navigation', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'playback' } });
    await settle();

    expect(host.querySelector('h1#playback')?.textContent).toContain('Playback');
    expect(host.querySelector('[data-settings-sidebar]')).toBeNull();
    expect(host.querySelector('[data-settings-navigation-toggle]')).toBeNull();
  });

  it('persists the advanced-settings preference and hides marked descendants by default', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'downloads' } });
    await settle();

    const engine = host.querySelector('#torrent-engine') as HTMLElement;
    expect(engine.getAttribute('aria-hidden')).toBe('true');
    expect(engine.hidden).toBe(true);
    expect(getComputedStyle(engine).display).toBe('none');

    button('Show advanced').click();
    flushSync();
    expect(localStorage.getItem('caravan.settings.show-advanced')).toBe('true');
    expect(engine.getAttribute('aria-hidden')).toBe('false');
    expect(engine.hidden).toBe(false);
    expect(getComputedStyle(engine).display).not.toBe('none');

    unmount(app);
    app = mount(Settings, { target: host, props: { section: 'downloads' } });
    await settle();
    expect(button('Hide advanced')).toBeDefined();
  });

  it('lands unknown sections on Metadata', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'not-a-section' } });
    await settle();

    expect(host.querySelector('#tmdb-key')).not.toBeNull();
    expect(host.querySelector('h1#metadata')?.textContent).toContain('Metadata');
  });

  it('resolves a retired playback slug onto the Playback pane', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'dlna' } });
    await settle();

    expect(host.querySelector('#dlna-friendly-name')).not.toBeNull();
    expect(host.querySelector('#jellyfin-url')).not.toBeNull();
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
      if (url.endsWith('/settings')) return jsonResponse({ tmdb_api_key_set: 'true' });
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

  it('keeps the field blank, saves typed keys, and clears explicitly', async () => {
    const calls = stubMetadata(() => jsonResponse({ status: 'ok' }));
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    const field = host.querySelector('#tmdb-key') as HTMLInputElement;
    expect(field.value).toBe('');
    expect(host.textContent).toContain('A key is stored. Leave blank to keep it.');

    field.value = 'typed-key';
    field.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button('Save').click();
    await settle();
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ tmdb_api_key: 'typed-key' });

    button('Clear').click();
    await settle();
    const puts = calls.filter((c) => c.method === 'PUT');
    expect(puts.at(-1)?.body).toEqual({ tmdb_api_key: '' });
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
