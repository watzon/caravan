<script lang="ts">
  /**
   * One concern inside a merged settings pane (the Paper redesign): a titled
   * card whose header carries that concern's own action, so a pane holding
   * three of them still reads as three separate things to decide.
   */
  import type { Snippet } from 'svelte';

  interface Props {
    title: string;
    description?: string;
    /** Right-aligned in the header row: this card's save, add or status. */
    action?: Snippet;
    children: Snippet;
  }

  let { title, description = '', action, children }: Props = $props();
</script>

<section class="rounded-md border border-border bg-surface">
  <div class="flex items-start gap-3 border-b border-border px-4 py-3">
    <div class="flex min-w-0 flex-1 flex-col gap-1">
      <h3 class="text-base font-medium text-ink">{title}</h3>
      {#if description}
        <p class="text-sm text-ink-secondary">{description}</p>
      {/if}
    </div>
    {#if action}
      <div class="flex shrink-0 items-center gap-2">{@render action()}</div>
    {/if}
  </div>

  <div class="flex flex-col gap-4 p-4">{@render children()}</div>
</section>
