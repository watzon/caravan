<script lang="ts">
  /**
   * A popover body that is a number, or a pair of them: year window, runtime
   * bounds, rating floor, scene duration.
   *
   * `max` is optional because one of these genuinely has no upper half — the
   * adult provider serves a single `duration` with no comparison operator, so a
   * second box there would be a control nothing could honour. The component
   * renders what it is given rather than rendering a pair and disabling one,
   * which would read as a bug rather than as a limit.
   *
   * 0 is "unset" for every one of these, so an emptied box clears the filter
   * rather than filtering on zero.
   *
   * THE BOX OWNS ITS TEXT. Every one of these fields drives a filter that lives
   * in the URL, so a committed value comes straight back down as a prop — and
   * echoing that back into the field is how the rating pill lost the half-star
   * its own control advertised. "7." is not a complete number, so it committed
   * as "unset", the URL was rewritten, and the re-render wiped the 7 before the
   * 5 could be typed. Hence the two rules here:
   *
   *  1. The text is local state. It is refilled from above only when the value
   *     coming down disagrees with what this component last sent up — Clear
   *     all, a removed chip, a Back — never as the echo of its own commit.
   *  2. A commit happens on a COMPLETE number, after the same debounce the
   *     typeaheads use. A half-typed "7." or "-" is not a filter yet, and
   *     without the wait every digit of a year was its own scope request.
   *
   * That is also why these are text boxes with `inputmode="decimal"` rather
   * than `type="number"`: a number input reports "" for a partly-typed value,
   * so the raw text rule 1 needs cannot be read back out of one at all. A
   * phone still gets the numeric keypad.
   */
  import { untrack } from 'svelte';
  import { DEBOUNCE_MS } from '../typeahead';

  interface Props {
    minValue: number;
    minLabel: string;
    onmin: (value: number) => void;
    /** Omit both to render a single box. */
    maxValue?: number;
    maxLabel?: string;
    onmax?: (value: number) => void;
    /** "min", "2019" — what an empty box would hold. */
    placeholder?: string;
    /** Rendered under the boxes; the honest note about a one-sided control. */
    hint?: string;
    /** A ceiling the endpoint imposes — the rating floor is out of 10. */
    max?: number;
  }

  let {
    minValue,
    minLabel,
    onmin,
    maxValue,
    maxLabel,
    onmax,
    placeholder = '',
    hint,
    max,
  }: Props = $props();

  const FIELD =
    'h-9 w-full rounded-sm border border-border-strong bg-raised px-2 text-md text-ink ' +
    'placeholder:text-ink-muted focus:border-accent focus:outline-none';

  /** A filter value as the box spells it; unset is an empty box, not "0". */
  function boxText(value: number): string {
    return value > 0 ? String(value) : '';
  }

  /**
   * What a box currently says, as a number — or null when it does not say a
   * number YET. "7.", "-" and "1e" are all somebody mid-keystroke, and
   * committing them would clear the filter under the typing hand.
   *
   * A complete number that is not positive is 0: "unset", the same reading an
   * empty box has.
   */
  function parseBox(raw: string): number | null {
    const text = raw.trim();
    if (text === '') return 0;
    if (!/^-?\d+(\.\d+)?$/.test(text)) return null;
    const n = Number(text);
    if (!Number.isFinite(n) || n <= 0) return 0;
    return max !== undefined ? Math.min(max, n) : n;
  }

  /**
   * The text on screen. Seeded from the value this mounted with — the popover
   * body is built fresh each time it opens — and its own from then on. The
   * `untrack` is the point rather than a formality: this is a ONE-TIME read of
   * the prop, and the effects below are the only thing that listens for a later
   * change to it.
   */
  let minText = $state(untrack(() => boxText(minValue)));
  let maxText = $state(untrack(() => boxText(maxValue ?? 0)));

  /**
   * The last value this component sent up. Plain variables, not $state: nothing
   * renders them, and making them reactive would only re-run the effects that
   * maintain them.
   */
  let sentMin = untrack(() => minValue);
  let sentMax = untrack(() => maxValue ?? 0);

  $effect(() => {
    if (minValue !== sentMin) {
      sentMin = minValue;
      minText = boxText(minValue);
    }
  });

  $effect(() => {
    const next = maxValue ?? 0;
    if (next !== sentMax) {
      sentMax = next;
      maxText = boxText(next);
    }
  });

  // One timer per box, not one for the pair: a runtime Max typed straight after
  // a Min must not cancel the Min's commit.
  const timers: Record<'min' | 'max', ReturnType<typeof setTimeout> | null> = {
    min: null,
    max: null,
  };

  function cancelPending(box: 'min' | 'max') {
    const timer = timers[box];
    if (timer !== null) {
      clearTimeout(timer);
      timers[box] = null;
    }
  }

  // A pending commit dies with the popover rather than navigating a screen that
  // has moved on. Blur commits first (see `onchange`), so clicking away from a
  // half-typed box still keeps what was typed.
  $effect(() => () => {
    cancelPending('min');
    cancelPending('max');
  });

  function commitMin() {
    const value = parseBox(minText);
    if (value === null || value === sentMin) return;
    sentMin = value;
    onmin(value);
  }

  function commitMax() {
    const value = parseBox(maxText);
    if (value === null || value === sentMax) return;
    sentMax = value;
    onmax?.(value);
  }

  /** Type freely; the filter follows once the typing pauses. */
  function schedule(box: 'min' | 'max', commit: () => void) {
    cancelPending(box);
    timers[box] = setTimeout(() => {
      timers[box] = null;
      commit();
    }, DEBOUNCE_MS);
  }

  /** Leaving the box is a decision, so it does not wait out the debounce. */
  function flush(box: 'min' | 'max', commit: () => void) {
    cancelPending(box);
    commit();
  }

  let pair = $derived(onmax !== undefined && maxLabel !== undefined);
</script>

<div class="flex flex-col gap-2">
  <div class="flex items-end gap-2">
    <label class="flex min-w-0 flex-1 flex-col gap-1">
      <span class="text-sm text-ink-secondary">{minLabel}</span>
      <input
        type="text"
        inputmode="decimal"
        {placeholder}
        bind:value={minText}
        oninput={() => schedule('min', commitMin)}
        onchange={() => flush('min', commitMin)}
        class={FIELD} />
    </label>

    {#if pair}
      <span class="pb-2 text-sm text-ink-muted">–</span>
      <label class="flex min-w-0 flex-1 flex-col gap-1">
        <span class="text-sm text-ink-secondary">{maxLabel}</span>
        <input
          type="text"
          inputmode="decimal"
          {placeholder}
          bind:value={maxText}
          oninput={() => schedule('max', commitMax)}
          onchange={() => flush('max', commitMax)}
          class={FIELD} />
      </label>
    {/if}
  </div>

  {#if hint}
    <p class="text-sm text-ink-muted">{hint}</p>
  {/if}
</div>
