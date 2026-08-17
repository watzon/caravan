<script lang="ts">
  /**
   * A labelled grid of browse destinations: networks, studios, genres, or
   * adult sites. The mark is optional — a genre tile is just the name, and a
   * network tile keeps the name under the logo so a missing image is still a
   * destination.
   */
  import SourceLogo from './SourceLogo.svelte';

  export interface DiscoverTile {
    href: string;
    name: string;
    /** Absolute image URL; omit or "" when the tile is text-only. */
    image?: string;
    /** True when a filled lockup must keep its tri-tone. */
    lockup?: boolean;
  }

  interface Props {
    title: string;
    tiles: DiscoverTile[];
  }

  let { title, tiles }: Props = $props();
</script>

{#if tiles.length > 0}
  <section class="flex flex-col gap-3">
    <h2 class="font-display text-lg font-semibold tracking-tight text-ink">{title}</h2>
    <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
      {#each tiles as tile (tile.href)}
        <a
          href={tile.href}
          title={tile.name}
          class="flex h-20 min-w-0 flex-col items-center justify-center gap-1.5 rounded-md border border-border
                 bg-surface px-3 text-center transition-colors duration-150 ease-out
                 hover:border-border-strong hover:bg-raised">
          {#if tile.image}
            <SourceLogo
              src={tile.image}
              lockup={tile.lockup}
              class="max-h-7 max-w-[7.5rem] object-contain" />
          {/if}
          <span class="line-clamp-1 text-sm text-ink-secondary">{tile.name}</span>
        </a>
      {/each}
    </div>
  </section>
{/if}
