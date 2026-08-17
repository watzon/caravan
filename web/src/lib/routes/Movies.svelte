<script lang="ts">
  /**
   * Library → Movies. DESIGN.md §5 poster grid with status dots and quality
   * badges.
   *
   * `?library=<id>` narrows the grid to one shelf — the sidebar's movie rows
   * all link that way — and the plain /movies URL keeps meaning "every visible
   * movie". The narrowing happens before everything else, so the chips count
   * and the search searches what is on screen rather than the whole install.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Movie } from '../api/types';
  import Button from '../components/Button.svelte';
  import Dropdown from '../components/Dropdown.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import FilterChips from '../components/FilterChips.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PosterCard from '../components/PosterCard.svelte';
  import PosterGrid from '../components/PosterGrid.svelte';
  import PosterGridSkeleton from '../components/PosterGridSkeleton.svelte';
  import SelectActions from '../components/SelectActions.svelte';
  import TextInput from '../components/TextInput.svelte';
  import { useI18n } from '../i18n.svelte';
  import { createSelection } from '../selection.svelte';
  import { navigate, router } from '../router.svelte';
  import {
    filterByLibrary,
    readLibraryFilter,
    readShelfSort,
    sortShelf,
    type ShelfSortKey,
  } from '../shelf';
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
  const { t } = useI18n();

  const SORT_OPTIONS: { key: ShelfSortKey; label: string }[] = [
    { key: 'added', label: t('route.library.sortAdded') },
    { key: 'title', label: t('route.library.sortTitle') },
    { key: 'status', label: t('route.library.sortStatus') },
  ];

  /** The dropdown takes {id, name}; the rail's order is the array's. */
  const SORT_CHOICES = SORT_OPTIONS.map((option) => ({ id: option.key, name: option.label }));

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

  function applySort(value: string) {
    const next = readShelfSort(value);
    const params = router.params;
    if (next === 'added') params.delete('sort');
    else params.set('sort', next);
    const search = params.toString();
    navigate(`${router.path}${search ? `?${search}` : ''}${router.hash}`);
  }

  let sort = $derived(readShelfSort(router.params.get('sort')));

  let all = $derived(filterByLibrary(movies ?? [], readLibraryFilter(router.params)));

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
    return sortShelf(filtered, sort, (m) => MOVIE_FILTERS.indexOf(movieStatus(m)));
  });
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <FilterChips {chips} active={filter} onselect={(key) => (filter = key)} />
    <div class="ml-auto flex w-full flex-wrap items-center justify-end gap-2 sm:w-auto">
      <Dropdown label={t('route.library.sortLabel')} options={SORT_CHOICES} value={sort} onselect={applySort} shape="box" />
      <div class="w-full sm:w-56">
        <TextInput bind:value={query} type="search" placeholder={t('route.library.filterTitles')} ariaLabel={t('route.movies.filterByTitle')} />
      </div>
      <!-- No add button on the rail: the top bar's global add (and ⌘K)
           opens the same dialog, and the empty state carries the contextual
           one. Refresh goes ghost-icon for the same reason: it is a utility,
           not a destination. -->
      <Button variant="ghost" onclick={load} title={t('route.library.reloadList')} class="px-2">
        <Icon name="refresh" size={14} />
        <span class="sr-only">{t('route.library.refresh')}</span>
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
      title={t('route.movies.emptyTitle')}
      message={t('route.movies.emptyMessage')}>
      {#snippet action()}
        <Button variant="primary" onclick={onadd}>
          <Icon name="plus" size={14} />
          {t('route.movies.add')}
        </Button>
      {/snippet}
    </EmptyState>
  {:else if visible.length === 0}
    <EmptyState
      icon="search"
      title={t('route.library.noFilterMatch')}
      message={t('route.movies.noFilterMatchMessage')}>
      {#snippet action()}
        <Button
          variant="secondary"
          onclick={() => {
            filter = 'all';
            query = '';
          }}>
          {t('route.library.clearFilters')}
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
    noun={t('route.movies.noun')}
    plural={t('route.movies.plural')}
    actions={{
      search: (id) => api.searchMovieNow(id),
      setMonitored: (id, monitored) => api.setMovieMonitored(id, monitored),
      remove: (id, deleteFiles) => api.deleteMovie(id, deleteFiles),
    }}
    onchanged={load} />
</div>
