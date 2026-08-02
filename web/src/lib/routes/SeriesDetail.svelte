<script lang="ts">
  /**
   * Series detail: header, then one section per season with its episode table.
   * Monitored flags exist at series, season and episode level (SPEC §7) and a
   * higher-level toggle cascades as a bulk update, not a lock — so after a
   * series or season toggle we reload rather than guessing the children.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Episode, Season, Series } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import ConvertFileButton from '../components/ConvertFileButton.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import Toggle from '../components/Toggle.svelte';
  import { UNKNOWN, episodeCode, formatBytes, formatDate, seasonLabel } from '../format';
  import MetadataLinks from '../components/MetadataLinks.svelte';
  import { episodeLink, seriesLinks } from '../metadataLinks';
  import { pushToast } from '../state/toast.svelte';
  import { episodeStatus, seriesStatus } from '../status';
  import { compatBadge } from '../tvcompat';

  interface Props {
    id: number;
  }

  let { id }: Props = $props();

  let series = $state<Series | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let busy = $state(false);
  let collapsed = $state<Record<number, boolean>>({});

  async function load() {
    loading = true;
    try {
      series = await api.getSeries(id);
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let seasons = $derived<Season[]>(series?.seasons ?? []);

  function episodesOf(season: Season): Episode[] {
    return season.episodes ?? [];
  }

  function ownedCount(season: Season): number {
    return episodesOf(season).filter((e) => e.file).length;
  }

  async function run(action: () => Promise<unknown>, failureNote: string) {
    busy = true;
    try {
      await action();
      await load();
    } catch (err) {
      pushToast(`${failureNote}: ${errorText(err)}`, 'danger');
    } finally {
      busy = false;
    }
  }
</script>

<div class="flex flex-col gap-6">
  <a
    href="/series"
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    Series
  </a>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && series === null}
    <div class="flex gap-6">
      <Skeleton class="aspect-[2/3] w-52 rounded-md" />
      <div class="flex flex-1 flex-col gap-3">
        <Skeleton class="h-8 w-1/2" />
        <Skeleton class="h-4 w-1/4" />
        <Skeleton class="h-20 w-full" />
      </div>
    </div>
  {:else if series}
    {@const current = series}
    <div class="flex flex-col gap-6 md:flex-row">
      <div class="w-40 shrink-0 md:w-52">
        <Poster path={current.poster_path} fallback={current.poster_url} alt={current.title} fallbackIcon="tv" />
      </div>

      <div class="flex min-w-0 flex-1 flex-col gap-4">
        <div class="flex flex-wrap items-start gap-4">
          <div class="min-w-0 flex-1">
            <h2 class="font-display text-2xl font-semibold tracking-tight text-ink">
              {current.title}
            </h2>
            <p class="mt-1 flex flex-wrap items-center gap-3 text-base text-ink-secondary">
              <span>{current.year > 0 ? current.year : UNKNOWN}</span>
              <span class="text-ink-muted">·</span>
              <StatusDot status={seriesStatus(current)} />
              {#if current.status}
                <span class="text-ink-muted">·</span>
                <span>{current.status}</span>
              {/if}
            </p>
            <div class="mt-2">
              <MetadataLinks links={seriesLinks(current)} />
            </div>
          </div>
          <Toggle
            checked={current.monitored}
            label="Monitored"
            disabled={busy}
            onchange={(next) =>
              run(
                () => api.setSeriesMonitored(current.id, next),
                'Could not update the series',
              )} />
        </div>

        <p class="max-w-3xl text-md text-ink-secondary">
          {current.overview || 'No overview available.'}
        </p>

        <dl class="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div>
            <dt class="micro-label">Folder</dt>
            <dd class="mt-1 truncate font-mono text-sm text-ink" title={current.path}>
              {current.path || UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">TMDB id</dt>
            <dd class="mt-1 font-mono text-sm text-ink">
              {current.tmdb_id > 0 ? current.tmdb_id : UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">First aired</dt>
            <dd class="mt-1 text-sm text-ink">{formatDate(current.first_aired)}</dd>
          </div>
        </dl>
      </div>
    </div>

    {#if seasons.length === 0}
      <EmptyState
        icon="tv"
        title="No seasons yet"
        message="Caravan has no season data for this series. Refresh metadata or run a library scan." />
    {:else}
      <div class="flex flex-col gap-4">
        {#each seasons as season (season.season_number)}
          {@const episodes = episodesOf(season)}
          {@const isCollapsed = collapsed[season.season_number] ?? false}
          <section class="overflow-hidden rounded-md border border-border">
            <header class="flex items-center gap-3 bg-surface px-3 py-2">
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 text-left"
                aria-expanded={!isCollapsed}
                onclick={() =>
                  (collapsed = { ...collapsed, [season.season_number]: !isCollapsed })}>
                <span class="text-ink-secondary">
                  <Icon name={isCollapsed ? 'chevronRight' : 'chevronDown'} />
                </span>
                <span class="text-md font-semibold text-ink">
                  {seasonLabel(season.season_number)}
                </span>
                <Badge
                  tone={episodes.length > 0 && ownedCount(season) >= episodes.length
                    ? 'success'
                    : ownedCount(season) > 0
                      ? 'warning'
                      : 'neutral'}>
                  {ownedCount(season)} / {episodes.length}
                </Badge>
              </button>

              <Button
                variant="secondary"
                size="sm"
                href="/series/{current.id}/search/{season.season_number}"
                title={`Search for a ${seasonLabel(season.season_number)} pack`}>
                <Icon name="search" size={14} />
                Search
              </Button>

              <Toggle
                checked={season.monitored}
                label={`Monitor ${seasonLabel(season.season_number)}`}
                labelHidden
                disabled={busy}
                onchange={(next) =>
                  run(
                    () => api.setSeasonMonitored(current.id, season.season_number, next),
                    'Could not update the season',
                  )} />
            </header>

            {#if !isCollapsed}
              {#if episodes.length === 0}
                <p class="px-3 py-6 text-center text-sm text-ink-secondary">
                  No episodes known for this season.
                </p>
              {:else}
                <div class="overflow-x-auto">
                  <table class="w-full min-w-[800px] border-collapse text-sm">
                    <thead>
                      <tr class="bg-surface text-left">
                        <th class="micro-label px-3 py-2 font-semibold">Episode</th>
                        <th class="micro-label px-3 py-2 font-semibold">Title</th>
                        <th class="micro-label px-3 py-2 font-semibold">Air date</th>
                        <th class="micro-label px-3 py-2 font-semibold">Status</th>
                        <th class="micro-label px-3 py-2 font-semibold">Quality</th>
                        <th class="micro-label px-3 py-2 text-right font-semibold">Size</th>
                        <th class="micro-label px-3 py-2 text-right font-semibold">Monitored</th>
                        <th class="micro-label px-3 py-2 text-right font-semibold">Search</th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each episodes as episode (episode.id)}
                        {@const tmdbPage = episodeLink(
                          current.tmdb_id,
                          episode.season_number,
                          episode.episode_number,
                        )}
                        <tr
                          class="h-10 border-t border-border transition-colors duration-150 hover:bg-raised">
                          <td class="px-3 py-2 font-mono text-ink-secondary">
                            {episodeCode(episode.season_number, episode.episode_number)}
                          </td>
                          <td class="max-w-[280px] truncate px-3 py-2 text-ink" title={episode.title}>
                            {#if tmdbPage}
                              <a
                                href={tmdbPage}
                                target="_blank"
                                rel="noopener noreferrer"
                                class="underline-offset-2 transition-colors duration-150 hover:text-accent-text hover:underline">
                                {episode.title || UNKNOWN}
                              </a>
                            {:else}
                              {episode.title || UNKNOWN}
                            {/if}
                          </td>
                          <td class="px-3 py-2 text-ink-secondary">
                            {formatDate(episode.air_date)}
                          </td>
                          <td class="px-3 py-2">
                            <StatusDot status={episodeStatus(episode)} />
                          </td>
                          <td class="px-3 py-2">
                            {#if episode.file}
                              {@const tv = compatBadge(episode.file.compatibility)}
                              <div class="flex flex-wrap items-center gap-1.5">
                                <Badge mono>{episode.file.quality}</Badge>
                                {#if tv}
                                  <Badge mono tone={tv.tone} title={tv.title}>{tv.label}</Badge>
                                {/if}
                              </div>
                            {:else}
                              <span class="text-ink-muted">{UNKNOWN}</span>
                            {/if}
                          </td>
                          <td class="px-3 py-2 text-right font-mono text-ink-secondary">
                            {episode.file ? formatBytes(episode.file.size) : UNKNOWN}
                          </td>
                          <td class="px-3 py-2">
                            <div class="flex justify-end">
                              <Toggle
                                checked={episode.monitored}
                                label={`Monitor ${episodeCode(episode.season_number, episode.episode_number)}`}
                                labelHidden
                                disabled={busy}
                                onchange={(next) =>
                                  run(
                                    () => api.setEpisodeMonitored(episode.id, next),
                                    'Could not update the episode',
                                  )} />
                            </div>
                          </td>
                          <td class="px-3 py-2">
                            <div class="flex justify-end gap-1">
                              {#if episode.file}
                                <ConvertFileButton file={episode.file} compact />
                              {/if}
                              <Button
                                variant="ghost"
                                size="sm"
                                href="/series/{current.id}/search/{episode.season_number}/{episode.episode_number}"
                                title={`Search for ${episodeCode(episode.season_number, episode.episode_number)}`}>
                                <Icon name="search" size={14} />
                                <span class="sr-only">
                                  Search for {episodeCode(episode.season_number, episode.episode_number)}
                                </span>
                              </Button>
                            </div>
                          </td>
                        </tr>
                      {/each}
                    </tbody>
                  </table>
                </div>
              {/if}
            {/if}
          </section>
        {/each}
      </div>
    {/if}
  {/if}
</div>
