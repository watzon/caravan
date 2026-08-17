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
    vote_count: 1,
    date: '2020-01-01',
    in_library: false,
    library_id: 0,
    requested: false,
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;

function show(items: DiscoverItem[], href = '') {
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(DiscoverShelf, {
    target: host,
    props: { title: 'Trending', items, href },
  });
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
  it('keeps the wide row inside a shrinkable scroller', () => {
    show(Array.from({ length: 12 }, (_, i) => item(i + 1)));

    const row = host.querySelector<HTMLElement>('.overflow-x-auto');
    expect(row?.classList.contains('w-full')).toBe(true);
    expect(row?.classList.contains('min-w-0')).toBe(true);
    expect(row?.classList.contains('max-w-full')).toBe(true);
    expect(row?.parentElement?.classList.contains('min-w-0')).toBe(true);
  });

  it('turns the heading into a link when the shelf has a wider view', () => {
    show([item(1)], '/discover/movies?view=grid');

    const heading = host.querySelector('h2 a');
    expect(heading?.getAttribute('href')).toBe('/discover/movies?view=grid');
    expect(heading?.textContent).toBe('Trending');
  });

  it('renders every item it is given, not a sample', () => {
    show(Array.from({ length: 25 }, (_, i) => item(i + 1)));

    expect(host.querySelectorAll('a[href^="/discover/"]')).toHaveLength(25);
  });

  it('keeps a card’s full title, accessible name, rating, and status in text', () => {
    const fullTitle = 'A Discover Card Title That Is Longer Than Its Poster Column';
    show([
      { ...item(1), title: fullTitle, in_library: true },
      { ...item(2), requested: true },
      { ...item(3), year: 0 },
    ]);

    const card = host.querySelector<HTMLAnchorElement>('a[href="/discover/movie/1"]')!;
    expect(card.getAttribute('aria-label')).toBe(
      `${fullTitle} (2020), Rated 7.0/10, In library`,
    );
    expect(card.querySelector('.truncate')?.getAttribute('title')).toBe(fullTitle);
    expect(card.querySelector('[title="Rated 7.0/10"]')).not.toBeNull();
    expect(card.textContent).toContain('In library');
    expect(
      host.querySelector('a[href="/discover/movie/2"]')?.getAttribute('aria-label'),
    ).toContain('Requested');
    expect(
      host.querySelector('a[href="/discover/movie/3"]')?.getAttribute('aria-label'),
    ).toContain('Title 3, year unknown');
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

  it('pages from the current position after user scrolling interrupts a pending page', () => {
    show(Array.from({ length: 12 }, (_, i) => item(i + 1)));
    const el = scroller(0);
    const scrollTo = vi.fn();
    el.scrollTo = scrollTo;

    arrow('Scroll Trending right')?.click();
    expect(scrollTo).toHaveBeenLastCalledWith({ left: 540, behavior: 'smooth' });

    // Input arrives before the browser reports the manually changed position.
    el.dispatchEvent(new Event('wheel'));
    el.scrollLeft = 240;
    el.dispatchEvent(new Event('scroll'));
    flushSync();

    arrow('Scroll Trending right')?.click();
    expect(scrollTo).toHaveBeenLastCalledWith({ left: 780, behavior: 'smooth' });
  });

  it('reconciles a pending page once the browser reaches its destination', () => {
    show(Array.from({ length: 12 }, (_, i) => item(i + 1)));
    const el = scroller(0);
    const scrollTo = vi.fn();
    el.scrollTo = scrollTo;

    arrow('Scroll Trending right')?.click();
    el.scrollLeft = 540;
    el.dispatchEvent(new Event('scroll'));
    flushSync();

    el.scrollLeft = 240;
    el.dispatchEvent(new Event('scroll'));
    flushSync();
    arrow('Scroll Trending right')?.click();

    expect(scrollTo).toHaveBeenLastCalledWith({ left: 780, behavior: 'smooth' });
  });
});
