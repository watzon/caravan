<script lang="ts">
  /**
   * The one page-title area every route shares (DESIGN.md §5): title left,
   * optional subtitle beside it, page actions, then the global add action
   * right. System health lives in the sidebar card, not here. Paper's
   * headers have no bottom border, and the bar must never shrink: it is a
   * flex item above a scrolling column, so shrink-0 is what keeps tall
   * pages from squishing it.
   */
  import Icon from '../components/Icon.svelte';
  import Button from '../components/Button.svelte';
  import { page } from '../state/page.svelte';

  interface Props {
    title: string;
    /**
     * Opens the add-to-library dialog. Omitted for a member, who has no
     * library to add to — the button goes with it rather than being disabled.
     */
    onadd?: () => void;
    /** The narrow-screen navigation drawer; absent from the desktop layout. */
    onmenu?: () => void;
    menuOpen?: boolean;
    menuButton?: HTMLButtonElement;
  }

  let {
    title,
    onadd,
    onmenu,
    menuOpen = false,
    menuButton = $bindable(),
  }: Props = $props();

  const isMac =
    typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent);
</script>

<header
  class="sticky top-0 z-30 flex h-16 shrink-0 items-center gap-2 bg-bg/95 px-3 backdrop-blur sm:gap-4 sm:px-6">
  <button
    type="button"
    class="flex h-9 items-center rounded-md border border-border-strong bg-raised px-3 text-sm text-ink-secondary transition-colors duration-150 ease-out hover:bg-overlay hover:text-ink md:hidden"
    aria-label={menuOpen ? 'Close navigation menu' : 'Open navigation menu'}
    aria-controls="primary-navigation-drawer"
    aria-expanded={menuOpen}
    onclick={onmenu}
    bind:this={menuButton}>
    Menu
  </button>

  <h1 class="min-w-0 truncate font-display text-xl font-semibold tracking-tight text-ink" title={title}>
    {title}
  </h1>

  {#if page.subtitle}
    <span class="hidden min-w-0 truncate text-sm text-ink-secondary sm:inline" title={page.subtitle}>{page.subtitle}</span>
  {/if}

  <div class="flex-1"></div>

  {#if page.actions}
    {@render page.actions()}
  {/if}

  {#if onadd}
    <Button variant="secondary" onclick={onadd} title="Add movie or series">
      <Icon name="plus" size={14} />
      <span class="sr-only lg:not-sr-only">Add movie or series</span>
      <kbd class="ml-2 hidden rounded-sm bg-surface px-1.5 py-0.5 font-mono text-xs text-ink-muted xl:inline">
        {isMac ? '⌘' : 'Ctrl'}K
      </kbd>
    </Button>
  {/if}
</header>
