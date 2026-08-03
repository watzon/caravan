/**
 * Tab seeding for the add flow: "Add series" must open on the Series tab
 * instead of always defaulting to Movies, while a fixed kind still locks the
 * picker down entirely (scan-review manual match). Keyboard contract: the
 * search field owns focus on open, and Tab flips the Movies/Series scope
 * instead of leaving the field.
 */
import { afterEach, describe, expect, it } from 'vitest';
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
});
