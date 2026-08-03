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
});
