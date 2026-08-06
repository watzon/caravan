<script lang="ts">
  /**
   * One control on the explore filter rail: a pill that opens a popover.
   *
   * It owns the shell — the trigger, the applied/idle look, and the three ways
   * a popover has to close (Escape, a click outside, the caller saying so) —
   * and nothing about what is inside it. That split is deliberate: a genre
   * checklist, a person typeahead and a runtime range are three different
   * bodies, and a component that took all three would be a switch on which
   * caller it is. OverflowMenu made the same call for the same reason; this is
   * its sibling for content that is not a list of actions.
   *
   * Focus returns to the trigger on close, so a keyboard reader is never
   * dropped at the top of the document.
   */
  import { untrack, type Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  interface Props {
    label: string;
    /** True when this pill's filter is set — it then wears the accent. */
    applied?: boolean;
    /**
     * Rendered inside the popover. It is not handed a `close`: none of the
     * bodies wants one — picking a filter leaves the popover open so a second
     * pick is not a second trip — and an unused escape hatch is an API nobody
     * has thought through.
     */
    children: Snippet;
    /** Popover width. A typeahead wants more room than a range pair. */
    width?: string;
    /**
     * Geometry. 'pill' is the explore rail's own shape at button height
     * (32px, DESIGN.md §6). 'box' takes the input radius and height (36px)
     * for rails where the trigger replaces a select beside inputs, so the
     * row keeps one baseline. Same control and colorway otherwise.
     */
    shape?: 'pill' | 'box';
  }

  let { label, applied = false, children, width = 'w-64', shape = 'pill' }: Props = $props();

  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);
  let panel = $state<HTMLElement | null>(null);
  let panelShift = $state(0);

  function placePanel() {
    if (!open || !panel) return;
    const gutter = 16;
    const rect = panel.getBoundingClientRect();
    // This runs inside an effect when the panel mounts. Geometry needs the
    // previous correction, but that read must not subscribe the effect to the
    // state it writes.
    const previousShift = untrack(() => panelShift);
    const left = rect.left - previousShift;
    const right = rect.right - previousShift;
    const maxRight = window.innerWidth - gutter;
    panelShift = left < gutter ? gutter - left : right > maxRight ? maxRight - right : 0;
  }

  function close(refocus = true) {
    if (!open) return;
    open = false;
    if (refocus) trigger?.focus();
  }

  // Pointer-down and capture, as in OverflowMenu: a popover must not survive a
  // press that started somewhere else, and it must go before whatever is under
  // the pointer reacts.
  function onpointerdown(event: PointerEvent) {
    if (!open) return;
    const target = event.target as Node;
    if (trigger?.contains(target) || panel?.contains(target)) return;
    close(false);
  }

  function onkeydown(event: KeyboardEvent) {
    if (!open || event.key !== 'Escape') return;
    event.stopPropagation();
    close();
  }

  $effect(() => {
    if (!open) {
      panelShift = 0;
      return;
    }
    placePanel();
    // The first field, not the first button: every body here opens on
    // something you type into or choose from, and a popover the keyboard has
    // to Tab into is one it cannot really reach.
    const first = panel?.querySelector<HTMLElement>('input, select, button');
    first?.focus();
  });
</script>

<svelte:window {onkeydown} onresize={placePanel} onpointerdowncapture={onpointerdown} />

<div class="relative">
  <button
    bind:this={trigger}
    type="button"
    aria-haspopup="dialog"
    aria-expanded={open}
    onclick={() => (open = !open)}
    class="inline-flex items-center gap-1.5 border px-3 text-base
           whitespace-nowrap transition-colors duration-150 ease-out
           {shape === 'pill' ? 'h-8 rounded-full' : 'h-9 rounded-md'}
           {applied
      ? 'border-accent bg-accent-tint text-accent-text'
      : 'border-border bg-surface text-ink-secondary hover:border-border-strong hover:bg-raised hover:text-ink'}
           {open ? 'border-border-strong' : ''}">
    {label}
    <Icon name="chevronDown" size={12} />
  </button>

  {#if open}
    <div
      bind:this={panel}
      role="dialog"
      aria-label={label}
      style:transform={`translateX(${panelShift}px)`}
      class="absolute left-0 top-full z-30 mt-1 max-w-[calc(100vw-2rem)] {width} rounded-lg border border-border-strong
             bg-overlay p-3 shadow-2xl">
      {@render children()}
    </div>
  {/if}
</div>
