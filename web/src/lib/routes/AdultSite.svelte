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
   * Monitor toggles are still absent. The routes for them exist and are gated
   * the same way, so it is a gap rather than an impossibility.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Scene, SiteDetail, SiteYear } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import { sceneLine, sceneNumber, sceneTitleLine } from '../adult';
  import { UNKNOWN, formatBytes, formatDate } from '../format';
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

  async function load() {
    loading = true;
    try {
      site = await api.getSite(id);
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let years = $derived<SiteYear[]>(site?.years ?? []);

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
    <LoadError message={error} onretry={load} />
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
      <div class="w-40 shrink-0 md:w-52">
        <Poster
          path={current.poster_path}
          fallback={current.poster_url}
          alt={current.title}
          fallbackIcon="flame" />
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
          </div>
          {#if session.isAdmin}
            <div class="flex items-center gap-3">
              <Button variant="primary" size="sm" disabled={searching} onclick={searchNow}>
                <Icon name="search" size={14} />
                {searching ? 'Searching…' : 'Search monitored'}
              </Button>
              <Button variant="secondary" size="sm" href="/adult/sites/{current.id}/search">
                Interactive search
              </Button>
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
            <dd class="mt-1 truncate font-mono text-sm text-ink" title={current.stash_id}>
              {#if current.stash_id && current.provider_url}
                <!-- Where it points depends on the configured endpoint, so the
                     server derives it: this page is one a member can read, and
                     the endpoint setting is not. -->
                <a
                  href={current.provider_url}
                  target="_blank"
                  rel="noopener noreferrer"
                  class="inline-flex items-center gap-1 underline-offset-2 transition-colors duration-150 hover:text-accent-text hover:underline">
                  {current.stash_id}
                  <Icon name="link" size={12} />
                </a>
              {:else}
                {current.stash_id || UNKNOWN}
              {/if}
            </dd>
          </div>
          <div>
            <dt class="micro-label">Added</dt>
            <dd class="mt-1 text-sm text-ink">{formatDate(current.added_at)}</dd>
          </div>
        </dl>
      </div>
    </div>

    {#if years.length === 0}
      <EmptyState
        icon="flame"
        title="No scenes yet"
        message="Caravan knows this site but has no scenes filed under it. A metadata refresh fills the catalogue in." />
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
                        <th class="micro-label px-3 py-2 font-semibold">Released</th>
                        <th class="micro-label px-3 py-2 font-semibold">Status</th>
                        <th class="micro-label px-3 py-2 font-semibold">Quality</th>
                        <th class="micro-label px-3 py-2 text-right font-semibold">Size</th>
                        {#if session.isAdmin}
                          <th class="micro-label px-3 py-2 text-right font-semibold">Search</th>
                        {/if}
                      </tr>
                    </thead>
                    <tbody>
                      {#each year.scenes as scene (scene.id)}
                        <tr
                          class="h-10 border-t border-border transition-colors duration-150 hover:bg-raised">
                          <td class="max-w-[420px] px-3 py-2 text-ink">
                            <span class="block truncate" title={sceneLine(scene)}>
                              <span class="font-mono text-ink-secondary">
                                {sceneNumber(scene.number)}
                              </span>
                              {#if scene.url}
                                <!-- The scene's own page on the site, as the
                                     provider stored it. Only shown when there
                                     is one: a scene with no url is common. -->
                                <a
                                  href={scene.url}
                                  target="_blank"
                                  rel="noopener noreferrer"
                                  class="underline-offset-2 transition-colors duration-150 hover:text-accent-text hover:underline">
                                  {sceneTitleLine(scene)}
                                </a>
                              {:else}
                                {sceneTitleLine(scene)}
                              {/if}
                            </span>
                          </td>
                          <td class="px-3 py-2 text-ink-secondary">
                            {formatDate(scene.release_date)}
                          </td>
                          <td class="px-3 py-2">
                            <StatusDot status={sceneStatus(scene)} />
                          </td>
                          <td class="px-3 py-2">
                            {#if scene.file}
                              {@const tv = compatBadge(scene.file.compatibility)}
                              <div class="flex flex-wrap items-center gap-1.5">
                                <Badge mono>{scene.file.quality}</Badge>
                                {#if tv}
                                  <Badge mono tone={tv.tone} title={tv.title}>{tv.label}</Badge>
                                {/if}
                              </div>
                            {:else}
                              <span class="text-ink-muted">{UNKNOWN}</span>
                            {/if}
                          </td>
                          <td class="px-3 py-2 text-right font-mono text-ink-secondary">
                            {scene.file ? formatBytes(scene.file.size) : UNKNOWN}
                          </td>
                          {#if session.isAdmin}
                            <td class="px-3 py-2">
                              <div class="flex justify-end">
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
  {/if}
</div>
