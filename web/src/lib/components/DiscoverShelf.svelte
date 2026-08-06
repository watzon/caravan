<script lang="ts">
  /**
   * One carousel of discover cards under an 18px display header: a scrollable
   * row paged by the chevrons in the header. The arrows live in the header
   * rather than overlaying the posters because an overlay would sit exactly
   * where a card's availability chip does.
   *
   * There is deliberately no "See all" link: the API serves each shelf whole
   * (internal/api/discover.go) and has nothing wider behind it, and a link
   * that lands somewhere else is worse than no link. The network and studio
   * tiles on the discover screen are the browsable half.
   */
  import type { DiscoverItem } from '../api/types';
  import DiscoverCard from './DiscoverCard.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    title: string;
    items: DiscoverItem[];
    showType?: boolean;
  }

  let { title, items, showType = false }: Props = $props();

  let scroller = $state<HTMLDivElement | null>(null);
  let canScrollLeft = $state(false);
  let canScrollRight = $state(false);
  let requestedScrollLeft: number | null = null;

  function updateArrows() {
    const el = scroller;
    if (!el) return;
    canScrollLeft = el.scrollLeft > 0;
    canScrollRight = el.scrollLeft + el.clientWidth < el.scrollWidth - 1;
  }

  // Arrow state depends on content width, so recompute when the list changes,
  // not only when it scrolls.
  $effect(() => {
    void items.length;
    requestedScrollLeft = null;
    updateArrows();
  });

  function page(direction: -1 | 1) {
    const el = scroller;
    if (!el) return;
    // Keep an explicit destination while a smooth scroll is in flight. A
    // second click can then extend the pending destination instead of asking
    // the browser to scroll again from a position it has not updated yet.
    const step = el.clientWidth * 0.9;
    const pending = requestedScrollLeft;
    const continuesPendingScroll = pending !== null && direction * (pending - el.scrollLeft) > 1;
    const base = continuesPendingScroll ? pending : el.scrollLeft;
    const target = Math.max(0, Math.min(el.scrollWidth - el.clientWidth, base + direction * step));
    requestedScrollLeft = target;
    el.scrollTo({ left: target, behavior: 'smooth' });
  }
</script>

<svelte:window onresize={updateArrows} />

{#if items.length > 0}
  <section class="flex flex-col gap-3">
    <div class="flex items-center justify-between gap-3">
      <h2 class="font-display text-lg font-semibold tracking-tight text-ink">{title}</h2>

      {#if canScrollLeft || canScrollRight}
        <div class="flex items-center gap-1">
          <button
            type="button"
            aria-label="Scroll {title} left"
            disabled={!canScrollLeft}
            onclick={() => page(-1)}
            class="flex size-7 items-center justify-center rounded-md border border-border bg-surface
                   text-ink-secondary transition-colors duration-150 ease-out
                   hover:border-border-strong hover:bg-raised hover:text-ink
                   disabled:cursor-default disabled:opacity-40 disabled:hover:border-border
                   disabled:hover:bg-surface disabled:hover:text-ink-secondary">
            <Icon name="chevronLeft" size={14} />
          </button>
          <button
            type="button"
            aria-label="Scroll {title} right"
            disabled={!canScrollRight}
            onclick={() => page(1)}
            class="flex size-7 items-center justify-center rounded-md border border-border bg-surface
                   text-ink-secondary transition-colors duration-150 ease-out
                   hover:border-border-strong hover:bg-raised hover:text-ink
                   disabled:cursor-default disabled:opacity-40 disabled:hover:border-border
                   disabled:hover:bg-surface disabled:hover:text-ink-secondary">
            <Icon name="chevronRight" size={14} />
          </button>
        </div>
      {/if}
    </div>

    <div bind:this={scroller} onscroll={updateArrows} class="flex gap-4 overflow-x-auto pb-1">
      {#each items as item (`${item.media_type}-${item.tmdb_id}`)}
        <div class="w-32 shrink-0 sm:w-40">
          <DiscoverCard {item} {showType} />
        </div>
      {/each}
    </div>
  </section>
{/if}
