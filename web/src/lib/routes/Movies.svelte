<script lang="ts">
  /** Library → Movies. DESIGN.md §5 poster grid with status dots and quality badges. */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Movie } from '../api/types';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import FilterChips from '../components/FilterChips.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PosterCard from '../components/PosterCard.svelte';
  import PosterGrid from '../components/PosterGrid.svelte';
  import PosterGridSkeleton from '../components/PosterGridSkeleton.svelte';
  import SelectActions from '../components/SelectActions.svelte';
  import TextInput from '../components/TextInput.svelte';
  import { createSelection } from '../selection.svelte';
  import { navigate, router } from '../router.svelte';
  import {
    MOVIE_FILTERS,
    STATUS,
    movieStatus,
    type FilterChip,
    type StatusKey,
  } from '../status';

  interface Props {
    onadd: () => void;
  }

  let { onadd }: Props = $props();

  type SortKey = 'title' | 'added' | 'status';

  const SORT_OPTIONS: { key: SortKey; label: string }[] = [
    { key: 'title', label: 'Title' },
    { key: 'added', label: 'Added' },
    { key: 'status', label: 'Status' },
  ];

  const SELECT_CLASS =
    'h-9 rounded-sm border border-border-strong bg-raised px-3 text-md text-ink ' +
    'focus:border-accent focus:outline-none';

  let movies = $state<Movie[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let filter = $state<StatusKey | 'all'>('all');
  let query = $state('');
  const selection = createSelection();

  async function load() {
    loading = true;
    try {
      movies = await api.listMovies();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function readSort(value: string | null): SortKey {
    return value === 'added' || value === 'status' ? value : 'title';
  }

  function compareTitle(a: Movie, b: Movie): number {
    return (
      (a.sort_title || a.title).localeCompare(b.sort_title || b.title) ||
      a.title.localeCompare(b.title) ||
      a.id - b.id
    );
  }

  function applySort(value: string) {
    const next = readSort(value);
    const params = router.params;
    if (next === 'title') params.delete('sort');
    else params.set('sort', next);
    const search = params.toString();
    navigate(`${router.path}${search ? `?${search}` : ''}${router.hash}`);
  }

  let sort = $derived(readSort(router.params.get('sort')));

  let all = $derived(movies ?? []);

  let chips = $derived<FilterChip[]>([
    { key: 'all', label: 'All', count: all.length },
    ...MOVIE_FILTERS.map((key) => ({
      key,
      label: STATUS[key].label,
      count: all.filter((m) => movieStatus(m) === key).length,
    })),
  ]);

  let visible = $derived.by(() => {
    const needle = query.trim().toLowerCase();
    const filtered = all.filter((m) => {
      if (filter !== 'all' && movieStatus(m) !== filter) return false;
      if (needle && !m.title.toLowerCase().includes(needle)) return false;
      return true;
    });
    return [...filtered].sort((a, b) => {
      if (sort === 'added') {
        return b.added_at.localeCompare(a.added_at) || compareTitle(a, b);
      }
      if (sort === 'status') {
        return (
          MOVIE_FILTERS.indexOf(movieStatus(a)) - MOVIE_FILTERS.indexOf(movieStatus(b)) ||
          compareTitle(a, b)
        );
      }
      return compareTitle(a, b);
    });
  });
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <FilterChips {chips} active={filter} onselect={(key) => (filter = key)} />
    <div class="ml-auto flex w-full flex-wrap items-center justify-end gap-2 sm:w-auto">
      <select
        value={sort}
        aria-label="Sort movies"
        onchange={(event) => applySort(event.currentTarget.value)}
        class={SELECT_CLASS}>
        {#each SORT_OPTIONS as option (option.key)}
          <option value={option.key}>{option.label}</option>
        {/each}
      </select>
      <div class="w-full sm:w-56">
        <TextInput bind:value={query} type="search" placeholder="Filter titles…" ariaLabel="Filter movies by title" />
      </div>
      <Button variant="secondary" onclick={load} title="Reload the library list">
        <Icon name="refresh" size={14} />
        Refresh
      </Button>
      <Button variant="primary" onclick={onadd}>
        <Icon name="plus" size={14} />
        Add movie
      </Button>
    </div>
  </div>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && movies === null}
    <PosterGridSkeleton />
  {:else if all.length === 0}
    <EmptyState
      icon="film"
      title="No movies yet"
      message="Add a movie from TMDB, or point Caravan at existing media and run a library scan.">
      {#snippet action()}
        <Button variant="primary" onclick={onadd}>
          <Icon name="plus" size={14} />
          Add movie
        </Button>
      {/snippet}
    </EmptyState>
  {:else if visible.length === 0}
    <EmptyState
      icon="search"
      title="Nothing matches this filter"
      message="No movie in the library matches the current filter and search.">
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
      {#each visible as movie (movie.id)}
        <PosterCard
          href={`/movies/${movie.id}`}
          title={movie.title}
          year={movie.year}
          posterPath={movie.poster_path}
          posterUrl={movie.poster_url}
          status={movieStatus(movie)}
          quality={movie.file?.quality && movie.file.quality !== 'unknown'
            ? movie.file.quality
            : null}
          selectable={selection.active}
          selected={selection.has(movie.id)}
          ontoggle={() => selection.toggle(movie.id)} />
      {/each}
    </PosterGrid>
  {/if}

  <SelectActions
    {selection}
    noun="movie"
    plural="movies"
    actions={{
      search: (id) => api.searchMovieNow(id),
      setMonitored: (id, monitored) => api.setMovieMonitored(id, monitored),
      remove: (id, deleteFiles) => api.deleteMovie(id, deleteFiles),
    }}
    onchanged={load} />
</div>
