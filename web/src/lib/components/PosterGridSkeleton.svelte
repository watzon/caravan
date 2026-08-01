<script lang="ts">
  import PosterGrid from './PosterGrid.svelte';
  import Skeleton from './Skeleton.svelte';

  interface Props {
    count?: number;
  }

  let { count = 12 }: Props = $props();
</script>

<PosterGrid>
  {#each Array.from({ length: count }) as _, i (i)}
    <div class="flex flex-col gap-2">
      <Skeleton class="aspect-[2/3] w-full rounded-md" />
      <Skeleton class="h-4 w-3/4" />
      <Skeleton class="h-3 w-1/3" />
    </div>
  {/each}
</PosterGrid>
