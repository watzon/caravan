<script lang="ts">
  /** Library → Series. Same grid as Movies; the note line carries episode counts. */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Series } from '../api/types';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import FilterChips from '../components/FilterChips.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PosterCard from '../components/PosterCard.svelte';
  import PosterGrid from '../components/PosterGrid.svelte';
  import PosterGridSkeleton from '../components/PosterGridSkeleton.svelte';
  import TextInput from '../components/TextInput.svelte';
  import {
    SERIES_FILTERS,
    STATUS,
    seriesStatus,
    type FilterChip,
    type StatusKey,
  } from '../status';

  interface Props {
    onadd: () => void;
  }

  let { onadd }: Props = $props();

  let series = $state<Series[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let filter = $state<StatusKey | 'all'>('all');
  let query = $state('');

  async function load() {
    loading = true;
    try {
      series = await api.listSeries();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let all = $derived(series ?? []);

  let chips = $derived<FilterChip[]>([
    { key: 'all', label: 'All', count: all.length },
    ...SERIES_FILTERS.map((key) => ({
      key,
      label: STATUS[key].label,
      count: all.filter((s) => seriesStatus(s) === key).length,
    })),
  ]);

  let visible = $derived.by(() => {
    const needle = query.trim().toLowerCase();
    return all.filter((s) => {
      if (filter !== 'all' && seriesStatus(s) !== filter) return false;
      if (needle && !s.title.toLowerCase().includes(needle)) return false;
      return true;
    });
  });

  function episodeNote(s: Series): string | null {
    const total = s.episode_count ?? 0;
    if (total === 0) return null;
    return `${s.episode_file_count ?? 0}/${total} eps`;
  }
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <FilterChips {chips} active={filter} onselect={(key) => (filter = key)} />
    <div class="ml-auto flex items-center gap-2">
      <div class="w-56">
        <TextInput bind:value={query} type="search" placeholder="Filter titles…" ariaLabel="Filter series by title" />
      </div>
      <Button variant="secondary" onclick={load} title="Reload the library list">
        <Icon name="refresh" size={14} />
        Refresh
      </Button>
      <Button variant="primary" onclick={onadd}>
        <Icon name="plus" size={14} />
        Add series
      </Button>
    </div>
  </div>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && series === null}
    <PosterGridSkeleton />
  {:else if all.length === 0}
    <EmptyState
      icon="tv"
      title="No series yet"
      message="Add a series from TMDB, or point Caravan at existing media and run a library scan.">
      {#snippet action()}
        <Button variant="primary" onclick={onadd}>
          <Icon name="plus" size={14} />
          Add series
        </Button>
      {/snippet}
    </EmptyState>
  {:else if visible.length === 0}
    <EmptyState
      icon="search"
      title="Nothing matches this filter"
      message="No series in the library matches the current filter and search.">
      {#snippet action()}
        <Button
          variant="secondary"
          onclick={() => {
            filter = 'all';
            query = '';
          }}>
          Clear filters
        </Button>
      {/snippet}
    </EmptyState>
  {:else}
    <PosterGrid>
      {#each visible as item (item.id)}
        <PosterCard
          href={`/series/${item.id}`}
          title={item.title}
          year={item.year}
          posterPath={item.poster_path}
          posterUrl={item.poster_url}
          status={seriesStatus(item)}
          note={episodeNote(item)}
          fallbackIcon="tv" />
      {/each}
    </PosterGrid>
  {/if}
</div>
