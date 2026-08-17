/**
 * The category picker's load/reload flow, mounted for real against a stubbed
 * /api/v1. Field report to reproduce: edit an indexer, the caps tree loads (or
 * a reload is clicked) — the tree must actually appear, not only after
 * switching forms and coming back.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import IndexerSettings from './IndexerSettings.svelte';
import type { Indexer, IndexerCategory } from '../api/types';

const GEEK: Indexer = {
  id: 1,
  name: 'NZBGeek',
  url: 'https://api.nzbgeek.info',
  has_api_key: true,
  type: 'newznab',
  categories: [2000],
  priority: 25,
  enabled: true,
};

const FULL_TREE: IndexerCategory[] = [
  {
    id: 2000,
    name: 'Movies',
    subcats: [
      { id: 2040, name: 'Movies/HD', subcats: [] },
      { id: 2045, name: 'Movies/UHD', subcats: [] },
    ],
  },
  { id: 5000, name: 'TV', subcats: [{ id: 5040, name: 'TV/HD', subcats: [] }] },
];

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

let host: HTMLElement;
let app: Record<string, unknown>;
/** Every answer the categories endpoint will give, consumed in order. */
let categoriesAnswers: Array<() => Response>;
let categoriesCalls: number;
let indexerWrites: Array<{ method: string; body: unknown }>;
let indexerList: Indexer[];

beforeEach(() => {
  categoriesAnswers = [];
  categoriesCalls = 0;
  indexerWrites = [];
  indexerList = [GEEK];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      // The add form probes with its typed-in values; the edit form probes
      // the stored indexer by id. Both share one answer queue.
      if (url.endsWith('/indexers/categories') || /\/indexers\/\d+\/categories$/.test(url)) {
        categoriesCalls += 1;
        const answer = categoriesAnswers.shift();
        if (!answer) throw new Error('unexpected categories fetch');
        return answer();
      }
      if (url.endsWith('/indexers/1') && init?.method === 'PUT') {
        indexerWrites.push({
          method: init.method,
          body: typeof init.body === 'string' ? JSON.parse(init.body) : null,
        });
        return jsonResponse(GEEK);
      }
      if (url.endsWith('/indexers')) return jsonResponse({ indexers: indexerList });
      if (url.includes('/indexers/catalog')) return jsonResponse({ definitions: [] });
      throw new Error(`unexpected fetch: ${url}`);
    }),
  );
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  unmount(app);
  host.remove();
  vi.unstubAllGlobals();
});

