import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import LibraryFilter from './LibraryFilter.svelte';
import type { SessionLibrary } from '../api/types';
import { session } from '../state/session.svelte';

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;

function library(over: Partial<SessionLibrary> & { id: number }): SessionLibrary {
  return {
    kind: 'movie',
    name: `Library ${over.id}`,
    icon: '',
    slug: `lib-${over.id}`,
    ...over,
  };
}

function mountFilter(selected: number[] = [], onchange: (ids: number[]) => void = () => {}) {
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(LibraryFilter, { target: host, props: { selected, onchange } }) as Record<string, unknown>;
  flushSync();
  return host;
}

function openPill(label: string): HTMLButtonElement[] {
  const trigger = [...host!.querySelectorAll<HTMLButtonElement>('button')].find((button) =>
    button.textContent?.includes(label),
  );
  trigger!.click();
  flushSync();
  return [...host!.querySelectorAll<HTMLButtonElement>('[role="dialog"] li button')];
}

afterEach(() => {
  if (app) unmount(app);
  host?.remove();
  app = undefined;
  host = undefined;
  session.user = null;
});

describe('LibraryFilter', () => {
  it('stays hidden when the session has fewer than two libraries', () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      libraries: [library({ id: 1, name: 'Movies' })],
    };
    mountFilter();
    expect(host!.querySelector('button')).toBeNull();
  });

  it('names the empty selection as all libraries', () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      libraries: [
        library({ id: 1, kind: 'movie', name: 'Movies' }),
        library({ id: 2, kind: 'tv', name: 'Series' }),
      ],
    };
    mountFilter();
    expect(host!.textContent).toContain('All libraries');
  });

  it('offers every session library, movies first', () => {
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: true,
      libraries: [
        library({ id: 9, kind: 'adult', name: 'Adult' }),
        library({ id: 2, kind: 'tv', name: 'Series' }),
        library({ id: 1, kind: 'movie', name: 'Movies' }),
      ],
    };
    mountFilter();
    expect(openPill('All libraries').map((button) => button.textContent?.trim())).toEqual([
      'Movies',
      'Series',
      'Adult',
    ]);
  });

  it('toggles libraries without replacing the rest of the selection', () => {
    const seen: number[][] = [];
    session.user = {
      username: 'root',
      role: 'admin',
      open: false,
      adult: false,
      libraries: [
        library({ id: 1, kind: 'movie', name: 'Movies' }),
        library({ id: 2, kind: 'tv', name: 'Series' }),
      ],
    };
    mountFilter([1], (ids) => seen.push(ids));
    const options = openPill('Movies');
    options[1]!.click();
    flushSync();
    expect(seen).toEqual([[1, 2]]);

    unmount(app!);
    app = mount(LibraryFilter, {
      target: host!,
      props: { selected: [1, 2], onchange: (ids: number[]) => seen.push(ids) },
    }) as Record<string, unknown>;
    flushSync();
    openPill('2 libraries')[0]!.click();
    flushSync();
    expect(seen.at(-1)).toEqual([2]);
  });
});
