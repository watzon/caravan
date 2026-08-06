/**
 * The home-shelf carousel. What matters here is that the shelf serves the
 * whole payload — it used to slice a 7-card sample, and a regression would
 * silently hide two thirds of what the API fetched — and that the paging
 * arrows track where the scroller actually is, because a dead arrow reads as
 * "the list ends here".
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DiscoverShelf from './DiscoverShelf.svelte';
import type { DiscoverItem } from '../api/types';

function item(tmdbID: number): DiscoverItem {
  return {
    media_type: 'movie',
    tmdb_id: tmdbID,
    title: `Title ${tmdbID}`,
    year: 2020,
    overview: '',
    poster_path: '',
    poster_url: '',
    backdrop_url: '',
    vote_average: 7,
    date: '2020-01-01',
    in_library: false,
    library_id: 0,
    requested: false,
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;

function show(items: DiscoverItem[]) {
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(DiscoverShelf, { target: host, props: { title: 'Trending', items } });
  flushSync();
}

/**
 * jsdom has no layout, so the scroller reports zero everywhere. Pin the
 * geometry of a row that is wider than its viewport, then let a scroll event
 * tell the component to look again.
 */
function scroller(scrollLeft: number): HTMLElement {
  const el = host.querySelector('.overflow-x-auto') as HTMLElement;
  Object.defineProperty(el, 'clientWidth', { configurable: true, value: 600 });
  Object.defineProperty(el, 'scrollWidth', { configurable: true, value: 1800 });
  Object.defineProperty(el, 'scrollLeft', { configurable: true, value: scrollLeft, writable: true });
  el.dispatchEvent(new Event('scroll'));
  flushSync();
  return el;
}

function arrow(label: string): HTMLButtonElement | null {
  return host.querySelector(`button[aria-label="${label}"]`);
}

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.restoreAllMocks();
});

describe('DiscoverShelf', () => {
  it('renders every item it is given, not a sample', () => {
    show(Array.from({ length: 25 }, (_, i) => item(i + 1)));

    expect(host.querySelectorAll('a[href^="/discover/"]')).toHaveLength(25);
  });

  it('shows no arrows while the row fits its viewport', () => {
    show([item(1), item(2)]);

    // jsdom's zero-width geometry doubles as "nothing overflows".
    expect(host.querySelectorAll('button')).toHaveLength(0);
  });

  it('keeps rapid paging clicks instead of dropping them during smooth scroll', () => {
    show(Array.from({ length: 12 }, (_, i) => item(i + 1)));
    const el = scroller(0);
    const scrollTo = vi.fn();
    el.scrollTo = scrollTo;

    // At the far left only the right arrow is live.
    expect(arrow('Scroll Trending left')?.disabled).toBe(true);
    const right = arrow('Scroll Trending right');
    expect(right?.disabled).toBe(false);

    right?.click();
    right?.click();
    expect(scrollTo.mock.calls).toEqual([
      [{ left: 600 * 0.9, behavior: 'smooth' }],
      [{ left: 600 * 0.9 * 2, behavior: 'smooth' }],
    ]);

    el.scrollLeft = 0.5;
    el.dispatchEvent(new Event('scroll'));
    flushSync();
    expect(arrow('Scroll Trending left')?.disabled).toBe(false);

    // At the far right the arrows trade places.
    el.scrollLeft = 1200;
    el.dispatchEvent(new Event('scroll'));
    flushSync();
    expect(arrow('Scroll Trending left')?.disabled).toBe(false);
    expect(arrow('Scroll Trending right')?.disabled).toBe(true);
  });
});
