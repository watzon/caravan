<script lang="ts">
  /**
   * The query rail every release search wears: what to ask for, which
   * categories, which indexers (plan part B7).
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
  }

  let {
    query = $bindable(''),
    categories = $bindable([]),
    indexerIDs = $bindable([]),
    indexers,
    busy,
    onsearch,
    contextLabel,
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

  let categoryLabel = $derived(
    categories.length === 0 ? 'All categories' : `${categories.length} categories`,
  );
  let indexerLabel = $derived(
    indexerIDs.length === 0 ? 'All indexers' : `${indexerIDs.length} indexers`,
  );
</script>

<div class="flex flex-col gap-2">
  <div class="flex flex-wrap items-center gap-2">
    <div class="min-w-[16rem] flex-1">
      <TextInput
        bind:value={query}
        type="search"
        {onkeydown}
        placeholder="Search every enabled indexer…"
        ariaLabel="Release search query" />
    </div>

    <FilterPill label={categoryLabel} applied={categories.length > 0} shape="box" width="w-56">
      {#snippet children()}
        <FilterOptions
          options={categoryOptions}
          selected={selectedCategories}
          onselect={toggleCategory}
          emptyText="No categories to choose from" />
      {/snippet}
    </FilterPill>

    <FilterPill label={indexerLabel} applied={indexerIDs.length > 0} shape="box" width="w-64">
      {#snippet children()}
        <FilterOptions
          options={indexerOptions}
          selected={selectedIndexers}
          onselect={toggleIndexer}
          emptyText="No enabled indexers" />
      {/snippet}
    </FilterPill>

    <Button variant="primary" onclick={onsearch} disabled={busy}>
      <Icon name="search" size={14} />
      {busy ? 'Searching…' : 'Search'}
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
      <span class="text-sm text-ink-muted">Grabs land on this item, whatever you search for.</span>
    </div>
  {/if}
</div>
