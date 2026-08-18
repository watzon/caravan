<script lang="ts">
  /** Movie detail (DESIGN.md §4: 32px display item title, machine text in mono). */
  import { onDestroy, onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { CastMember, Movie } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import ConvertFileButton from '../components/ConvertFileButton.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Poster from '../components/Poster.svelte';
  import RemoveItemModal from '../components/RemoveItemModal.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import EditMediaModal, {
    type MediaEditValues,
  } from '../components/EditMediaModal.svelte';
  import MoveItemModal from '../components/MoveItemModal.svelte';
  import OverflowMenu from '../components/OverflowMenu.svelte';
  import { libraries } from '../state/libraries.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import { UNKNOWN, formatBytes, formatDate, titleWithYear, truncateMiddle } from '../format';
  import MetadataLinks from '../components/MetadataLinks.svelte';
  import { movieLinks } from '../metadataLinks';
  import { navigate } from '../router.svelte';
  import { shelfBack } from '../library';
  import { session } from '../state/session.svelte';
  import { libraryChanged, searchQueued } from '../state/activity';
  import { pushToast } from '../state/toast.svelte';
  import { movieStatus } from '../status';
  import { compatBadge } from '../tvcompat';

  import { useI18n } from '../i18n.svelte';

  const { t } = useI18n();

  interface Props {
    id: number;
  }

  let { id }: Props = $props();

  let movie = $state<Movie | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let searching = $state(false);
  let editing = $state(false);
  let confirmingRemove = $state(false);
  let movingLibrary = $state(false);
  // Loaded once per session; the menu offers a move only when there is
  // somewhere else of this kind to move to.
  $effect(() => {
    void libraries.load();
  });
  let canMove = $derived(libraries.accepting('movie').length > 1);
  let removing = $state(false);
  let cast = $state<CastMember[]>([]);
  let loadAbort: AbortController | null = null;

  async function loadCast(tmdbID: number, controller: AbortController) {
    try {
      const detail = await api.discoverTitle('movie', tmdbID, controller.signal);
      if (loadAbort === controller && !controller.signal.aborted) {
        cast = detail.cast ?? [];
      }
    } catch {
      // Cast is supplemental: unavailable metadata never hides the library movie.
    }
  }

  async function load() {
    loadAbort?.abort();
    const controller = new AbortController();
    loadAbort = controller;
    cast = [];
    loading = true;
    try {
      const loaded = await api.getMovie(id, controller.signal);
      if (loadAbort !== controller || controller.signal.aborted) return;
      movie = loaded;
      error = null;
      if (loaded.tmdb_id > 0) void loadCast(loaded.tmdb_id, controller);
    } catch (err) {
      if (loadAbort !== controller || controller.signal.aborted) return;
      error = errorText(err);
    } finally {
      if (loadAbort === controller && !controller.signal.aborted) loading = false;
    }
  }

  onMount(() => void load());
  onDestroy(() => loadAbort?.abort());

  async function saveSettings(values: MediaEditValues) {
    const current = movie;
    if (!current) return;
    movie = await api.updateMovie(current.id, {
      monitored: values.monitored,
      quality_profile_id: values.qualityProfileID,
      min_availability: values.minAvailability ?? current.min_availability,
    });
    libraryChanged();
    pushToast(t('route.movieDetail.updated'), 'success');
  }

  /**
   * Automatic search (SPEC §9): the server queues the job and answers how many
   * it added, so a movie with a file or active download does not claim that a
   * search started.
   */
  async function searchNow() {
    const current = movie;
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

  async function monitorAndSearch() {
    const current = movie;
    if (!current) return;
    searching = true;
    try {
      const updated = await api.setMovieMonitored(current.id, true);
      movie = updated;
      libraryChanged();
      await queueSearch(updated);
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      searching = false;
    }
  }

  async function queueSearch(current: Movie) {
    const { queued } = await api.searchMovieNow(current.id);
    if (queued > 0) {
      searchQueued(queued);
    } else {
      pushToast(t('route.movieDetail.searchNone'), 'info');
    }
  }

  /**
   * Removing leaves the page: the movie it describes is gone. A failure keeps
   * the user here with the reason, because the movie is still in the library.
   */
  async function remove(deleteFiles: boolean) {
    const current = movie;
    if (!current) return;
    removing = true;
    try {
      await api.deleteMovie(current.id, deleteFiles);
      libraryChanged();
      confirmingRemove = false;
      pushToast(
        deleteFiles
          ? t('route.movieDetail.removedFiles', { title: current.title })
          : t('route.movieDetail.removedLibrary', { title: current.title }),
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
    shelfBack(session.user, movie?.library_id ?? 0, {
      href: '/movies',
      label: t('route.movieDetail.back'),
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
  {:else if loading && movie === null}
    <div class="flex flex-col gap-6 md:flex-row">
      <Skeleton class="aspect-[2/3] w-52 rounded-md" />
      <div class="flex min-w-0 flex-1 flex-col gap-3">
        <Skeleton class="h-8 w-1/2" />
        <Skeleton class="h-4 w-1/4" />
        <Skeleton class="h-20 w-full" />
      </div>
    </div>
  {:else if movie}
    {@const file = movie.file}
    <div class="flex flex-col gap-6 md:flex-row">
      <div class="w-40 shrink-0 md:w-52">
        <Poster path={movie.poster_path} fallback={movie.poster_url} alt={movie.title} />
      </div>

      <div class="flex min-w-0 flex-1 flex-col gap-4">
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
            <h2 class="font-display text-2xl font-semibold tracking-tight text-ink">
              {movie.title}
            </h2>
            <Button variant="ghost" size="sm" onclick={() => (editing = true)}>
              <Icon name="edit" size={14} />
              {t('component.actions.edit')}
            </Button>
            <OverflowMenu
              subject={movie.title}
              items={[
                ...(canMove
                  ? [
                      {
                        label: t('route.movieDetail.moveToLibrary'),
                        onselect: () => (movingLibrary = true),
                      },
                    ]
                  : []),
                {
                  label: t('route.movieDetail.removeFromLibrary'),
                  danger: true,
                  disabled: removing,
                  onselect: () => (confirmingRemove = true),
                },
              ]} />
          </div>
          <p class="mt-1 flex flex-wrap items-center gap-3 text-base text-ink-secondary">
            <span>{movie.year > 0 ? movie.year : UNKNOWN}</span>
            <span class="text-ink-muted">·</span>
            <StatusDot status={movieStatus(movie)} />
            {#if movie.release_date}
              <span class="text-ink-muted">·</span>
              <span>{t('route.movieDetail.released', { date: formatDate(movie.release_date) })}</span>
            {/if}
          </p>
          <div class="mt-2">
            <MetadataLinks links={movieLinks(movie)} />
          </div>
        </div>

        <p class="max-w-3xl text-md text-ink-secondary">
          {movie.overview || t('route.movieDetail.noOverview')}
        </p>

        {#if cast.length > 0}
          <section class="max-w-3xl" aria-labelledby="movie-cast-heading">
            <h3 id="movie-cast-heading" class="micro-label">{t('route.movieDetail.cast')}</h3>
            <ul class="mt-2 grid grid-cols-1 gap-x-6 gap-y-1 sm:grid-cols-2">
              {#each cast.slice(0, 6) as member (`${member.tmdb_id}-${member.name}-${member.character}`)}
                <li class="flex min-w-0 items-baseline gap-2 text-sm">
                  <span class="min-w-0 flex-1 truncate text-ink" title={member.name}>
                    {member.name}
                  </span>
                  {#if member.character}
                    <span class="shrink-0 text-xs text-ink-muted">{t('route.movieDetail.as')}</span>
                    <span
                      class="min-w-0 flex-1 truncate text-ink-secondary"
                      title={member.character}>
                      {member.character}
                    </span>
                  {/if}
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        <dl class="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div>
            <dt class="micro-label">{t('route.movieDetail.folder')}</dt>
            <dd class="mt-1 truncate font-mono text-sm text-ink" title={movie.path}>
              {movie.path || UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">{t('route.movieDetail.tmdbId')}</dt>
            <dd class="mt-1 font-mono text-sm text-ink">
              {movie.tmdb_id > 0 ? movie.tmdb_id : UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">{t('route.movieDetail.added')}</dt>
            <dd class="mt-1 text-sm text-ink">{formatDate(movie.added_at)}</dd>
          </div>
        </dl>
      </div>
    </div>

    <section class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center gap-3">
        <h3 class="text-lg font-semibold text-ink">{t('route.movieDetail.file')}</h3>
        {#if file}
          <div class="ml-auto flex flex-wrap items-center gap-2">
            <Button variant="secondary" size="sm" href="/movies/{movie.id}/search">
              {t('route.movieDetail.chooseAnotherRelease')}
            </Button>
            <ConvertFileButton {file} />
          </div>
        {/if}
      </div>

      {#if !file}
        <EmptyState
          icon={movie.downloading ? 'download' : 'folder'}
          title={movie.downloading
            ? t('route.movieDetail.downloadingTitle')
            : t('route.movieDetail.noFileTitle')}
          message={movie.downloading
            ? t('route.movieDetail.downloadingMessage')
            : movie.monitored
              ? t('route.movieDetail.noFileMonitoredMessage')
              : t('route.movieDetail.noFileUnmonitoredMessage')}>
          {#snippet action()}
            {#if movie.downloading}
              <Button variant="primary" href="/queue">
                {t('route.movieDetail.viewQueue')}
              </Button>
            {:else}
              <div class="flex flex-col items-center gap-3">
                <div class="flex flex-wrap justify-center gap-2">
                  <Button
                    variant="primary"
                    disabled={searching}
                    onclick={movie.monitored ? searchNow : monitorAndSearch}>
                    <Icon name="search" size={14} />
                    {searching
                      ? t('route.movieDetail.searching')
                      : movie.monitored
                        ? t('route.movieDetail.searchNow')
                        : t('route.movieDetail.monitorAndSearch')}
                  </Button>
                  <Button variant="secondary" href="/movies/{movie.id}/search">
                    {t('route.movieDetail.chooseRelease')}
                  </Button>
                </div>
                <a
                  href="/scan-review"
                  class="text-sm text-ink-secondary underline-offset-4 transition-colors duration-150 hover:text-ink hover:underline">
                  {t('route.movieDetail.alreadyCopied')}
                </a>
              </div>
            {/if}
          {/snippet}
        </EmptyState>
      {:else}
        {@const tv = compatBadge(file.compatibility)}
        <div class="overflow-x-auto rounded-md border border-border">
          <table class="w-full min-w-[640px] border-collapse text-sm">
            <thead>
              <tr class="bg-surface text-left">
                <th class="micro-label px-3 py-2 font-semibold">{t('route.movieDetail.path')}</th>
                <th class="micro-label px-3 py-2 font-semibold">{t('route.movieDetail.quality')}</th>
                <th class="micro-label px-3 py-2 font-semibold">{t('route.movieDetail.source')}</th>
                <th class="micro-label px-3 py-2 font-semibold">{t('route.movieDetail.codec')}</th>
                <th class="micro-label px-3 py-2 font-semibold">{t('route.movieDetail.audio')}</th>
                <th class="micro-label px-3 py-2 font-semibold">{t('route.movieDetail.tv')}</th>
                <th class="micro-label px-3 py-2 text-right font-semibold">{t('route.movieDetail.size')}</th>
              </tr>
            </thead>
            <tbody>
              <tr class="h-10 border-t border-border transition-colors duration-150 hover:bg-raised">
                <td class="px-3 py-2 font-mono text-ink" title={file.path}>
                  {truncateMiddle(file.path, 64)}
                </td>
                <td class="px-3 py-2"><Badge mono>{file.quality}</Badge></td>
                <td class="px-3 py-2"><Badge mono>{file.source}</Badge></td>
                <td class="px-3 py-2">
                  {#if file.codec}<Badge mono>{file.codec}</Badge>{:else}<span class="text-ink-muted">{UNKNOWN}</span>{/if}
                </td>
                <td class="px-3 py-2">
                  {#if file.audio}<Badge mono tone={file.audio.toUpperCase().includes('DTS') ? 'warning' : 'neutral'}>{file.audio}</Badge>{:else}<span class="text-ink-muted">{UNKNOWN}</span>{/if}
                </td>
                <td class="px-3 py-2">
                  {#if tv}
                    <Badge mono tone={tv.tone} title={tv.title}>{tv.label}</Badge>
                  {:else}
                    <span class="text-ink-muted">{UNKNOWN}</span>
                  {/if}
                </td>
                <td class="px-3 py-2 text-right font-mono text-ink-secondary">
                  {formatBytes(file.size)}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      {/if}
    </section>

    {#if confirmingRemove}
      <RemoveItemModal
        title={t('route.movieDetail.removeTitle', { title: movie.title })}
        subject={titleWithYear(movie.title, movie.year)}
        fileCount={file ? 1 : 0}
        busy={removing}
        onconfirm={remove}
        onclose={() => (confirmingRemove = false)} />
    {/if}
    {#if editing}
      <EditMediaModal
        title={movie.title}
        kind="movie"
        libraryID={movie.library_id}
        monitored={movie.monitored}
        qualityProfileID={movie.quality_profile_id}
        minAvailability={movie.min_availability}
        onsave={saveSettings}
        onclose={() => (editing = false)} />
    {/if}
    {#if movingLibrary}
      <MoveItemModal
        itemType="movie"
        itemID={movie.id}
        itemTitle={movie.title}
        kind="movie"
        currentLibraryID={movie.library_id}
        onclose={() => (movingLibrary = false)} />
    {/if}
  {/if}
</div>
