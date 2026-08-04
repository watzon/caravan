/**
 * The shared keyboard rules for a search-as-you-type list. Both add dialogs
 * exercise these through their own DOM; what is covered here is the edges they
 * do not reach — an empty list and a key pressed somewhere that is not a result
 * — because those are where a swallowed key traps the user.
 */
import { afterEach, describe, expect, it } from 'vitest';
import { focusFirstResult, moveResultFocus } from './typeahead';

let container: HTMLElement | undefined;

function build(rows: number): HTMLElement {
  container = document.createElement('div');
  container.innerHTML = `<input /><ul>${'<li><button></button></li>'.repeat(rows)}</ul>`;
  document.body.appendChild(container);
  return container;
}

function buttons(): HTMLElement[] {
  return [...container!.querySelectorAll<HTMLElement>('ul button')];
}

function press(target: Element, key: string): KeyboardEvent {
  const event = new KeyboardEvent('keydown', { key, cancelable: true, bubbles: true });
  target.dispatchEvent(event);
  return event;
}

afterEach(() => {
  container?.remove();
  container = undefined;
});

describe('focusFirstResult', () => {
  it('moves to the first result and says it did', () => {
    build(2);
    expect(focusFirstResult(container!)).toBe(true);
    expect(document.activeElement).toBe(buttons()[0]);
  });

  it('reports no move on an empty list, so the caller leaves the key alone', () => {
    build(0);
    expect(focusFirstResult(container!)).toBe(false);
  });

  it('tolerates a container that is not mounted yet', () => {
    expect(focusFirstResult(null)).toBe(false);
  });
});

describe('moveResultFocus', () => {
  it('stops at the bottom rather than wrapping', () => {
    build(2);
    const [first, last] = buttons();
    last!.focus();
    moveResultFocus(press(last!, 'ArrowDown'), container!);
    expect(document.activeElement).toBe(last);
    expect(document.activeElement).not.toBe(first);
  });

  it('hands focus back to the field from the top', () => {
    build(2);
    const first = buttons()[0]!;
    first.focus();
    moveResultFocus(press(first, 'ArrowUp'), container!);
    expect(document.activeElement).toBe(container!.querySelector('input'));
  });

  it('leaves keys pressed outside the list alone', () => {
    build(2);
    const input = container!.querySelector('input')!;
    const event = press(input, 'ArrowUp');
    moveResultFocus(event, container!);
    // Not the list's key to take: Up in the field is the browser's own
    // caret/history behaviour.
    expect(event.defaultPrevented).toBe(false);
  });

  it('ignores keys that are not the arrows it owns', () => {
    build(2);
    const first = buttons()[0]!;
    const event = press(first, 'Enter');
    moveResultFocus(event, container!);
    expect(event.defaultPrevented).toBe(false);
  });
});
