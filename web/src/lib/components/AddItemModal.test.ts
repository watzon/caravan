/**
 * Tab seeding for the add flow: "Add series" must open on the Series tab
 * instead of always defaulting to Movies, while a fixed kind still locks the
 * picker down entirely (scan-review manual match). Keyboard contract: the
 * search field owns focus on open, and Tab flips the Movies/Series scope
 * instead of leaving the field.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AddItemModal from './AddItemModal.svelte';
import type { SessionUser } from '../api/types';
import { session } from '../state/session.svelte';
import { clearToasts } from '../state/toast.svelte';

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;

function mountModal(props: {
  kind?: 'movie' | 'series' | 'site' | null;
  initialKind?: 'movie' | 'series' | 'site';
  onpick?: (kind: 'movie' | 'series', tmdbID: number) => void;
} = {}): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(AddItemModal, {
    target: host,
    props: { onclose: () => {}, ...props },
  }) as Record<string, unknown>;
  flushSync();
  return host;
}

function selectedTab(): string | null {
  return host?.querySelector('[role="tab"][aria-selected="true"]')?.textContent?.trim() ?? null;
}

afterEach(() => {
  if (app) unmount(app);
  host?.remove();
  app = undefined;
  host = undefined;
  // A session leaking into the next test would decide whether the adult scope
  // exists there; null is "we do not know yet", which is not granted.
  session.user = null;
  clearToasts();
  window.localStorage.clear();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe('AddItemModal', () => {
  it('defaults to the Movies tab', () => {
    mountModal();
    expect(selectedTab()).toBe('Movies');
  });

  it('opens on the Series tab when seeded with initialKind', () => {
    mountModal({ initialKind: 'series' });
    expect(selectedTab()).toBe('Series');
    expect(host!.querySelector('input')?.getAttribute('placeholder')).toBe(
      'Search TMDB for a series…',
    );
  });

  it('lets a fixed kind override the seed and hide the tabs', () => {
    mountModal({ kind: 'movie', initialKind: 'series' });
    expect(host!.querySelector('[role="tablist"]')).toBeNull();
    expect(host!.querySelector('input')?.getAttribute('placeholder')).toBe(
      'Search TMDB for a movie…',
    );
  });

  it('focuses the search field on open', () => {
    mountModal();
    expect(document.activeElement).toBe(host!.querySelector('input'));
  });

  function pressTab(shiftKey = false): KeyboardEvent {
    const input = host!.querySelector('input')!;
    const event = new KeyboardEvent('keydown', { key: 'Tab', shiftKey, cancelable: true, bubbles: true });
    input.dispatchEvent(event);
    flushSync();
    return event;
  }

  it('flips between Movies and Series on Tab in the search field', () => {
    mountModal();
    expect(pressTab().defaultPrevented).toBe(true);
    expect(selectedTab()).toBe('Series');
    pressTab();
    expect(selectedTab()).toBe('Movies');
  });

  it('leaves Shift+Tab alone so reverse focus navigation still works', () => {
    mountModal();
    expect(pressTab(true).defaultPrevented).toBe(false);
    expect(selectedTab()).toBe('Movies');
  });

  it('leaves Tab alone when the kind is fixed', () => {
    mountModal({ kind: 'movie' });
    expect(pressTab().defaultPrevented).toBe(false);
  });

  function press(target: Element, key: string) {
    target.dispatchEvent(new KeyboardEvent('keydown', { key, cancelable: true, bubbles: true }));
    flushSync();
  }

  it('walks the results with Up/Down and hands focus back to the field from the top', async () => {
    vi.useFakeTimers();
    const movies = [
      { tmdb_id: 1, title: 'Dune', year: 2021, overview: '', poster_url: '' },
      { tmdb_id: 2, title: 'Dune: Part Two', year: 2024, overview: '', poster_url: '' },
    ];
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ movies, series: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    );
    mountModal();

    const input = host!.querySelector('input')!;
    input.value = 'dune';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
    flushSync();

    const buttons = [...host!.querySelectorAll<HTMLElement>('ul button')];
    expect(buttons).toHaveLength(2);

    press(input, 'ArrowDown');
    expect(document.activeElement).toBe(buttons[0]);
    press(buttons[0]!, 'ArrowDown');
    expect(document.activeElement).toBe(buttons[1]);
    // The bottom is a stop, not a wrap: Down again stays put.
    press(buttons[1]!, 'ArrowDown');
    expect(document.activeElement).toBe(buttons[1]);
    press(buttons[1]!, 'ArrowUp');
    expect(document.activeElement).toBe(buttons[0]);
    press(buttons[0]!, 'ArrowUp');
    expect(document.activeElement).toBe(input);
  });

  /**
   * Search-on-add (SPEC §9). The checkbox is a sticky per-browser habit, so it
   * is asserted through what the add request actually carries rather than
   * through the DOM alone.
   */
  const MOVIES = [{ tmdb_id: 1, title: 'Dune', year: 2021, overview: '', poster_url: '' }];
  const SERIES = [{ tmdb_id: 2, title: 'Severance', year: 2022, overview: '', poster_url: '' }];

  function stubSearchAndAdd(): { url: string; method: string; body: unknown }[] {
    const calls: { url: string; method: string; body: unknown }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        calls.push({
          url,
          method: init?.method ?? 'GET',
          body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
        });
        const payload = init?.method === 'POST'
          ? { id: 9, title: 'Added' }
          : { movies: MOVIES, series: SERIES };
        return new Response(JSON.stringify(payload), {
          status: init?.method === 'POST' ? 201 : 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );
    return calls;
  }

  async function addFirstResult(kindTab: 'movie' | 'series' = 'movie') {
    const input = host!.querySelector('input[type="search"]') as HTMLInputElement;
    input.value = 'dune';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
    flushSync();

    const add = host!.querySelector('ul button') as HTMLButtonElement;
    expect(add, `an ${kindTab} result to add`).toBeTruthy();
    add.click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();
  }

  function checkbox(): HTMLInputElement | null {
    return host!.querySelector('input[type="checkbox"]');
  }

  it('defaults to searching on add and sends search_now for a movie', async () => {
    vi.useFakeTimers();
    const calls = stubSearchAndAdd();
    mountModal();

    expect(checkbox()?.checked).toBe(true);
    await addFirstResult();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/api/v1/library/movies');
    expect(post?.body).toMatchObject({ tmdb_id: 1, search_now: true });
  });

  it('sends search_missing for a series, not search_now', async () => {
    vi.useFakeTimers();
    const calls = stubSearchAndAdd();
    mountModal({ initialKind: 'series' });

    await addFirstResult('series');

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/api/v1/library/series');
    expect(post?.body).toMatchObject({ tmdb_id: 2, search_missing: true });
    expect((post?.body as Record<string, unknown>).search_now).toBeUndefined();
  });

  it('omits the search when the box is cleared, and remembers the choice', async () => {
    vi.useFakeTimers();
    const calls = stubSearchAndAdd();
    mountModal();

    const box = checkbox()!;
    box.checked = false;
    box.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();

    await addFirstResult();
    expect(calls.find((c) => c.method === 'POST')?.body).toMatchObject({ search_now: false });

    // The next modal opens with the same answer: this is a habit, not a
    // per-item decision.
    unmount(app!);
    host!.remove();
    mountModal();
    expect(checkbox()?.checked).toBe(false);
  });

  it('hides the checkbox in pick mode, where there is nothing to search for', () => {
    mountModal({ onpick: () => {} });
    expect(checkbox()).toBeNull();
  });
});

