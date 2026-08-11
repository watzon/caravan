<script lang="ts">
  /** Movie detail (DESIGN.md §4: 32px display item title, machine text in mono). */
  import { onDestroy, onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { CastMember, MinAvailability, Movie } from '../api/types';
  import { AVAILABILITY_OPTIONS } from '../discover';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import ConvertFileButton from '../components/ConvertFileButton.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Poster from '../components/Poster.svelte';
  import RemoveItemModal from '../components/RemoveItemModal.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import MonitorButton from '../components/MonitorButton.svelte';
  import MoveItemModal from '../components/MoveItemModal.svelte';
  import OverflowMenu from '../components/OverflowMenu.svelte';
  import { libraries } from '../state/libraries.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import { UNKNOWN, formatBytes, formatDate, titleWithYear, truncateMiddle } from '../format';
  import MetadataLinks from '../components/MetadataLinks.svelte';
  import { movieLinks } from '../metadataLinks';
  import { navigate } from '../router.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { movieStatus } from '../status';
  import { compatBadge } from '../tvcompat';

  import ItemQualityProfileSelect from '../components/ItemQualityProfileSelect.svelte';
  import { useI18n } from '../i18n.svelte';

  const { t } = useI18n();

  interface Props {
    id: number;
  }

  let { id }: Props = $props();

  let movie = $state<Movie | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let savingMonitored = $state(false);
  let searching = $state(false);
  let confirmingRemove = $state(false);
  let movingLibrary = $state(false);
  // Loaded once per session; the menu offers a move only when there is
  // somewhere else of this kind to move to.
  $effect(() => {
    void libraries.load();
  });
  let canMove = $derived(libraries.ofKind('movie').length > 1);
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

  async function setMonitored(next: boolean) {
    const current = movie;
    if (!current) return;
    savingMonitored = true;
    // Optimistic: the toggle is the whole point of the control, so it must
    // respond immediately; a failure rolls it back and says so.
    movie = { ...current, monitored: next };
    try {
      await api.setMovieMonitored(current.id, next);
    } catch (err) {
      movie = current;
      pushToast(errorText(err), 'danger');
    } finally {
      savingMonitored = false;
    }
  }

  async function setQualityProfile(profileID: number) {
    const current = movie;
    if (!current) return;
    movie = await api.setMovieQualityProfile(current.id, profileID);
  }

  /** Same optimistic shape as the monitored toggle, for the same reason. */
  async function setMinAvailability(next: MinAvailability) {
    const current = movie;
    if (!current || next === current.min_availability) return;
    movie = { ...current, min_availability: next };
    try {
      await api.setMovieMinAvailability(current.id, next);
    } catch (err) {
      movie = current;
      pushToast(errorText(err), 'danger');
    }
  }

  /**
   * Automatic search (SPEC §9): the server queues the job and answers how many
   * it added, so a movie that already meets its cutoff says so instead of
   * claiming a search started that would never grab anything.
   */
  async function searchNow() {
    const current = movie;
    if (!current) return;
    searching = true;
    try {
      const { queued } = await api.searchMovieNow(current.id);
      if (queued > 0) {
        pushToast(t('route.movieDetail.searchStarted'), 'success');
      } else {
        pushToast(t('route.movieDetail.searchNone'), 'info');
      }
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      searching = false;
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
      confirmingRemove = false;
      pushToast(
        deleteFiles
          ? t('route.movieDetail.removedFiles', { title: current.title })
          : t('route.movieDetail.removedLibrary', { title: current.title }),
        'neutral',
      );
      navigate('/movies');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      removing = false;
    }
  }
</script>

<div class="flex flex-col gap-6">
  <a
    href="/movies"
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    {t('route.movieDetail.back')}
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
        <div class="flex flex-wrap items-start gap-4">
          <div class="min-w-0 flex-1">
            <h2 class="font-display text-2xl font-semibold tracking-tight text-ink">
              {movie.title}
            </h2>
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
          <div class="flex w-full flex-wrap items-center gap-3 sm:w-auto">
            <Button variant="primary" disabled={searching} onclick={searchNow}>
              <Icon name="search" size={14} />
              {searching ? t('route.movieDetail.searching') : t('route.movieDetail.searchNow')}
            </Button>
            <Button variant="secondary" href="/movies/{movie.id}/search">
              {t('route.movieDetail.interactiveSearch')}
            </Button>
            <MonitorButton
              monitored={movie.monitored}
              subject={movie.title}
              disabled={savingMonitored}
              onchange={setMonitored} />
            <!-- Removal lives behind the ⋯ rather than one mis-click from the
                 search buttons. There is no per-movie metadata refresh route,
                 so removal is the only item there is. -->
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

        <dl class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-5">
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
          <div>
            <dt class="micro-label">{t('route.movieDetail.minAvailability')}</dt>
            <dd class="mt-1">
              <select
                aria-label={t('route.movieDetail.minAvailability')}
                value={movie.min_availability}
                onchange={(event) =>
                  setMinAvailability(event.currentTarget.value as MinAvailability)}
                class="h-8 w-full rounded-sm border border-border-strong bg-raised px-2 text-sm text-ink
                       focus:border-accent focus:outline-none">
                {#each AVAILABILITY_OPTIONS as option (option.value)}
                  <option value={option.value}>{option.label}</option>
                {/each}
              </select>
            </dd>
          </div>
          <ItemQualityProfileSelect
            profileID={movie.quality_profile_id}
            kind="movie"
            onassign={setQualityProfile} />
        </dl>
      </div>
    </div>

    <section class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center gap-3">
        <h3 class="text-lg font-semibold text-ink">{t('route.movieDetail.file')}</h3>
        {#if file}
          <div class="ml-auto"><ConvertFileButton {file} /></div>
        {/if}
      </div>

      {#if !file}
        <EmptyState
          icon="folder"
          title={t('route.movieDetail.noFileTitle')}
          message={t('route.movieDetail.noFileMessage')}>
          {#snippet action()}
            <Button variant="secondary" href="/scan-review">{t('route.movieDetail.openScanReview')}</Button>
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
