import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import LibrariesSettings from './LibrariesSettings.svelte';
import type { Library, LibraryIndexer } from '../api/types';

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

/** An indexer row as a migrated install reports it: no override anywhere. */
function indexerRow(over: Partial<LibraryIndexer> = {}): LibraryIndexer {
  return {
    indexer_id: 1,
    name: 'Prowlarr',
    type: 'torznab',
    indexer_enabled: true,
    enabled: true,
    categories: [2000, 5000],
    categories_overridden: false,
    default_categories: [2000, 5000],
    ...over,
  };
}

function library(over: Partial<Library> = {}): Library {
  return {
    id: 1,
    kind: 'movie',
    name: 'Movies',
    root_path: 'library/Movies',
    dlna_visible: true,
    route_torrent: '',
    route_usenet: '',
    quality_profile_id: 0,
    indexers: [indexerRow()],
    ...over,
  };
}

const MOVIES = library();
const SERIES = library({ id: 2, kind: 'tv', name: 'Series', root_path: 'library/TV' });

let host: HTMLElement;
let app: Record<string, unknown>;
/** Every write the screen made, in order: [method, url, parsed body]. */
let writes: { method: string; url: string; body: unknown }[];
/** What GET /libraries answers, so a test can start from any stored state. */
let libraries: Library[];
/** What each write answers with, shifted in order; falls back to no change. */
let writeReplies: Library[];
let writeStatus: number;
let holdWrites: boolean;
let releaseWrite: (() => void) | null;
let scanPosts: number;
let scanStatusReads: number;
let scanCounts: { media_files: number; unmatched: number };

const SETTINGS = { route_torrent: '3', route_usenet: '' };
const CLIENTS = [
  { id: 3, type: 'qbittorrent', name: 'Seedbox', url: '', username: '', has_password: false, has_api_key: false, category: '', priority: 25, enabled: true },
  { id: 4, type: 'sabnzbd', name: 'SAB', url: '', username: '', has_password: false, has_api_key: true, category: '', priority: 25, enabled: true },
];
const TYPES = [
  { type: 'qbittorrent', label: 'qBittorrent', protocol: 'torrent', uses_login: true, uses_api_key: false, supported: true },
  { type: 'sabnzbd', label: 'SABnzbd', protocol: 'usenet', uses_login: false, uses_api_key: true, supported: true },
];
const PROFILES = [
  { id: 7, name: 'HD-1080p', cutoff: '1080p', items: ['1080p'], upgrade_allowed: true, created_at: '', updated_at: '' },
];

