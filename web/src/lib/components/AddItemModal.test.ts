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
import type { MovieMeta, SeriesMeta, SessionUser } from '../api/types';
import { session } from '../state/session.svelte';
import { clearToasts, toasts } from '../state/toast.svelte';

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;

function mountModal(props: {
  kind?: 'movie' | 'series' | 'site' | null;
  initialKind?: 'movie' | 'series' | 'site';
  libraryID?: number;
  onpick?: (kind: 'movie' | 'series', row: MovieMeta | SeriesMeta) => void;
  onadded?: (kind: 'movie' | 'series', item: { id: number; title: string }) => void;
  onclose?: () => void;
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
  vi.restoreAllMocks();
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
      'Search TMDB for a series...',
    );
  });

  it('lets a fixed kind override the seed and hide the tabs', () => {
    mountModal({ kind: 'movie', initialKind: 'series' });
    expect(host!.querySelector('[role="tablist"]')).toBeNull();
    expect(host!.querySelector('input')?.getAttribute('placeholder')).toBe(
      'Search TMDB for a movie...',
    );
  });

  it('focuses the search field on open', () => {
    mountModal();
    expect(document.activeElement).toBe(host!.querySelector('input'));
  });

  it('names the search field, scope tabs, and close control', () => {
    mountModal();

    expect(host!.querySelector('input[type="search"]')?.getAttribute('aria-label')).toBe(
      'Search TMDB',
    );
    expect(host!.querySelector('[role="tablist"]')?.getAttribute('aria-label')).toBe(
      'Search type',
    );
    expect(host!.querySelector('button[aria-label="Close"]')).not.toBeNull();
  });

  function pressTab(target: Element, shiftKey = false): KeyboardEvent {
    const event = new KeyboardEvent('keydown', { key: 'Tab', shiftKey, cancelable: true, bubbles: true });
    target.dispatchEvent(event);
    flushSync();
    return event;
  }

  it('flips between Movies and Series on Tab in the search field', () => {
    mountModal();
    const input = host!.querySelector('input')!;
    expect(pressTab(input).defaultPrevented).toBe(true);
    expect(selectedTab()).toBe('Series');
    pressTab(input);
    expect(selectedTab()).toBe('Movies');
  });

  it('leaves interior Tab and Shift+Tab navigation native', () => {
    mountModal({ kind: 'movie' });
    const input = host!.querySelector('input')!;

    input.focus();
    expect(pressTab(input).defaultPrevented).toBe(false);
    input.focus();
    expect(pressTab(input, true).defaultPrevented).toBe(false);
  });

  it('wraps Tab only when focus would leave the dialog', () => {
    mountModal();
    const close = host!.querySelector<HTMLButtonElement>('[aria-label="Close"]')!;
    const monitor = host!.querySelector<HTMLInputElement>('input[type="checkbox"]')!;

    close.focus();
    expect(pressTab(close, true).defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(monitor);

    monitor.focus();
    expect(pressTab(monitor).defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(close);
  });

  function press(target: Element, key: string) {
    target.dispatchEvent(new KeyboardEvent('keydown', { key, cancelable: true, bubbles: true }));
    flushSync();
  }

  it('walks the results with Up/Down and hands focus back to the field from the top', async () => {
    vi.useFakeTimers();
    const movies = [
      {
        tmdb_id: 1,
        title: 'Dune',
        year: 2021,
        overview: '',
        release_date: '2021-10-22',
        vote_average: 8.1,
        vote_count: 12_341,
        poster_url: '',
      },
      {
        tmdb_id: 2,
        title: 'Dune: Part Two',
        year: 2024,
        overview: '',
        release_date: '2024-03-01',
        vote_average: 8.5,
        vote_count: 8_921,
        poster_url: '',
      },
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
   * The add options (owner decision: both OFF by default).
   *
   * They are asserted through what the add request actually carries rather than
   * through the DOM alone: the checkbox is the affordance, the request body is
   * the contract. They are deliberately NOT sticky — a remembered "monitor
   * everything" is exactly the accident the off-by-default pair prevents — so
   * each assertion mounts a fresh modal and expects the same defaults.
   */
  const MOVIES = [
    {
      tmdb_id: 1,
      title: 'Dune',
      year: 2021,
      overview: '',
      release_date: '2021-10-22',
      vote_average: 8.1,
      vote_count: 12_341,
      poster_url: '',
    },
  ];
  const SERIES = [
    {
      tmdb_id: 2,
      title: 'Severance',
      year: 2022,
      overview: '',
      first_air_date: '2022-02-18',
      vote_average: 8.7,
      vote_count: 4_208,
      poster_url: '',
    },
  ];

  function stubSearchAndAdd(
    results: { movies: unknown[]; series: unknown[] } = { movies: MOVIES, series: SERIES },
    added: { id: number; title: string } = { id: 9, title: 'Added' },
  ): { url: string; method: string; body: unknown }[] {
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
        const payload = init?.method === 'POST' ? added : results;
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

  async function addSearchResults(query: string) {
    const input = host!.querySelector('input[type="search"]') as HTMLInputElement;
    input.value = query;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
    flushSync();
  }

  function resultButton(): HTMLButtonElement {
    return host!.querySelector('ul button') as HTMLButtonElement;
  }

  function buttonWithText(text: string): HTMLButtonElement | null {
    return (
      [...host!.querySelectorAll<HTMLButtonElement>('button')].find(
        (button) => button.textContent?.trim() === text,
      ) ?? null
    );
  }

  /** The options row, in the order they appear: monitor first, search second. */
  function optionBoxes(): HTMLInputElement[] {
    return [...host!.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')];
  }

  function toggle(box: HTMLInputElement, next: boolean) {
    box.checked = next;
    box.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
  }

  it('offers only the monitor box, unchecked, until monitoring is on', () => {
    vi.useFakeTimers();
    stubSearchAndAdd();
    mountModal();

    let boxes = optionBoxes();
    expect(boxes).toHaveLength(1);
    expect(boxes[0]!.checked).toBe(false);
    expect(host!.textContent).toContain('Add and monitor');
    expect(host!.textContent).not.toContain('Search now');

    toggle(boxes[0]!, true);
    boxes = optionBoxes();
    expect(boxes).toHaveLength(2);
    // Revealed, but not pre-answered: the second decision is its own.
    expect(boxes[1]!.checked).toBe(false);
    expect(host!.textContent).toContain('Search now');
  });

  it('unchecking monitor hides the search box and resets it', async () => {
    vi.useFakeTimers();
    const calls = stubSearchAndAdd();
    mountModal();

    toggle(optionBoxes()[0]!, true);
    toggle(optionBoxes()[1]!, true);
    toggle(optionBoxes()[0]!, false);

    expect(optionBoxes()).toHaveLength(1);
    // The reset is the point: a hidden box that stayed true would search on the
    // next add for a reason nothing on screen explains.
    toggle(optionBoxes()[0]!, true);
    expect(optionBoxes()[1]!.checked).toBe(false);

    toggle(optionBoxes()[0]!, false);
    await addFirstResult();
    expect(calls.find((c) => c.method === 'POST')?.body).toMatchObject({
      monitored: false,
      search_now: false,
    });
  });

  it('adds a movie unmonitored and unsearched by default', async () => {
    vi.useFakeTimers();
    const calls = stubSearchAndAdd();
    mountModal();

    await addFirstResult();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/api/v1/library/movies');
    expect(post?.body).toMatchObject({ tmdb_id: 1, monitored: false, search_now: false });
  });

  it('sends both flags for a movie once both boxes are checked', async () => {
    vi.useFakeTimers();
    const calls = stubSearchAndAdd();
    mountModal();

    toggle(optionBoxes()[0]!, true);
    toggle(optionBoxes()[1]!, true);
    await addFirstResult();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/api/v1/library/movies');
    expect(post?.body).toMatchObject({ tmdb_id: 1, monitored: true, search_now: true });
  });

  it('sends search_missing for a series, not search_now', async () => {
    vi.useFakeTimers();
    const calls = stubSearchAndAdd();
    mountModal({ initialKind: 'series' });

    toggle(optionBoxes()[0]!, true);
    toggle(optionBoxes()[1]!, true);
    await addFirstResult('series');

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/api/v1/library/series');
    expect(post?.body).toMatchObject({ tmdb_id: 2, monitored: true, search_missing: true });
    expect((post?.body as Record<string, unknown>).search_now).toBeUndefined();
  });

  /**
   * The grab-target dialog adds a title mid-flow and then ties a release to
   * it, so the add must not navigate: it would abandon the release the user
   * came in with. `onadded` is what suppresses that, and nothing else.
   */
  it('hands a created movie back instead of navigating, when asked to', async () => {
    vi.useFakeTimers();
    const calls = stubSearchAndAdd(undefined, { id: 9, title: 'Dune' });
    const handed: unknown[] = [];
    let closed = 0;
    mountModal({
      onadded: (kind, item) => handed.push([kind, item]),
      onclose: () => (closed += 1),
    });

    await addFirstResult();

    // The add itself is unchanged; only what happens afterwards is.
    expect(calls.find((c) => c.method === 'POST')?.url).toBe('/api/v1/library/movies');
    expect(handed).toEqual([['movie', { id: 9, title: 'Dune' }]]);
    expect(closed).toBe(0);
    expect(window.location.pathname).not.toBe('/movies/9');
  });

  it('hands a created series back under its own kind', async () => {
    vi.useFakeTimers();
    stubSearchAndAdd(undefined, { id: 12, title: 'Severance' });
    const handed: unknown[] = [];
    mountModal({ initialKind: 'series', onadded: (kind, item) => handed.push([kind, item]) });

    await addFirstResult('series');

    expect(handed).toEqual([['series', { id: 12, title: 'Severance' }]]);
  });

  it('drops the adult scope for a hand-back caller, which can only tie two kinds', () => {
    session.user = { username: 'admin', role: 'admin', open: false, adult: true } as SessionUser;
    mountModal({ onadded: () => {} });

    const tabs = [...host!.querySelectorAll('[role="tab"]')].map((t) => t.textContent?.trim());
    expect(tabs).toEqual(['Movies', 'Series']);
  });

  it('forgets the choice between modals: the options are per-add, not a habit', async () => {
    vi.useFakeTimers();
    stubSearchAndAdd();
    mountModal();

    toggle(optionBoxes()[0]!, true);
    toggle(optionBoxes()[1]!, true);

    unmount(app!);
    host!.remove();
    mountModal();
    expect(optionBoxes()).toHaveLength(1);
    expect(optionBoxes()[0]!.checked).toBe(false);
  });

  it('hides both options in pick mode, where nothing is being added', () => {
    mountModal({ onpick: () => {} });
    expect(optionBoxes()).toHaveLength(0);
  });

  it('disambiguates same-title results with distinct year and real rating badges', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-05T12:00:00Z'));
    stubSearchAndAdd({
      movies: [
        {
          tmdb_id: 10,
          title: 'Dune',
          year: 1984,
          overview: '',
          release_date: '1984-12-14',
          vote_average: 6.3,
          vote_count: 1_847,
          poster_url: '',
        },
        {
          tmdb_id: 11,
          title: 'Dune',
          year: 2021,
          overview: '',
          release_date: '2021-10-22',
          vote_average: 8.1,
          vote_count: 12_341,
          poster_url: '',
        },
      ],
      series: [],
    });
    mountModal();

    const input = host!.querySelector('input[type="search"]') as HTMLInputElement;
    input.value = 'dune';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
    flushSync();

    const rows = [...host!.querySelectorAll('ul li')];
    expect(rows).toHaveLength(2);
    expect(rows[0]!.textContent).toContain('Dune');
    expect(rows[0]!.textContent).toContain('1984');
    expect(rows[0]!.textContent).toContain('6.3/10');
    expect(rows[1]!.textContent).toContain('Dune');
    expect(rows[1]!.textContent).toContain('2021');
    expect(rows[1]!.textContent).toContain('8.1/10');
  });

  it('exposes full search-result names and overviews when the row truncates them', async () => {
    vi.useFakeTimers();
    const fullTitle = 'A Search Result Title That Is Much Longer Than the Modal Row';
    const fullOverview =
      'A complete overview that remains available even when the visible result is clamped to two lines.';
    stubSearchAndAdd({
      movies: [
        {
          tmdb_id: 18,
          title: fullTitle,
          year: 2024,
          overview: fullOverview,
          release_date: '2024-06-14',
          vote_average: 7.2,
          vote_count: 320,
          poster_url: '',
        },
      ],
      series: [],
    });
    mountModal();

    await addSearchResults('long result');

    const row = host!.querySelector('ul li')!;
    expect(row.querySelector('.truncate')?.getAttribute('title')).toBe(fullTitle);
    expect(row.querySelector('.line-clamp-2')?.getAttribute('title')).toBe(fullOverview);
  });

  it('suppresses a nonzero provider average when its vote count is zero', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-05T12:00:00Z'));
    stubSearchAndAdd({
      movies: [
        {
          tmdb_id: 12,
          title: 'No Votes Yet',
          year: 2020,
          overview: '',
          release_date: '2020-01-01',
          vote_average: 8.6,
          vote_count: 0,
          poster_url: '',
        },
      ],
      series: [],
    });
    mountModal();

    await addSearchResults('no votes');

    const row = host!.querySelector('ul li')!;
    expect(row.textContent).toContain('2020');
    expect(row.textContent).toContain('Not yet rated');
    expect(row.textContent).not.toContain('8.6/10');
  });

  it('suppresses a numeric rating for a future first-air date', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-05T12:00:00Z'));
    stubSearchAndAdd({
      movies: [],
      series: [
        {
          tmdb_id: 13,
          title: 'Tomorrow Show',
          year: 2027,
          overview: '',
          first_air_date: '2027-01-10',
          vote_average: 9.8,
          vote_count: 86,
          poster_url: '',
        },
      ],
    });
    mountModal({ initialKind: 'series' });

    await addSearchResults('tomorrow');

    const row = host!.querySelector('ul li')!;
    expect(row.textContent).toContain('2027');
    expect(row.textContent).toContain('Not yet rated');
    expect(row.textContent).not.toContain('9.8/10');
  });

  it('sends no add request when future confirmation is cancelled and adds after confirmation', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-05T12:00:00Z'));
    const calls = stubSearchAndAdd({
      movies: [
        {
          tmdb_id: 14,
          title: 'Future Film',
          year: 2027,
          overview: '',
          release_date: '2027-04-02',
          vote_average: 7.4,
          vote_count: 37,
          poster_url: '',
        },
      ],
      series: [],
    });
    mountModal();
    await addSearchResults('future');
    expect(host!.querySelector('ul li')!.textContent).toContain('Not yet rated');
    expect(host!.querySelector('ul li')!.textContent).not.toContain('7.4/10');

    resultButton().click();
    flushSync();
    expect(buttonWithText('Add unreleased title')).not.toBeNull();
    expect(host!.textContent).toContain('has not been released yet');
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);

    buttonWithText('Cancel')!.click();
    flushSync();
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);

    resultButton().click();
    flushSync();
    buttonWithText('Add unreleased title')!.click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(1);
  });

  it.each([
    {
      case: 'an empty movie release date',
      initialKind: 'movie' as const,
      title: 'Undated Film',
      expectedURL: '/api/v1/library/movies',
      results: {
        movies: [
          {
            tmdb_id: 16,
            title: 'Undated Film',
            year: 0,
            overview: '',
            release_date: '',
            vote_average: 7.2,
            vote_count: 24,
            poster_url: '',
          },
        ],
        series: [],
      },
    },
    {
      case: 'an invalid series first-air date',
      initialKind: 'series' as const,
      title: 'Impossible Premiere',
      expectedURL: '/api/v1/library/series',
      results: {
        movies: [],
        series: [
          {
            tmdb_id: 17,
            title: 'Impossible Premiere',
            year: 2026,
            overview: '',
            first_air_date: '2026-02-30',
            vote_average: 8.3,
            vote_count: 91,
            poster_url: '',
          },
        ],
      },
    },
  ])('requires confirmation for $case and sends no request when cancelled', async ({
    initialKind,
    title,
    expectedURL,
    results,
  }) => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-05T12:00:00Z'));
    const calls = stubSearchAndAdd(results);
    mountModal({ initialKind });
    await addSearchResults(title);

    resultButton().click();
    flushSync();
    expect(host!.textContent).toContain(`${title}'s release date is unknown.`);
    expect(buttonWithText('Add title anyway')).not.toBeNull();
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);

    buttonWithText('Cancel')!.click();
    flushSync();
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);

    resultButton().click();
    flushSync();
    buttonWithText('Add title anyway')!.click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();
    expect(calls.filter((call) => call.method === 'POST')).toEqual([
      expect.objectContaining({ url: expectedURL }),
    ]);
  });

  it('keeps released titles as a one-click add', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-05T12:00:00Z'));
    const calls = stubSearchAndAdd();
    mountModal();

    await addSearchResults('dune');
    resultButton().click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(1);
    expect(buttonWithText('Add unreleased title')).toBeNull();
  });

  it('preserves toast, close, and navigation after a released add', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-05T12:00:00Z'));
    const onclose = vi.fn();
    const pushState = vi.spyOn(window.history, 'pushState');
    vi.stubGlobal('scrollTo', vi.fn());
    stubSearchAndAdd(undefined, { id: 41, title: 'Dune' });
    mountModal({ onclose });

    await addSearchResults('dune');
    resultButton().click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    expect(onclose).toHaveBeenCalledOnce();
    expect(toasts.items.at(-1)).toMatchObject({ message: 'Added Dune.', tone: 'success' });
    expect(pushState).toHaveBeenCalledWith({}, '', '/movies/41');
  });

  it('keeps an unknown-date manual match as one click with no add confirmation or request', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-05T12:00:00Z'));
    const onpick = vi.fn();
    const calls = stubSearchAndAdd({
      movies: [
        {
          tmdb_id: 15,
          title: 'Undated Match',
          year: 0,
          overview: '',
          release_date: '',
          vote_average: 8.8,
          vote_count: 42,
          poster_url: '',
        },
      ],
      series: [],
    });
    mountModal({ kind: 'movie', onpick });

    await addSearchResults('undated match');
    resultButton().click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    // The whole row, not an id: a chain hit is named by provider/provider_ref,
    // and everything but a TMDB hit carries tmdb_id 0.
    expect(onpick).toHaveBeenCalledWith('movie', expect.objectContaining({ tmdb_id: 15 }));
    expect(buttonWithText('Add title anyway')).toBeNull();
    expect(buttonWithText('Add unreleased title')).toBeNull();
    expect(calls.filter((call) => call.method === 'POST')).toHaveLength(0);
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
      'Search TMDB for a movie...',
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

  it('exposes complete site names and aliases when the result row truncates them', async () => {
    vi.useFakeTimers();
    const fullName = 'A Provider Site Name That Is Longer Than the Available Result Row';
    const aliases = ['A very long release alias', 'Another complete alias'];
    stubProviders({
      sites: [
        {
          ...SITES[0],
          name: fullName,
          aliases,
        },
      ],
    });
    mountSiteScope();
    await settle();
    type('provider');
    await settle(300);

    const row = host!.querySelector('ul li')!;
    expect(row.querySelector('.truncate')?.getAttribute('title')).toBe(fullName);
    expect(row.querySelectorAll('.truncate')[1]?.getAttribute('title')).toBe(
      `Also ${aliases.join(', ')}`,
    );
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

  it('offers the same two options a movie or series add does', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    mountSiteScope();
    await settle();

    // A site is followed and searched exactly like a series, so it gets the
    // pair rather than nothing: the catalogue walk is a background job now, and
    // "start searching" happens once it has finished.
    const boxes = () => [...host!.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')];
    expect(boxes()).toHaveLength(1);
    expect(boxes()[0]!.checked).toBe(false);

    type('brazzers');
    await settle(300);
    (host!.querySelector('ul button') as HTMLButtonElement).click();
    await settle(0);

    const post = calls.find((c) => c.method === 'POST');
    expect(post!.url).toContain('/adult/sites');
    expect(post!.body).toMatchObject({
      stash_id: 'site-1',
      monitored: false,
      search_now: false,
    });
  });

  it('sends both flags for a site once both boxes are checked', async () => {
    vi.useFakeTimers();
    const calls = stubProviders();
    mountSiteScope();
    await settle();

    const boxes = () => [...host!.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')];
    const check = (box: HTMLInputElement) => {
      box.checked = true;
      box.dispatchEvent(new Event('change', { bubbles: true }));
      flushSync();
    };
    check(boxes()[0]!);
    check(boxes()[1]!);

    type('brazzers');
    await settle(300);
    (host!.querySelector('ul button') as HTMLButtonElement).click();
    await settle(0);

    expect(calls.find((c) => c.method === 'POST')!.body).toMatchObject({
      stash_id: 'site-1',
      monitored: true,
      search_now: true,
    });
  });

  it('says the catalogue is still being filled in when a site is added', async () => {
    vi.useFakeTimers();
    stubProviders();
    mountSiteScope();
    await settle();
    type('brazzers');
    await settle(300);
    (host!.querySelector('ul button') as HTMLButtonElement).click();
    await settle(0);

    // The add answers before the scenes exist, so the toast must not read as
    // "done" — the site page it navigates to is empty for a moment.
    expect(toasts.items.map((t) => t.message).join(' ')).toContain(
      'Cataloguing scenes in the background',
    );
  });
});

/**
 * The guarded add surface (PLAN phase 10 task 3).
 *
 * TMDB is what names a movie or a series, so with no usable key this dialog
 * has nothing to search and nothing to add. It must say that where the results
 * would have been — with the destination attached — rather than throwing the
 * provider's complaint at a toast and leaving an empty list behind it.
 */
describe('AddItemModal — metadata credential', () => {
  let calls: { url: string; method: string }[];

  function stub(reply: () => Response) {
    calls = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push({ url: String(input), method: init?.method ?? 'GET' });
        return reply();
      }),
    );
  }

  function coded(code: string, message: string, status = 503): Response {
    return new Response(JSON.stringify({ error: message, code }), {
      status,
      headers: { 'Content-Type': 'application/json' },
    });
  }

  async function search(text: string) {
    const input = host!.querySelector('input[type="search"]') as HTMLInputElement;
    input.value = text;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
    flushSync();
  }

  it('sends the user to metadata settings when the key is missing', async () => {
    vi.useFakeTimers();
    stub(() => coded('metadata_credential_absent', 'no metadata provider configured'));
    mountModal();

    await search('dune');

    expect(host!.textContent).toContain('No TMDB API key');
    expect(host!.textContent).toContain('Settings / Metadata');
    expect(host!.querySelector('a[href="/settings/metadata"]')).not.toBeNull();
    // An empty state, not an error toast.
    expect(toasts.items).toHaveLength(0);
  });

  it('says a rejected key is rejected, not missing', async () => {
    vi.useFakeTimers();
    stub(() => coded('metadata_credential_invalid', 'the TMDB API key was rejected'));
    mountModal();

    await search('dune');

    expect(host!.textContent).toContain('TMDB rejected this API key');
    expect(toasts.items).toHaveLength(0);
  });

  // A key revoked between the search and the click: the add fails where the
  // search succeeded, and the dialog answers the same way rather than toasting.
  it('turns a credential failure on the add itself into the same empty state', async () => {
    vi.useFakeTimers();
    stub(() => {
      const post = calls[calls.length - 1]?.method === 'POST';
      if (post) return coded('metadata_credential_invalid', 'the TMDB API key was rejected');
      return new Response(
        JSON.stringify({
          movies: [
            {
              tmdb_id: 1,
              title: 'Dune',
              year: 2021,
              overview: '',
              release_date: '2021-10-22',
              vote_average: 8.1,
              vote_count: 12_341,
              poster_url: '',
            },
          ],
          series: [],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      );
    });
    mountModal();

    await search('dune');
    (host!.querySelector('ul button') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    expect(host!.textContent).toContain('TMDB rejected this API key');
    expect(toasts.items).toHaveLength(0);
  });

  // Settings is admin-only (MEMBER_ROUTES in router.ts) and App.svelte bounces a
  // member off it, so the admin copy's destination is a door a member cannot
  // open. Offering it made "every metadata-needing surface names the fix" false
  // for the only role that lives on those surfaces.
  it('offers a member no door they cannot open', async () => {
    vi.useFakeTimers();
    stub(() => coded('metadata_credential_absent', 'no metadata provider configured'));
    session.user = { username: 'housemate', role: 'member', open: false, adult: false };
    mountModal();

    await search('dune');

    expect(host!.textContent).toContain('No TMDB API key');
    expect(host!.textContent).toContain('Ask a Caravan admin');
    expect(host!.textContent).not.toContain('Settings / Metadata');
    expect(host!.querySelector('a[href="/settings/metadata"]')).toBeNull();
    expect(toasts.items).toHaveLength(0);
  });

  // The fault belongs to TMDB, and the Adult scope does not call TMDB. Leaking
  // it across replaced working stash-box rows with an empty state pointing at a
  // settings screen that had nothing to do with the failure.
  it('keeps a TMDB add fault out of the Adult scope', async () => {
    vi.useFakeTimers();
    session.user = { username: 'someone', role: 'admin', open: false, adult: true };
    const ok = (payload: unknown) =>
      new Response(JSON.stringify(payload), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if ((init?.method ?? 'GET') === 'POST') {
          return coded('metadata_credential_invalid', 'the TMDB API key was rejected');
        }
        if (url.includes('/adult/search')) {
          return ok({
            sites: [
              {
                stash_id: 'site-1',
                name: 'Brazzers',
                aliases: [],
                parent_name: '',
                url: '',
                image_url: '',
                in_library: false,
                library_id: 0,
              },
            ],
          });
        }
        return ok({
          movies: [
            {
              tmdb_id: 1,
              title: 'Dune',
              year: 2021,
              overview: '',
              release_date: '2021-10-22',
              vote_average: 8.1,
              vote_count: 12_341,
              poster_url: '',
            },
          ],
          series: [],
        });
      }),
    );
    mountModal();

    await search('dune');
    (host!.querySelector('ul button') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();
    expect(host!.textContent).toContain('TMDB rejected this API key');

    // Tab twice: Movies → Series → Adult. The query never changes, so the
    // effect that clears the add fault on a new query does not fire.
    const input = host!.querySelector('input[type="search"]') as HTMLInputElement;
    for (let i = 0; i < 2; i++) {
      input.dispatchEvent(
        new KeyboardEvent('keydown', { key: 'Tab', cancelable: true, bubbles: true }),
      );
      flushSync();
    }
    await vi.advanceTimersByTimeAsync(300);
    flushSync();

    expect(host!.textContent).toContain('Brazzers');
    expect(host!.textContent).not.toContain('TMDB rejected this API key');
  });

  // A provider that is simply unhappy is not a credential problem, and the
  // dialog must not send anyone to a settings screen that is already correct.
  it('leaves an uncoded provider failure as the error it is', async () => {
    vi.useFakeTimers();
    stub(
      () =>
        new Response(JSON.stringify({ error: 'tmdb: http 500' }), {
          status: 502,
          headers: { 'Content-Type': 'application/json' },
        }),
    );
    mountModal();

    await search('dune');

    expect(host!.textContent).toContain('tmdb: http 500');
    expect(host!.textContent).not.toContain('Settings / Metadata');
  });
});

describe('AddItemModal — target library', () => {
  afterEach(() => {
    if (app) unmount(app);
    app = undefined;
    host?.remove();
    host = undefined;
    vi.unstubAllGlobals();
    vi.useRealTimers();
    clearToasts();
  });

  function lib(over: Record<string, unknown>) {
    return {
      id: 1,
      kind: 'tv',
      name: 'Series',
      root_path: 'library/TV',
      provider: 'tmdb',
      is_default: true,
      item_count: 0,
      dlna_visible: true,
      route_torrent: '',
      route_usenet: '',
      quality_profile_id: 0,
      indexers: [],
      ...over,
    };
  }

  it('offers a library select when a kind has several and sends the pick', async () => {
    vi.useFakeTimers();
    const calls: { url: string; method: string; body: unknown }[] = [];
    const series = [
      {
        tmdb_id: 2,
        title: 'Frieren',
        year: 2023,
        overview: '',
        first_air_date: '2023-09-29',
        vote_average: 8.9,
        vote_count: 1000,
        poster_url: '',
      },
    ];
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
        let payload: unknown = { movies: [], series };
        let status = 200;
        if (url.includes('/libraries')) {
          payload = {
            libraries: [
              lib({}),
              lib({ id: 9, name: 'Anime', root_path: 'library/Anime', is_default: false }),
            ],
          };
        } else if (method === 'POST') {
          payload = { id: 7, title: 'Frieren' };
          status = 201;
        }
        return new Response(JSON.stringify(payload), {
          status,
          headers: { 'Content-Type': 'application/json' },
        });
      }),
    );

    const { libraries } = await import('../state/libraries.svelte');
    await libraries.load(true);

    mountModal({ initialKind: 'series' });
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    const select = host!.querySelector(
      'select[aria-label="Target library"]',
    ) as HTMLSelectElement;
    expect(select, 'library select').not.toBeNull();
    select.value = '9';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();

    const input = host!.querySelector('input[type="search"]') as HTMLInputElement;
    input.value = 'frieren';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
    flushSync();

    const add = host!.querySelector('ul button') as HTMLButtonElement;
    add.click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/api/v1/library/series');
    expect(post?.body).toMatchObject({ tmdb_id: 2, library_id: 9 });
  });
});

