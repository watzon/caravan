<script lang="ts">
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { WantedEpisode, WantedLists, WantedMovie, WantedReason } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PageTabs from '../components/PageTabs.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import Icon from '../components/Icon.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { episodeCode, formatDate, titleWithYear } from '../format';
  import { createSelection } from '../selection.svelte';

  type Tab = WantedReason;

  const TABS: { key: Tab; label: string }[] = [
    { key: 'missing', label: 'Missing' },
    { key: 'below_cutoff', label: 'Below cutoff' },
  ];

  let wanted = $state<WantedLists | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let tab = $state<Tab>('missing');
  let searching = $state(false);
  const movieSelection = createSelection();
  const episodeSelection = createSelection();
  let bulkSearching = $state(false);

  async function load() {
    loading = true;
    try {
      wanted = await api.wanted();
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let movies = $derived((wanted?.movies ?? []).filter((item) => item.reason === tab));
  let episodes = $derived((wanted?.episodes ?? []).filter((item) => item.reason === tab));
  let count = $derived(movies.length + episodes.length);
  let selectionActive = $derived(movieSelection.active || episodeSelection.active);
  let selectedCount = $derived(movieSelection.count + episodeSelection.count);

  let tabs = $derived(
    TABS.map((item) => ({
      ...item,
      count: wanted
        ? wanted.movies.filter((movie) => movie.reason === item.key).length +
          wanted.episodes.filter((episode) => episode.reason === item.key).length
        : null,
    })),
  );

  function detail(item: WantedMovie | WantedEpisode): string {
    if (item.reason === 'below_cutoff') {
      return `${item.file_quality || 'Unknown quality'} on disk, cutoff 1080p`;
    }
    if ('air_date' in item && item.air_date) return `Aired ${formatDate(item.air_date)}`;
    if ('air_date' in item) return 'Air date unknown';
    return 'No file in the library';
  }

  /**
   * Queue an automatic search for the whole wanted list — the backlog sweep on
   * demand. The count comes back from the server because it deduplicates
   * against searches already on the queue, so it is not simply `count`.
   */
  async function searchAll() {
    searching = true;
    try {
      const { queued } = await api.searchWanted();
      pushToast(`Queued ${queued} search${queued === 1 ? '' : 'es'}`, queued > 0 ? 'success' : 'info');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      searching = false;
    }
  }

  /**
   * Search exactly the selected rows. Episode ids deliberately use the episode
   * endpoint: the series endpoint would expand one selection to every wanted
   * episode in that series.
   */
  async function searchSelected() {
    const movieIDs = [...movieSelection.ids];
    const episodeIDs = [...episodeSelection.ids];
    const total = movieIDs.length + episodeIDs.length;
    if (total === 0 || bulkSearching) return;

    bulkSearching = true;
    let queued = 0;
    const failedMovies: number[] = [];
    const failedEpisodes: number[] = [];
    try {
      for (const id of movieIDs) {
        try {
          queued += (await api.searchMovieNow(id)).queued;
        } catch {
          failedMovies.push(id);
        }
      }
      for (const id of episodeIDs) {
        try {
          queued += (await api.searchEpisodeNow(id)).queued;
        } catch {
          failedEpisodes.push(id);
        }
      }

      movieSelection.clear();
      episodeSelection.clear();
      for (const id of failedMovies) movieSelection.toggle(id);
      for (const id of failedEpisodes) episodeSelection.toggle(id);

      const failed = failedMovies.length + failedEpisodes.length;
      const message = `Queued ${queued} search${queued === 1 ? '' : 'es'}`;
      if (failed > 0) pushToast(`${message}; ${failed} failed`, 'danger');
      else pushToast(message, queued > 0 ? 'success' : 'info');
      await load();
    } finally {
      bulkSearching = false;
    }
  }

  function clearSelection() {
    movieSelection.clear();
    episodeSelection.clear();
  }

  function onkeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && selectionActive) clearSelection();
  }

  function searchHref(item: WantedMovie | WantedEpisode): string {
    if ('series_id' in item) {
      return `/series/${item.series_id}/search/${item.season_number}/${item.episode_number}`;
    }
    return `/movies/${item.id}/search`;
  }
</script>
<svelte:window {onkeydown} />

