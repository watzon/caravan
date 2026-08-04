/**
 * The monitored icon toggle. It replaced a labeled switch, so what is asserted
 * here is the part that had to survive losing the visible word: the state is
 * still announced, and the name says what a click will DO.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import MonitorButton from './MonitorButton.svelte';

let host: HTMLElement | undefined;
let app: Record<string, unknown> | undefined;
let changes: boolean[] = [];

function mountButton(props: { monitored: boolean; disabled?: boolean }): HTMLButtonElement {
  changes = [];
  host = document.createElement('div');
  document.body.appendChild(host);
  app = mount(MonitorButton, {
    target: host,
    props: { subject: 'Dune', onchange: (next: boolean) => changes.push(next), ...props },
  }) as Record<string, unknown>;
  flushSync();
  return host.querySelector('button')!;
}

afterEach(() => {
  if (app) unmount(app);
  host?.remove();
  app = undefined;
  host = undefined;
  changes = [];
  vi.unstubAllGlobals();
});

describe('MonitorButton', () => {
  it('is a toggle button, not a checkbox lookalike', () => {
    const button = mountButton({ monitored: true });
    expect(button.getAttribute('type')).toBe('button');
    expect(button.getAttribute('role')).toBeNull();
    expect(button.getAttribute('aria-pressed')).toBe('true');
  });

  it('names the action rather than only the state', () => {
    const on = mountButton({ monitored: true });
    // The word survives where a word helps, now that the label beside it is gone.
    expect(on.getAttribute('aria-label')).toBe('Monitored — click to stop monitoring Dune');
    expect(on.getAttribute('title')).toBe(on.getAttribute('aria-label'));
  });

  it('says the opposite when it is off', () => {
    const off = mountButton({ monitored: false });
    expect(off.getAttribute('aria-pressed')).toBe('false');
    expect(off.getAttribute('aria-label')).toBe('Unmonitored — click to monitor Dune');
  });

  it('asks for the opposite of what it shows', () => {
    mountButton({ monitored: true }).click();
    expect(changes).toEqual([false]);

    mountButton({ monitored: false }).click();
    expect(changes).toEqual([true]);
  });

  it('does nothing while a write is in flight', () => {
    const button = mountButton({ monitored: true, disabled: true });
    expect(button.disabled).toBe(true);
    button.click();
    expect(changes).toEqual([]);
  });
});
