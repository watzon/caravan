/**
 * Poster fallback chain: local artwork first, provider URL when the local
 * file fails to load (a stale poster_path after the library tree vanished),
 * icon only when both are unusable.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Poster from './Poster.svelte';

let target: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;

function mountPoster(props: { path?: string | null; fallback?: string | null }): HTMLElement {
  target = document.createElement('div');
  document.body.appendChild(target);
  app = mount(Poster, {
    target,
    props: { alt: 'poster', ...props },
  }) as Record<string, unknown>;
  flushSync();
  return target;
}

function img(): HTMLImageElement | null {
  return target?.querySelector('img') ?? null;
}

afterEach(() => {
  if (app) unmount(app);
  target?.remove();
  app = undefined;
  target = undefined;
});

describe('Poster', () => {
  it('serves local artwork through the image endpoint', () => {
    mountPoster({ path: 'library/Movies/B (2008)/poster.jpg' });
    expect(img()?.getAttribute('src')).toBe('/api/v1/images/library/Movies/B%20(2008)/poster.jpg');
  });

  it('falls back to the provider URL when the local file fails to load', () => {
    mountPoster({
      path: 'library/Movies/B (2008)/poster.jpg',
      fallback: 'https://image.tmdb.org/t/p/w500/abc.jpg',
    });
    const first = img();
    expect(first).not.toBeNull();
    first!.dispatchEvent(new Event('error'));
    flushSync();
    expect(img()?.getAttribute('src')).toBe('https://image.tmdb.org/t/p/w500/abc.jpg');
  });

  it('shows the icon only after every source fails', () => {
    mountPoster({
      path: 'library/Movies/B (2008)/poster.jpg',
      fallback: 'https://image.tmdb.org/t/p/w500/abc.jpg',
    });
    img()!.dispatchEvent(new Event('error'));
    flushSync();
    img()!.dispatchEvent(new Event('error'));
    flushSync();
    expect(img()).toBeNull();
    expect(target!.querySelector('svg')).not.toBeNull();
  });

  it('uses the provider URL directly when there is no local poster', () => {
    mountPoster({ path: '', fallback: 'https://image.tmdb.org/t/p/w500/abc.jpg' });
    expect(img()?.getAttribute('src')).toBe('https://image.tmdb.org/t/p/w500/abc.jpg');
  });
});
