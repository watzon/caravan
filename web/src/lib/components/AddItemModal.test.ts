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

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;

function mountModal(props: {
  kind?: 'movie' | 'series' | null;
  initialKind?: 'movie' | 'series';
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
