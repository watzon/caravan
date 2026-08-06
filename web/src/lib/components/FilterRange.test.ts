/**
 * FilterRange (PLAN phase 12 task 5).
 *
 * The property under test is the one that is invisible until it breaks: these
 * boxes drive filters that live in the URL, so every committed value comes
 * straight back down as a prop. A component that echoed that into the field
 * would fight the person typing in it — which is exactly what a `type="number"`
 * box did, because a partly-typed "7." reads back as "" and committed as
 * "unset" before the "5" arrived.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import FilterRange from './FilterRange.svelte';
import { DEBOUNCE_MS } from '../typeahead';
import { reactiveProps } from '../reactiveprops.svelte';

let host: HTMLElement;
let app: Record<string, unknown> | undefined;

beforeEach(() => {
  vi.useFakeTimers();
  host = document.createElement('div');
  document.body.appendChild(host);
});

afterEach(() => {
  if (app) unmount(app);
  app = undefined;
  host.remove();
  vi.useRealTimers();
});

/**
 * A screen whose filter value is the one this component last committed — the
 * URL round trip, in the small. Without it the echo cannot be observed.
 */
function mountRange(extra: Record<string, unknown> = {}) {
  const committed: number[] = [];
  const bag = reactiveProps<Record<string, unknown>>({
    minValue: 0,
    minLabel: 'At least',
    onmin: (value: number) => {
      committed.push(value);
      bag.set({ minValue: value });
    },
    ...extra,
  });
  app = mount(FilterRange, { target: host, props: bag.props });
  flushSync();
  return { committed, bag };
}

function boxes(): HTMLInputElement[] {
  return [...host.querySelectorAll('input')];
}

/** One keystroke: the browser sets the value, then fires input. */
function type(box: HTMLInputElement, text: string) {
  box.value = text;
  box.dispatchEvent(new Event('input', { bubbles: true }));
  flushSync();
}

function settle() {
  vi.advanceTimersByTime(DEBOUNCE_MS + 1);
  flushSync();
}

describe('FilterRange', () => {
  it('associates each input with its visible label and shared hint', () => {
    mountRange({
      minLabel: 'Minimum runtime',
      maxValue: 0,
      maxLabel: 'Maximum runtime',
      onmax: () => {},
      hint: 'Runtime is measured in minutes.',
    });

    const [min, max] = boxes() as [HTMLInputElement, HTMLInputElement];
    const labels = [...host.querySelectorAll<HTMLLabelElement>('label')];
    expect(labels.map((label) => label.control)).toEqual([min, max]);

    const hintID = min.getAttribute('aria-describedby');
    expect(hintID).toBeTruthy();
    expect(max.getAttribute('aria-describedby')).toBe(hintID);
    expect(host.querySelector(`#${hintID}`)?.textContent).toBe('Runtime is measured in minutes.');
  });

  /**
   * The regression. Every intermediate state of typing "7.5" must survive, and
   * the only complete number among them is the one that reaches the filter.
   */
  it('lets a half-star be typed without the round trip wiping it', () => {
    const { committed } = mountRange({ max: 10 });
    const box = boxes()[0] as HTMLInputElement;

    type(box, '7');
    type(box, '7.');
    // Mid-keystroke, and the debounce has not run: nothing has been committed
    // and the field still says what was typed.
    expect(committed).toEqual([]);
    expect(box.value).toBe('7.');

    // Even if the typing pauses here, "7." is not a filter — committing it as
    // "unset" is what used to rewrite the URL and blank the box.
    settle();
    expect(committed).toEqual([]);
    expect(box.value).toBe('7.');

    type(box, '7.5');
    settle();
    expect(committed).toEqual([7.5]);
    expect(box.value).toBe('7.5');
  });

  /** One request per pause, not one per digit. */
  it('commits once for a year typed straight through', () => {
    const { committed } = mountRange({ minLabel: 'Released in' });
    const box = boxes()[0] as HTMLInputElement;

    for (const text of ['2', '20', '201', '2019']) type(box, text);
    expect(committed).toEqual([]);
    settle();

    expect(committed).toEqual([2019]);
  });

  /** Leaving the box is a decision, so it does not wait out the debounce. */
  it('commits immediately on blur', () => {
    const { committed } = mountRange();
    const box = boxes()[0] as HTMLInputElement;

    type(box, '90');
    box.dispatchEvent(new Event('change', { bubbles: true }));
    flushSync();

    expect(committed).toEqual([90]);
  });

  /** An emptied box clears the filter rather than filtering on zero. */
  it('reads an emptied box as unset', () => {
    const { committed, bag } = mountRange();
    const box = boxes()[0] as HTMLInputElement;

    type(box, '90');
    settle();
    type(box, '');
    settle();

    expect(committed).toEqual([90, 0]);
    expect(bag.props.minValue).toBe(0);
  });

  /** The ceiling the endpoint imposes is applied here rather than at the API. */
  it('clamps to the max it was given', () => {
    const { committed } = mountRange({ max: 10 });
    const box = boxes()[0] as HTMLInputElement;

    type(box, '99');
    settle();

    expect(committed).toEqual([10]);
  });

  /**
   * The other half of rule 1: a value that changes for a reason OTHER than this
   * box — Clear all, a removed chip, a Back — does refill it.
   */
  it('refills from above when the filter is cleared elsewhere', () => {
    const { bag } = mountRange();
    const box = boxes()[0] as HTMLInputElement;

    type(box, '90');
    settle();
    expect(box.value).toBe('90');

    bag.set({ minValue: 0 });
    flushSync();
    expect(box.value).toBe('');
  });

  /** A pair keeps two independent debounces, or the first edit is swallowed. */
  it('does not let the second box cancel the first box’s commit', () => {
    const mins: number[] = [];
    const maxes: number[] = [];
    app = mount(FilterRange, {
      target: host,
      props: {
        minValue: 0,
        minLabel: 'Min',
        onmin: (v: number) => mins.push(v),
        maxValue: 0,
        maxLabel: 'Max',
        onmax: (v: number) => maxes.push(v),
      },
    });
    flushSync();

    const [min, max] = boxes() as [HTMLInputElement, HTMLInputElement];
    type(min, '60');
    type(max, '120');
    settle();

    expect(mins).toEqual([60]);
    expect(maxes).toEqual([120]);
  });
});
