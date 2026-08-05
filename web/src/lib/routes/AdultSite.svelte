<script lang="ts">
  /**
   * One site's page (PLAN phase 9 task 7c): release years as seasons, scene
   * rows inside them.
   *
   * It is SeriesDetail's shape with this shelf's nouns — a year where a season
   * heading goes, "#003 Title · Performers" where an episode's SxxEyy and title
   * go, a release date where an air date goes — and it deliberately reuses the
   * same status vocabulary, badges and collapse behaviour rather than inventing
   * a parallel one.
   *
   * The actions are SeriesDetail's, at the same three levels: search the whole
   * site, open the picker for one release year, open it for one scene. They go
   * to the series routes, because a site IS a series row and a scene IS an
   * episode — the server gates those routes on the same adult grant that let
   * this page load, so none of them is a button that 404s.
   *
   * Every one of them is an admin write, and this page is one a granted MEMBER
   * reads. So the actions are behind `session.isAdmin` while everything that
   * reports state — monitored flags, counts, statuses — is not: a member should
   * see what will happen next, and be offered nothing that would 403.
   *
   * Monitoring is controllable at all three levels and the site can be removed,
   * through the same routes and the same confirm SeriesDetail uses. A higher
   * level's toggle cascades as a bulk update rather than a lock, so a site or
   * year toggle reloads instead of guessing what happened to its children.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Scene, SiteDetail, SiteYear } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import ConvertFileButton from '../components/ConvertFileButton.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import MetadataLinks from '../components/MetadataLinks.svelte';
  import MonitorButton from '../components/MonitorButton.svelte';
  import OverflowMenu from '../components/OverflowMenu.svelte';
  import Poster from '../components/Poster.svelte';
  import RemoveItemModal from '../components/RemoveItemModal.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import Toggle from '../components/Toggle.svelte';
  import {
    CATALOGUING_POLL_MS,
    performerSummary,
    sceneLine,
    sceneNumber,
    scenePerformers,
  } from '../adult';
  import { UNKNOWN, formatDate } from '../format';
  import { siteLinks } from '../metadataLinks';
  import { navigate } from '../router.svelte';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { episodeStatus } from '../status';
  import { compatBadge } from '../tvcompat';

  interface Props {
    id: number;
  }

  let { id }: Props = $props();

  let site = $state<SiteDetail | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let collapsed = $state<Record<number, boolean>>({});
  let searching = $state(false);
  /** A monitor write is in flight; every toggle on the page waits for it. */
  let busy = $state(false);
  let confirmingRemove = $state(false);
  let removing = $state(false);

  /**
   * Reload the page's data.
   *
   * `quiet` is what the cataloguing poll uses: it skips the loading flag, so a
   * background refresh does not replace a page the reader is looking at with a
   * skeleton every few seconds. An error is swallowed on a quiet pass too — a
   * single failed poll against a page that is already rendered is not worth
   * throwing the reader out for, and the next tick retries.
   */
  async function load(quiet = false) {
    if (!quiet) loading = true;
    try {
      site = await api.getSite(id);
      error = null;
    } catch (err) {
      if (!quiet) error = errorText(err);
    } finally {
      if (!quiet) loading = false;
    }
  }

  /**
   * While the catalogue walk runs, the page watches it.
   *
   * The walk publishes a whole release year at a time (library.walkSiteScenes),
   * so re-reading on a timer is enough to make the years appear as they land —
   * there is no partial state to stitch together, each poll is simply a later
   * version of the same page. The interval lives for the life of the component
   * and checks the flag itself rather than being started and stopped, because
   * `cataloguing` can go true again without a remount: a re-add or a refresh
   * queues another walk while this page is open.
   */
  onMount(() => {
    void load();
    let wasCataloguing = false;
    const timer = setInterval(() => {
      const now = site?.cataloguing ?? false;
      // One last read after it goes false: the poll that observes the end of
      // the walk is reading state from just before the final year landed.
      if (now || wasCataloguing) void load(true);
      wasCataloguing = now;
    }, CATALOGUING_POLL_MS);
    return () => clearInterval(timer);
  });

  let years = $derived<SiteYear[]>(site?.years ?? []);

  // The detail response carries every year's scenes with their files, so the
  // confirm can name a real count rather than a vague "its files".
  let fileCount = $derived(
    years.reduce((total, year) => total + year.scenes.filter((scene) => scene.file).length, 0),
  );

  function ownedCount(year: SiteYear): number {
    return year.scenes.filter((scene) => scene.file).length;
  }

  /**
   * A scene's status. `episodeStatus` is the shared rule and this renames the
   * three fields it reads rather than restating it: a scene with no file whose
   * release date is still in the future is unaired for exactly the reason an
   * episode is.
   */
  /**
   * Automatic search for the whole site, which is SeriesDetail's searchNow
   * against the same route: the server queues one job per wanted scene and
   * answers the count, so a site that is already complete says so rather than
   * reporting work nobody started.
   */
  async function searchNow() {
    const current = site;
    if (!current) return;
    searching = true;
    try {
      const { queued } = await api.searchSeriesNow(current.id);
      if (queued > 0) {
        pushToast(`${queued} search${queued === 1 ? '' : 'es'} started`, 'success');
      } else {
        pushToast('Nothing to search — every monitored scene is covered', 'info');
      }
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      searching = false;
    }
  }

  /**
   * Run a write, then reload. Reloading rather than patching in place is
   * SeriesDetail's rule and it is here for the same reason: a site or year
   * toggle cascades to its children on the server, so the only honest way to
   * know what the page now says is to ask.
   */
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

  /** See SeriesDetail.remove: a successful removal leaves the page it emptied. */
  async function remove(deleteFiles: boolean) {
    const current = site;
    if (!current) return;
    removing = true;
    try {
      await api.deleteSeries(current.id, deleteFiles);
      confirmingRemove = false;
      pushToast(
        deleteFiles
          ? `Removed ${current.title} and its files`
          : `Removed ${current.title} from the library`,
        'neutral',
      );
      navigate('/adult');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      removing = false;
    }
  }

  function sceneStatus(scene: Scene) {
    return episodeStatus({
      file: scene.file,
      monitored: scene.monitored,
      air_date: scene.release_date,
    });
  }
