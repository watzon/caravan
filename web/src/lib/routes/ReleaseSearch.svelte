<script lang="ts">
  /**
   * Interactive release picker (SPEC §5.1, §9 step 4; DESIGN.md §5).
   *
   * A first-class full-width screen, not a modal: this is the graceful
   * degradation path when automatic matching is uncertain, so it has to be
   * linkable, reloadable and readable at a glance. Every result is grabbable,
   * including the flagged ones — the UI de-emphasizes a bad release, it does
   * not decide for the user (SPEC §13).
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Movie, Release, Series } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import {
    UNKNOWN,
    episodeCode,
    formatAge,
    formatBytes,
    seasonLabel,
    truncateMiddle,
  } from '../format';
  import { sceneNumber } from '../adult';
  import { isFlagged, releaseFlags, releaseScore, sortReleases } from '../release';
  import { compatBadge } from '../tvcompat';
  import { navigate } from '../router.svelte';
  import { pushToast } from '../state/toast.svelte';

  interface Props {
    /**
     * What is being searched for. A site is a series row with different nouns
     * (its seasons are release years, its episodes are scenes), so it takes the
     * same endpoints and the same season/episode narrowing — only the labels
     * and the way back differ.
     */
    kind: 'movie' | 'series' | 'site';
    id: number;
    /** Series/site only: the season, or release year. -1 means the whole item. */
    season?: number;
    /** Series/site only: the episode or scene number, -1 for a whole season. */
    episode?: number;
  }

  let { kind, id, season = -1, episode = -1 }: Props = $props();

  /** Sites travel the series routes; only the screen's nouns are different. */
  let asSeries = $derived(kind === 'series' || kind === 'site');

  let movie = $state<Movie | null>(null);
  let series = $state<Series | null>(null);
  let releases = $state<Release[] | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  /** Keyed by GUID, not row id: an uncached result has id 0. */
  let grabbingGUID = $state<string | null>(null);

  /** Where the "back" link points. */
  let itemHref = $derived.by(() => {
    if (kind === 'movie') return `/movies/${id}`;
    if (kind === 'site') return `/adult/sites/${id}`;
    return `/series/${id}`;
  });

  /**
   * The episodes this grab is expected to satisfy (core.AddOpts.EpisodeIDs).
   * A single episode search resolves to one id; a season search hands over the
   * whole season so a pack imports in one go.
   */
  let episodeIDs = $derived.by((): number[] => {
    if (!asSeries || season < 0) return [];
    const found = series?.seasons?.find((s) => s.season_number === season);
    const eps = found?.episodes ?? [];
    if (episode < 0) return eps.map((e) => e.id);
    return eps.filter((e) => e.episode_number === episode).map((e) => e.id);
  });

  let heading = $derived.by((): string => {
    if (kind === 'movie') return movie?.title ?? 'Movie';
    const title = series?.title ?? (kind === 'site' ? 'Site' : 'Series');
    if (season < 0) return title;
    // A scene has no SxxEyy: it is numbered within its release year, which is
    // exactly how the site's own page names it.
    if (kind === 'site') {
      return episode >= 0 ? `${title} · ${season} · ${sceneNumber(episode)}` : `${title} · ${season}`;
    }
    if (episode >= 0) return `${title} · ${episodeCode(season, episode)}`;
    return `${title} · ${seasonLabel(season)}`;
  });

  async function load() {
    loading = true;
    releases = null;
    try {
      if (!asSeries) {
        // The item load is what gives the screen a title; the search is the
        // slow half, so they run together rather than in sequence.
        const [item, found] = await Promise.all([api.getMovie(id), api.movieReleases(id)]);
        movie = item;
        releases = found.releases;
      } else {
        const [item, found] = await Promise.all([
          api.getSeries(id),
          api.seriesReleases(id, {
            season: season >= 0 ? season : undefined,
            episode: episode >= 0 ? episode : undefined,
          }),
        ]);
        series = item;
        releases = found.releases;
      }
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  async function grab(release: Release) {
    grabbingGUID = release.guid;
    try {
      if (!asSeries) {
        await api.grabForMovie(id, { release_id: release.id });
      } else {
        await api.grabForSeries(id, {
          release_id: release.id,
          ...(season >= 0 ? { season } : {}),
          ...(episodeIDs.length > 0 ? { episode_ids: episodeIDs } : {}),
        });
      }
      pushToast(`Grabbed ${release.title}`, 'success');
      navigate('/queue');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      grabbingGUID = null;
    }
  }

  let rows = $derived(sortReleases(releases ?? []));
</script>

<div class="flex flex-col gap-6">
  <a
    href={itemHref}
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    Back to item
  </a>

  <div class="flex flex-wrap items-center gap-3">
    <div class="min-w-0">
      <h2 class="font-display text-xl font-semibold tracking-tight text-ink">{heading}</h2>
      <p class="text-base text-ink-secondary">
        Every enabled indexer, searched now. Nothing is grabbed until you say so.
      </p>
    </div>
    <div class="ml-auto flex items-center gap-2">
      {#if releases}
        <span class="text-sm text-ink-secondary">
          {rows.length} release{rows.length === 1 ? '' : 's'}
        </span>
      {/if}
      <Button variant="secondary" onclick={load} disabled={loading}>
        <Icon name="refresh" size={14} />
        {loading ? 'Searching…' : 'Search again'}
      </Button>
    </div>
  </div>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading}
    <div class="overflow-hidden rounded-md border border-border">
      {#each Array.from({ length: 6 }) as _, i (i)}
        <div class="flex items-center gap-3 border-b border-border px-3 py-3 last:border-b-0">
          <Skeleton class="h-4 w-24" />
          <Skeleton class="h-4 flex-1" />
          <Skeleton class="h-4 w-12" />
          <Skeleton class="h-4 w-16" />
          <Skeleton class="h-4 w-14" />
          <Skeleton class="h-7 w-16 rounded-md" />
        </div>
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="search"
      title="No releases found"
      message="No enabled indexer returned anything for this item. Check that at least one indexer is configured and passing its test, then search again.">
      {#snippet action()}
        <Button variant="secondary" href="/settings">Open indexer settings</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <div class="overflow-x-auto rounded-md border border-border">
      <table class="w-full min-w-[1000px] border-collapse text-sm">
        <thead>
          <tr class="bg-surface text-left">
            <th class="micro-label px-3 py-2 font-semibold">Source</th>
            <th class="micro-label px-3 py-2 font-semibold">Release</th>
            <th class="micro-label px-3 py-2 font-semibold">Age</th>
            <th class="micro-label px-3 py-2 text-right font-semibold">Size</th>
            <th class="micro-label px-3 py-2 text-right font-semibold">Peers</th>
            <th class="micro-label px-3 py-2 font-semibold">Quality</th>
            <th class="micro-label px-3 py-2 text-right font-semibold">Score</th>
            <th class="micro-label px-3 py-2 text-right font-semibold">Grab</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as release, index (release.guid || `${release.indexer_id}:${release.title}`)}
            {@const flags = releaseFlags(release)}
            {@const tv = compatBadge(release.compatibility)}
            {@const flagged = isFlagged(release)}
            {@const best = index === 0}
            <tr
              class="relative border-t border-border align-top transition-colors duration-150
                     {best ? 'bg-accent-tint' : 'hover:bg-raised'} {flagged ? 'opacity-60' : ''}">
              <td class="px-3 py-3">
                <span class="flex items-center gap-2">
                  {#if best}
                    <span class="h-4 w-0.5 shrink-0 rounded-full bg-accent" aria-hidden="true"></span>
                    <span class="text-xs font-semibold uppercase tracking-wide text-accent-text">Best</span>
                  {/if}
                  <span class="truncate text-ink-secondary" title={release.indexer}>
                    {release.indexer || UNKNOWN}
                  </span>
                </span>
              </td>

              <td class="px-3 py-3 font-mono text-ink" title={release.title}>
                {truncateMiddle(release.title, 58)}
              </td>

              <td class="px-3 py-3 text-ink-secondary">{formatAge(release.published_at)}</td>

              <td class="px-3 py-3 text-right font-mono text-ink-secondary">
                {release.size > 0 ? formatBytes(release.size) : UNKNOWN}
              </td>

              <td class="px-3 py-3 text-right font-mono">
                {#if release.protocol === 'torrent'}
                  <span class={release.seeders > 0 ? 'text-success' : 'text-danger'}>
                    {release.seeders}
                  </span>
                  <span class="text-ink-muted">/{release.leechers}</span>
                {:else}
                  <span class="text-ink-muted">{UNKNOWN}</span>
                {/if}
              </td>

              <td class="px-3 py-3">
                <div class="flex flex-wrap items-center gap-1.5">
                  {#if release.parsed.quality && release.parsed.quality !== 'unknown'}
                    <Badge mono>{release.parsed.quality}</Badge>
                  {/if}
                  {#if release.parsed.source && release.parsed.source !== 'unknown'}
                    <Badge mono>{release.parsed.source}</Badge>
                  {/if}
                  {#if release.parsed.codec}
                    <Badge mono>{release.parsed.codec}</Badge>
                  {/if}
                  {#if release.parsed.proper}
                    <Badge mono tone="success">PROPER</Badge>
                  {/if}
                  {#if release.parsed.repack}
                    <Badge mono tone="success">REPACK</Badge>
                  {/if}
                  {#each flags as flag (flag.key)}
                    <Badge mono tone={flag.tone} title={flag.title}>{flag.label}</Badge>
                  {/each}
                  {#if tv}
                    <Badge mono tone={tv.tone} title={tv.title}>{tv.label}</Badge>
                  {/if}
                </div>
              </td>

              <td class="px-3 py-3 text-right font-mono {best ? 'text-accent-text' : 'text-ink-secondary'}">
                {releaseScore(release)}
              </td>

              <td class="px-3 py-3">
                <div class="flex justify-end">
                  <Button
                    variant={best ? 'primary' : 'secondary'}
                    size="sm"
                    disabled={grabbingGUID !== null}
                    title={flagged ? flags.map((f) => f.title).join(' ') : undefined}
                    onclick={() => grab(release)}>
                    <Icon name="download" size={14} />
                    {grabbingGUID === release.guid ? 'Grabbing…' : 'Grab'}
                  </Button>
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