async function settle() {
  for (let i = 0; i < 3; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  flushSync();
}

function clickButton(label: string) {
  const button = [...host.querySelectorAll('button')].find((b) =>
    (b.textContent ?? '').includes(label),
  );
  expect(button, `a button labelled "${label}"`).toBeDefined();
  button!.click();
  flushSync();
}

function editor(): HTMLElement {
  const el = host.querySelector<HTMLElement>('[role="dialog"]');
  expect(el, 'the indexer editor').not.toBeNull();
  return el!;
}

function type(id: string, value: string) {
  const el = host.querySelector<HTMLInputElement>(`#${id}`);
  expect(el, `an input #${id}`).not.toBeNull();
  el!.value = value;
  el!.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

describe('IndexerSettings category picker', () => {
  it('names compact row controls and exposes the full truncated values', async () => {
    app = mount(IndexerSettings, { target: host });
    await settle();

    expect(host.querySelector('span[title="NZBGeek"]')?.textContent).toBe('NZBGeek');
    expect(host.textContent).toContain('Enabled');
    expect(host.querySelector('span.bg-success')?.getAttribute('aria-hidden')).toBe('true');
    expect(host.querySelector('p[title="https://api.nzbgeek.info"]')?.textContent?.trim()).toBe(
      'https://api.nzbgeek.info',
    );

    const remove = [...host.querySelectorAll<HTMLButtonElement>('button')].find((candidate) =>
      candidate.textContent?.includes('Remove NZBGeek'),
    );
    expect(remove).toBeDefined();
    expect(remove?.textContent?.trim()).toBe('Remove NZBGeek');
    expect(remove?.title).toBe('Remove NZBGeek');
    expect(remove?.parentElement?.classList.contains('w-full')).toBe(true);
  });

  it('loads the caps tree when the edit form opens', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: FULL_TREE })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();

    expect(categoriesCalls).toBe(1);
    // The tree rendered: subcategory labels exist nowhere but in the caps
    // response, so their presence proves the picker replaced the text input.
    expect(host.textContent).toContain('Movies/HD');
    expect(host.textContent).toContain('TV/HD');
    expect(host.querySelector('#indexer-categories')).toBeNull();
    // The stored selection (2000) is partial: Movies renders indeterminate.
    const movies = [...host.querySelectorAll('[role="checkbox"]')].find((b) =>
      (b.textContent ?? '').includes('Movies'),
    );
    expect(movies?.getAttribute('aria-checked')).toBe('mixed');
  });

  it('recovers via Reload after a failed first load', async () => {
    categoriesAnswers = [
      () => new Response(JSON.stringify({ error: 'rate limited' }), { status: 429 }),
      () => jsonResponse({ categories: FULL_TREE }),
    ];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();

    // Failed load: the text input stays, and the failure is said out loud.
    expect(host.querySelector('#indexer-categories')).not.toBeNull();
    expect(host.textContent).toContain('rate limited');

    clickButton('Load from indexer');
    await settle();

    expect(categoriesCalls).toBe(2);
    expect(host.textContent).toContain('Movies/HD');
    expect(host.querySelector('#indexer-categories')).toBeNull();
  });

  it('shows a fresh tree after Reload replaces a loaded one', async () => {
    categoriesAnswers = [
      () => jsonResponse({ categories: FULL_TREE.slice(0, 1) }),
      () => jsonResponse({ categories: FULL_TREE }),
    ];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    expect(host.textContent).toContain('Movies/HD');
    expect(host.textContent).not.toContain('TV/HD');

    clickButton('Reload from indexer');
    await settle();
    expect(host.textContent).toContain('TV/HD');
  });

  it('edits a definition indexer: no type switch, blank stored settings, sends only typed values', async () => {
    indexerList = [
      {
        id: 1,
        name: 'TorrentDownload',
        url: 'https://www.torrentdownload.info',
        has_api_key: false,
        type: 'torznab',
        categories: [],
        priority: 25,
        enabled: false,
        definition_id: 'managed:torrentdownload',
        has_settings: ['sort', 'passkey'],
      },
    ];
    categoriesAnswers = [() => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();

    // A definition indexer's protocol is fixed; the radio must not render.
    expect(editor().textContent).not.toContain('Newznab');
    // Stored settings render as blank write-only inputs.
    expect(host.querySelector('#indexer-setting-sort')).not.toBeNull();
    expect(host.querySelector('#indexer-setting-passkey')).not.toBeNull();

    type('indexer-setting-passkey', 'new-passkey');
    clickButton('Save');
    await settle();

    expect(indexerWrites.length).toBe(1);
    const body = indexerWrites[0]!.body as { settings?: Record<string, string> };
    // Only the retyped value is sent; the server keeps the rest.
    expect(body.settings).toEqual({ passkey: 'new-passkey' });
  });

  it('starts edits with a blank write-only key and stored hint', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();

    expect((host.querySelector('#indexer-key') as HTMLInputElement).value).toBe('');
    expect(host.textContent).toContain('A key is stored. Leave this blank to keep it.');
    expect(host.querySelector('#indexer-categories')?.closest('[data-settings-advanced]')).not.toBeNull();
    expect((host.querySelector('#indexer-priority') as HTMLInputElement).value).toBe('25');
    expect(host.textContent).toContain('Priority 25');
  });

  it('disables unchanged Save and writes only changed drafts', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] }), () => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    const unchangedSave = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'No changes',
    );
    expect(unchangedSave).toBeDefined();
    expect(unchangedSave!.disabled).toBe(true);
    expect(indexerWrites).toHaveLength(0);

    type('indexer-name', 'NZBGeek renamed');
    clickButton('Save');
    await settle();
    expect(indexerWrites[0]?.body).toMatchObject({ name: 'NZBGeek renamed' });
    expect(indexerWrites[0]?.body).not.toHaveProperty('api_key');

    clickButton('Edit');
    await settle();
    clickButton('Clear API key');
    clickButton('Save');
    await settle();
    expect(indexerWrites[1]?.body).toHaveProperty('api_key', '');
  });

  it('validates and saves search priority', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    type('indexer-priority', '-1');
    expect(host.textContent).toContain('Priority must be a whole number of zero or more.');
    const invalidSave = [...editor().querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'Fix errors',
    );
    expect(invalidSave?.disabled).toBe(true);
    expect(indexerWrites).toHaveLength(0);

    type('indexer-priority', '7');
    clickButton('Save');
    await settle();
    expect(indexerWrites[0]?.body).toMatchObject({ priority: 7 });
  });

  it('keeps a dirty draft open until Modal discards it', async () => {
    categoriesAnswers = [() => jsonResponse({ categories: [] })];
    app = mount(IndexerSettings, { target: host });
    await settle();

    clickButton('Edit');
    await settle();
    type('indexer-name', 'Unsaved');

    const dialog = editor();
    dialog.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle();
    expect(host.textContent).toContain('Discard changes');
    expect(host.querySelector('[role="dialog"]')).toBe(dialog);
    clickButton('Keep editing');

    const backdrop = host.querySelector<HTMLElement>('[data-modal-backdrop]');
    expect(backdrop).not.toBeNull();
    backdrop!.click();
    await settle();
    expect(host.textContent).toContain('Discard changes');
    clickButton('Keep editing');

    const close = dialog.querySelector<HTMLButtonElement>('button[aria-label="Close"]');
    expect(close).not.toBeNull();
    close!.click();
    await settle();
    expect(host.textContent).toContain('Discard changes');
    clickButton('Discard changes');
    await settle();
    expect(host.querySelector('[role="dialog"]')).toBeNull();
  });
});

