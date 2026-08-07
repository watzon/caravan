import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Settings from './Settings.svelte';
import TopBar from '../layout/TopBar.svelte';
import FilterOptions from '../components/FilterOptions.svelte';
import PosterCard from '../components/PosterCard.svelte';
import { reactiveProps } from '../reactiveprops.svelte';
import { system } from '../state/system.svelte';
import { providers } from '../state/providers.svelte';
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
  // Same for the provider list, which load() otherwise fetches only once.
  providers.all = [];
  providers.loaded = false;
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
    if (url.endsWith('/libraries/providers')) return jsonResponse({ providers: [] });
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

    expect(host.querySelectorAll('h1')).toHaveLength(0);
    expect(host.querySelector('#tmdb-key')).toBeNull();
    expect(host.textContent).not.toContain('Find a setting');
    expect(host.querySelector('h2#setup-heading')?.textContent).toContain('Set up Caravan');
    expect(host.querySelector('h2#settings-categories-heading')?.textContent).toContain(
      'Browse settings',
    );
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

  it('makes every setup checklist row one full-row link', async () => {
    stubFetch();
    app = mount(Settings, { target: host });
    await settle();

    const rows = [...host.querySelectorAll('ol > li')];
    expect(rows).toHaveLength(4);
    for (const row of rows) {
      const link = row.querySelector(':scope > a');
      expect(link).not.toBeNull();
      expect(link?.querySelector('.text-ink-secondary')).not.toBeNull();
      expect(link?.textContent).toMatch(/Done|Checking|Needs setup/);
    }
  });

  it('renders the settings action with an accessible search name and honest placeholder', async () => {
    stubFetch();
    app = mount(Settings, { target: host });
    await settle();

    const chrome = document.createElement('div');
    host.appendChild(chrome);
    const topBar = mount(TopBar, { target: chrome, props: { title: 'Settings' } });
    flushSync();

    const search = host.querySelector<HTMLInputElement>('#settings-search');
    expect(search?.getAttribute('aria-label')).toBe('Search settings');
    expect(search?.placeholder).toBe('Search settings');
    unmount(topBar);
  });

  it('keeps one mounted instance synchronized with section, fragment, and overview routes', async () => {
    stubFetch();
    navigate('/settings', { replace: true });
    const props = reactiveProps({ section: router.match?.params.section ?? '' });
    app = mount(Settings, { target: host, props: props.props });
    await settle();

    expect(host.querySelector('h1')).toBeNull();
    expect(host.querySelector('#setup-heading')).not.toBeNull();

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
    expect(host.querySelector('h1')).toBeNull();
    expect(host.querySelector('#setup-heading')).not.toBeNull();
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

  it('offers advanced settings only on catalog entries marked advanced', async () => {
    stubFetch();
    app = mount(Settings, { target: host, props: { section: 'playback' } });
    await settle();

    expect(
      [...host.querySelectorAll('button')].some((item) => item.textContent?.includes('advanced')),
    ).toBe(false);

    unmount(app);
    app = mount(Settings, { target: host, props: { section: 'downloads' } });
    await settle();

    expect(button('Show advanced')).toBeDefined();
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

describe('Plan 020 truncation contracts', () => {
  it('exposes complete filter option names and hints', () => {
    const name = 'A provider filter name that cannot fit in the popover';
    const hint = 'A complete secondary description for matching names';
    app = mount(FilterOptions, {
      target: host,
      props: {
        options: [{ id: 'provider-1', name, hint }],
        selected: [],
        onselect: vi.fn(),
      },
    });

    const truncated = [...host.querySelectorAll<HTMLElement>('button .truncate')];
    expect(truncated.map((element) => element.title)).toEqual([name, hint]);
  });

  it('exposes complete poster metadata and visibly names its status', () => {
    const note = 'A note long enough to be visually truncated by a narrow poster card';
    app = mount(PosterCard, {
      target: host,
      props: {
        href: '/movies/1',
        title: 'Example movie',
        year: 2026,
        posterPath: null,
        status: 'downloaded',
        note,
      },
    });

    const subtitle = [...host.querySelectorAll<HTMLParagraphElement>('p.truncate')].find((element) =>
      element.textContent?.includes(note),
    );
    expect(subtitle?.title).toBe(`2026 · ${note}`);
    expect(host.querySelector('a')?.getAttribute('aria-label')).toBe(
      'Example movie (2026), Downloaded',
    );

    const statusText = [...host.querySelectorAll('span')].find(
      (element) => element.children.length === 0 && element.textContent?.trim() === 'Downloaded',
    );
    expect(statusText).toBeDefined();
    expect(statusText?.classList.contains('sr-only')).toBe(false);
  });

  it('retains selection semantics and status in a selectable card name', () => {
    app = mount(PosterCard, {
      target: host,
      props: {
        href: '/movies/1',
        title: 'Example movie',
        year: 2026,
        posterPath: null,
        status: 'downloaded',
        selectable: true,
        selected: true,
      },
    });

    const card = host.querySelector('button');
    expect(card?.getAttribute('aria-pressed')).toBe('true');
    expect(card?.getAttribute('aria-label')).toBe('Example movie (2026), Downloaded');
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
  const ALL_PROVIDERS = [
    { id: 'tmdb', name: 'TMDB', kinds: ['movie', 'tv'] },
    { id: 'anilist', name: 'AniList', kinds: ['tv'] },
    { id: 'stashbox', name: 'Stash-box', kinds: ['adult'] },
  ];

  function stubMetadata(
    testReply: () => Response,
    providerList: unknown[] = ALL_PROVIDERS,
  ): { url: string; method: string; body: unknown }[] {
    const calls: { url: string; method: string; body: unknown }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
      });
      if (url.includes('/settings/metadata/test')) return testReply();
      if (url.endsWith('/libraries/providers')) return jsonResponse({ providers: providerList });
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

  // Jellyfin's split: this page is each provider's own configuration; which
  // library uses which provider lives in Libraries. The page must say both.
  it('shows a card per provider and points chain ordering at Libraries', async () => {
    stubMetadata(() => jsonResponse({ status: 'ok' }));
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    expect(host.textContent).toContain('AniList needs no key or account');
    expect(host.querySelector('a[href="/settings/libraries#libraries"]')).not.toBeNull();
    // Stash-box configures itself with the adult module; this page only points.
    expect(host.querySelector('#stashbox-endpoint')).toBeNull();
    expect(host.querySelector('a[href="/settings/adult#adult-content"]')).not.toBeNull();
  });

  // TVmaze is keyless, so "Ready" is the whole of its configuration. The
  // negative half is the load-bearing one: a card with a key field on it would
  // ask for a credential that does not exist, and the next provider to gain one
  // must not gain it here by accident.
  it('offers TVmaze as ready with nothing to enter', async () => {
    stubMetadata(() => jsonResponse({ status: 'ok' }), [
      ...ALL_PROVIDERS,
      { id: 'tvmaze', name: 'TVmaze', kinds: ['tv'] },
    ]);
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    const card = [...host.querySelectorAll('section')].find(
      (s) => s.querySelector('h3')?.textContent === 'TVmaze',
    );
    expect(card).toBeDefined();
    expect(card!.textContent).toContain('Ready');
    expect(card!.textContent).toContain('TVmaze needs no key or account');
    expect(card!.querySelector('input')).toBeNull();
  });

  // TheTVDB is the second key-based provider, so the card it renders through is
  // shared with TMDB. These say the shared card is addressed per provider —
  // its own field ids, its own settings keys, its own `provider` on the test —
  // which is the whole of what "shared" is allowed to mean.
  it('offers TheTVDB a key field and the subscriber PIN beside it', async () => {
    stubMetadata(() => jsonResponse({ status: 'ok' }));
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    const card = [...host.querySelectorAll('section')].find(
      (s) => s.querySelector('h3')?.textContent === 'TheTVDB',
    );
    expect(card).toBeDefined();
    expect(card!.querySelector('#thetvdb-key')).not.toBeNull();
    expect(card!.querySelector('#thetvdb-pin')).not.toBeNull();
    expect(card!.textContent).toContain('Subscriber PIN');
    // The support question the PIN raises, answered where it is asked.
    expect(card!.textContent).toContain('Only for user-supported keys. Leave blank for a licensed key.');
  });

  it('tests the TheTVDB field against TheTVDB, not TMDB', async () => {
    const calls = stubMetadata(() => jsonResponse({ status: 'ok' }));
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    const field = host.querySelector('#thetvdb-key') as HTMLInputElement;
    field.value = 'tvdb-key';
    field.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    cardButton('#thetvdb-key', 'Test').click();
    await settle();

    expect(calls.find((c) => c.url.includes('/settings/metadata/test'))).toMatchObject({
      method: 'POST',
      body: { api_key: 'tvdb-key', provider: 'thetvdb' },
    });
    expect(host.textContent).toContain('TheTVDB accepted this key');
  });

  it('saves the TheTVDB key and the PIN to their own settings keys', async () => {
    const calls = stubMetadata(() => jsonResponse({ status: 'ok' }));
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    const key = host.querySelector('#thetvdb-key') as HTMLInputElement;
    key.value = 'tvdb-key';
    key.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    cardButton('#thetvdb-key', 'Save').click();
    await settle();
    expect(calls.filter((c) => c.method === 'PUT').at(-1)?.body).toEqual({
      thetvdb_api_key: 'tvdb-key',
    });

    // A licensed key needs no PIN, so the PIN is savable on its own — a blank
    // key beside it must not be read as "clear the key".
    key.value = '';
    key.dispatchEvent(new Event('input', { bubbles: true }));
    const pin = host.querySelector('#thetvdb-pin') as HTMLInputElement;
    pin.value = '  1234  ';
    pin.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    cardButton('#thetvdb-key', 'Save').click();
    await settle();
    expect(calls.filter((c) => c.method === 'PUT').at(-1)?.body).toEqual({ thetvdb_pin: '1234' });
  });

  // The point of the per-provider map (phase 5): one rejected key marks its own
  // provider and says nothing about the other.
  it('reads each card’s badge from that provider’s entry in the status map', async () => {
    stubMetadata(() => jsonResponse({ status: 'ok' }));
    system.status = {
      ...SYSTEM_STATUS,
      metadata_credentials: {
        tmdb: { state: 'ok', reason: '', checked_at: '' },
        thetvdb: { state: 'invalid', reason: 'TheTVDB says no', checked_at: '' },
      },
    } as never;
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    const cardFor = (title: string) =>
      [...host.querySelectorAll('section')].find((s) => s.querySelector('h3')?.textContent === title)!;

    expect(cardFor('TheTVDB').textContent).toContain('Key rejected');
    expect(cardFor('TheTVDB').textContent).toContain('TheTVDB rejected this key');
    // The provider's own complaint, not a generic one.
    expect(cardFor('TheTVDB').textContent).toContain('TheTVDB says no');
    expect(cardFor('TMDB').textContent).toContain('Connected');
    expect(cardFor('TMDB').textContent).not.toContain('TMDB rejected this key');
  });

  // The keyless cards must not gain a field from the card the keyed ones share.
  it('leaves the keyless providers with nothing to enter', async () => {
    stubMetadata(() => jsonResponse({ status: 'ok' }), [
      ...ALL_PROVIDERS,
      { id: 'tvmaze', name: 'TVmaze', kinds: ['tv'] },
    ]);
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    for (const title of ['AniList', 'TVmaze']) {
      const card = [...host.querySelectorAll('section')].find(
        (s) => s.querySelector('h3')?.textContent === title,
      );
      expect(card, `${title} card`).toBeDefined();
      expect(card!.querySelector('input'), `${title} key input`).toBeNull();
    }
  });

  // The server omits the adult provider when the module is absent; the page
  // must not reintroduce it (promise-of-absence).
  it('omits the Stash-box card when the server does not list the provider', async () => {
    stubMetadata(
      () => jsonResponse({ status: 'ok' }),
      ALL_PROVIDERS.filter((p) => p.id !== 'stashbox'),
    );
    app = mount(Settings, { target: host, props: { section: 'metadata' } });
    await settle();

    expect(host.textContent).toContain('AniList');
    expect(host.textContent).not.toContain('Stash-box');
  });
});
