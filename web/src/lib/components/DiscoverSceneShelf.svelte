<script lang="ts">
  /**
   * Adult twin of DiscoverShelf: a labelled row of 16:9 scene cards. The
   * heading is a link when the shelf has a filtered view behind it.
   */
  import type { SceneMeta } from '../api/types';
  import { useI18n } from '../i18n.svelte';
  import Icon from './Icon.svelte';
  import SceneCard from './SceneCard.svelte';

  interface Props {
    title: string;
    scenes: SceneMeta[];
    href?: string;
    requesting?: string | null;
    busy?: boolean;
    onrequest: (scene: SceneMeta) => void;
  }

  let {
    title,
    scenes,
    href = '',
    requesting = null,
    busy = false,
    onrequest,
  }: Props = $props();
  const { t } = useI18n();

  let scroller = $state<HTMLDivElement | null>(null);
  let canScrollLeft = $state(false);
  let canScrollRight = $state(false);
  let requestedScrollLeft: number | null = null;

  function updateArrows() {
    const el = scroller;
    if (!el) return;
    if (requestedScrollLeft !== null && Math.abs(el.scrollLeft - requestedScrollLeft) <= 1) {
      requestedScrollLeft = null;
    }
    canScrollLeft = el.scrollLeft > 0;
    canScrollRight = el.scrollLeft + el.clientWidth < el.scrollWidth - 1;
  }

  function cancelPendingScroll() {
    requestedScrollLeft = null;
  }

  function cancelPendingScrollOnKeydown(event: KeyboardEvent) {
    switch (event.key) {
      case 'ArrowLeft':
      case 'ArrowRight':
      case 'ArrowUp':
      case 'ArrowDown':
      case 'PageUp':
      case 'PageDown':
      case 'Home':
      case 'End':
      case ' ':
        cancelPendingScroll();
    }
  }

  $effect(() => {
    void scenes.length;
    requestedScrollLeft = null;
    updateArrows();
  });

  function page(direction: -1 | 1) {
    const el = scroller;
    if (!el) return;
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

{#if scenes.length > 0}
  <section class="flex min-w-0 flex-col gap-3">
    <div class="flex items-center justify-between gap-3">
      <h2 class="font-display text-lg font-semibold tracking-tight text-ink">
        {#if href}
          <a
            {href}
            class="transition-colors duration-150 ease-out hover:text-accent-text">
            {title}
          </a>
        {:else}
          {title}
        {/if}
      </h2>

      {#if canScrollLeft || canScrollRight}
        <div class="flex items-center gap-1">
          <button
            type="button"
            aria-label={t('component.discoverShelf.scrollLeft', { title })}
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
            aria-label={t('component.discoverShelf.scrollRight', { title })}
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

    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div
      bind:this={scroller}
      onscroll={updateArrows}
      onpointerdown={cancelPendingScroll}
      onwheel={cancelPendingScroll}
      ontouchstart={cancelPendingScroll}
      onkeydown={cancelPendingScrollOnKeydown}
      class="flex w-full min-w-0 max-w-full gap-4 overflow-x-auto pb-1">
      {#each scenes as scene (scene.stash_id)}
        <div class="w-72 shrink-0 sm:w-80">
          <SceneCard
            {scene}
            requesting={requesting === scene.stash_id}
            {busy}
            {onrequest} />
        </div>
      {/each}
    </div>
  </section>
{/if}
