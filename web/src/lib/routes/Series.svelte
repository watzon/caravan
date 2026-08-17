<script lang="ts">
  /**
   * Library → Series. Same grid as Movies; the note line carries episode counts,
   * and `?library=<id>` narrows it to one shelf exactly as it does there.
   *
   * This screen lists television only. A series filed as anime lives on /anime
   * — the server's `GET /library/series` defaults to `kind=tv` and the anime
   * screen is the one that asks for the other vocabulary.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Series } from '../api/types';
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
  const { t, tp } = useI18n();

  const SORT_OPTIONS: { key: ShelfSortKey; label: string }[] = [
    { key: 'added', label: t('route.library.sortAdded') },
    { key: 'title', label: t('route.library.sortTitle') },
    { key: 'status', label: t('route.library.sortStatus') },
  ];

  /** The dropdown takes {id, name}; the rail's order is the array's. */
  const SORT_CHOICES = SORT_OPTIONS.map((option) => ({ id: option.key, name: option.label }));

  let series = $state<Series[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let filter = $state<StatusKey | 'all'>('all');
  let query = $state('');
  const selection = createSelection();

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

  function applySort(value: string) {
    const next = readShelfSort(value);
    const params = router.params;
    if (next === 'added') params.delete('sort');
    else params.set('sort', next);
    const search = params.toString();
    navigate(`${router.path}${search ? `?${search}` : ''}${router.hash}`);
  }

  let sort = $derived(readShelfSort(router.params.get('sort')));

  let all = $derived(filterByLibrary(series ?? [], readLibraryFilter(router.params)));

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
    const filtered = all.filter((s) => {
      if (filter !== 'all' && seriesStatus(s) !== filter) return false;
      if (needle && !s.title.toLowerCase().includes(needle)) return false;
      return true;
    });
    return sortShelf(filtered, sort, (s) => SERIES_FILTERS.indexOf(seriesStatus(s)));
  });

  function episodeNote(s: Series): string | null {
    const total = s.episode_count ?? 0;
    if (total === 0) return null;
    return tp('route.series.episodeNote', total, { files: s.episode_file_count ?? 0 });
  }
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <FilterChips {chips} active={filter} onselect={(key) => (filter = key)} />
    <div class="ml-auto flex w-full flex-wrap items-center justify-end gap-2 sm:w-auto">
      <Dropdown label={t('route.library.sortLabel')} options={SORT_CHOICES} value={sort} onselect={applySort} shape="box" />
      <div class="w-full sm:w-56">
        <TextInput bind:value={query} type="search" placeholder={t('route.library.filterTitles')} ariaLabel={t('route.series.filterByTitle')} />
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
  {:else if loading && series === null}
    <PosterGridSkeleton />
  {:else if all.length === 0}
    <EmptyState
      icon="tv"
      title={t('route.series.emptyTitle')}
      message={t('route.series.emptyMessage')}>
      {#snippet action()}
        <Button variant="primary" onclick={onadd}>
          <Icon name="plus" size={14} />
          {t('route.series.add')}
        </Button>
      {/snippet}
    </EmptyState>
  {:else if visible.length === 0}
    <EmptyState
      icon="search"
      title={t('route.library.noFilterMatch')}
      message={t('route.series.noFilterMatchMessage')}>
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
      {#each visible as item (item.id)}
        <PosterCard
          href={`/series/${item.id}`}
          title={item.title}
          year={item.year}
          posterPath={item.poster_path}
          posterUrl={item.poster_url}
          status={seriesStatus(item)}
          note={episodeNote(item)}
          fallbackIcon="tv"
          selectable={selection.active}
          selected={selection.has(item.id)}
          ontoggle={() => selection.toggle(item.id)} />
      {/each}
    </PosterGrid>
  {/if}

  <SelectActions
    {selection}
    noun="series"
    plural="series"
    actions={{
      search: (id) => api.searchSeriesNow(id),
      setMonitored: (id, monitored) => api.setSeriesMonitored(id, monitored),
      remove: (id, deleteFiles) => api.deleteSeries(id, deleteFiles),
    }}
    onchanged={load} />
</div>
