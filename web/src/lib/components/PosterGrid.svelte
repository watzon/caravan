<script lang="ts">
  /** DESIGN.md §5: responsive 2:3 columns with a 16px gap. */
  import type { Snippet } from 'svelte';

  interface Props {
    children: Snippet;
  }

  let { children }: Props = $props();
</script>

<div
  class="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
  {@render children()}
</div>
