/**
 * The shared search rail (plan part B7).
 *
 * Two things here are promises rather than decoration: Enter searches (a box
 * you have to reach for a button to use is not a search box), and the adult
 * category block is absent unless the module is visible to this account — the
 * same single boolean every other adult surface reads.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ReleaseSearchControls from './ReleaseSearchControls.svelte';
import type { Indexer } from '../api/types';
import { session } from '../state/session.svelte';

function indexer(overrides: Partial<Indexer> = {}): Indexer {
  return {
    id: 1,
    name: 'Test Indexer',
    url: 'http://localhost',
    has_api_key: true,
    type: 'torznab',
    categories: [2000, 5000],
    priority: 0,
    enabled: true,
    ...overrides,
  };
}

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;

function mountControls(props: Partial<{
  query: string;
  categories: number[];
  indexerIDs: number[];
  indexers: Indexer[];
  busy: boolean;
  onsearch: () => void;
  contextLabel: string;
  onhelp: () => void;
}> = {}): HTMLElement {
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(ReleaseSearchControls, {
    target: host,
    props: {
      query: '',
      categories: [],
      indexerIDs: [],
      indexers: [indexer()],
      busy: false,
      onsearch: () => {},
      ...props,
    },
  }) as Record<string, unknown>;
  flushSync();
  return host;
}

/** Open a pill by its trigger label and read the option list inside it. */
function openPill(label: string): string[] {
  const trigger = [...host!.querySelectorAll<HTMLButtonElement>('button')].find((b) =>
    b.textContent?.includes(label),
  );
  trigger!.click();
  flushSync();
  return [...host!.querySelectorAll<HTMLElement>('[role="dialog"] li button')].map(
    (b) => b.textContent?.trim() ?? '',
  );
}

afterEach(() => {
  if (app) unmount(app);
  host?.remove();
  app = undefined;
  host = undefined;
  // A session leaking into the next test would decide whether the adult block
  // is offered there; null is "we do not know yet", which is not granted.
  session.user = null;
});

describe('ReleaseSearchControls', () => {
  it('searches on Enter in the query box', () => {
    let searched = 0;
    mountControls({ query: 'blade runner', onsearch: () => (searched += 1) });
    const input = host!.querySelector('input')!;
    const event = new KeyboardEvent('keydown', { key: 'Enter', cancelable: true, bubbles: true });
    input.dispatchEvent(event);
    flushSync();
    expect(searched).toBe(1);
    // The default is a form submit that would reload the SPA.
    expect(event.defaultPrevented).toBe(true);
  });

  it('clears the query with its own in-field button, never the native one', () => {
    mountControls({ query: 'blade runner', onhelp: () => {} });
    const clear = host!.querySelector<HTMLButtonElement>('[data-clear-search]')!;
    expect(clear).not.toBeNull();
    expect(clear.getAttribute('aria-label')).toBe('Clear search');
    // It shares the field with the help button, in one control cluster.
    expect(clear.parentElement?.querySelector('[data-syntax-toggle]')).not.toBeNull();

    clear.click();
    flushSync();
    expect(host!.querySelector('input')!.value).toBe('');
    // Nothing left to clear, so the button withdraws.
    expect(host!.querySelector('[data-clear-search]')).toBeNull();
  });

  it('offers the adult category block only while the module is visible', () => {
    session.user = { username: 'admin', role: 'admin', open: false, adult: true } as never;
    mountControls();
    expect(openPill('All categories')).toContain('6000 XXX');
  });

  it('hides the adult category block from an account the module is absent to', () => {
    session.user = { username: 'admin', role: 'admin', open: false, adult: false } as never;
    mountControls();
    const options = openPill('All categories');
    expect(options).not.toContain('6000 XXX');
    expect(options).toContain('5000 TV');
  });

  it('offers only enabled indexers, since a disabled one is not searched', () => {
    mountControls({
      indexers: [indexer({ id: 1, name: 'Live' }), indexer({ id: 2, name: 'Off', enabled: false })],
    });
    const options = openPill('All indexers');
    expect(options).toEqual(['Live']);
  });

  it('names the empty selection as "all", not as nothing', () => {
    mountControls();
    expect(host!.textContent).toContain('All categories');
    expect(host!.textContent).toContain('All indexers');
  });

  it('counts a narrowed selection on the trigger', () => {
    mountControls({ categories: [2000, 5000], indexerIDs: [1] });
    expect(host!.textContent).toContain('2 categories');
    expect(host!.textContent).toContain('1 indexer');
  });

  it('shows a locked grab context that offers no way to remove it', () => {
    mountControls({ contextLabel: 'Movie · Blade Runner 2049' });
    const chip = host!.querySelector('[data-search-context]');
    expect(chip?.textContent).toContain('Movie · Blade Runner 2049');
    expect(chip?.querySelector('button')).toBeNull();
  });

  it('renders no context chip when nothing is locked', () => {
    mountControls();
    expect(host!.querySelector('[data-search-context]')).toBeNull();
  });
});
