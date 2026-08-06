<script lang="ts">
  /**
   * The waiting state for a search-as-you-type list: poster-and-two-lines rows
   * standing in for the results that are on their way.
   *
   * Shared by the add dialogs so the two cannot drift into different kinds of
   * waiting — a spinner in one and a skeleton in the other is how a user learns
   * that two screens are unrelated when they are not.
   */
  import Skeleton from './Skeleton.svelte';

  interface Props {
    /** How many placeholder rows to draw. */
    rows?: number;
  }

  let { rows = 4 }: Props = $props();
</script>

<span class="sr-only" role="status">Searching…</span>

<div class="flex flex-col gap-2">
  {#each Array.from({ length: rows }) as _, i (i)}
    <div class="flex items-center gap-3 rounded-md border border-border p-2">
      <Skeleton class="h-[72px] w-12 rounded-sm" />
      <div class="flex flex-1 flex-col gap-2">
        <Skeleton class="h-4 w-1/2" />
        <Skeleton class="h-3 w-3/4" />
      </div>
    </div>
  {/each}
</div>
