<script lang="ts">
  /** Label + control + help/error, so every form row is spaced identically. */
  import type { Snippet } from 'svelte';

  interface Props {
    label: string;
    for?: string;
    help?: string;
    error?: string | null;
    /** Right-aligned on the label row: what this field's current value means. */
    note?: Snippet;
    class?: string;
    children: Snippet;
  }

  let {
    label,
    for: htmlFor,
    help,
    error = null,
    note,
    class: klass = '',
    children,
  }: Props = $props();
</script>

<div class="flex flex-col gap-2 {klass}">
  {#if note}
    <div class="flex items-baseline justify-between gap-2">
      <label class="micro-label" for={htmlFor}>{label}</label>
      {@render note()}
    </div>
  {:else}
    <label class="micro-label" for={htmlFor}>{label}</label>
  {/if}
  {@render children()}
  {#if error}
    <p class="text-sm text-danger">{error}</p>
  {:else if help}
    <p class="text-sm text-ink-secondary">{help}</p>
  {/if}
</div>
