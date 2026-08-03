<script lang="ts">
  /**
   * Explore → one curated shelf, paged through TMDB's discover endpoint.
   *
   * The media type follows the shelf: a network browses series, a studio
   * browses movies (internal/api/discover.go). The type pills are therefore a
   * filter over what came back, not a second query parameter — which is also
   * why "Movies" on a network shelf legitimately shows nothing.
   */
  import { onMount } from 'svelte';
  import type { DiscoverBrowse, DiscoverItem, DiscoverSourceType, MediaType } from '../api/types';
  import { ApiError, api, errorText } from '../api/client';
  import Button from '../components/Button.svelte';
  import DiscoverCard from '../components/DiscoverCard.svelte';
  import DiscoverError from '../components/DiscoverError.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import PosterGrid from '../components/PosterGrid.svelte';
  import PosterGridSkeleton from '../components/PosterGridSkeleton.svelte';
  import Toggle from '../components/Toggle.svelte';

  interface Props {
    type: DiscoverSourceType;
    id: number;
  }

  let { type, id }: Props = $props();

  type TypeFilter = 'all' | MediaType;
  type SortKey = 'popularity' | 'rating' | 'newest' | 'title';

  const SORTS: { key: SortKey; label: string }[] = [
    { key: 'popularity', label: 'Popularity' },
    { key: 'rating', label: 'Rating' },
    { key: 'newest', label: 'Newest' },
    { key: 'title', label: 'Title' },
  ];

  const SELECT_CLASS =
    'h-9 rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none disabled:opacity-50';

  let page = $state<DiscoverBrowse | null>(null);
  let items = $state<DiscoverItem[]>([]);
  let loading = $state(true);
  let loadingMore = $state(false);
  let error = $state<string | null>(null);
  let status = $state(0);
  let filter = $state<TypeFilter>('all');
  let hideOwned = $state(false);
  let sort = $state<SortKey>('popularity');

  async function load(pageNumber: number) {
    if (pageNumber === 1) loading = true;
    else loadingMore = true;
    try {
      const fetched = await api.discoverBrowse(type, id, pageNumber);
      page = fetched;
      // Pages append: this is one long shelf, not four separate screens. The
      // append dedupes because the grid is keyed by (media_type, tmdb_id) and
      // a duplicate key tears the screen down — and TMDB really does hand back
      // a page twice, either side of a retry or at its own page ceiling.
      items = pageNumber === 1 ? fetched.items : mergeItems(items, fetched.items);
      error = null;
      status = 0;
    } catch (err) {
      error = errorText(err);
      status = err instanceof ApiError ? err.status : 0;
    } finally {
      loading = false;
      loadingMore = false;
    }
  }

  /** A movie and a series can share a TMDB id, so neither half keys alone. */
  function itemKey(item: DiscoverItem): string {
    return `${item.media_type}-${item.tmdb_id}`;
  }

  /** Existing rows plus the ones the new page did not already contain. */
  function mergeItems(existing: DiscoverItem[], fetched: DiscoverItem[]): DiscoverItem[] {
    const seen = new Set(existing.map(itemKey));
    return [...existing, ...fetched.filter((item) => !seen.has(itemKey(item)))];
  }

  onMount(() => void load(1));

  /** The page a "Load more" — first press or retry — should ask for. */
  let nextPage = $derived((page?.page ?? 0) + 1);

  let hasMore = $derived(page !== null && page.page < page.total_pages);

  /**
   * Sorting is client-side over what has been paged in so far, and says so:
   * the browse endpoint has no sort parameter, so pretending to sort the whole
   * catalogue would be a lie about a list the user can only see part of.
   */
  let visible = $derived.by(() => {
    const rows = items.filter((item) => {
      if (filter !== 'all' && item.media_type !== filter) return false;
      if (hideOwned && item.in_library) return false;
      return true;
    });
    const sorted = [...rows];
    switch (sort) {
      case 'rating':
        return sorted.sort((a, b) => b.vote_average - a.vote_average);
      case 'newest':
        return sorted.sort((a, b) => b.date.localeCompare(a.date));
      case 'title':
        return sorted.sort((a, b) => a.title.localeCompare(b.title));
      default:
        return sorted;
    }
  });

  let mixed = $derived(new Set(items.map((i) => i.media_type)).size > 1);
  let sourceName = $derived(page?.source.name ?? '');
  let monogram = $derived(sourceName ? sourceName.slice(0, 1).toUpperCase() : '?');
  let ownedCount = $derived(items.filter((i) => i.in_library).length);
</script>

<div class="flex flex-col gap-6">
  <a
    href="/discover"
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    Discover
  </a>

  {#if error && items.length === 0}
    <DiscoverError message={error} {status} onretry={() => void load(1)} />
  {:else}
    <div class="flex items-center gap-4">
      <span
        class="flex size-12 shrink-0 items-center justify-center rounded-md border border-border
               bg-surface font-display text-lg font-bold text-ink-secondary"
        aria-hidden="true">
        {monogram}
      </span>
      <div class="min-w-0">
        <h2 class="truncate font-display text-xl font-semibold tracking-tight text-ink">
          {sourceName || 'Loading…'}
        </h2>
        <!-- No catalogue count: TMDB's is the whole catalogue, not what
             Caravan could get (internal/api/discover.go). What is countable is
             how many of these rows are already ours. -->
        <p class="text-sm text-ink-secondary">
          {type === 'network' ? 'Network' : 'Studio'} · {ownedCount} in library
        </p>
      </div>
    </div>

    <div class="flex flex-wrap items-center gap-3">
      <div class="flex gap-2" role="group" aria-label="Filter by type">
        {#each [{ key: 'all', label: 'All' }, { key: 'series', label: 'Series' }, { key: 'movie', label: 'Movies' }] as pill (pill.key)}
          <button
            type="button"
            aria-pressed={filter === pill.key}
            onclick={() => (filter = pill.key as TypeFilter)}
            class="inline-flex h-7 items-center rounded-full border px-3 text-sm transition-colors duration-150 ease-out
                   {filter === pill.key
              ? 'border-accent bg-accent-tint text-accent-text'
              : 'border-border bg-surface text-ink-secondary hover:bg-raised hover:text-ink'}">
            {pill.label}
          </button>
        {/each}
      </div>

      <Toggle
        checked={hideOwned}
        label="Hide items in library"
        onchange={(next) => (hideOwned = next)} />

      <select
        bind:value={sort}
        aria-label="Sort results"
        class="ml-auto {SELECT_CLASS}">
        {#each SORTS as option (option.key)}
          <option value={option.key}>{option.label}</option>
        {/each}
      </select>
    </div>

    {#if loading && items.length === 0}
      <PosterGridSkeleton />
    {:else if visible.length === 0}
      <EmptyState
        icon="compass"
        title="Nothing here"
        message="No title on this shelf matches the current filter." />
    {:else}
      <PosterGrid>
        {#each visible as item (itemKey(item))}
          <DiscoverCard {item} showType={mixed} />
        {/each}
      </PosterGrid>
    {/if}

    {#if error}
      <!-- `page` only advances on success, so retrying targets the page that
           failed, never the last one that worked. -->
      <DiscoverError message={error} {status} onretry={() => void load(nextPage)} />
    {/if}

    {#if hasMore}
      <div class="flex justify-center">
        <Button
          variant="secondary"
          disabled={loadingMore}
          onclick={() => void load(nextPage)}>
          {loadingMore ? 'Loading…' : 'Load more'}
        </Button>
      </div>
    {/if}
  {/if}
</div>
