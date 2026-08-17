/**
 * Discover landing shelves. The curated tiles are the part a helper cannot
 * cover: a logo must render when the payload has one, and the name must stay
 * when it does not.
 */
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { DiscoverHome, DiscoverSource } from '../api/types';
import { discover } from '../state/discover.svelte';
import Discover from './Discover.svelte';

function source(extra: Partial<DiscoverSource> = {}): DiscoverSource {
  return {
    id: 213,
    name: 'Netflix',
    type: 'network',
    logo_url: '',
    ...extra,
  };
}

function home(extra: Partial<DiscoverHome> = {}): DiscoverHome {
  return {
    trending: [],
    popular_movies: [],
    upcoming_movies: [],
    now_playing: [],
    popular_series: [],
    upcoming_series: [],
    airing_series: [],
    movie_genres: [],
    series_genres: [],
    networks: [],
    studios: [],
    ...extra,
  };
}

let host: HTMLElement;
let app: Record<string, unknown> | undefined;

beforeEach(() => {
  discover.reset();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  discover.reset();
});

describe('Discover source tiles', () => {
  it('puts studios before networks, and genre tiles on each section', () => {
    discover.home = home({
      movie_genres: [{ tmdb_id: 28, name: 'Action' }],
      series_genres: [{ tmdb_id: 18, name: 'Drama' }],
      studios: [source({ id: 41077, name: 'A24', type: 'studio' })],
      networks: [source({ id: 213, name: 'Netflix', type: 'network' })],
    });
    app = mount(Discover, { target: host }) as Record<string, unknown>;
    flushSync();

    const text = host.textContent ?? '';
    expect(text.indexOf('Browse by studio')).toBeGreaterThan(-1);
    expect(text.indexOf('Browse by network')).toBeGreaterThan(text.indexOf('Browse by studio'));
    expect(text.indexOf('Movie genres')).toBeLessThan(text.indexOf('Browse by studio'));
    expect(text.indexOf('Series genres')).toBeLessThan(text.indexOf('Browse by network'));
    expect(host.querySelector('a[href="/discover/movies?genres=28%3AAction"]')?.textContent).toContain(
      'Action',
    );
    expect(host.querySelector('a[href="/discover/series?genres=18%3ADrama"]')?.textContent).toContain(
      'Drama',
    );
  });

  it('renders a logo when the shelf has one', () => {
    const logo = 'https://images.test/w185/netflix.png';
    discover.home = home({
      networks: [source({ logo_url: logo })],
    });
    app = mount(Discover, { target: host }) as Record<string, unknown>;
    flushSync();

    const tile = host.querySelector('a[href="/discover/network/213"]');
    const img = tile?.querySelector('img');
    expect(img?.getAttribute('src')).toBe(logo);
    expect(img?.getAttribute('alt')).toBe('');
    expect(tile?.textContent).toContain('Netflix');
    expect(tile?.getAttribute('title')).toBe('Netflix');
  });

  it('keeps the name when the shelf has no logo', () => {
    discover.home = home({
      studios: [source({ id: 41077, name: 'A24', type: 'studio' })],
    });
    app = mount(Discover, { target: host }) as Record<string, unknown>;
    flushSync();

    const tile = host.querySelector('a[href="/discover/studio/41077"]');
    expect(tile?.querySelector('img')).toBeNull();
    expect(tile?.textContent).toContain('A24');
  });
});