/**
 * The adult scope (owner decision: sites are addable from ⌘K).
 *
 * Two properties are being defended here. The first is the phase's safety
 * property — where the module is not visible there is no tab, no adult request,
 * and no way to reach one by passing a prop. The second is that the site scope
 * behaves like the dialog it replaced: the nine behaviours below are the ones
 * AddSiteModal was carrying before it was folded in here.
 */
describe('AddItemModal adult scope', () => {
  const SITES = [
    {
      stash_id: 'site-1',
      name: 'Brazzers',
      aliases: ['BRZ'],
      parent_name: '',
      url: '',
      image_url: '',
      in_library: false,
      library_id: 0,
    },
    {
      stash_id: 'site-2',
      name: 'Brazzers Exxtra',
      aliases: [],
      parent_name: 'Brazzers',
      url: '',
      image_url: '',
      in_library: false,
      library_id: 0,
    },
  ];

  function user(adult: boolean, role: 'admin' | 'member' = 'admin'): SessionUser {
    return { username: 'someone', role, open: false, adult };
  }

  interface SiteCall {
    url: string;
    method: string;
    body: unknown;
  }

  /** Stubs both providers, so a request to the wrong one is visible, not fatal. */
  function stubProviders(options: { searchStatus?: number; sites?: unknown[] } = {}): SiteCall[] {
    const calls: SiteCall[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method ?? 'GET';
        calls.push({
          url,
          method,
          body: typeof init?.body === 'string' ? JSON.parse(init.body) : null,
        });
        const json = (payload: unknown, status = 200) =>
          new Response(JSON.stringify(payload), {
            status,
            headers: { 'Content-Type': 'application/json' },
          });
        if (method === 'POST') return json({ id: 3, title: 'Brazzers' }, 201);
        if (url.includes('/adult/search')) {
          if (options.searchStatus && options.searchStatus !== 200) {
            return json(
              { error: 'stashbox: SearchSites: Internal Server Error' },
              options.searchStatus,
            );
          }
          return json({ sites: options.sites ?? SITES });
        }
        return json({ movies: [], series: [] });
      }),
    );
    return calls;
  }

  const adultCalls = (calls: SiteCall[]) => calls.filter((c) => c.url.includes('/adult'));
  const siteSearches = (calls: SiteCall[]) =>
    calls.filter((c) => c.method === 'GET' && c.url.includes('/adult/search'));

  function tabs(): string[] {
    return [...host!.querySelectorAll('[role="tab"]')].map((t) => t.textContent?.trim() ?? '');
  }

  function type(text: string) {
    const input = host!.querySelector('input[type="search"]') as HTMLInputElement;
    input.value = text;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
  }

  async function settle(ms = 300) {
    await vi.advanceTimersByTimeAsync(ms);
    flushSync();
  }

  function keydown(target: Element, key: string): KeyboardEvent {
    const event = new KeyboardEvent('keydown', { key, cancelable: true, bubbles: true });
    target.dispatchEvent(event);
    flushSync();
    return event;
  }

  /* ------------------------------------------------------------------ *
   * Visibility. The matrix is the point: the tab must track the one
   * boolean the server computes, not a role guess.
   * ------------------------------------------------------------------ */

  const MATRIX: { name: string; session: SessionUser | null; visible: boolean }[] = [
    { name: 'an identity nobody has answered for yet', session: null, visible: false },
    { name: 'an admin on a server with the module off', session: user(false), visible: false },
    { name: 'a member without the grant', session: user(false, 'member'), visible: false },
    { name: 'a member with the grant', session: user(true, 'member'), visible: true },
    { name: 'an admin with the module on', session: user(true), visible: true },
  ];

  for (const row of MATRIX) {
    it(`${row.visible ? 'offers' : 'hides'} the Adult scope for ${row.name}`, () => {
      vi.useFakeTimers();
      stubProviders();
      session.user = row.session;
      mountModal();
      expect(tabs()).toEqual(row.visible ? ['Movies', 'Series', 'Adult'] : ['Movies', 'Series']);
    });
  }

  it('makes no adult request at all where the module is not visible', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    session.user = user(false);
    mountModal();

    // Walk every scope the keyboard can reach before typing: if the cycle could
    // land on the adult scope, the search below would go to the endpoint.
    const input = host!.querySelector('input')!;
    for (let i = 0; i < 4; i++) {
      keydown(input, 'Tab');
      type(`brazzers ${i}`);
      await settle();
    }
    // It searched — this is not the vacuous "nothing happened" version of the
    // assertion — and everything it asked for was TMDB's.
    expect(calls.length).toBeGreaterThan(0);
    expect(adultCalls(calls)).toEqual([]);
  });

  it('refuses a site scope asked for by a caller the module is invisible to', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    session.user = user(false);
    mountModal({ kind: 'site' });

    await settle();
    type('brazzers');
    await settle();

    // The prop cannot select the scope into being: it falls back to Movies, and
    // the adult endpoint is never touched.
    expect(adultCalls(calls)).toEqual([]);
    expect(host!.querySelector('input')?.getAttribute('placeholder')).toBe(
      'Search TMDB for a movie…',
    );
  });

  it('cycles Tab through the adult scope only while it is there', () => {
    vi.useFakeTimers();
    stubProviders();
    session.user = user(true);
    mountModal();

    const input = host!.querySelector('input')!;
    const selected = () =>
      host!.querySelector('[role="tab"][aria-selected="true"]')?.textContent?.trim();

    keydown(input, 'Tab');
    expect(selected()).toBe('Series');
    keydown(input, 'Tab');
    expect(selected()).toBe('Adult');
    keydown(input, 'Tab');
    expect(selected()).toBe('Movies');
  });

  /* ------------------------------------------------------------------ *
   * The nine behaviours ported from AddSiteModal.
   * ------------------------------------------------------------------ */

  function mountSiteScope() {
    session.user = user(true);
    mountModal({ kind: 'site' });
  }

  it('focuses the search field on open', () => {
    vi.useFakeTimers();
    stubProviders();
    mountSiteScope();
    expect(document.activeElement).toBe(host!.querySelector('input'));
  });

  it('offers no Search button: typing is the search', async () => {
    vi.useFakeTimers();
    stubProviders();
    mountSiteScope();
    await settle();
    const labels = [...host!.querySelectorAll('button')].map((b) => b.textContent?.trim());
    expect(labels).not.toContain('Search');
  });

  it('opens on the minimum-length hint like every other scope, fetching nothing', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    mountSiteScope();
    await settle();

    // Consistency is the contract: a blank adult query behaves exactly like a
    // blank TMDB one — hint, no request — even though the endpoint could
    // answer it. The blank-query API path stays for surfaces that want it.
    expect(siteSearches(calls)).toHaveLength(0);
    expect(host!.textContent).toContain('Type at least two characters');
  });

  it('searches sites as you type once past the debounce', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    mountSiteScope();
    await settle();
    const before = siteSearches(calls).length;

    type('brazzers');
    await settle(200);
    expect(siteSearches(calls)).toHaveLength(before);

    await settle(100);
    const search = siteSearches(calls);
    expect(search).toHaveLength(before + 1);
    expect(search[before]!.url).toContain('q=brazzers');
  });

  it('does not search a site query below the minimum length', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    mountSiteScope();
    await settle();
    const before = siteSearches(calls).length;

    type('b');
    await settle();
    expect(siteSearches(calls)).toHaveLength(before);
    expect(host!.textContent).toContain('Type at least two characters');
  });

  it('adds the site the arrow keys landed on when Enter is pressed', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    mountSiteScope();
    await settle();
    type('brazzers');
    await settle(300);

    const input = host!.querySelector('input')!;
    keydown(input, 'ArrowDown');
    const buttons = [...host!.querySelectorAll<HTMLElement>('ul button')];
    expect(document.activeElement).toBe(buttons[0]);
    keydown(buttons[0]!, 'ArrowDown');
    expect(document.activeElement).toBe(buttons[1]);

    const enter = keydown(buttons[1]!, 'Enter');
    // The browser would otherwise activate the focused button as well, which
    // would be two adds for one keypress.
    expect(enter.defaultPrevented).toBe(true);
    await settle(0);

    const posts = calls.filter((c) => c.method === 'POST');
    expect(posts).toHaveLength(1);
    expect(posts[0]!.url).toContain('/adult/sites');
    expect(posts[0]!.body).toMatchObject({ stash_id: 'site-2' });
  });

  it('adds once when Enter and a click land on the same row', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    mountSiteScope();
    await settle();
    type('brazzers');
    await settle(300);

    const button = host!.querySelector<HTMLElement>('ul button')!;
    keydown(button, 'Enter');
    button.click();
    await settle(0);

    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(1);
  });

  it('renders a provider failure with a retry that searches again', async () => {
    vi.useFakeTimers();
    const calls = stubProviders({ searchStatus: 502 });
    mountSiteScope();
    await settle();
    type('brazzers');
    await settle(300);

    expect(host!.textContent).toContain('stashbox: SearchSites: Internal Server Error');
    const before = siteSearches(calls).length;

    const retry = [...host!.querySelectorAll('button')].find(
      (b) => b.textContent?.trim() === 'Retry',
    );
    expect(retry, 'a Retry control on the failure').toBeTruthy();
    retry!.click();
    await settle();
    expect(siteSearches(calls)).toHaveLength(before + 1);
  });

  it('says so when the provider matches no site', async () => {
    vi.useFakeTimers();
    stubProviders({ sites: [] });
    mountSiteScope();
    await settle();
    type('nothing at all');
    await settle();

    expect(host!.querySelector('ul')).toBeNull();
    expect(host!.textContent).toContain('No site matches');
  });

  it('drops the search-on-add habit, which a catalogue walk has no room for', async () => {
    vi.useFakeTimers();
    stubProviders();
    mountSiteScope();
    await settle();
    expect(host!.querySelector('input[type="checkbox"]')).toBeNull();
  });
});
