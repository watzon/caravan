<script lang="ts">
  /** Label + control + help/error, so every form row is spaced identically. */
  import type { Snippet } from 'svelte';

  interface Props {
    label: string;
    for?: string;
    help?: string;
    error?: string | null;
    class?: string;
    children: Snippet;
  }

  let { label, for: htmlFor, help, error = null, class: klass = '', children }: Props = $props();
</script>

<div class="flex flex-col gap-2 {klass}">
  <label class="micro-label" for={htmlFor}>{label}</label>
  {@render children()}
  {#if error}
    <p class="text-sm text-danger">{error}</p>
  {:else if help}
    <p class="text-sm text-ink-secondary">{help}</p>
  {/if}
</div>