</script>

<div class="flex flex-col gap-6">
  <a
    href="/adult"
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    Adult
  </a>

  {#if error}
    <!-- Wrapped rather than passed by reference: onretry is wired straight to a
         button's onclick, so `load` would receive the MouseEvent as its `quiet`
         argument and a retry would silently swallow its own failure. -->
    <LoadError message={error} onretry={() => load()} />
  {:else if loading && site === null}
    <div class="flex gap-6">
      <Skeleton class="aspect-[2/3] w-52 rounded-md" />
      <div class="flex flex-1 flex-col gap-3">
        <Skeleton class="h-8 w-1/2" />
        <Skeleton class="h-4 w-1/4" />
        <Skeleton class="h-20 w-full" />
      </div>
    </div>
  {:else if site}
    {@const current = site}
    <div class="flex flex-col gap-6 md:flex-row">
      <div class="w-56 shrink-0 md:w-72">
        <Poster
          path={current.poster_path}
          fallback={current.poster_url}
          alt={current.title}
          fallbackIcon="flame"
          fit="contain"
          aspect="video" />
      </div>

      <div class="flex min-w-0 flex-1 flex-col gap-4">
        <div class="flex flex-wrap items-start gap-4">
          <div class="min-w-0 flex-1">
            <h2 class="font-display text-2xl font-semibold tracking-tight text-ink">
              {current.title}
            </h2>
            <p class="mt-1 flex flex-wrap items-center gap-3 text-base text-ink-secondary">
              <span>{current.scene_file_count} / {current.scene_count} scenes</span>
              <span class="text-ink-muted">·</span>
              <span>{years.length} year{years.length === 1 ? '' : 's'}</span>
              {#if !current.monitored}
                <span class="text-ink-muted">·</span>
                <Badge tone="neutral">Unmonitored</Badge>
              {/if}
            </p>
            <div class="mt-2">
              <MetadataLinks links={siteLinks(current)} />
            </div>
          </div>
          {#if session.isAdmin}
            <div class="flex items-center gap-3">
              <Button variant="primary" disabled={searching} onclick={searchNow}>
                <Icon name="search" size={14} />
                {searching ? 'Searching…' : 'Search monitored'}
              </Button>
              <Button variant="secondary" href="/adult/sites/{current.id}/search">
                Interactive search
              </Button>
              <MonitorButton
                monitored={current.monitored}
                subject={current.title}
                disabled={busy}
                onchange={(next) =>
                  run(() => api.setSeriesMonitored(current.id, next), 'Could not update the site')} />
              <!-- Removal lives behind the ⋯ rather than one mis-click from the
                   search buttons. There is no per-site metadata refresh route,
                   so removal is the only item there is. -->
              <OverflowMenu
                subject={current.title}
                items={[
                  {
                    label: 'Remove from library…',
                    danger: true,
                    disabled: removing,
                    onselect: () => (confirmingRemove = true),
                  },
                ]} />
            </div>
          {/if}
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
          <div class="min-w-0">
            <dt class="micro-label">Provider id</dt>
            <!-- Plain text, as the movie and series pages keep their TMDB id:
                 the header's provider chip is where the link lives. -->
            <dd class="mt-1 truncate font-mono text-sm text-ink" title={current.stash_id}>
              {current.stash_id || UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">Added</dt>
            <dd class="mt-1 text-sm text-ink">{formatDate(current.added_at)}</dd>
          </div>
        </dl>
      </div>
    </div>

    <!-- The walk publishes a release year at a time, so there are two honest
         things to say and they need different room. With nothing filed yet the
         page has space for the full explanation; once years are on screen the
         reader can see it working, and a slim line that keeps count is all that
         is left to add. -->
    {#if current.cataloguing && years.length > 0}
      <Banner
        tone="info"
        icon="refresh"
        message="Cataloguing scenes — {current.scene_count} so far. More release years appear as they are indexed." />
    {/if}

    {#if years.length === 0}
      {#if current.cataloguing}
        <EmptyState
          icon="refresh"
          title="Cataloguing scenes"
          message="Caravan is reading this site's catalogue from the metadata provider. Its release years appear here as they are indexed, newest first — there is nothing to do but watch." />
      {:else}
        <EmptyState
          icon="flame"
          title="No scenes yet"
          message="Caravan knows this site but has no scenes filed under it. A metadata refresh fills the catalogue in." />
      {/if}
    {:else}
      <div class="flex flex-col gap-4">
        {#each years as year (year.year)}
          {@const isCollapsed = collapsed[year.year] ?? false}
          <section class="overflow-hidden rounded-md border border-border">
            <header class="flex items-center gap-3 bg-surface px-3 py-2">
              <button
                type="button"
                class="flex min-w-0 flex-1 items-center gap-2 text-left"
                aria-expanded={!isCollapsed}
                onclick={() => (collapsed = { ...collapsed, [year.year]: !isCollapsed })}>
                <span class="text-ink-secondary">
                  <Icon name={isCollapsed ? 'chevronRight' : 'chevronDown'} />
                </span>
                <span class="text-md font-semibold text-ink">{year.year}</span>
                <Badge
                  tone={year.scenes.length > 0 && ownedCount(year) >= year.scenes.length
                    ? 'success'
                    : ownedCount(year) > 0
                      ? 'warning'
                      : 'neutral'}>
                  {ownedCount(year)} / {year.scenes.length}
                </Badge>
                {#if !year.monitored}
                  <Badge tone="neutral">Unmonitored</Badge>
                {/if}
              </button>

              {#if session.isAdmin}
                <Button
                  variant="secondary"
                  size="sm"
                  href="/adult/sites/{current.id}/search/{year.year}"
                  title={`Search for a ${year.year} pack`}>
                  <Icon name="search" size={14} />
                  Search
                </Button>

                <Toggle
                  checked={year.monitored}
                  label={`Monitor ${year.year}`}
                  labelHidden
                  disabled={busy}
                  onchange={(next) =>
                    run(
                      () => api.setSeasonMonitored(current.id, year.year, next),
                      'Could not update the year',
                    )} />
              {/if}
            </header>

            {#if !isCollapsed}
              {#if year.scenes.length === 0}
                <p class="px-3 py-6 text-center text-sm text-ink-secondary">
                  No scenes known for this year.
                </p>
              {:else}
                <div class="overflow-x-auto">
                  <table class="w-full min-w-[720px] border-collapse text-sm">
                    <thead>
                      <tr class="bg-surface text-left">
                        <th class="micro-label px-3 py-2 font-semibold">Scene</th>
                        <!-- On a scene, the performers are what a title is on an
                             episode: the thing somebody is actually looking for.
                             They get a column rather than a suffix on the title.
                             Quality and size left with them — a scene either has
                             its file or does not, which the status says, and the
                             picker is where a release's quality is chosen. -->
                        <th class="micro-label px-3 py-2 font-semibold">Performers</th>
                        <th class="micro-label px-3 py-2 font-semibold">Released</th>
                        <th class="micro-label px-3 py-2 font-semibold">Status</th>
                        {#if session.isAdmin}
                          <th class="micro-label px-3 py-2 text-right font-semibold">Monitored</th>
                          <th class="micro-label px-3 py-2 text-right font-semibold">Search</th>
                        {/if}
                      </tr>
                    </thead>
                    <tbody>
                      {#each year.scenes as scene (scene.id)}
                        {@const cast = scenePerformers(scene)}
                        <tr
                          class="h-10 border-t border-border transition-colors duration-150 hover:bg-raised">
                          <td class="max-w-[420px] px-3 py-2 text-ink">
                            <span class="block truncate" title={sceneLine(scene)}>
                              <span class="font-mono text-ink-secondary">
                                {sceneNumber(scene.number)}
                              </span>
                              {#if scene.provider_url}
                                <!-- The scene's page on the metadata provider,
                                     not on the site itself: the provider page
                                     is the one that explains what Caravan
                                     thinks this row is. -->
                                <a
                                  href={scene.provider_url}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  class="underline-offset-2 transition-colors duration-150 hover:text-accent-text hover:underline">
                                  {scene.title || UNKNOWN}
                                </a>
                              {:else}
                                {scene.title || UNKNOWN}
                              {/if}
                            </span>
                          </td>
                          <td
                            class="max-w-[220px] px-3 py-2 text-ink-secondary"
                            title={cast.join(', ')}>
                            <span class="block truncate">
                              {cast.length > 0 ? performerSummary(cast) : UNKNOWN}
                            </span>
                          </td>
                          <td class="px-3 py-2 text-ink-secondary">
                            {formatDate(scene.release_date)}
                          </td>
                          <td class="px-3 py-2">
                            <div class="flex flex-wrap items-center gap-1.5">
                              <StatusDot status={sceneStatus(scene)} />
                              <!-- A downloaded scene's quality rides with its
                                   status, which is the only place it still says
                                   something: "downloaded" and "downloaded at
                                   1080p" are different answers. -->
                              {#if scene.file}
                                {@const tv = compatBadge(scene.file.compatibility)}
                                <Badge mono>{scene.file.quality}</Badge>
                                {#if tv}
                                  <Badge mono tone={tv.tone} title={tv.title}>{tv.label}</Badge>
                                {/if}
                              {/if}
                            </div>
                          </td>
                          {#if session.isAdmin}
                            <td class="px-3 py-2">
                              <div class="flex justify-end">
                                <Toggle
                                  checked={scene.monitored}
                                  label={`Monitor ${sceneNumber(scene.number)}`}
                                  labelHidden
                                  disabled={busy}
                                  onchange={(next) =>
                                    run(
                                      () => api.setEpisodeMonitored(scene.id, next),
                                      'Could not update the scene',
                                    )} />
                              </div>
                            </td>
                            <td class="px-3 py-2">
                              <div class="flex justify-end gap-1">
                                {#if scene.file}
                                  <ConvertFileButton file={scene.file} compact />
                                {/if}
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  href="/adult/sites/{current.id}/search/{year.year}/{scene.number}"
                                  title={`Search for ${sceneNumber(scene.number)}`}>
                                  <Icon name="search" size={14} />
                                  <span class="sr-only">
                                    Search for {sceneNumber(scene.number)}
                                  </span>
                                </Button>
                              </div>
                            </td>
                          {/if}
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

    {#if confirmingRemove}
      <RemoveItemModal
        title="Remove {current.title}"
        subject={current.title}
        {fileCount}
        busy={removing}
        onconfirm={remove}
        onclose={() => (confirmingRemove = false)} />
    {/if}
  {/if}
</div>