/**
 * Merged provider chains (PLAN phase 8).
 *
 * A library may name several metadata providers, and then one search is
 * several providers' answers in one list. Three things change, and each is a
 * way the single-provider dialog was quietly wrong:
 *
 *   - identity. Every hit from a provider that is not TMDB carries tmdb_id 0,
 *     so the old `(row.tmdb_id)` key gave a whole AniList page the same key
 *     and Svelte refused to render the list at all.
 *   - the question. The chain belongs to a library, so changing the target
 *     library asks a different set of providers and must re-run the search.
 *   - the answer. An add says which provider identified the title, because
 *     "154587" means different titles to different providers.
 */
describe('AddItemModal — provider chains', () => {
  const PROVIDERS = [
    { id: 'tmdb', name: 'TMDB', kinds: ['movie', 'tv'] },
    { id: 'anilist', name: 'AniList', kinds: ['tv'] },
  ];

  /** Two providers' hits in one list, the AniList ones with no TMDB id at all. */
  const CHAIN_SERIES = [
    {
      tmdb_id: 95_479,
      provider: 'tmdb',
      provider_ref: '95479',
      title: 'Jujutsu Kaisen',
      year: 2020,
      overview: '',
      first_air_date: '2020-10-03',
      vote_average: 8.6,
      vote_count: 3_000,
      poster_url: '',
    },
    {
      tmdb_id: 0,
      provider: 'anilist',
      provider_ref: '113415',
      title: 'Jujutsu Kaisen (AniList)',
      year: 2020,
      overview: '',
      first_air_date: '2020-10-03',
      vote_average: 8.6,
      vote_count: 3_000,
      poster_url: '',
    },
    {
      tmdb_id: 0,
      provider: 'anilist',
      provider_ref: '145064',
      title: 'Jujutsu Kaisen 2nd Season',
      year: 2023,
      overview: '',
      first_air_date: '2023-07-06',
      vote_average: 8.7,
      vote_count: 1_200,
      poster_url: '',
    },
  ];

  function lib(over: Record<string, unknown>) {
    return {
      id: 1,
      kind: 'tv',
      name: 'Series',
      root_path: 'library/TV',
      provider: 'tmdb',
      providers: ['tmdb'],
      is_default: true,
      item_count: 0,
      dlna_visible: true,
      route_torrent: '',
      route_usenet: '',
      quality_profile_id: 0,
      indexers: [],
      ...over,
    };
  }

  const LIBRARIES = [
    lib({}),
    lib({
      id: 9,
      name: 'Anime',
      root_path: 'library/Anime',
      is_default: false,
      provider: 'tmdb',
      providers: ['tmdb', 'anilist'],
    }),
  ];

  interface Call {
    url: string;
    method: string;
    body: unknown;
  }

  let calls: Call[];

  /**
   * Answers /search with the merged chain, /libraries and
   * /libraries/providers with the fixtures above, and any POST with a created
   * item. Every request is recorded so a test can assert what was asked.
   */
  function stubChain(): Call[] {
    calls = [];
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
        if (method === 'POST') return json({ id: 7, title: 'Jujutsu Kaisen' }, 201);
        if (url.includes('/libraries/providers')) return json({ providers: PROVIDERS });
        if (url.includes('/libraries')) return json({ libraries: LIBRARIES });
        return json({
          movies: [],
          series: CHAIN_SERIES,
          providers: ['tmdb', 'anilist'],
          library_id: 9,
          errors: [],
        });
      }),
    );
    return calls;
  }

  const searches = () => calls.filter((c) => c.url.includes('/api/v1/search'));

  async function loadStores() {
    const [{ libraries }, { providers }] = await Promise.all([
      import('../state/libraries.svelte'),
      import('../state/providers.svelte'),
    ]);
    providers.all = [];
    providers.loaded = false;
    await libraries.load(true);
  }

  async function type(text: string) {
    const input = host!.querySelector('input[type="search"]') as HTMLInputElement;
    input.value = text;
    input.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
    flushSync();
  }

  function targetSelect(): HTMLSelectElement {
    const found = host!.querySelector('select[aria-label="Target library"]');
    expect(found, 'library select').not.toBeNull();
    return found as HTMLSelectElement;
  }

  afterEach(async () => {
    const { providers } = await import('../state/providers.svelte');
    providers.all = [];
    providers.loaded = false;
  });

  it('renders every chain hit and names the provider that answered', async () => {
    vi.useFakeTimers();
    stubChain();
    await loadStores();
    mountModal({ initialKind: 'series' });
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    await type('jujutsu');

    // Three rows, two of which share tmdb_id 0: keying on it made this list
    // impossible to render at all.
    const rows = [...host!.querySelectorAll('ul li')];
    expect(rows).toHaveLength(3);
    // Named, not id'd: the badge reads the provider list, not the raw token.
    expect(rows[0]!.textContent).toContain('TMDB');
    expect(rows[1]!.textContent).toContain('AniList');
    expect(rows[2]!.textContent).toContain('AniList');
    // The copy stops naming one provider once several are answering.
    expect(host!.querySelector('input[type="search"]')?.getAttribute('aria-label')).toBe(
      'Search metadata providers',
    );
  });

  it('leaves a single-provider chain unbadged and its copy naming TMDB', async () => {
    vi.useFakeTimers();
    calls = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        calls.push({ url, method: init?.method ?? 'GET', body: null });
        const json = (payload: unknown) =>
          new Response(JSON.stringify(payload), {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          });
        if (url.includes('/libraries')) return json({ libraries: [lib({})] });
        return json({
          movies: [],
          series: [CHAIN_SERIES[0]],
          providers: ['tmdb'],
          library_id: 1,
          errors: [],
        });
      }),
    );
    await loadStores();
    mountModal({ initialKind: 'series' });
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    await type('jujutsu');

    expect(host!.querySelector('ul li')!.textContent).not.toContain('TMDB');
    // Not even fetched: the provider list is admin-only, and nothing on screen
    // needs a provider's name.
    expect(calls.some((c) => c.url.includes('/libraries/providers'))).toBe(false);
    expect(host!.querySelector('input[type="search"]')?.getAttribute('aria-label')).toBe(
      'Search TMDB',
    );
  });

  it('re-asks the search when the target library changes, naming the new one', async () => {
    vi.useFakeTimers();
    stubChain();
    await loadStores();
    mountModal({ initialKind: 'series' });
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    await type('jujutsu');
    // The default library needs no id: an absent library_id already means it.
    expect(searches().at(-1)!.url).toBe('/api/v1/search?q=jujutsu&type=series');

    const select = targetSelect();
    select.value = '9';
    select.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();
    await vi.advanceTimersByTimeAsync(300);
    flushSync();

    // Not just one more request: the same query asked of a different chain.
    expect(searches()).toHaveLength(2);
    expect(searches().at(-1)!.url).toBe('/api/v1/search?q=jujutsu&type=series&library_id=9');
  });

  it('adds the ref pair the picked row carries, beside the compat tmdb_id', async () => {
    vi.useFakeTimers();
    stubChain();
    await loadStores();
    mountModal({ initialKind: 'series' });
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    await type('jujutsu');

    // The second row: an AniList hit, which tmdb_id alone could not name.
    const add = [...host!.querySelectorAll<HTMLButtonElement>('ul button')][1]!;
    add.click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    const post = calls.find((c) => c.method === 'POST');
    expect(post?.url).toBe('/api/v1/library/series');
    expect(post?.body).toMatchObject({
      tmdb_id: 0,
      provider: 'anilist',
      provider_ref: '113415',
    });
  });

  it('sends no half pair for a stub from before provider refs existed', async () => {
    vi.useFakeTimers();
    calls = [];
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
        if (method === 'POST') return json({ id: 7, title: 'Jujutsu Kaisen' }, 201);
        if (url.includes('/libraries')) return json({ libraries: [lib({})] });
        // No provider, no ref: what a server from before chains answered with.
        return json({
          movies: [],
          series: [{ ...CHAIN_SERIES[0], provider: '', provider_ref: '' }],
          providers: [],
          library_id: 0,
          errors: [],
        });
      }),
    );
    await loadStores();
    mountModal({ initialKind: 'series' });
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    await type('jujutsu');
    (host!.querySelector('ul button') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(0);
    flushSync();

    // Half a pair is a 400, so a row with neither half sends neither.
    const body = calls.find((c) => c.method === 'POST')?.body as Record<string, unknown>;
    expect(body).toMatchObject({ tmdb_id: 95_479 });
    expect(body.provider).toBeUndefined();
    expect(body.provider_ref).toBeUndefined();
  });
});
