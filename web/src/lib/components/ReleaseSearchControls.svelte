<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * The query rail every release search wears: what to ask for, which
   * categories, which indexers.
   *
   * It is one component rather than two because the per-item picker and the
   * universal search ask indexers the same question — the only difference is
   * that the picker starts from a query the server derived, and carries a
   * locked item alongside it. That lock is `contextLabel`: a chip the user
   * cannot remove, so an edited query never reads as if it had also changed
   * what the grab lands on.
   *
   * Every control is uncommitted until Search: typing does not fan out over
   * every enabled indexer, because that request is slow, remote and rate
   * limited. This is deliberately NOT the explore rail's search-as-you-type.
   */
  import type { Indexer } from '../api/types';
  import { searchCategoryOptions } from '../indexer';
  import { session } from '../state/session.svelte';
  import Button from './Button.svelte';
  import FilterOptions from './FilterOptions.svelte';
  import FilterPill from './FilterPill.svelte';
  import Icon from './Icon.svelte';
  import TextInput from './TextInput.svelte';

  interface Props {
    query: string;
    /** Indexer category ids; empty searches genuinely unfiltered. */
    categories: number[];
    /** Restrict to these indexers; empty asks every enabled one. */
    indexerIDs: number[];
    /** Every configured indexer — the picker list, and the category union. */
    indexers: Indexer[];
    busy: boolean;
    onsearch: () => void;
    /** The locked grab target, when there is one ("Movie · Blade Runner 2049"). */
    contextLabel?: string;
    /** Opens the query-syntax help; the "?" in the box renders only when set. */
    onhelp?: () => void;
  }

  let {
    query = $bindable(''),
    categories = $bindable([]),
    indexerIDs = $bindable([]),
    indexers,
    busy,
    onsearch,
    contextLabel,
    onhelp,
  }: Props = $props();

  // FilterOptions speaks string ids because a provider id is not always a
  // number; these are, so the conversion lives here at the boundary rather
  // than in every option list.
  let categoryOptions = $derived(searchCategoryOptions(indexers, session.adult));
  let selectedCategories = $derived(categories.map(String));

  // Only the enabled ones: a disabled indexer is not searched however it is
  // ticked, so offering it would be offering a choice with no effect.
  let indexerOptions = $derived(
    indexers.filter((indexer) => indexer.enabled).map((indexer) => ({
      id: String(indexer.id),
      name: indexer.name,
    })),
  );
  let selectedIndexers = $derived(indexerIDs.map(String));

  function toggleCategory(id: string) {
    const value = Number(id);
    categories = categories.includes(value)
      ? categories.filter((c) => c !== value)
      : [...categories, value];
  }

  function toggleIndexer(id: string) {
    const value = Number(id);
    indexerIDs = indexerIDs.includes(value)
      ? indexerIDs.filter((c) => c !== value)
      : [...indexerIDs, value];
  }

  // Enter is what a search box promises; the button exists for the pointer.
  function onkeydown(event: KeyboardEvent) {
    if (event.key !== 'Enter') return;
    event.preventDefault();
    onsearch();
  }

  let queryWrapper = $state<HTMLDivElement>();

  /** Room for the in-field buttons, so a long expression never runs under them. */
  let inputPadding = $derived(
    query !== '' && onhelp ? 'pr-16' : query !== '' || onhelp ? 'pr-10' : '',
  );

  // Clearing is a step in typing the next query, so the caret goes back where
  // typing happens rather than stranding focus on a button that just vanished.
  function clearQuery() {
    query = '';
    queryWrapper?.querySelector('input')?.focus();
  }

  const { t, tp } = useI18n();
  let categoryLabel = $derived(
    categories.length === 0
      ? t('component.releaseSearchControls.allCategories')
      : tp('component.releaseSearchControls.categories', categories.length),
  );
  let indexerLabel = $derived(
    indexerIDs.length === 0
      ? t('component.releaseSearchControls.allIndexers')
      : tp('component.releaseSearchControls.indexers', indexerIDs.length),
  );
</script>

<div class="flex flex-col gap-2">
  <div class="flex flex-wrap items-center gap-2">
    <div bind:this={queryWrapper} class="relative min-w-[16rem] flex-1">
      <TextInput
        bind:value={query}
        type="search"
        {onkeydown}
        class={inputPadding}
        placeholder={t('component.releaseSearchControls.searchEveryEnabledIndexer')}
        ariaLabel={t('component.releaseSearchControls.releaseSearchQuery')} />
      <!-- The in-field controls replace the native search ✕, which ignores the
           design tokens. Both draw from the app icon set so the box reads as
           one piece. -->
      <div class="absolute right-1.5 top-1/2 flex -translate-y-1/2 items-center gap-0.5">
        {#if query !== ''}
          <button
            type="button"
            data-clear-search
            aria-label={t('component.releaseSearchControls.clearSearch')}
            title={t('component.releaseSearchControls.clearSearch')}
            onclick={clearQuery}
            class="inline-flex h-7 w-7 items-center justify-center rounded-full text-ink-muted
                   transition-colors duration-150 ease-out hover:bg-raised hover:text-ink">
            <Icon name="close" size={16} />
          </button>
        {/if}
        {#if onhelp}
          <button
            type="button"
            data-syntax-toggle
            aria-haspopup="dialog"
            aria-label={t('component.releaseSearchControls.querySyntax')}
            title={t('component.releaseSearchControls.querySyntax')}
            onclick={onhelp}
            class="inline-flex h-7 w-7 items-center justify-center rounded-full text-ink-muted
                   transition-colors duration-150 ease-out hover:bg-raised hover:text-ink">
            <Icon name="help" size={16} />
          </button>
        {/if}
      </div>
    </div>

    <FilterPill label={categoryLabel} applied={categories.length > 0} shape="box" width="w-56">
      {#snippet children()}
        <FilterOptions
          options={categoryOptions}
          selected={selectedCategories}
          onselect={toggleCategory}
          emptyText={t('component.releaseSearchControls.noCategoriesToChooseFrom')} />
      {/snippet}
    </FilterPill>

    <FilterPill label={indexerLabel} applied={indexerIDs.length > 0} shape="box" width="w-64">
      {#snippet children()}
        <FilterOptions
          options={indexerOptions}
          selected={selectedIndexers}
          onselect={toggleIndexer}
          emptyText={t('component.releaseSearchControls.noEnabledIndexers')} />
      {/snippet}
    </FilterPill>

    <Button variant="primary" onclick={onsearch} disabled={busy}>
      <Icon name="search" size={14} />
      {busy ? t('component.releaseSearchControls.searching') : t('component.releaseSearchControls.search')}
    </Button>
  </div>

  {#if contextLabel}
    <!-- Not a filter chip: there is no way to take it off, because the item is
         what the grab targets whatever the query above says. -->
    <div class="flex flex-wrap items-center gap-2">
      <span
        data-search-context
        class="inline-flex h-7 items-center gap-2 rounded-full border border-accent bg-accent-tint px-3 text-sm text-accent-text">
        <Icon name="link" size={12} />
        {contextLabel}
      </span>
      <span class="text-sm text-ink-muted">{t('component.releaseSearchControls.grabsLandOnThisItemWhateverYouSearchFor')}</span>
    </div>
  {/if}
</div>
