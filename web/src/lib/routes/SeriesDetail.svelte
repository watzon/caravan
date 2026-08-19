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
  import RemoveItemModal from '../components/RemoveItemModal.svelte';
  import EditMediaModal, {
    type MediaEditValues,
  } from '../components/EditMediaModal.svelte';
  import MoveItemModal from '../components/MoveItemModal.svelte';
  import OverflowMenu from '../components/OverflowMenu.svelte';
  import { libraries } from '../state/libraries.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import Toggle from '../components/Toggle.svelte';
  import {
    UNKNOWN,
    episodeCode,
    formatBytes,
    formatDate,
    formatUntil,
    isFuture,
    seasonLabel,
    titleWithYear,
  } from '../format';
  import MetadataLinks from '../components/MetadataLinks.svelte';
  import { episodeLink, seriesLinks } from '../metadataLinks';
  import { navigate, router } from '../router.svelte';
  import {
    LIBRARY_ITEM_TARGET_CLASS,
    isLibraryItemTarget,
    libraryItemAnchor,
    parseLibraryItemHash,
    revealHashTarget,
    shelfBack,
  } from '../library';
  import { session } from '../state/session.svelte';
  import { libraryChanged, searchQueued } from '../state/activity';
  import { pushToast } from '../state/toast.svelte';
  import { episodeStatus, seriesStatus } from '../status';
  import { compatBadge } from '../tvcompat';
  import { useI18n } from '../i18n.svelte';

  const { t, tp } = useI18n();

  interface Props {
    id: number;
  }

  let { id }: Props = $props();

  let series = $state<Series | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let busy = $state(false);
  let searching = $state(false);
  let editing = $state(false);
  let confirmingRemove = $state(false);
  let movingLibrary = $state(false);
  $effect(() => {
    void libraries.load();
  });
  let canMove = $derived(libraries.accepting('tv').length > 1);
  let removing = $state(false);
  let collapsed = $state<Record<number, boolean>>({});

  let loadEpoch = 0;

  async function load() {
    const epoch = ++loadEpoch;
    loading = true;
    try {
      const next = await api.getSeries(id);
      if (epoch !== loadEpoch) return;
      series = next;
      error = null;
    } catch (err) {
      if (epoch !== loadEpoch) return;
      error = errorText(err);
    } finally {
      if (epoch === loadEpoch) loading = false;
    }
  }

  onMount(load);

  let seasons = $derived<Season[]>(series?.seasons ?? []);

  $effect(() => {
    if (loading || !series) return;
    const target = parseLibraryItemHash(router.hash);
    if (!target || target.adult) return;
    const season = seasons.find((row) => row.season_number === target.season);
    if (season && collapsed[season.season_number]) {
      collapsed = { ...collapsed, [season.season_number]: false };
    }
    void revealHashTarget(router.hash.slice(1));
  });

  function episodesOf(season: Season): Episode[] {
    return season.episodes ?? [];
  }

  function ownedCount(season: Season): number {
    return episodesOf(season).filter((e) => e.file).length;
  }

  // The detail response carries every season's episodes with their files, so
  // the confirm can name a real count rather than a vague "its files".
  let fileCount = $derived(seasons.reduce((total, season) => total + ownedCount(season), 0));
  let episodeCount = $derived(
    seasons.reduce((total, season) => total + episodesOf(season).length, 0),
  );
  let allEpisodesOwned = $derived(episodeCount > 0 && fileCount >= episodeCount);

  async function run(action: () => Promise<unknown>, failureNote: string) {
    busy = true;
    try {
      await action();
      libraryChanged();
      await load();
    } catch (err) {
      pushToast(`${failureNote}: ${errorText(err)}`, 'danger');
    } finally {
      busy = false;
    }
  }

  async function saveSettings(values: MediaEditValues) {
    const current = series;
    if (!current) return;
    series = await api.updateSeries(current.id, {
      monitored: values.monitored,
      quality_profile_id: values.qualityProfileID,
    });
    libraryChanged();
    pushToast(t('route.seriesDetail.updated'), 'success');
  }

  /**
   * Automatic search for monitored missing episodes (SPEC §9). The server
   * queues one job per wanted episode without an active download and answers
   * the count. Unmonitored episodes stay unmonitored.
   */
  async function searchNow() {
    const current = series;
    if (!current) return;
    searching = true;
    try {
      await queueSearch(current);
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      searching = false;
    }
  }

  /**
   * Cascade the series monitored flag to every season and episode, then search
   * the wanted list. The PATCH response is the same tree GET returns, so the
   * page can show the cascaded flags before the search starts.
   */
  function cascadeSeriesMonitored(current: Series): Series {
    return {
      ...current,
      monitored: true,
      seasons: (current.seasons ?? []).map((season) => ({
        ...season,
        monitored: true,
        episodes: (season.episodes ?? []).map((episode) => ({ ...episode, monitored: true })),
      })),
    };
  }

  async function monitorAndSearch() {
    const current = series;
    if (!current) return;
    searching = true;
    const snapshot = current;
    loadEpoch += 1;
    series = cascadeSeriesMonitored(current);
    try {
      const updated = await api.setSeriesMonitored(current.id, true);
      series = updated.seasons ? updated : cascadeSeriesMonitored(updated);
      libraryChanged();
      await queueSearch(updated);
    } catch (err) {
      series = snapshot;
      pushToast(errorText(err), 'danger');
    } finally {
      searching = false;
    }
  }

  let hasUnmonitored = $derived(
    !series?.monitored ||
      seasons.some(
        (season) =>
          !season.monitored || episodesOf(season).some((episode) => !episode.monitored),
      ),
  );

  async function queueSearch(current: Series) {
    const { queued } = await api.searchSeriesNow(current.id);
    if (queued > 0) {
      searchQueued(queued);
    } else {
      pushToast(t('route.seriesDetail.nothingToSearch'), 'info');
    }
  }

  /** See MovieDetail.remove: a successful removal leaves the page it emptied. */
  async function remove(deleteFiles: boolean) {
    const current = series;
    if (!current) return;
    removing = true;
    try {
      await api.deleteSeries(current.id, deleteFiles);
      libraryChanged();
      confirmingRemove = false;
      pushToast(
        deleteFiles
          ? t('route.seriesDetail.removedFiles', { title: current.title })
          : t('route.seriesDetail.removed', { title: current.title }),
        'neutral',
      );
      navigate(back.href);
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      removing = false;
    }
  }

  let back = $derived(
    shelfBack(session.user, series?.library_id ?? 0, {
      href: '/series',
      label: t('route.seriesDetail.back'),
    }),
  );