<div class="flex max-w-5xl flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <PageTabs
      {tabs}
      active={tab}
      onchange={(key) => (tab = key)}
      ariaLabel="Wanted filter" />
    <div class="ml-auto">
      <!-- The whole list, both tabs: the sweep is not scoped to the filter the
           user happens to be looking at. -->
      <Button variant="primary" size="sm" disabled={searching} onclick={searchAll}>
        <Icon name="search" size={14} />
        {searching ? 'Searching…' : 'Search all'}
      </Button>
    </div>
  </div>

  {#if error && wanted === null}
    <LoadError message={error} onretry={load} />
  {:else if loading && wanted === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 4 }) as _, i (i)}
        <Skeleton class="h-[72px] w-full rounded-md" />
      {/each}
    </div>
  {:else if count === 0}
    <EmptyState
      icon="search"
      title={tab === 'missing' ? 'Nothing is missing' : 'Everything meets the cutoff'}
      message={tab === 'missing'
        ? 'Monitored movies and episodes without a file will appear here.'
        : 'Files below their quality profile cutoff will appear here.'} />
  {:else}
    <div class="flex flex-col gap-6">
      {#if movies.length > 0}
        <section class="flex flex-col gap-2" aria-labelledby="wanted-movies">
          <h2 id="wanted-movies" class="micro-label">Movies</h2>
          <ul class="overflow-hidden rounded-md border border-border bg-surface">
            {#each movies as movie (movie.id)}
              <li
                class="group/row relative flex min-w-0 items-center gap-3 border-b border-border
                       px-3 py-2 last:border-b-0 {movieSelection.has(movie.id) ? 'ring-2 ring-inset ring-accent' : ''}">
                {#if selectionActive}
                  <button
                    type="button"
                    class="absolute inset-0 z-10 focus:outline-none focus:ring-2 focus:ring-inset focus:ring-accent"
                    aria-label="{movieSelection.has(movie.id) ? 'Deselect' : 'Select'} {titleWithYear(movie.title, movie.year)}"
                    aria-pressed={movieSelection.has(movie.id)}
                    onclick={() => movieSelection.toggle(movie.id)}></button>
                {/if}
                <div
                  class="relative z-20 flex min-w-0 flex-1 items-center gap-3
                         {selectionActive ? 'pointer-events-none' : ''}">
                  {#if selectionActive}
                    <span
                      class="flex size-5 shrink-0 items-center justify-center rounded-full border
                             {movieSelection.has(movie.id)
                        ? 'border-accent bg-accent text-ink-inverse'
                        : 'border-border-strong bg-bg text-transparent'}"
                      aria-hidden="true">
                      <Icon name="check" size={12} />
                    </span>
                  {:else}
                    <button
                      type="button"
                      class="pointer-events-auto flex size-5 shrink-0 items-center justify-center rounded-full
                             border border-border-strong bg-bg text-ink-secondary opacity-0
                             transition-opacity duration-150 ease-out hover:border-accent hover:text-accent
                             focus-visible:opacity-100 group-hover/row:opacity-100
                             group-focus-within/row:opacity-100 pointer-coarse:opacity-100"
                      aria-label="Select {titleWithYear(movie.title, movie.year)}"
                      aria-pressed="false"
                      onclick={() => movieSelection.toggle(movie.id)}>
                      <Icon name="check" size={12} />
                    </button>
                  {/if}
                  <!-- Sized by a wrapper: Poster fills its container (w-full),
                       so a width passed via class would fight it and lose. -->
                  <div class="w-9 shrink-0">
                    <Poster path={movie.poster_path} fallback={movie.poster_url} alt={movie.title} />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="truncate font-medium text-ink">{titleWithYear(movie.title, movie.year)}</p>
                    <p class="mt-0.5 truncate text-sm text-ink-secondary">{detail(movie)}</p>
                  </div>
                  <Badge tone={movie.reason === 'missing' ? 'danger' : 'warning'}>{movie.reason === 'missing' ? 'Missing' : 'Below cutoff'}</Badge>
                  {#if !selectionActive}
                    <Button href={searchHref(movie)} size="sm">Search</Button>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      {#if episodes.length > 0}
        <section class="flex flex-col gap-2" aria-labelledby="wanted-episodes">
          <h2 id="wanted-episodes" class="micro-label">Episodes</h2>
          <ul class="overflow-hidden rounded-md border border-border bg-surface">
            {#each episodes as episode (episode.id)}
              <li
                class="group/row relative flex min-w-0 items-center gap-3 border-b border-border
                       px-3 py-2 last:border-b-0 {episodeSelection.has(episode.id) ? 'ring-2 ring-inset ring-accent' : ''}">
                {#if selectionActive}
                  <button
                    type="button"
                    class="absolute inset-0 z-10 focus:outline-none focus:ring-2 focus:ring-inset focus:ring-accent"
                    aria-label="{episodeSelection.has(episode.id) ? 'Deselect' : 'Select'} {episode.series_title} {episodeCode(episode.season_number, episode.episode_number)}"
                    aria-pressed={episodeSelection.has(episode.id)}
                    onclick={() => episodeSelection.toggle(episode.id)}></button>
                {/if}
                <div
                  class="relative z-20 flex min-w-0 flex-1 items-center gap-3
                         {selectionActive ? 'pointer-events-none' : ''}">
                  {#if selectionActive}
                    <span
                      class="flex size-5 shrink-0 items-center justify-center rounded-full border
                             {episodeSelection.has(episode.id)
                        ? 'border-accent bg-accent text-ink-inverse'
                        : 'border-border-strong bg-bg text-transparent'}"
                      aria-hidden="true">
                      <Icon name="check" size={12} />
                    </span>
                  {:else}
                    <button
                      type="button"
                      class="pointer-events-auto flex size-5 shrink-0 items-center justify-center rounded-full
                             border border-border-strong bg-bg text-ink-secondary opacity-0
                             transition-opacity duration-150 ease-out hover:border-accent hover:text-accent
                             focus-visible:opacity-100 group-hover/row:opacity-100
                             group-focus-within/row:opacity-100 pointer-coarse:opacity-100"
                      aria-label="Select {episode.series_title} {episodeCode(episode.season_number, episode.episode_number)}"
                      aria-pressed="false"
                      onclick={() => episodeSelection.toggle(episode.id)}>
                      <Icon name="check" size={12} />
                    </button>
                  {/if}
                  <div class="w-9 shrink-0">
                    <Poster
                      path={episode.poster_path}
                      fallback={episode.poster_url}
                      alt={episode.series_title}
                      fallbackIcon="tv" />
                  </div>
                  <div class="min-w-0 flex-1">
                    <p class="truncate font-medium text-ink">
                      {episode.series_title} - {episodeCode(episode.season_number, episode.episode_number)} - {episode.title}
                    </p>
                    <p class="mt-0.5 truncate text-sm text-ink-secondary">{detail(episode)}</p>
                  </div>
                  <Badge tone={episode.reason === 'missing' ? 'danger' : 'warning'}>{episode.reason === 'missing' ? 'Missing' : 'Below cutoff'}</Badge>
                  {#if !selectionActive}
                    <Button href={searchHref(episode)} size="sm">Search</Button>
                  {/if}
                </div>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    </div>
  {/if}
  {#if selectionActive}
    <div class="pointer-events-none fixed bottom-6 left-60 right-0 z-40 flex justify-center">
      <div
        class="pointer-events-auto flex items-center gap-1 rounded-lg border border-border-strong
               bg-overlay py-1.5 pl-4 pr-1.5 shadow-2xl"
        role="group"
        aria-label="Selection actions">
        <span class="mr-2 whitespace-nowrap text-base font-medium text-ink">
          {selectedCount} selected
        </span>
        <Button
          variant="primary"
          size="sm"
          disabled={bulkSearching}
          onclick={() => void searchSelected()}>
          <Icon name="search" size={14} />
          {bulkSearching ? 'Searching…' : 'Search selected'}
        </Button>
        <span class="mx-1 h-5 w-px bg-border" aria-hidden="true"></span>
        <Button
          variant="ghost"
          size="sm"
          disabled={bulkSearching}
          onclick={clearSelection}
          title="Clear selection">
          <Icon name="close" size={14} />
          <span class="sr-only">Clear selection</span>
        </Button>
      </div>
    </div>
  {/if}
</div>
