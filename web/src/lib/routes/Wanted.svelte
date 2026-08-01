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
  import { episodeCode, formatDate, titleWithYear } from '../format';

  type Tab = WantedReason;

  const TABS: { key: Tab; label: string }[] = [
    { key: 'missing', label: 'Missing' },
    { key: 'below_cutoff', label: 'Below cutoff' },
  ];

  let wanted = $state<WantedLists | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let tab = $state<Tab>('missing');

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
    return 'No file in the library';
  }

  function searchHref(item: WantedMovie | WantedEpisode): string {
    if ('series_id' in item) {
      return `/series/${item.series_id}/search/${item.season_number}/${item.episode_number}`;
    }
    return `/movies/${item.id}/search`;
  }
</script>

<div class="flex max-w-5xl flex-col gap-6">
  <PageTabs
    {tabs}
    active={tab}
    onchange={(key) => (tab = key)}
    ariaLabel="Wanted filter" />

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
              <li class="flex min-w-0 items-center gap-3 border-b border-border px-3 py-2 last:border-b-0">
                <Poster path={movie.poster_path} fallback={movie.poster_url} alt={movie.title} class="h-[54px] w-[36px] shrink-0" />
                <div class="min-w-0 flex-1">
                  <p class="truncate font-medium text-ink">{titleWithYear(movie.title, movie.year)}</p>
                  <p class="mt-0.5 truncate text-sm text-ink-secondary">{detail(movie)}</p>
                </div>
                <Badge tone={movie.reason === 'missing' ? 'danger' : 'warning'}>{movie.reason === 'missing' ? 'Missing' : 'Below cutoff'}</Badge>
                <Button href={searchHref(movie)} size="sm">Search</Button>
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
              <li class="flex min-w-0 items-center gap-3 px-3 py-3">
                <div class="flex size-[54px] shrink-0 items-center justify-center rounded-md bg-raised font-mono text-xs text-ink-secondary">
                  {episodeCode(episode.season_number, episode.episode_number)}
                </div>
                <div class="min-w-0 flex-1">
                  <p class="truncate font-medium text-ink">
                    {episode.series_title} - {episodeCode(episode.season_number, episode.episode_number)} - {episode.title}
                  </p>
                  <p class="mt-0.5 truncate text-sm text-ink-secondary">{detail(episode)}</p>
                </div>
                <Badge tone={episode.reason === 'missing' ? 'danger' : 'warning'}>{episode.reason === 'missing' ? 'Missing' : 'Below cutoff'}</Badge>
                <Button href={searchHref(episode)} size="sm">Search</Button>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    </div>
  {/if}
</div>