</script>

<div class="flex flex-col gap-6">
  <a
    href={back.href}
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    {back.label}
  </a>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && series === null}
    <div class="flex flex-col gap-6 md:flex-row">
      <Skeleton class="aspect-[2/3] w-52 rounded-md" />
      <div class="flex min-w-0 flex-1 flex-col gap-3">
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
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <h2 class="font-display text-2xl font-semibold tracking-tight text-ink">
              {current.title}
            </h2>
            <Button variant="ghost" size="sm" onclick={() => (editing = true)}>
              <Icon name="edit" size={14} />
              {t('component.actions.edit')}
            </Button>
            <OverflowMenu
              subject={current.title}
              items={[
                ...(canMove
                  ? [
                      {
                        label: t('route.seriesDetail.moveLibrary'),
                        onselect: () => (movingLibrary = true),
                      },
                    ]
                  : []),
                {
                  label: t('route.seriesDetail.removeLibrary'),
                  danger: true,
                  disabled: removing,
                  onselect: () => (confirmingRemove = true),
                },
              ]} />
          </div>
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

        <p class="max-w-3xl text-md text-ink-secondary">
          {current.overview || t('route.seriesDetail.noOverview')}
        </p>

        <dl class="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div>
            <dt class="micro-label">{t('route.seriesDetail.folder')}</dt>
            <dd class="mt-1 truncate font-mono text-sm text-ink" title={current.path}>
              {current.path || UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">{t('route.seriesDetail.tmdbId')}</dt>
            <dd class="mt-1 font-mono text-sm text-ink">
              {current.tmdb_id > 0 ? current.tmdb_id : UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">{t('route.seriesDetail.firstAired')}</dt>
            <dd class="mt-1 text-sm text-ink">{formatDate(current.first_aired)}</dd>
          </div>
        </dl>
      </div>
    </div>

    <section class="flex flex-col gap-3" aria-labelledby="series-episodes-heading">
      <div class="flex flex-wrap items-center gap-3">
        <div class="min-w-0 flex-1">
          <h3 id="series-episodes-heading" class="text-lg font-semibold text-ink">
            {t('route.seriesDetail.episodes')}
          </h3>
          {#if episodeCount > 0}
            <p class="mt-0.5 text-sm text-ink-secondary">
              {t('route.seriesDetail.onDisk', { owned: fileCount, total: episodeCount })}
            </p>
          {/if}
        </div>

        <div class="ml-auto flex flex-wrap items-center gap-2">
          {#if current.downloading}
            <Button variant="primary" size="sm" href="/queue">
              {t('route.seriesDetail.viewQueue')}
            </Button>
          {/if}
          {#if allEpisodesOwned}
            {#if !current.downloading}
              <Button variant="secondary" size="sm" href="/series/{current.id}/search">
                {t('route.seriesDetail.chooseAnotherRelease')}
              </Button>
            {/if}
          {:else if episodeCount > 0}
            <Button
              variant="primary"
              size="sm"
              disabled={searching}
              onclick={searchNow}>
              <Icon name="search" size={14} />
              {searching ? t('route.seriesDetail.searching') : t('route.seriesDetail.searchMonitored')}
            </Button>
            {#if hasUnmonitored}
              <Button
                variant="secondary"
                size="sm"
                disabled={searching}
                onclick={monitorAndSearch}>
                {t('route.seriesDetail.monitorAndSearch')}
              </Button>
            {/if}
            <Button variant="secondary" size="sm" href="/series/{current.id}/search">
              {t('route.seriesDetail.chooseRelease')}
            </Button>
          {:else}
            <Button variant="secondary" size="sm" href="/series/{current.id}/search">
              {t('route.seriesDetail.chooseRelease')}
            </Button>
          {/if}
        </div>
      </div>

      {#if seasons.length === 0}
        <EmptyState
          icon="tv"
          title={t('route.seriesDetail.noSeasonsTitle')}
          message={t('route.seriesDetail.noSeasonsMessage')} />
      {:else}
        <div class="flex flex-col gap-4">
          {#each seasons as season (season.season_number)}
            {@const episodes = episodesOf(season)}
            {@const isCollapsed = collapsed[season.season_number] ?? false}
            <section class="overflow-hidden rounded-md border border-border">
            <header class="flex flex-wrap items-center gap-3 bg-surface px-3 py-2">
              <button
                type="button"
                class="flex w-full min-w-0 items-center gap-2 text-left sm:w-auto sm:flex-1"
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
                  {t('route.seriesDetail.onDisk', { owned: ownedCount(season), total: episodes.length })}
                </Badge>
              </button>

              <Button
                variant="secondary"
                size="sm"
                href="/series/{current.id}/search/{season.season_number}"
                title={t('route.seriesDetail.searchSeasonPack', { season: seasonLabel(season.season_number) })}>
                <Icon name="search" size={14} />
                {t('route.seriesDetail.chooseReleaseShort')}
              </Button>

              <Toggle
                checked={season.monitored}
                label={t('route.seriesDetail.monitorSeason', { season: seasonLabel(season.season_number) })}
                labelHidden
                disabled={busy}
                onchange={(next) =>
                  run(
                    () => api.setSeasonMonitored(current.id, season.season_number, next),
                    t('route.seriesDetail.updateSeasonFailed'),
                  )} />
            </header>

            {#if !isCollapsed}
              {#if episodes.length === 0}
                <p class="px-3 py-6 text-center text-sm text-ink-secondary">
                  {t('route.seriesDetail.noEpisodes')}
                </p>
              {:else}
                <div class="overflow-x-auto">
                  <table class="w-full min-w-[800px] border-collapse text-sm">
                    <thead>
                      <tr class="bg-surface text-left">
                        <th class="micro-label px-3 py-2 font-semibold">{t('route.seriesDetail.episode')}</th>
                        <th class="micro-label px-3 py-2 font-semibold">{t('route.seriesDetail.title')}</th>
                        <th class="micro-label px-3 py-2 font-semibold">{t('route.seriesDetail.airDate')}</th>
                        <th class="micro-label px-3 py-2 font-semibold">{t('route.seriesDetail.status')}</th>
                        <th class="micro-label px-3 py-2 font-semibold">{t('route.seriesDetail.quality')}</th>
                        <th class="micro-label px-3 py-2 text-right font-semibold">{t('route.seriesDetail.size')}</th>
                        <th class="micro-label px-3 py-2 text-right font-semibold">{t('route.seriesDetail.monitored')}</th>
                        <th class="micro-label px-3 py-2 text-right font-semibold">{t('route.seriesDetail.actions')}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {#each episodes as episode (episode.id)}
                        {@const tmdbPage = episodeLink(
                          current.tmdb_id,
                          episode.season_number,
                          episode.episode_number,
                        )}
                        {@const upcoming = !episode.file && isFuture(episode.air_date)}
                        <tr
                          id={libraryItemAnchor({
                            season_number: episode.season_number,
                            episode_number: episode.episode_number,
                          })}
                          class="h-10 scroll-mt-16 border-t border-border transition-colors duration-150 {isLibraryItemTarget(
                            router.hash,
                            episode,
                          )
                            ? LIBRARY_ITEM_TARGET_CLASS
                            : upcoming
                              ? 'bg-danger/15 hover:bg-danger/20'
                              : 'hover:bg-raised'}">
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
                            {#if upcoming}
                              <span
                                class="inline-flex items-center gap-1.5 text-danger"
                                title={t('route.seriesDetail.airsIn', {
                                  wait: formatUntil(episode.air_date),
                                })}>
                                <Icon name="clock" size={12} class="shrink-0" />
                                {formatDate(episode.air_date)}
                              </span>
                            {:else}
                              {formatDate(episode.air_date)}
                            {/if}
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
                              <span class="text-ink-muted">{t('route.seriesDetail.noFile')}</span>
                            {/if}
                          </td>
                          <td class="px-3 py-2 text-right font-mono text-ink-secondary">
                            {episode.file ? formatBytes(episode.file.size) : t('route.seriesDetail.noFile')}
                          </td>
                          <td class="px-3 py-2">
                            <div class="flex justify-end">
                              <Toggle
                                checked={episode.monitored}
                                label={t('route.seriesDetail.monitorEpisode', { episode: episodeCode(episode.season_number, episode.episode_number) })}
                                labelHidden
                                disabled={busy}
                                onchange={(next) =>
                                  run(
                                    () => api.setEpisodeMonitored(episode.id, next),
                                    t('route.seriesDetail.updateEpisodeFailed'),
                                  )} />
                            </div>
                          </td>
                          <td class="px-3 py-2">
                            <div class="flex justify-end gap-1">
                              {#if episode.file}
                                <ConvertFileButton file={episode.file} compact />
                              {/if}
                              {#if episode.downloading && !episode.file}
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  href="/queue"
                                  title={t('route.seriesDetail.viewQueue')}>
                                  <Icon name="download" size={14} />
                                  <span class="sr-only">{t('route.seriesDetail.viewQueue')}</span>
                                </Button>
                              {:else}
                                {@const releaseAction = episode.file
                                  ? t('route.seriesDetail.chooseAnotherEpisodeRelease', {
                                      episode: episodeCode(episode.season_number, episode.episode_number),
                                    })
                                  : t('route.seriesDetail.chooseEpisodeRelease', {
                                      episode: episodeCode(episode.season_number, episode.episode_number),
                                    })}
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  href="/series/{current.id}/search/{episode.season_number}/{episode.episode_number}"
                                  title={releaseAction}>
                                  <Icon name="search" size={14} />
                                  <span class="sr-only">{releaseAction}</span>
                                </Button>
                              {/if}
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
    </section>

    {#if confirmingRemove}
      <RemoveItemModal
        title={t('route.seriesDetail.removeTitle', { title: current.title })}
        subject={titleWithYear(current.title, current.year)}
        {fileCount}
        busy={removing}
        onconfirm={remove}
        onclose={() => (confirmingRemove = false)} />
    {/if}
    {#if editing}
      <EditMediaModal
        title={current.title}
        kind="series"
        libraryID={current.library_id}
        monitored={current.monitored}
        qualityProfileID={current.quality_profile_id}
        onsave={saveSettings}
        onclose={() => (editing = false)} />
    {/if}
    {#if movingLibrary}
      <MoveItemModal
        itemType="series"
        itemID={current.id}
        itemTitle={current.title}
        kind="tv"
        currentLibraryID={current.library_id}
        onclose={() => (movingLibrary = false)} />
    {/if}
  {/if}
</div>
