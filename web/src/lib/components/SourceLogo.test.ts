/**
 * The shelf mark must install its tri-tone filters once and still render as
 * a normal image, so the Discover tiles can keep querying `img`.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import SourceLogo from './SourceLogo.svelte';

const DARK_FILTER = 'source-logo-tritone-dark';
const LIGHT_FILTER = 'source-logo-tritone-light';

let target: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;

function mountLogo(src: string): HTMLElement {
  target = document.createElement('div');
  document.body.appendChild(target);
  app = mount(SourceLogo, { target, props: { src } }) as Record<string, unknown>;
  flushSync();
  return target;
}

afterEach(() => {
  if (app) unmount(app);
  target?.remove();
  app = undefined;
  target = undefined;
  document.getElementById(DARK_FILTER)?.closest('svg')?.remove();
});

describe('SourceLogo', () => {
  it('renders the mark and installs both theme filters once', () => {
    const first = mountLogo('https://images.test/w185/marvel.png');
    expect(first.querySelector('img')?.getAttribute('src')).toBe(
      'https://images.test/w185/marvel.png',
    );
    expect(document.getElementById(DARK_FILTER)).not.toBeNull();
    expect(document.getElementById(LIGHT_FILTER)).not.toBeNull();

    const secondTarget = document.createElement('div');
    document.body.appendChild(secondTarget);
    const second = mount(SourceLogo, {
      target: secondTarget,
      props: { src: 'https://images.test/w185/a24.png' },
    });
    flushSync();
    expect(document.querySelectorAll(`#${DARK_FILTER}`)).toHaveLength(1);
    unmount(second);
    secondTarget.remove();
  });

  it('silhouettes a flat mark and keeps the tri-tone on a lockup', () => {
    const flat = mountLogo('https://images.test/w185/hulu.png');
    expect(flat.querySelector('img')?.className).toContain('source-logo-flat');

    if (app) unmount(app);
    target?.remove();
    const lockup = mountLogo('https://image.tmdb.org/t/p/w185/hUzeosd33nzE5MCNsZxCGEKTXaQ.png');
    expect(lockup.querySelector('img')?.className).toContain('source-logo');
    expect(lockup.querySelector('img')?.className).not.toContain('source-logo-flat');
  });
});
