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
  vi.restoreAllMocks();
});

describe('OverflowMenu', () => {
  it('is closed until it is asked for, and says so', () => {
    mountMenu();
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
    expect(host!.querySelector('[role="menu"]')).toBeNull();
    // Named for a screen reader by what it acts on, not "more".
    expect(trigger().getAttribute('aria-label')).toBe('More actions for Dune');
    expect(trigger().title).toBe('More actions for Dune');
  });

  it('opens with its items and focuses the first one', () => {
    mountMenu();
    open();

    expect(trigger().getAttribute('aria-expanded')).toBe('true');
    expect(host!.querySelector('[role="menu"]')?.getAttribute('tabindex')).toBe('-1');
    expect(host!.querySelector('[role="menu"]')?.getAttribute('id'))
      .toBe(trigger().getAttribute('aria-controls'));
    expect(host!.querySelector('[role="menu"]')?.getAttribute('aria-label'))
      .toBe('More actions for Dune');
    expect(items().map((i) => i.textContent?.trim())).toEqual([
      'Do something',
      'Remove from library…',
    ]);
    // A menu the keyboard has to Tab into is a menu it cannot really reach.
    expect(document.activeElement).toBe(items()[0]);
  });

  it('keeps the menu within phone gutters and right-aligns it on desktop', () => {
    const viewportWidth = vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(320);
    mountMenu();
    open();
    const menu = host!.querySelector<HTMLElement>('[role="menu"]')!;
    Object.defineProperty(menu, 'offsetWidth', { configurable: true, value: 176 });
    const triggerRect = vi.spyOn(trigger(), 'getBoundingClientRect');
    triggerRect.mockReturnValue({
      x: 8,
      y: 40,
      width: 32,
      height: 32,
      top: 40,
      right: 40,
      bottom: 72,
      left: 8,
      toJSON: () => ({}),
    });

    window.dispatchEvent(new Event('resize'));
    flushSync();
    expect(menu.classList.contains('fixed')).toBe(true);
    expect(menu.style.left).toBe('16px');
    expect(Number.parseInt(menu.style.left, 10) + menu.offsetWidth).toBeLessThanOrEqual(304);

    triggerRect.mockReturnValue({
      x: 284,
      y: 40,
      width: 32,
      height: 32,
      top: 40,
      right: 316,
      bottom: 72,
      left: 284,
      toJSON: () => ({}),
    });
    window.dispatchEvent(new Event('resize'));
    flushSync();
    expect(menu.style.left).toBe('128px');
    expect(Number.parseInt(menu.style.left, 10) + menu.offsetWidth).toBeLessThanOrEqual(304);

    viewportWidth.mockReturnValue(1024);
    triggerRect.mockReturnValue({
      x: 868,
      y: 40,
      width: 32,
      height: 32,
      top: 40,
      right: 900,
      bottom: 72,
      left: 868,
      toJSON: () => ({}),
    });
    window.dispatchEvent(new Event('resize'));
    flushSync();
    expect(menu.style.left).toBe('724px');
  });

  it('dismisses when an inner app scroll container moves the trigger', () => {
    mountMenu();
    const scrollContainer = document.createElement('div');
    host!.appendChild(scrollContainer);
    const menuTrigger = trigger();
    const focus = vi.spyOn(menuTrigger, 'focus');
    open();

    scrollContainer.dispatchEvent(new Event('scroll'));
    flushSync();

    expect(host!.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(menuTrigger);
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  });

  it('opens from the trigger arrow keys at the expected edge', () => {
    mountMenu();
    trigger().focus();
    trigger().dispatchEvent(new KeyboardEvent('keydown', {
      key: 'ArrowUp',
      bubbles: true,
      cancelable: true,
    }));
    flushSync();
    expect(document.activeElement).toBe(items().at(-1));

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    flushSync();
    trigger().dispatchEvent(new KeyboardEvent('keydown', {
      key: 'ArrowDown',
      bubbles: true,
      cancelable: true,
    }));
    flushSync();
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

  it('dismisses when Tab leaves the menu', () => {
    mountMenu();
    open();

    items()[0]!.dispatchEvent(new KeyboardEvent('keydown', {
      key: 'Tab',
      bubbles: true,
      cancelable: true,
    }));
    flushSync();

    expect(host!.querySelector('[role="menu"]')).toBeNull();
    expect(document.activeElement).toBe(trigger());
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

  it('wraps arrow navigation and supports Home and End', () => {
    mountMenu();
    open();

    const [first, second] = items();
    first!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }));
    flushSync();
    expect(document.activeElement).toBe(second);

    second!.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
    flushSync();
    expect(document.activeElement).toBe(first);

    first!.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }));
    flushSync();
    expect(document.activeElement).toBe(second);

    second!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true }));
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
