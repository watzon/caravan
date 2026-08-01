<script lang="ts">
  /**
   * DESIGN.md §5: 2:3 posters with a 16px gap. Tracks are fixed-width
   * (Sonarr/Radarr-style): a wider window fits more posters per row instead
   * of inflating them, so a poster is the same size on every screen. The one
   * concession is small phones, where two columns always fit.
   */
  import type { Snippet } from 'svelte';

  interface Props {
    children: Snippet;
  }

  let { children }: Props = $props();
</script>

<div
  class="grid grid-cols-2 gap-4 sm:[grid-template-columns:repeat(auto-fill,10rem)]">
  {@render children()}
</div>
