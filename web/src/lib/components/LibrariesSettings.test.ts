import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import LibrariesSettings from './LibrariesSettings.svelte';
import type { Library, LibraryAccess, LibraryIndexer } from '../api/types';

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
    provider: 'tmdb',
    providers: ['tmdb'],
    is_default: true,
    item_count: 0,
    active: true,
    restricted: false,
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
/** How often the screen re-read the identity. */
let meReads: number;
let scanCounts: { media_files: number; unmatched: number };
/** What GET /libraries/providers answers; a test may empty the adult half. */
let providerList: { id: string; name: string; kinds: string[] }[];
/** What GET (and then PUT) /libraries/{id}/access answers. */
let accessBody: LibraryAccess;

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
  meReads = 0;
  scanCounts = { media_files: 12, unmatched: 2 };
  providerList = [
    { id: 'tmdb', name: 'TMDB', kinds: ['movie', 'tv'] },
    // Television only: it is what makes the chain editor's eligibility filter a
    // real rule rather than a formality.
    { id: 'anilist', name: 'AniList', kinds: ['tv'] },
    // The adult descriptors are one per CONFIGURED stash-box instance (PLAN
    // Part 2 phase 3 merges them into this list), so an id here may be
    // instance-qualified and the chain editor names it from the same list as
    // everything else.
    { id: 'stashbox', name: 'ThePornDB', kinds: ['adult'] },
    { id: 'stashbox:stashdb', name: 'StashDB', kinds: ['adult'] },
  ];
  accessBody = { restricted: false, users: [] };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? 'GET';
      if (url.endsWith('/library/rescan') && method === 'POST') {
        scanPosts += 1;
        return jsonResponse({ status: 'scanning' }, 202);
      }
      // The access pair answers with itself, never with the library row, so a
      // caller wanting the DLNA clearing that restricting causes has to apply
      // it locally — which is exactly what the screen is asserted to do.
      if (url.endsWith('/access')) {
        if (method === 'GET') return jsonResponse(accessBody);
        const body = JSON.parse(String(init!.body)) as { restricted: boolean; user_ids: number[] };
        writes.push({ method, url, body });
        if (writeStatus !== 200) return jsonResponse({ error: 'nope' }, writeStatus);
        accessBody = {
          restricted: body.restricted,
          users: accessBody.users.map((u) => ({ ...u, granted: body.user_ids.includes(u.id) })),
        };
        if (body.restricted) {
          libraries = libraries.map((l) =>
            url.includes(`/libraries/${l.id}/`) ? { ...l, restricted: true, dlna_visible: false } : l,
          );
        }
        return jsonResponse(accessBody);
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
      if (url.endsWith('/libraries/providers')) return jsonResponse({ providers: providerList });
      if (url.endsWith('/libraries')) return jsonResponse({ libraries });
      if (url.endsWith('/settings')) return jsonResponse(SETTINGS);
      if (url.endsWith('/quality-profiles')) return jsonResponse({ profiles: PROFILES });
      if (url.endsWith('/download-clients/types')) return jsonResponse({ types: TYPES });
      if (url.endsWith('/download-clients')) return jsonResponse({ download_clients: CLIENTS });
      if (url.endsWith('/auth/me')) {
        meReads += 1;
        return jsonResponse({ username: 'root', role: 'admin', open: false, adult: false, libraries: [] });
      }
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

/**
 * The switch whose accessible name is `name` — the visible label, or the
 * aria-label when the control hides it. Positional lookup is not usable here:
 * every library card carries several switches now (active, restricted, one per
 * indexer, DLNA) and their order is a layout decision, not a contract.
 */
function toggle(name: string): HTMLButtonElement {
  const found = [...host.querySelectorAll<HTMLButtonElement>('[role="switch"]')].find(
    (candidate) => (candidate.getAttribute('aria-label') ?? candidate.textContent ?? '').trim() === name,
  );
  expect(found, `switch named ${name}`).toBeDefined();
  return found!;
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

/**
 * The provider chain editor.
 *
 * A library identifies new items through an ORDERED list of providers, and the
 * order is the setting: the first that recognizes a title wins a scan. So every
 * control here writes the whole list — the server validates it as one thing —
 * and the screen must never be able to send an empty one.
 */
describe('LibrariesSettings — provider chain', () => {
  /** The chain rows, in the order the screen renders them. */
  function chain(): string[] {
    return [...host.querySelectorAll('[data-provider-row]')].map(
      (row) => row.getAttribute('data-provider-row') ?? '',
    );
  }

  /** A chain row's control, found by the accessible name inside it. */
  function rowButton(provider: string, label: string): HTMLButtonElement {
    const row = host.querySelector(`[data-provider-row="${provider}"]`);
    expect(row, `chain row for ${provider}`).not.toBeNull();
    const found = [...row!.querySelectorAll('button')].find((candidate) =>
      candidate.textContent?.includes(label),
    );
    expect(found, `${label} control for ${provider}`).toBeDefined();
    return found as HTMLButtonElement;
  }

  const ANIME = library({
    id: 9,
    kind: 'tv',
    name: 'Anime',
    root_path: 'library/Anime',
    is_default: false,
    provider: 'anilist',
    providers: ['anilist', 'tmdb'],
  });

  async function openAnime() {
    libraries = [MOVIES, SERIES, ANIME];
    await mountLoaded();
    button('Anime').click();
    await settle();
  }

  it('renders the stored chain in its stored order, numbered', async () => {
    await openAnime();

    expect(chain()).toEqual(['anilist', 'tmdb']);
    // Named, and positioned: the order is the setting, so it is shown rather
    // than left to be counted off the rows.
    const rows = [...host.querySelectorAll('[data-provider-row]')];
    expect(rows[0]!.textContent).toContain('AniList');
    expect(rows[0]!.textContent).toContain('1');
    expect(rows[1]!.textContent).toContain('TMDB');
    expect(rows[1]!.textContent).toContain('2');
  });

  it('PATCHes the whole reordered chain when one entry moves up', async () => {
    await openAnime();
    writeReplies = [library({ ...ANIME, provider: 'tmdb', providers: ['tmdb', 'anilist'] })];

    rowButton('tmdb', 'Move TMDB earlier').click();
    await settle();

    expect(writes).toEqual([
      { method: 'PATCH', url: '/api/v1/libraries/9', body: { providers: ['tmdb', 'anilist'] } },
    ]);
    expect(chain()).toEqual(['tmdb', 'anilist']);
  });

  it('PATCHes the whole reordered chain when one entry moves down', async () => {
    await openAnime();
    writeReplies = [library({ ...ANIME, provider: 'tmdb', providers: ['tmdb', 'anilist'] })];

    rowButton('anilist', 'Move AniList later').click();
    await settle();

    expect(write(0).body).toEqual({ providers: ['tmdb', 'anilist'] });
  });

  it('cannot move the ends off either end of the chain', async () => {
    await openAnime();

    expect(rowButton('anilist', 'Move AniList earlier').disabled).toBe(true);
    expect(rowButton('tmdb', 'Move TMDB later').disabled).toBe(true);
    // Not merely disabled controls: nothing was written either.
    expect(writes).toEqual([]);
  });

  it('PATCHes the grown chain when a provider is added, and stops offering it', async () => {
    // A library that names one of the two eligible providers, so the add
    // select has exactly one thing to offer.
    libraries = [MOVIES, library({ ...SERIES, provider: 'tmdb', providers: ['tmdb'] })];
    await mountLoaded();
    button('Series').click();
    await settle();

    const add = select('library-provider-add');
    expect([...add.options].map((o) => o.value)).toEqual(['', 'anilist']);

    writeReplies = [library({ ...SERIES, provider: 'tmdb', providers: ['tmdb', 'anilist'] })];
    pick(add, 'anilist');
    await settle();

    expect(writes).toEqual([
      { method: 'PATCH', url: '/api/v1/libraries/2', body: { providers: ['tmdb', 'anilist'] } },
    ]);
    expect(chain()).toEqual(['tmdb', 'anilist']);
    // Nothing left to add, so the select goes away rather than offering the
    // duplicate the server would refuse.
    expect(host.querySelector('#library-provider-add')).toBeNull();
  });

  it('PATCHes the shortened chain when a provider is removed', async () => {
    await openAnime();
    writeReplies = [library({ ...ANIME, provider: 'anilist', providers: ['anilist'] })];

    rowButton('tmdb', 'Remove').click();
    await settle();

    expect(write(0).body).toEqual({ providers: ['anilist'] });
    expect(chain()).toEqual(['anilist']);
  });

  it('refuses to remove the last provider, which would leave nothing to identify with', async () => {
    // The movie library names one provider, which is the common install.
    await mountLoaded();

    expect(chain()).toEqual(['tmdb']);
    expect(rowButton('tmdb', 'Remove').disabled).toBe(true);
    expect(writes).toEqual([]);
  });

  it('offers a library only the providers that serve its kind', async () => {
    // AniList serves television, so it must not be offered to a movie library
    // however many providers exist.
    await mountLoaded();
    expect(host.querySelector('#library-provider-add')).toBeNull();

    button('Series').click();
    await settle();
    expect([...select('library-provider-add').options].map((o) => o.value)).toEqual([
      '',
      'anilist',
    ]);
  });
});

describe('LibrariesSettings — indexer rows', () => {
  it('says a row is not searched here, without blaming the indexer', async () => {
    libraries = [library({ indexers: [indexerRow({ enabled: false })] }), SERIES];
    await mountLoaded();

    expect(host.textContent).toContain('Not searched for this library');
    expect(host.textContent).not.toContain('Disabled in Settings');
    expect(toggle('Search Prowlarr for Movies').getAttribute('aria-checked')).toBe('false');
    // The dot reports the indexer's own health, which is still green — the
    // library is what switched it off, not the indexer.
    expect(host.querySelector('li span.bg-success')).not.toBeNull();
    expect(host.textContent).toContain('Indexer enabled');
    expect(host.querySelector('span[title="Prowlarr"]')?.textContent).toBe('Prowlarr');
    const searchToggle = toggle('Search Prowlarr for Movies');
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
    expect(toggle('Search Prowlarr for Movies').getAttribute('aria-checked')).toBe('true');
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
    // The indexer list, not the provider chain's ordered one above it.
    expect(host.querySelector('ul li')?.className).toContain('border-accent');
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

    toggle('Search Prowlarr for Movies').click();
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

    toggle('Search Prowlarr for Movies').click();
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
    toggle('Search Prowlarr for Series').click();
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
      library({
        id: 3,
        kind: 'adult',
        name: 'Adult',
        root_path: 'library/Adult',
        provider: 'stashbox',
        providers: ['stashbox'],
      }),
    ];
    await mountLoaded();

    button('Adult').click();
    await settle();

    expect(rootPath()).toBe('library/Adult');
  });

  // An instance-qualified id is a provider like any other here: the chain
  // editor names it from the merged descriptor list, so a second stash-box
  // reads as its own box rather than as a raw id.
  it('names an instance-qualified stash-box in the chain', async () => {
    libraries = [
      MOVIES,
      SERIES,
      library({
        id: 3,
        kind: 'adult',
        name: 'Adult',
        root_path: 'library/Adult',
        provider: 'stashbox:stashdb',
        providers: ['stashbox:stashdb', 'stashbox'],
      }),
    ];
    await mountLoaded();

    button('Adult').click();
    await settle();

    const chain = host.querySelector('ol[aria-label="Provider chain for Adult"]');
    expect(chain).not.toBeNull();
    expect(chain!.textContent).toContain('StashDB');
    expect(chain!.textContent).toContain('ThePornDB');
    expect(chain!.textContent).not.toContain('stashbox:stashdb');
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

describe('LibrariesSettings — multiple libraries', () => {
  it('creates a library through the add modal and selects it', async () => {
    await mountLoaded();
    const addButton = [...host.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Add library'),
    );
    expect(addButton, 'Add library button').toBeDefined();
    addButton!.click();
    flushSync();

    pick(select('new-library-kind'), 'tv');

    const name = host.querySelector('#new-library-name') as HTMLInputElement;
    expect(name, 'name input').not.toBeNull();
    name.value = 'Anime';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    const root = host.querySelector('#new-library-root') as HTMLInputElement;
    expect(root.value).toBe('library/Anime');

    writeReplies = [
      library({ id: 9, kind: 'tv', name: 'Anime', root_path: 'library/Anime', is_default: false }),
    ];
    const submit = [...host.querySelectorAll('button')].find(
      (b) => b.textContent?.includes('Add library') && b.closest('[role="dialog"], dialog, .fixed'),
    );
    (submit ?? addButton)!.click();
    await settle();

    const post = writes.find((w) => w.method === 'POST' && w.url.endsWith('/libraries'));
    expect(post, 'POST /libraries').toBeDefined();
    expect(post!.body).toMatchObject({
      kind: 'tv',
      name: 'Anime',
      root_path: 'library/Anime',
      // A chain of one: the create form picks the head, and growing it is the
      // identity card's job once the library exists.
      providers: ['tmdb'],
    });
    // The new library is selected and its pill rendered.
    expect(host.textContent).toContain('Anime');
  });

  it('disables deletion while the library is the default or holds items', async () => {
    libraries = [
      MOVIES,
      library({ id: 2, kind: 'tv', name: 'Series', root_path: 'library/TV' }),
      library({
        id: 9,
        kind: 'tv',
        name: 'Anime',
        root_path: 'library/Anime',
        is_default: false,
        item_count: 3,
      }),
    ];
    await mountLoaded();
    const pill = [...host.querySelectorAll('button')].find((b) => b.textContent?.includes('Anime'));
    pill!.click();
    flushSync();

    const del = [...host.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Delete library'),
    ) as HTMLButtonElement;
    expect(del.disabled).toBe(true);
    expect(host.textContent).toContain('still has 3 items');
  });
});

/**
 * The Active switch and the Access card (PLAN Part 3 phase 5).
 *
 * These two used to be the adult module's master switch and its member roster,
 * on a settings page of their own. A library is the object both always
 * described, so they are library controls now — offered for every kind, with
 * no adult branch anywhere in the screen.
 */
describe('LibrariesSettings — active and access', () => {
  it('PATCHes the master switch and never disables the control that undoes it', async () => {
    writeReplies = [library({ active: false })];
    await mountLoaded();

    toggle('Library active').click();
    await settle();

    expect(writes).toEqual([
      { method: 'PATCH', url: '/api/v1/libraries/1', body: { active: false } },
    ]);
    expect(autosaveStatus('1:active')).toBe('Saved');
    // The nav item, the Explore scopes and the request form read what a session
    // may see from /auth/me, and switching a library off changes that for the
    // admin who did it too.
    expect(meReads).toBe(1);
    // Reachable is the whole reason an admin still receives inactive rows.
    expect(toggle('Library active').disabled).toBe(false);
    expect(toggle('Library active').getAttribute('aria-checked')).toBe('false');
  });

  it('renders an inactive library greyed, badged, and still editable', async () => {
    libraries = [library({ active: false }), SERIES];
    await mountLoaded();

    expect(host.textContent).toContain('Inactive');
    const veil = host.querySelector('[data-library-behaviour]');
    expect(veil, 'the behaviour cards').not.toBeNull();
    expect(veil!.classList.contains('opacity-60')).toBe(true);
    // Greyed, not hidden: what the library will come back with is still here.
    expect(host.querySelector('#library-name')).not.toBeNull();
    expect(toggle('Library active').getAttribute('aria-checked')).toBe('false');
  });

  it('drops the veil for an active library', async () => {
    await mountLoaded();

    expect(host.textContent).not.toContain('Inactive');
    expect(
      host.querySelector('[data-library-behaviour]')?.classList.contains('opacity-60'),
    ).toBe(false);
  });

  it('restricts a library, and reflects the DLNA share the server cleared', async () => {
    libraries = [library({ dlna_visible: true }), SERIES];
    await mountLoaded();

    expect(toggle('Share over DLNA').getAttribute('aria-checked')).toBe('true');

    toggle('Limit to named accounts').click();
    await settle();

    expect(writes).toEqual([
      {
        method: 'PUT',
        url: '/api/v1/libraries/1/access',
        // The whole decision in one request: split in two there is a window in
        // which the library is restricted to nobody.
        body: { restricted: true, user_ids: [] },
      },
    ]);
    expect(autosaveStatus('1:access')).toBe('Saved');
    // DLNA has no accounts, so the server clears the share in the same
    // transaction — and the access answer does not carry the library row, so
    // the screen has to apply that itself or it keeps showing a live share.
    expect(toggle('Share over DLNA').getAttribute('aria-checked')).toBe('false');
    expect(host.textContent).not.toContain('is on the network');
  });

  it('reads the roster, names the accounts a role already admits, and writes the whole list', async () => {
    libraries = [library({ restricted: true }), SERIES];
    accessBody = {
      restricted: true,
      users: [
        { id: 1, username: 'root', role: 'admin', granted: false, always_granted: true },
        { id: 2, username: 'kid', role: 'member', granted: false, always_granted: false },
      ],
    };
    await mountLoaded();

    // An admin gets a statement, not a checkbox: a control that changes
    // nothing is a lie about who can see the shelf.
    const adminRow = host.querySelector('[data-access-row="1"]');
    expect(adminRow?.textContent).toContain('Always has access');
    expect(adminRow?.querySelector('[role="switch"]')).toBeNull();

    toggle('Movies for kid').click();
    await settle();

    expect(writes).toEqual([
      {
        method: 'PUT',
        url: '/api/v1/libraries/1/access',
        body: { restricted: true, user_ids: [2] },
      },
    ]);
    expect(toggle('Movies for kid').getAttribute('aria-checked')).toBe('true');
  });

  it('offers an open install nothing to grant rather than an empty list', async () => {
    libraries = [library({ restricted: true }), SERIES];
    await mountLoaded();

    expect(host.textContent).toContain('No accounts yet');
    expect(host.textContent).toContain('anyone who can reach it is an admin');
  });

  it('keeps the roster off the screen while a library is open to everyone', async () => {
    libraries = [library({ restricted: true }), SERIES];
    accessBody = {
      restricted: true,
      users: [{ id: 2, username: 'kid', role: 'member', granted: true, always_granted: false }],
    };
    await mountLoaded();

    // Unrestricted, the list is not on screen at all — the toggle is the whole
    // card until somebody turns it on.
    button('Series').click();
    await settle();
    expect(host.textContent).not.toContain('kid');
  });
});

describe('LibrariesSettings — the DLNA warning', () => {
  /** The banner's own words, so a passing test cannot be satisfied by prose. */
  const WARNING = 'every device on this network can browse it';

  it('warns when a restricted library is also shared on the LAN', async () => {
    libraries = [library({ restricted: true, dlna_visible: true }), SERIES];
    await mountLoaded();

    expect(host.textContent).toContain(WARNING);
    expect(host.querySelector('[role="alert"]')).not.toBeNull();
  });

  it('stays quiet for a restricted library that is not shared', async () => {
    libraries = [library({ restricted: true, dlna_visible: false }), SERIES];
    await mountLoaded();

    expect(host.textContent).not.toContain(WARNING);
  });

  it('stays quiet for an open library that is shared', async () => {
    libraries = [library({ restricted: false, dlna_visible: true }), SERIES];
    await mountLoaded();

    expect(host.textContent).not.toContain(WARNING);
  });

  it('names the CLI flag on an adult library and nowhere else', async () => {
    libraries = [
      MOVIES,
      library({ id: 3, kind: 'adult', name: 'Adult', root_path: 'library/Adult', provider: 'stashbox', providers: ['stashbox'], restricted: true }),
    ];
    await mountLoaded();

    expect(host.textContent).not.toContain('--include-adult');

    button('Adult').click();
    await settle();
    expect(host.textContent).toContain('--include-adult');
  });
});

/**
 * Creating an adult library IS how adult content is turned on now, and the
 * stash-box instance CRUD only appears once one exists. So the form has to
 * offer the kind on an install with no box configured, and warn rather than
 * block.
 */
describe('LibrariesSettings — the adult create flow', () => {
  async function openAdd() {
    await mountLoaded();
    button('Add library').click();
    flushSync();
    pick(select('new-library-kind'), 'adult');
  }

  it('warns, and points at Metadata, when no stash-box endpoint is configured', async () => {
    providerList = [{ id: 'tmdb', name: 'TMDB', kinds: ['movie', 'tv'] }];
    await openAdd();

    expect([...select('new-library-kind').options].map((o) => o.value)).toContain('adult');
    expect(host.textContent).toContain('No stash-box endpoint yet');
    expect(
      [...host.querySelectorAll('a[href="/settings/metadata"]')].length,
      'a link to Metadata',
    ).toBeGreaterThan(0);
  });

  it('says nothing about endpoints once one is configured', async () => {
    await openAdd();

    expect(host.textContent).not.toContain('No stash-box endpoint yet');
  });

  it('creates the library without a chain the boxless install could not name', async () => {
    providerList = [{ id: 'tmdb', name: 'TMDB', kinds: ['movie', 'tv'] }];
    await openAdd();

    const name = host.querySelector('#new-library-name') as HTMLInputElement;
    name.value = 'Adult';
    name.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();

    writeReplies = [
      library({ id: 9, kind: 'adult', name: 'Adult', root_path: 'library/Adult', is_default: true, restricted: true, dlna_visible: false, provider: 'stashbox', providers: ['stashbox'] }),
    ];
    const submit = [...host.querySelectorAll('button')].find(
      (b) => b.textContent?.includes('Add library') && b.closest('[role="dialog"], dialog, .fixed'),
    );
    submit!.click();
    await settle();

    const post = writes.find((w) => w.method === 'POST' && w.url.endsWith('/libraries'));
    expect(post, 'POST /libraries').toBeDefined();
    // An empty chain, so the server picks the bare legacy stash-box id — the
    // one exception that makes the module bootstrappable.
    expect(post!.body).toMatchObject({ kind: 'adult', name: 'Adult', providers: [] });
  });
});