describe('IndexerSettings add wizard', () => {
  const GEEK_DEF = {
    id: 'nzbgeek',
    name: 'NZBgeek',
    kind: 'usenet',
    protocol: 'newznab',
    privacy: 'private',
    language: 'en-US',
    description: 'Private Usenet indexer.',
    info_url: 'https://nzbgeek.info',
    url: 'https://api.nzbgeek.info',
    urls: ['https://api.nzbgeek.info'],
    url_placeholder: '',
    requires_api_key: true,
    categories: [2000, 5000],
    content: ['movies', 'tv'],
  };
  const PUBLIC_TORRENT = {
    id: '1337x',
    name: '1337x',
    kind: 'torrent',
    protocol: 'torznab',
    privacy: 'public',
    language: 'en-US',
    description: 'Public torrent site that offers verified torrent downloads',
    info_url: 'https://1337x.to/',
    url: 'https://1337x.to',
    urls: ['https://1337x.to', 'https://1337x.st'],
    url_placeholder: 'http://127.0.0.1:9117/api/v2.0/indexers/1337x/results/torznab',
    requires_api_key: false,
    categories: [],
    content: ['movies', 'tv', 'anime'],
  };
  const PUBLIC_TORRENT_INVENTORY = {
    id: '1337x',
    name: '1337x',
    privacy: 'public',
    language: 'en-US',
    description: 'Public torrent site that offers verified torrent downloads',
    info_url: 'https://1337x.to/',
    metadata_urls: ['https://1337x.to', 'https://1337x.st'],
    requires_api_key: false,
    content: ['movies', 'tv', 'anime'],
    state: 'metadata-only',
    addable: false,
    definitions: [],
  };
  const PRIVATE_FR = {
    id: 'yggtorrent',
    name: 'Yggtorrent',
    kind: 'torrent',
    protocol: 'torznab',
    privacy: 'private',
    language: 'fr-FR',
    description: 'French private tracker',
    info_url: '',
    url: 'https://www.yggtorrent.top',
    urls: ['https://www.yggtorrent.top'],
    url_placeholder: '',
    requires_api_key: false,
    categories: [],
    content: ['movies', 'tv'],
  };
  const JACKETT_DEF = {
    id: 'jackett',
    name: 'Jackett',
    kind: 'generic',
    protocol: 'torznab',
    privacy: 'private',
    language: 'en-US',
    description: 'Any Jackett indexer via its Torznab feed.',
    info_url: '',
    url: '',
    urls: [],
    url_placeholder: 'http://127.0.0.1:9117/api/v2.0/indexers/INDEXER/results/torznab',
    requires_api_key: true,
    categories: [],
    content: [],
  };
  const TPB_DEF = {
    id: 'thepiratebay',
    definition_id: 'thepiratebay',
    name: 'The Pirate Bay',
    kind: 'torrent',
    protocol: 'torznab',
    privacy: 'public',
    language: 'en-US',
    description: 'Local JSON adapter.',
    info_url: 'https://thepiratebay.org',
    url: 'https://thepiratebay.org',
    urls: ['https://thepiratebay.org'],
    url_placeholder: '',
    requires_api_key: false,
    categories: [],
    content: ['movies', 'tv'],
    settings: [
      { name: 'apiurl', label: 'API URL', type: 'text', default: 'https://apibay.org', secret: false },
    ],
  };

  function stubCatalog(definitions: unknown[], inventory: unknown[] = []) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes('/indexers/catalog')) return jsonResponse({ definitions, inventory });
        if (url.endsWith('/indexers') && init?.method === 'POST') {
          indexerWrites.push({
            method: init.method,
            body: typeof init.body === 'string' ? JSON.parse(init.body) : null,
          });
          return jsonResponse(GEEK);
        }
        if (url.endsWith('/indexers')) return jsonResponse({ indexers: [GEEK] });
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );
  }

  it('starts Add on a kind picker, not the details form', async () => {
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();

    expect(host.textContent).toContain('What kind of indexer?');
    expect(host.textContent).toContain('Torrent site');
    expect(host.textContent).toContain('Usenet indexer');
    expect(host.textContent).toContain('Generic source');
    expect(host.querySelector('#indexer-name')).toBeNull();
  });

  it('opens the Usenet catalog and prefills NZBgeek', async () => {
    stubCatalog([GEEK_DEF]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Usenet indexer');
    await settle();

    expect(host.textContent).toContain('NZBgeek');
    expect(host.textContent).toContain('Private');
    clickButton('NZBgeek');
    await settle();

    expect((host.querySelector('#indexer-name') as HTMLInputElement).value).toBe('NZBgeek');
    expect((host.querySelector('#indexer-url') as HTMLInputElement).value).toBe(
      'https://api.nzbgeek.info/api',
    );
    expect(host.textContent).toContain('Base URL: api.nzbgeek.info');
    expect(host.querySelector('[role="radiogroup"]')).toBeNull();
    expect(host.textContent).toContain('This indexer requires an API key');
    const save = [...editor().querySelectorAll('button')].find((button) =>
      (button.textContent ?? '').includes('Fix errors') || (button.textContent ?? '').includes('Save'),
    );
    expect(save?.disabled).toBe(true);
    type('indexer-key', 'secret');
    await settle();
    const ready = [...editor().querySelectorAll('button')].find((button) =>
      button.textContent?.trim().includes('Save'),
    );
    expect(ready?.disabled).toBe(false);
  });

  it('uses a Jackett placeholder for a generic source without a URL', async () => {
    stubCatalog([JACKETT_DEF]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Generic source');
    await settle();
    clickButton('Jackett');
    await settle();

    const url = host.querySelector('#indexer-url') as HTMLInputElement;
    expect(url.value).toBe('http://127.0.0.1:9117/api/v2.0/indexers/INDEXER/results/torznab');
    expect(host.textContent).toContain('This indexer requires an API key');
  });

  it('submits local definition identity and settings without inventing /api', async () => {
    stubCatalog([TPB_DEF]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Torrent site');
    await settle();
    clickButton('The Pirate Bay');
    await settle();

    expect((host.querySelector('#indexer-url') as HTMLInputElement).value).toBe('https://thepiratebay.org');
    expect((host.querySelector('#indexer-setting-apiurl') as HTMLInputElement).value).toBe('https://apibay.org');
    expect(host.textContent).toContain('Local adapter');
    expect(host.querySelector('#indexer-key')).toBeNull();

    clickButton('Save');
    await settle();
    expect(indexerWrites).toHaveLength(1);
    expect(indexerWrites.at(0)?.body).toMatchObject({
      definition_id: 'thepiratebay',
      url: 'https://thepiratebay.org',
      settings: { apiurl: 'https://apibay.org' },
    });
  });

  it('hides unsupported torrent sites until Show Unsupported is enabled', async () => {
    stubCatalog([], [PUBLIC_TORRENT_INVENTORY]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Torrent site');
    await settle();

    const toggle = [...host.querySelectorAll<HTMLButtonElement>('[role="switch"]')].find((button) =>
      button.textContent?.includes('Show Unsupported'),
    );
    expect(toggle, 'the Show Unsupported toggle').toBeDefined();
    expect(toggle?.getAttribute('aria-checked')).toBe('false');
    expect(host.textContent).not.toContain('1337x');
    expect(host.textContent).not.toContain('Metadata only');

    toggle!.click();
    await settle();
    expect(toggle?.getAttribute('aria-checked')).toBe('true');
    expect(host.textContent).toContain('1337x');
    expect(host.textContent).toContain('Metadata only');
    expect(host.textContent).not.toContain('Use this variant');
    expect(host.querySelector('#indexer-url')).toBeNull();
    expect(indexerWrites).toHaveLength(0);

    toggle!.click();
    await settle();
    expect(host.textContent).not.toContain('1337x');
  });

  it('selects a managed definition directly without exposing implementation pins', async () => {
    stubCatalog([], [
      {
        ...PUBLIC_TORRENT_INVENTORY,
        state: 'verified',
        addable: true,
        definitions: [
          {
            definition_id: 'managed:1337x',
            state: 'verified',
            source: 'managed',
            revision: 'current',
            digest: 'sha256:managed-definition-aaaaaaaa',
            base_urls: ['https://definition-authority.example'],
            unsupported: [],
            addable: true,
            settings: [{ name: 'cookie', label: 'Cookie', type: 'text', default: '', secret: true, editable: true }],
          },
        ],
      },
    ]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Torrent site');
    await settle();

    expect(host.textContent).not.toContain('managed');
    expect(host.textContent).not.toContain('sha256:');
    expect(host.textContent).not.toContain('Use this variant');
    expect(host.textContent).not.toContain('Definition pack');
    expect(host.textContent).not.toContain('Install pack');
    clickButton('1337x');
    await settle();

    expect((host.querySelector('#indexer-url') as HTMLInputElement).value).toBe('https://definition-authority.example');
    expect(host.textContent).not.toContain('Pack managed');
    expect((host.querySelector('#indexer-setting-cookie') as HTMLInputElement)).not.toBeNull();
    type('indexer-setting-cookie', 'session=owner');
    clickButton('Save');
    await settle();
    expect(indexerWrites).toHaveLength(1);
    expect(indexerWrites.at(0)?.body).toEqual({
      definition_id: 'managed:1337x',
      settings: { cookie: 'session=owner' },
      name: '1337x',
      type: 'torznab',
      url: 'https://definition-authority.example',
      categories: [],
      priority: 25,
      enabled: true,
    });
    expect(indexerWrites.at(0)?.body).not.toHaveProperty('definition_source');
    expect(indexerWrites.at(0)?.body).not.toHaveProperty('definition_revision');
    expect(indexerWrites.at(0)?.body).not.toHaveProperty('definition_digest');
  });

  it('renders Cardigann authentication and preference fields inside Add Indexer', async () => {
    stubCatalog([], [
      {
        ...PUBLIC_TORRENT_INVENTORY,
        state: 'verified',
        definitions: [
          {
            definition_id: 'managed:1337x',
            state: 'verified',
            source: 'managed',
            revision: 'current',
            digest: 'sha256:managed-definition',
            base_urls: ['https://1337x.to'],
            unsupported: [],
            addable: true,
            settings: [
              { name: 'username', label: 'Username', type: 'text', default: '', secret: false, editable: true },
              { name: 'password', label: 'Password', type: 'password', default: '', secret: true, editable: true },
              { name: 'freeleech', label: 'Freeleech only', type: 'checkbox', default: 'false', secret: false, editable: true },
              {
                name: 'sort', label: 'Sort requested from site', type: 'select', default: 'added', secret: false, editable: true,
                options: [{ value: 'added', label: 'Created' }, { value: 'seeders', label: 'Seeders' }],
              },
              { name: 'info_tpp', label: 'Results per page', type: 'info', default: 'Use 100 results per page.', secret: false, editable: false },
            ],
          },
        ],
      },
    ]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Torrent site');
    await settle();
    clickButton('1337x');
    await settle();

    expect((host.querySelector('#indexer-setting-password') as HTMLInputElement).type).toBe('password');
    expect(host.querySelector('[role="switch"]')?.textContent).toContain('Freeleech only');
    expect(host.textContent).toContain('Sort requested from site: Created');
    expect(host.textContent).toContain('Results per page');
    expect(host.textContent).toContain('Use 100 results per page.');

    type('indexer-setting-username', 'owner');
    type('indexer-setting-password', 'secret');
    clickButton('Freeleech only');
    clickButton('Sort requested from site: Created');
    clickButton('Seeders');
    clickButton('Save');
    await settle();

    expect(indexerWrites.at(0)?.body).toMatchObject({
      definition_id: 'managed:1337x',
      settings: { username: 'owner', password: 'secret', freeleech: 'true', sort: 'seeders' },
    });
    expect((indexerWrites.at(0)?.body as { settings?: Record<string, string> }).settings).not.toHaveProperty('info_tpp');
  });

  it('keeps quarantined, pending, and inactive variants inspectable when unsupported rows are shown', async () => {
    stubCatalog([], [
      {
        ...PUBLIC_TORRENT_INVENTORY,
        id: 'packsite',
        name: 'Packsite',
        state: 'quarantined',
        addable: false,
        definitions: [
          {
            definition_id: 'community:packsite',
            state: 'quarantined',
            source: 'community',
            revision: '2026.08.01',
            digest: 'sha256:quarantined-digest',
            blocked_code: 'compiler.invalid',
            unsupported: ['login'],
            addable: false,
          },
          {
            definition_id: 'community:packsite',
            state: 'runnable-unverified',
            source: 'community',
            revision: '2026.08.10',
            digest: 'sha256:pending-digest',
            blocked_code: 'pack.restart_required',
            unsupported: [],
            addable: false,
          },
          {
            definition_id: 'community:packsite',
            state: 'verified',
            source: 'community',
            revision: '2026.08.14',
            digest: 'sha256:inactive-digest',
            unsupported: [],
            addable: false,
          },
        ],
      },
    ]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Torrent site');
    await settle();
    clickButton('Show Unsupported');
    await settle();

    expect(host.textContent).toContain('Quarantined');
    expect(host.textContent).toContain('Blocked: compiler.invalid');
    expect(host.textContent).toContain('Blocked: pack.restart_required');
    expect(host.textContent).toContain('1 unsupported');
    expect(host.textContent).not.toContain('Use this variant');
    expect(host.textContent).not.toContain('community');
    expect(host.querySelector('#indexer-url')).toBeNull();
  });

  it('deduplicates a managed definition when a builtin catalog row shares the metadata id', async () => {
    stubCatalog([PUBLIC_TORRENT], [
      {
        ...PUBLIC_TORRENT_INVENTORY,
        definitions: [
          {
            definition_id: 'community:1337x',
            state: 'verified',
            source: 'community',
            revision: '2026.08.14',
            digest: 'sha256:verified-digest-aaaaaaaa',
            unsupported: [],
            addable: true,
          },
        ],
      },
    ]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Torrent site');
    await settle();

    expect(host.textContent).toContain('1337x');
    expect(host.textContent).not.toContain('Use this variant');
    expect(host.textContent).not.toContain('community');
  });

  it('preserves exact pins when editing an existing pack-backed indexer', async () => {
    const exact: Indexer = {
      ...GEEK,
      name: '1337x',
      url: 'https://1337x.to',
      type: 'torznab',
      has_api_key: false,
      definition_id: 'community:1337x',
      definition_source: 'community',
      definition_revision: '2026.08.14',
      definition_digest: 'sha256:verified-digest-aaaaaaaa',
    };
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.endsWith('/indexers/categories')) return jsonResponse({ categories: [] });
        if (url.endsWith('/indexers/1') && init?.method === 'PUT') {
          indexerWrites.push({
            method: init.method,
            body: typeof init.body === 'string' ? JSON.parse(init.body) : null,
          });
          return jsonResponse(exact);
        }
        if (url.endsWith('/indexers')) return jsonResponse({ indexers: [exact] });
        throw new Error(`unexpected fetch: ${url}`);
      }),
    );
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Edit');
    await settle();
    type('indexer-name', '1337x local');
    clickButton('Save');
    await settle();
    expect(indexerWrites.at(0)?.body).toMatchObject({
      definition_id: 'community:1337x',
      definition_source: 'community',
      definition_revision: '2026.08.14',
      definition_digest: 'sha256:verified-digest-aaaaaaaa',
      name: '1337x local',
    });
  });

  it('clears exact pins when a static catalog definition is chosen', async () => {
    stubCatalog([TPB_DEF], [
      {
        ...PUBLIC_TORRENT_INVENTORY,
        definitions: [
          {
            definition_id: 'community:1337x',
            state: 'verified',
            source: 'community',
            revision: '2026.08.14',
            digest: 'sha256:verified-digest-aaaaaaaa',
            base_urls: ['https://1337x.to'],
            unsupported: [],
            addable: true,
          },
        ],
      },
    ]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Torrent site');
    await settle();
    clickButton('1337x');
    await settle();
    expect((host.querySelector('#indexer-url') as HTMLInputElement).value).toBe('https://1337x.to');
    clickButton('Back');
    await settle();
    clickButton('The Pirate Bay');
    await settle();
    clickButton('Save');
    await settle();
    expect(indexerWrites.at(0)?.body).toMatchObject({
      definition_id: 'thepiratebay',
      url: 'https://thepiratebay.org',
      settings: { apiurl: 'https://apibay.org' },
    });
    expect(indexerWrites.at(0)?.body).not.toHaveProperty('definition_source');
    expect(indexerWrites.at(0)?.body).not.toHaveProperty('definition_revision');
    expect(indexerWrites.at(0)?.body).not.toHaveProperty('definition_digest');
  });

  it('filters torrent sites by privacy and content', async () => {
    stubCatalog([PUBLIC_TORRENT, PRIVATE_FR]);
    app = mount(IndexerSettings, { target: host });
    await settle();
    clickButton('Add indexer');
    await settle();
    clickButton('Torrent site');
    await settle();

    expect(host.textContent).toContain('1337x');
    expect(host.textContent).toContain('Yggtorrent');
    expect(host.textContent).toContain('Privacy');
    expect(host.textContent).toContain('Language');
    expect(host.textContent).toContain('Content');

    clickButton('Privacy');
    await settle();
    clickButton('Public');
    await settle();
    expect(host.textContent).toContain('1337x');
    expect(host.textContent).not.toContain('Yggtorrent');

    clickButton('Clear all');
    await settle();
    clickButton('Content');
    await settle();
    clickButton('Anime');
    await settle();
    expect(host.textContent).toContain('1337x');
    expect(host.textContent).not.toContain('Yggtorrent');
  });
});
