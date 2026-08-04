/**
 * The ⋯ menu's own contract, tested once here rather than three times through
 * the detail pages that use it: what it opens, the three ways it closes, and
 * where focus lands afterwards.
 *
 * A menu that stays open is the failure that matters — it would float over the
 * dialog its own item opened — so each dismissal path gets its own case.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import OverflowMenu from './OverflowMenu.svelte';
import type { MenuItem } from '../menu';

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;
let chosen: string[] = [];

function mountMenu(items: MenuItem[] = defaultItems()): HTMLElement {
  chosen = [];
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(OverflowMenu, {
    target: host,
    props: { subject: 'Dune', items },
  }) as Record<string, unknown>;
  flushSync();
  return host;
}

function defaultItems(): MenuItem[] {
  return [
    {
      label: 'Do something',
      onselect: () => {
        chosen.push('something');
      },
    },
    {
      label: 'Remove from library…',
      danger: true,
      onselect: () => {
        chosen.push('remove');
      },
    },
  ];
}

function trigger(): HTMLButtonElement {
  return host!.querySelector<HTMLButtonElement>('button[aria-haspopup="menu"]')!;
}

function items(): HTMLButtonElement[] {
  return [...host!.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')];
}

function open() {
  trigger().click();
  flushSync();
}

afterEach(() => {
  if (app) unmount(app);
  host?.remove();
  app = undefined;
  host = undefined;
  chosen = [];
  vi.unstubAllGlobals();
});

describe('OverflowMenu', () => {
  it('is closed until it is asked for, and says so', () => {
    mountMenu();
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
    expect(host!.querySelector('[role="menu"]')).toBeNull();
    // Named for a screen reader by what it acts on, not "more".
    expect(trigger().getAttribute('aria-label')).toBe('More actions for Dune');
  });

  it('opens with its items and focuses the first one', () => {
    mountMenu();
    open();

    expect(trigger().getAttribute('aria-expanded')).toBe('true');
    expect(items().map((i) => i.textContent?.trim())).toEqual([
      'Do something',
      'Remove from library…',
    ]);
    // A menu the keyboard has to Tab into is a menu it cannot really reach.
    expect(document.activeElement).toBe(items()[0]);
  });

  it('runs the chosen item and closes behind it', () => {
    mountMenu();
    open();
    items()[1]!.click();
    flushSync();

    expect(chosen).toEqual(['remove']);
    // Closed BEFORE the item ran: an item that opens a dialog must not leave a
    // menu floating over it.
    expect(host!.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger());
  });

  it('closes on Escape and gives the trigger its focus back', () => {
    mountMenu();
    open();

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();

    expect(host!.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger());
    expect(chosen).toEqual([]);
  });

  it('closes when the pointer goes down anywhere else', () => {
    mountMenu();
    open();

    const elsewhere = document.createElement('button');
    document.body.appendChild(elsewhere);
    elsewhere.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    flushSync();

    expect(host!.querySelector('[role="menu"]')).toBeNull();
    elsewhere.remove();
  });

  it('stays open when the pointer goes down inside it', () => {
    mountMenu();
    open();

    items()[0]!.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true }));
    flushSync();

    expect(host!.querySelector('[role="menu"]')).not.toBeNull();
  });

  it('walks its items with the arrow keys', () => {
    mountMenu();
    open();

    const [first, second] = items();
    first!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    flushSync();
    expect(document.activeElement).toBe(second);

    second!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }));
    flushSync();
    expect(document.activeElement).toBe(first);
  });

  it('does not run a disabled item', () => {
    mountMenu([
      {
        label: 'Remove from library…',
        danger: true,
        disabled: true,
        onselect: () => {
          chosen.push('remove');
        },
      },
    ]);
    open();

    items()[0]!.click();
    flushSync();
    expect(chosen).toEqual([]);
  });
});
