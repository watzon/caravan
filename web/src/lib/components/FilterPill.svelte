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
  import type { Snippet } from 'svelte';
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
  }

  let { label, applied = false, children, width = 'w-64' }: Props = $props();

  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);
  let panel = $state<HTMLElement | null>(null);

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
    if (!open) return;
    // The first field, not the first button: every body here opens on
    // something you type into or choose from, and a popover the keyboard has
    // to Tab into is one it cannot really reach.
    const first = panel?.querySelector<HTMLElement>('input, select, button');
    first?.focus();
  });
</script>

<svelte:window {onkeydown} onpointerdowncapture={onpointerdown} />

<div class="relative">
  <button
    bind:this={trigger}
    type="button"
    aria-haspopup="dialog"
    aria-expanded={open}
    onclick={() => (open = !open)}
    class="inline-flex h-8 items-center gap-1.5 rounded-full border px-3 text-base
           whitespace-nowrap transition-colors duration-150 ease-out
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
      class="absolute left-0 top-full z-30 mt-1 {width} rounded-lg border border-border-strong
             bg-overlay p-3 shadow-2xl">
      {@render children()}
    </div>
  {/if}
</div>
