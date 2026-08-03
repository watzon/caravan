<script lang="ts">
  /**
   * The one page-title area every route shares (DESIGN.md §5): title left,
   * optional subtitle beside it, page actions, then global search (⌘K)
   * right. System health lives in the sidebar card, not here. Paper's
   * headers have no bottom border, and the bar must never shrink: it is a
   * flex item above a scrolling column, so shrink-0 is what keeps tall
   * pages from squishing it.
   */
  import Icon from '../components/Icon.svelte';
  import { page } from '../state/page.svelte';

  interface Props {
    title: string;
    /**
     * Opens the add-to-library dialog. Omitted for a member, who has no
     * library to add to — the button goes with it rather than being disabled.
     */
    onsearch?: () => void;
  }

  let { title, onsearch }: Props = $props();

  const isMac =
    typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent);
</script>

<header
  class="sticky top-0 z-30 flex h-16 shrink-0 items-center gap-4 bg-bg/95 px-6 backdrop-blur">
  <h1 class="truncate font-display text-xl font-semibold tracking-tight text-ink">
    {title}
  </h1>

  {#if page.subtitle}
    <span class="truncate text-sm text-ink-secondary">{page.subtitle}</span>
  {/if}

  <div class="flex-1"></div>

  {#if page.actions}
    {@render page.actions()}
  {/if}

  {#if onsearch}
    <button
      type="button"
      onclick={onsearch}
      class="flex h-8 items-center gap-2 rounded-md border border-border-strong bg-raised px-3
             text-base text-ink-muted transition-colors duration-150 ease-out hover:bg-overlay hover:text-ink-secondary">
      <Icon name="search" size={14} />
      <span class="hidden sm:inline">Add movie or series</span>
      <kbd class="ml-2 rounded-sm bg-surface px-1.5 py-0.5 font-mono text-xs text-ink-muted">
        {isMac ? '⌘' : 'Ctrl'}K
      </kbd>
    </button>
  {/if}
</header>