beforeEach(() => {
  writes = [];
  libraries = [MOVIES, SERIES];
  writeReplies = [];
  writeStatus = 200;
  holdWrites = false;
  releaseWrite = null;
  scanPosts = 0;
  scanStatusReads = 0;
  scanCounts = { media_files: 12, unmatched: 2 };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.endsWith('/library/rescan') && method === 'POST') {
        scanPosts += 1;
        return jsonResponse({ status: 'scanning' }, 202);
      }
      if (method !== 'GET') {
        const body = init?.body === undefined ? null : JSON.parse(String(init.body));
        writes.push({ method, url, body });
        if (holdWrites) {
          await new Promise<void>((resolve) => {
            releaseWrite = resolve;
          });
        }
        if (writeStatus !== 200) return jsonResponse({ error: 'nope' }, writeStatus);
        const reply = writeReplies.shift();
        if (reply) libraries = libraries.map((l) => (l.id === reply.id ? reply : l));
        return jsonResponse(reply ?? libraries.find((l) => url.includes(`/libraries/${l.id}`)));
      }
      if (url.endsWith('/libraries')) return jsonResponse({ libraries });
      if (url.endsWith('/settings')) return jsonResponse(SETTINGS);
      if (url.endsWith('/quality-profiles')) return jsonResponse({ profiles: PROFILES });
      if (url.endsWith('/download-clients/types')) return jsonResponse({ types: TYPES });
      if (url.endsWith('/download-clients')) return jsonResponse({ download_clients: CLIENTS });
      if (url.endsWith('/system/status')) {
        scanStatusReads += 1;
        return jsonResponse({
          scanning: false,
          counts: {
            movies: 0,
            series: 0,
            media_files: scanCounts.media_files,
            unmatched: scanCounts.unmatched,
          },
        });
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

async function mountLoaded() {
  app = mount(LibrariesSettings, { target: host, props: {} });
  await settle();
}

function select(id: string): HTMLSelectElement {
  const found = host.querySelector(`#${id}`) as HTMLSelectElement | null;
  expect(found, `select #${id}`).not.toBeNull();
  return found!;
}

function pick(el: HTMLSelectElement, value: string) {
  el.value = value;
  el.dispatchEvent(new Event('change', { bubbles: true }));
  flushSync();
}

function autosaveStatus(key: string): string | null {
  return host.querySelector(`[data-autosave-status="${key}"]`)?.textContent?.trim() ?? null;
}

function finishWrite() {
  expect(releaseWrite, 'held write release').not.toBeNull();
  const release = releaseWrite!;
  releaseWrite = null;
  holdWrites = false;
  release();
}

function button(label: string): HTMLButtonElement {
  const found = [...host.querySelectorAll('button')].find((candidate) =>
    candidate.textContent?.includes(label),
  );
  expect(found, `button labelled ${label}`).toBeDefined();
  return found as HTMLButtonElement;
}

/** The label-row note beside a route field: OVERRIDE or GLOBAL DEFAULT. */
function noteFor(id: string): string {
  const label = host.querySelector(`label[for="${id}"]`);
  expect(label, `label for #${id}`).not.toBeNull();
  const note = label!.nextElementSibling as HTMLElement | null;
  expect(note, `override note for #${id}`).not.toBeNull();
  return note!.textContent!.trim();
}

/** The i-th write, asserted to exist so a missing one fails loudly. */
function write(i: number): { method: string; url: string; body: unknown } {
  const found = writes[i];
  expect(found, `write #${i}`).toBeDefined();
  return found!;
}

function rootPath(): string {
  return (host.querySelector('#library-root') as HTMLInputElement).value;
}

describe('LibrariesSettings — override vs global default', () => {
  // The whole point of the screen: a value inherited from the global settings
  // must not read the same as one this library chose.
  it('names an unanswered route as the global default and shows what it resolves to', async () => {
    await mountLoaded();

    expect(noteFor('library-route-torrent')).toBe('Global default');
    expect(noteFor('library-route-usenet')).toBe('Global default');
    // The global torrent route is client 3, so the option says so rather than
    // leaving the user to go and look it up.
    expect(select('library-route-torrent').options[0]?.textContent).toContain('Seedbox');
    expect(select('library-route-usenet').options[0]?.textContent).toContain('Built-in engine');
    // classList, not className: the shared select style carries a
    // focus:border-accent that a substring check would read as an override.
    expect(select('library-route-torrent').classList.contains('border-border-strong')).toBe(true);
    expect(select('library-route-torrent').classList.contains('border-accent')).toBe(false);
  });

  it('flips to an override, rust border and all, once the library answers', async () => {
    writeReplies = [library({ route_torrent: 'embedded' })];
    await mountLoaded();

    pick(select('library-route-torrent'), 'embedded');
    await settle();

    expect(writes).toEqual([
      { method: 'PATCH', url: '/api/v1/libraries/1', body: { route_torrent: 'embedded' } },
    ]);
    expect(noteFor('library-route-torrent')).toBe('Override');
    expect(select('library-route-torrent').classList.contains('border-accent')).toBe(true);
  });

  it('clears a Saved acknowledgment when the next autosave begins', async () => {
    writeReplies = [
      library({ route_torrent: 'embedded' }),
      library({ route_torrent: 'embedded', quality_profile_id: 7 }),
    ];
    await mountLoaded();

    holdWrites = true;
    pick(select('library-route-torrent'), 'embedded');
    expect(autosaveStatus('1:torrent-route')).toBe('Saving…');

    finishWrite();
    await settle();
    expect(autosaveStatus('1:torrent-route')).toBe('Saved');

    holdWrites = true;
    pick(select('library-profile'), '7');
    expect(autosaveStatus('1:torrent-route')).toBeNull();
    expect(autosaveStatus('1:profile')).toBe('Saving…');

    finishWrite();
    await settle();
    expect(autosaveStatus('1:profile')).toBe('Saved');
  });

  it('reads a stored override as an override on load', async () => {
    libraries = [library({ route_usenet: '4' }), SERIES];
    await mountLoaded();

    expect(noteFor('library-route-usenet')).toBe('Override');
    expect(noteFor('library-route-torrent')).toBe('Global default');
    expect(select('library-route-usenet').value).toBe('4');
  });

  // A rejected write that leaves the new value sitting in the select is a lie
  // about what the server holds.
  it('puts a rejected override back to what the server still holds', async () => {
    writeStatus = 400;
    await mountLoaded();

    pick(select('library-route-torrent'), '3');
    await settle();

    expect(select('library-route-torrent').value).toBe('');
    expect(noteFor('library-route-torrent')).toBe('Global default');
    expect(autosaveStatus('1:torrent-route')).toBe('Error');
  });

  it('clearing an override sends the empty value rather than omitting it', async () => {
    libraries = [library({ quality_profile_id: 7 }), SERIES];
    writeReplies = [library({ quality_profile_id: 0 })];
    await mountLoaded();

    expect(select('library-profile').value).toBe('7');
    pick(select('library-profile'), '');
    await settle();

    expect(writes).toEqual([
      { method: 'PATCH', url: '/api/v1/libraries/1', body: { quality_profile_id: 0 } },
    ]);
  });
});

describe('LibrariesSettings — indexer rows', () => {
  it('says a row is not searched here, without blaming the indexer', async () => {
    libraries = [library({ indexers: [indexerRow({ enabled: false })] }), SERIES];
    await mountLoaded();

    expect(host.textContent).toContain('Not searched for this library');
    expect(host.textContent).not.toContain('Disabled in Settings');
    expect(host.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('false');
    // The dot reports the indexer's own health, which is still green — the
    // library is what switched it off, not the indexer.
    expect(host.querySelector('li span.bg-success')).not.toBeNull();
    expect(host.textContent).toContain('Indexer enabled');
    expect(host.querySelector('span[title="Prowlarr"]')?.textContent).toBe('Prowlarr');
    const searchToggle = host.querySelector<HTMLButtonElement>('[role="switch"]');
    expect(searchToggle?.getAttribute('aria-label')).toBe('Search Prowlarr for Movies');
    expect(searchToggle?.getAttribute('title')).toBe('Search Prowlarr for Movies');
    expect(searchToggle?.parentElement?.classList.contains('w-full')).toBe(true);
  });

  it('points to Indexers when an indexer is off everywhere', async () => {
    libraries = [
      library({ indexers: [indexerRow({ indexer_enabled: false, enabled: true })] }),
      SERIES,
    ];
    await mountLoaded();

    expect(host.textContent).toContain('This indexer is disabled globally');
    expect(host.textContent).not.toContain('Not searched for this library');
    expect(host.querySelector('a[href="/settings/indexers"]')?.textContent?.trim()).toBe(
      'Open Indexers',
    );
    expect(host.querySelector('li span.bg-ink-muted')).not.toBeNull();
    expect(host.textContent).toContain('Indexer disabled');
    // Still on for this library: the row must not offer to "re-enable" it.
    expect(host.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  });

  it('renders the resolved categories as chips and marks an overridden set', async () => {
    libraries = [
      library({
        indexers: [indexerRow({ categories: [2000], categories_overridden: true })],
      }),
      SERIES,
    ];
    await mountLoaded();

    const chips = [...host.querySelectorAll('li span.font-mono')].map((c) => c.textContent?.trim());
    expect(chips).toContain('2000');
    expect(chips).not.toContain('5000');
    expect(host.querySelector('li')?.className).toContain('border-accent');
  });

  // The server rewrites the whole row, so a toggle that forgets the category
  // override silently widens the search back to the indexer's own list.
  it('carries the category override through a disable toggle', async () => {
    libraries = [
      library({
        indexers: [indexerRow({ categories: [2000], categories_overridden: true })],
      }),
      SERIES,
    ];
    await mountLoaded();

    (host.querySelector('[role="switch"]') as HTMLButtonElement).click();
    await settle();

    expect(writes).toEqual([
      {
        method: 'PUT',
        url: '/api/v1/libraries/1/indexers/1',
        body: { enabled: false, categories: [2000] },
      },
    ]);
    expect(autosaveStatus('1:indexer-1')).toBe('Saved');
  });

  it('sends a null category list when a row has no override to carry', async () => {
    await mountLoaded();

    (host.querySelector('[role="switch"]') as HTMLButtonElement).click();
    await settle();

    expect(write(0).body).toEqual({ enabled: false, categories: null });
  });

  it('edits categories to an explicit list and reverts to the indexer default', async () => {
    libraries = [
      library({
        indexers: [indexerRow({ categories: [2000], categories_overridden: true })],
      }),
      SERIES,
    ];
    await mountLoaded();

    button('Edit').click();
    flushSync();
    expect(
      host.querySelector('[role="dialog"] footer > div')?.classList.contains('flex-wrap'),
    ).toBe(true);
    const input = host.querySelector('#library-indexer-categories') as HTMLInputElement;
    input.value = '2010, 2020';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    button('Save').click();
    await settle();

    expect(write(0).body).toEqual({ enabled: true, categories: [2010, 2020] });

    button('Edit').click();
    flushSync();
    // null, not [] — [] is the override "search unfiltered", which is a
    // different answer from "use the indexer's own list".
    button("Use the indexer's").click();
    await settle();

    expect(write(1).body).toEqual({ enabled: true, categories: null });
  });
});

describe('LibrariesSettings - actionable setup links', () => {
  it('links each related settings task from the library controls', async () => {
    libraries = [library({ indexers: [] }), SERIES];
    await mountLoaded();

    expect(host.querySelector('a[href="/settings/indexers"]')?.textContent?.trim()).toBe(
      'Manage indexers',
    );
    expect(
      host.querySelector('a[href="/settings/quality-profiles"]')?.textContent?.trim(),
    ).toBe('Manage download profiles');
    expect(host.querySelector('a[href="/settings/downloads"]')?.textContent?.trim()).toBe(
      'Configure global download routing',
    );
    expect(host.querySelector('a[href="/settings/playback"]')?.textContent?.trim()).toBe(
      'Playback',
    );
  });
});

describe('LibrariesSettings — switcher and reach', () => {
  it('switches libraries and addresses the one that is showing', async () => {
    libraries = [MOVIES, library({ id: 2, kind: 'tv', name: 'Series', root_path: 'library/TV' })];
    await mountLoaded();

    expect(rootPath()).toBe('library/Movies');
    expect((host.querySelector('#library-root') as HTMLInputElement).readOnly).toBe(true);
    expect(
      (host.querySelector('#library-root') as HTMLInputElement).closest('[title]')?.getAttribute('title'),
    ).toBe('library/Movies');

    button('Series').click();
    await settle();

    expect(rootPath()).toBe('library/TV');

    // A write from here must land on the library the screen is showing, not on
    // whichever one happened to load first.
    (host.querySelector('[role="switch"]') as HTMLButtonElement).click();
    await settle();

    expect(write(0).url).toBe('/api/v1/libraries/2/indexers/1');
  });

  it('patches dlna visibility for the library on screen', async () => {
    await mountLoaded();

    const reach = [...host.querySelectorAll('[role="switch"]')].at(-1) as HTMLButtonElement;
    expect(reach.getAttribute('aria-checked')).toBe('true');
    reach.click();
    await settle();

    expect(writes).toEqual([
      { method: 'PATCH', url: '/api/v1/libraries/1', body: { dlna_visible: false } },
    ]);
    expect(autosaveStatus('1:dlna')).toBe('Saved');
  });

  /**
   * The Adult pill's conditional visibility (PLAN phase 9 track 5).
   *
   * It is conditional for the ordinary reason every pill is: GET /libraries
   * returned a row. The server drops that row for any caller the module is not
   * visible to (internal/api.libraryVisible, and the adult row outlives a
   * disable, which is why it filters rather than trusting the table), so this
   * screen needs no adult rule of its own — and cannot leak a pill the server
   * did not send.
   */
  it('shows no Adult pill, and no trace of one, when the server sends no adult row', async () => {
    libraries = [MOVIES, SERIES];
    await mountLoaded();

    expect(host.textContent).not.toContain('Adult');
    expect([...host.querySelectorAll('button')].some((b) => b.textContent?.includes('Adult'))).toBe(
      false,
    );
  });

  it('shows the Adult pill exactly when the server sends the row', async () => {
    libraries = [
      MOVIES,
      SERIES,
      library({ id: 3, kind: 'adult', name: 'Adult', root_path: 'library/Adult' }),
    ];
    await mountLoaded();

    button('Adult').click();
    await settle();

    expect(rootPath()).toBe('library/Adult');
  });

  it('surfaces a failed load with a retry rather than an empty screen', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ error: 'boom' }, 500)));
    await mountLoaded();

    expect(host.textContent).toContain('boom');
    expect(button('Retry')).toBeDefined();
  });
});

describe('LibrariesSettings - library scan', () => {
  it('starts one scan at a time and reports the completed library counts', async () => {
    await mountLoaded();

    const rescan = button('Rescan library');
    rescan.click();
    flushSync();

    expect(rescan.disabled).toBe(true);
    expect(rescan.textContent).toContain('Scanning');
    rescan.click();
    await settle();

    expect(scanPosts).toBe(1);
    expect(scanStatusReads).toBe(1);
    expect(rescan.disabled).toBe(false);
    expect(host.textContent).toContain(
      'Scan finished: 12 files in the library, 2 unmatched.',
    );
  });
});
