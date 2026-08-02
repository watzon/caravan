<script lang="ts">
  /** Dialog on --color-overlay, radius-lg (DESIGN.md §3/§6). Escape closes. */
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  interface Props {
    title: string;
    onclose: () => void;
    /** Tailwind max-width class; modals are content-sized, not full-bleed. */
    width?: string;
    children: Snippet;
    footer?: Snippet;
  }

  let { title, onclose, width = 'max-w-2xl', children, footer }: Props = $props();

  let dialog = $state<HTMLElement | null>(null);

  function onkeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.stopPropagation();
      onclose();
    }
  }

  $effect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialog?.focus();
    // Restore rather than clear on close: a confirm opened from inside an
    // editor stacks two modals, and clearing would unlock page scroll while
    // the editor underneath is still open.
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = previousOverflow;
      previous?.focus?.();
    };
  });
</script>

<div class="fixed inset-0 z-50 flex items-start justify-center p-4 pt-16 sm:p-6 sm:pt-24">
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-bg/80"
    aria-label="Close dialog"
    onclick={onclose}>
  </button>

  <div
    bind:this={dialog}
    role="dialog"
    aria-modal="true"
    aria-label={title}
    tabindex="-1"
    {onkeydown}
    class="relative flex max-h-full w-full {width} flex-col overflow-hidden rounded-lg
           border border-border-strong bg-overlay shadow-2xl focus:outline-none">
    <header class="flex items-center justify-between gap-4 border-b border-border px-4 py-3">
      <h2 class="font-display text-md font-semibold tracking-tight text-ink">{title}</h2>
      <button
        type="button"
        class="rounded-sm p-1 text-ink-secondary transition-colors duration-150 hover:bg-raised hover:text-ink"
        aria-label="Close"
        onclick={onclose}>
        <Icon name="close" />
      </button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto">{@render children()}</div>

    {#if footer}
      <footer class="flex items-center justify-end gap-2 border-t border-border px-4 py-3">
        {@render footer()}
      </footer>
    {/if}
  </div>
</div>
