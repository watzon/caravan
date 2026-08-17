<script lang="ts">
  /**
   * Library → Anime. The one shelf that lists two item tables at once.
   *
   * An anime library owns films AND series, because the catalogue it identifies
   * against files both under one vocabulary (core.LibraryKindAnime). So this
   * screen asks for both — `GET /library/movies` and `GET /library/series?
   * kind=anime` — and merges them into a single grid, where each card links to
   * the detail screen its own type already has. There is no anime detail page
   * and there should not be one: an anime film is a movie row and an anime
   * series is a series row, and only the SHELF is unified.
   *
   * With no `?library=`, "anime" means the items owned by an anime-kind library
   * in this session. That list comes from /auth/me rather than the admin-only
   * GET /libraries, so this screen needs no second permission model: a shelf the
   * session cannot see contributes no ids, and therefore no rows.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Movie, Series } from '../api/types';
  import Button from '../components/Button.svelte';
  import Dropdown from '../components/Dropdown.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import FilterChips from '../components/FilterChips.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PosterCard from '../components/PosterCard.svelte';
  import PosterGrid from '../components/PosterGrid.svelte';
  import PosterGridSkeleton from '../components/PosterGridSkeleton.svelte';
  import TextInput from '../components/TextInput.svelte';
  import { useI18n } from '../i18n.svelte';
  import { sessionLibraryIDs } from '../library';
  import { navigate, router } from '../router.svelte';
  import { session } from '../state/session.svelte';
  import {
    filterByLibrary,
    inLibrary,
    readLibraryFilter,
    readShelfSort,
    sortShelf,
    type ShelfSortKey,
  } from '../shelf';
  import {
    SERIES_FILTERS,
    STATUS,
    movieStatus,
    seriesStatus,
    type FilterChip,
    type StatusKey,
  } from '../status';

  interface Props {
    onadd: () => void;
  }

  let { onadd }: Props = $props();
  const { t, tp } = useI18n();

  /**
   * One card's worth of a merged row: the shelf item fields the sort needs,
   * plus which of the two tables it came from. `type` is what decides the href
   * and the fallback glyph, so it travels with the row rather than being
   * re-derived at render time.
   */
  interface AnimeItem {
    id: number;
    type: 'movie' | 'series';
    title: string;
    sort_title: string;
    added_at: string;
    library_id?: number;
    year: number;
    poster_path: string;
    poster_url: string;
    status: StatusKey;
    quality: string | null;
    note: string | null;
  }

  const SORT_OPTIONS: { key: ShelfSortKey; label: string }[] = [
    { key: 'added', label: t('route.library.sortAdded') },
    { key: 'title', label: t('route.library.sortTitle') },
    { key: 'status', label: t('route.library.sortStatus') },
  ];

  /** The dropdown takes {id, name}; the rail's order is the array's. */
  const SORT_CHOICES = SORT_OPTIONS.map((option) => ({ id: option.key, name: option.label }));

  let movies = $state<Movie[] | null>(null);
  let series = $state<Series[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let filter = $state<StatusKey | 'all'>('all');
  let query = $state('');

  async function load() {
    loading = true;
    try {
      // Both together: a merged grid that painted its films a tick before its
      // series would re-sort itself under the reader.
      const [loadedMovies, loadedSeries] = await Promise.all([
        api.listMovies(),
        api.listSeries({ kind: 'anime' }),
      ]);
      movies = loadedMovies;
      series = loadedSeries;
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
  let libraryFilter = $derived(readLibraryFilter(router.params));

  /**
   * The anime shelves this session has, for the unfiltered view.
   *
   * The series half needs no such filter — the server already answered with
   * anime rows only — but the movie half does: `GET /library/movies` is every
   * visible film, and only the ones an anime library owns belong here.
   */
  let animeLibraryIDs = $derived(sessionLibraryIDs(session.user, 'anime'));

  function movieCard(m: Movie): AnimeItem {
    return {
      id: m.id,
      type: 'movie',
      title: m.title,
      sort_title: m.sort_title,
      added_at: m.added_at,
      library_id: m.library_id,
      year: m.year,
      poster_path: m.poster_path,
      poster_url: m.poster_url,
      status: movieStatus(m),
      quality: m.file?.quality && m.file.quality !== 'unknown' ? m.file.quality : null,
      note: null,
    };
  }

  function seriesCard(s: Series): AnimeItem {
    const total = s.episode_count ?? 0;
    return {
      id: s.id,
      type: 'series',
      title: s.title,
      sort_title: s.sort_title,
      added_at: s.added_at,
      library_id: s.library_id,
      year: s.year,
      poster_path: s.poster_path,
      poster_url: s.poster_url,
      status: seriesStatus(s),
      quality: null,
      note: total === 0 ? null : tp('route.series.episodeNote', total, { files: s.episode_file_count ?? 0 }),
    };
  }

  let all = $derived.by<AnimeItem[]>(() => {
    const films = movies ?? [];
    const shows = series ?? [];
    if (libraryFilter !== 0) {
      return [
        ...filterByLibrary(films, libraryFilter).map(movieCard),
        ...filterByLibrary(shows, libraryFilter).map(seriesCard),
      ];
    }
    return [
      ...films.filter((m) => animeLibraryIDs.some((id) => inLibrary(m, id))).map(movieCard),
      ...shows.map(seriesCard),
    ];
  });

  /**
   * The series chip vocabulary, not the movie one: it is the wider of the two
   * (a film is never `incomplete`), so one set of chips grades both halves
   * without a chip that can never match.
   */
  let chips = $derived<FilterChip[]>([
    { key: 'all', label: 'All', count: all.length },
    ...SERIES_FILTERS.map((key) => ({
      key,
      label: STATUS[key].label,
      count: all.filter((item) => item.status === key).length,
    })),
  ]);

  let visible = $derived.by(() => {
    const needle = query.trim().toLowerCase();
    const filtered = all.filter((item) => {
      if (filter !== 'all' && item.status !== filter) return false;
      if (needle && !item.title.toLowerCase().includes(needle)) return false;
      return true;
    });
    return sortShelf(filtered, sort, (item) => SERIES_FILTERS.indexOf(item.status));
  });

  /** A film and a series may share an id; the table they came from breaks the tie. */
  function cardKey(item: AnimeItem): string {
    return `${item.type}:${item.id}`;
  }
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <FilterChips {chips} active={filter} onselect={(key) => (filter = key)} />
    <div class="ml-auto flex w-full flex-wrap items-center justify-end gap-2 sm:w-auto">
      <Dropdown label={t('route.library.sortLabel')} options={SORT_CHOICES} value={sort} onselect={applySort} shape="box" />
      <div class="w-full sm:w-56">
        <TextInput bind:value={query} type="search" placeholder={t('route.library.filterTitles')} ariaLabel={t('route.anime.filterByTitle')} />
      </div>
      <!-- Ghost icon, like the other two shelves: the top bar's global add (and
           ⌘K) opens the same dialog, and refresh is a utility rather than a
           destination. -->
      <Button variant="ghost" onclick={load} title={t('route.library.reloadList')} class="px-2">
        <Icon name="refresh" size={14} />
        <span class="sr-only">{t('route.library.refresh')}</span>
      </Button>
    </div>
  </div>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && movies === null && series === null}
    <PosterGridSkeleton />
  {:else if all.length === 0}
    <EmptyState
      icon="sparkles"
      title={t('route.anime.emptyTitle')}
      message={t('route.anime.emptyMessage')}>
      {#snippet action()}
        <Button variant="primary" onclick={onadd}>
          <Icon name="plus" size={14} />
          {t('route.anime.add')}
        </Button>
      {/snippet}
    </EmptyState>
  {:else if visible.length === 0}
    <EmptyState
      icon="search"
      title={t('route.library.noFilterMatch')}
      message={t('route.anime.noFilterMatchMessage')}>
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
      {#each visible as item (cardKey(item))}
        <!-- The href is the item's own detail screen. A merged shelf does not
             mean a merged item: /movies/:id and /series/:id already know how to
             render an anime film and an anime series. -->
        <PosterCard
          href={item.type === 'movie' ? `/movies/${item.id}` : `/series/${item.id}`}
          title={item.title}
          year={item.year}
          posterPath={item.poster_path}
          posterUrl={item.poster_url}
          status={item.status}
          quality={item.quality}
          note={item.note}
          fallbackIcon={item.type === 'movie' ? 'film' : 'tv'} />
      {/each}
    </PosterGrid>
  {/if}
</div>
