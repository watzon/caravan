<script lang="ts">
  /**
   * Interactive release picker (SPEC §5.1, §9 step 4; DESIGN.md §5).
   *
   * A first-class full-width screen, not a modal: this is the graceful
   * degradation path when automatic matching is uncertain, so it has to be
   * linkable, reloadable and readable at a glance. Every result is grabbable,
   * including the flagged ones — the UI de-emphasizes a bad release, it does
   * not decide for the user (SPEC §13).
   *
   * The query is editable (plan part B7). The server's derived query is a guess
   * at what an indexer calls this item, and it is wrong often enough that the
   * screen it lands on has to let the user say otherwise. What is NOT editable
   * is the target: the grab always posts to this item's own endpoint with the
   * season and episode ids the route named, whatever the query box says. That
   * split is the whole point — a wrong query is a bad search, a wrong target is
   * a file in the wrong place.
   */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Indexer, IndexerError, Movie, Release, Series } from '../api/types';
  import Icon from '../components/Icon.svelte';
  import IndexerErrors from '../components/IndexerErrors.svelte';
  import LoadError from '../components/LoadError.svelte';
  import ReleaseSearchControls from '../components/ReleaseSearchControls.svelte';
  import ReleaseTable from '../components/ReleaseTable.svelte';
  import { episodeCode, seasonLabel } from '../format';
  import { sceneNumber } from '../adult';
  import { navigate } from '../router.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { useI18n } from '../i18n.svelte';

  const { t, tp } = useI18n();

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

  /* The editable rail. `query` starts as whatever the server derived, and
     `derivedQuery` remembers that so an untouched box can go back to the
     per-item endpoint rather than to the free-text one — see runSearch. */
  let query = $state('');
  let derivedQuery = $state('');
  let categories = $state<number[]>([]);
  let indexerIDs = $state<number[]>([]);
  let indexers = $state<Indexer[]>([]);
  let failures = $state<IndexerError[]>([]);

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
    if (kind === 'movie') return movie?.title ?? t('route.releaseSearch.movie');
    const title = series?.title ?? (kind === 'site' ? t('route.releaseSearch.site') : t('route.releaseSearch.series'));
    if (season < 0) return title;
    if (kind === 'site') {
      return episode >= 0 ? `${title} · ${season} · ${sceneNumber(episode)}` : `${title} · ${season}`;
    }
    if (episode >= 0) return `${title} · ${episodeCode(season, episode)}`;
    return `${title} · ${seasonLabel(season)}`;
  });

  let contextLabel = $derived(
    t('route.releaseSearch.context', {
      kind: kind === 'movie'
        ? t('route.releaseSearch.movie')
        : kind === 'site'
          ? t('route.releaseSearch.site')
          : t('route.releaseSearch.series'),
      heading,
    }),
  );

  /**
   * The library whose quality profile scores a free-text re-search, so an
   * edited query ranks rows the same way the derived one did. 0 — a row from
   * before libraries were plural — falls back to the store-wide default.
   */
  let libraryID = $derived((kind === 'movie' ? movie?.library_id : series?.library_id) ?? 0);

  /** One search against this item's own endpoint, whatever the rail says. */
  function perItemSearch() {
    if (!asSeries) return api.movieReleases(id);
    return api.seriesReleases(id, {
      season: season >= 0 ? season : undefined,
      episode: episode >= 0 ? episode : undefined,
    });
  }

  async function load() {
    loading = true;
    releases = null;
    try {
      if (!asSeries) {
        // The item load is what gives the screen a title; the search is the
        // slow half, so they run together rather than in sequence.
        const [item, found] = await Promise.all([api.getMovie(id), perItemSearch()]);
        movie = item;
        applyResponse(found);
      } else {
        const [item, found] = await Promise.all([api.getSeries(id), perItemSearch()]);
        series = item;
        applyResponse(found);
      }
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  /**
   * Seed the rail from the server's own answer. `query` is only overwritten on
   * a per-item search, which is the only one that derives a query at all — a
   * free-text search echoes back what the user typed.
   */
  function applyResponse(found: { query: string; releases: Release[]; errors: IndexerError[] }, seed = true) {
    releases = found.releases;
    failures = found.errors ?? [];
    askedCount = countAsked();
    if (!seed) return;
    derivedQuery = found.query;
    query = found.query;
  }

  /**
   * How many indexers this search reached. Snapshotted with the answer rather
   * than derived at render time: the rail is editable, so "2 of 5 answered"
   * must keep meaning the search on screen after the user ticks a sixth box.
   * Zero — the indexer list has not arrived — reads as "we do not know".
   */
  let askedCount = $state(0);

  function countAsked(): number {
    const enabled = indexers.filter((indexer) => indexer.enabled);
    if (indexerIDs.length === 0) return enabled.length;
    return enabled.filter((indexer) => indexerIDs.includes(indexer.id)).length;
  }

  onMount(() => {
    void load();
    // The rail's indexer list and the ids behind its category union. A failure
    // here is not the screen failing: the rail simply offers no indexer filter,
    // and the search still fans out over every enabled one.
    void api
      .listIndexers()
      .then((list) => (indexers = list))
      .catch(() => (indexers = []));
  });

  /**
   * Re-run the search from the rail.
   *
   * An untouched rail goes back to the per-item endpoint rather than to
   * /search/releases, and not as an optimization: the per-item builders send
   * SEVERAL queries per search (an adult scene is looked up by two different
   * naming conventions at once), and the free-text endpoint sends exactly the
   * one string it was handed. Routing an unedited re-search through it would
   * quietly lose half the results the first load found.
   */
  async function runSearch() {
    const asked = query.trim();
    if (asked === '') return;
    const untouched =
      asked === derivedQuery.trim() && categories.length === 0 && indexerIDs.length === 0;

    loading = true;
    releases = null;
    try {
      if (untouched) {
        applyResponse(await perItemSearch());
      } else {
        applyResponse(
          await api.searchReleases({
            q: asked,
            cats: categories,
            indexer_ids: indexerIDs,
            library_id: libraryID,
          }),
          false,
        );
      }
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  async function grab(release: Release) {
    grabbingGUID = release.guid;
    try {
      // Always the per-item endpoints, with the ids the ROUTE named. The query
      // box decides what was searched for; it never decides what is grabbed.
      if (!asSeries) {
        await api.grabForMovie(id, { release_id: release.id });
      } else {
        await api.grabForSeries(id, {
          release_id: release.id,
          ...(season >= 0 ? { season } : {}),
          ...(episodeIDs.length > 0 ? { episode_ids: episodeIDs } : {}),
        });
      }
      pushToast(t('route.releaseSearch.grabbed', { title: release.title }), 'success');
      navigate('/queue');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      grabbingGUID = null;
    }
  }

</script>

<div class="flex flex-col gap-6">
  <a
    href={itemHref}
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    {t('route.releaseSearch.back')}
  </a>

  <div class="flex flex-wrap items-center gap-3">
    <div class="min-w-0">
      <h2 class="font-display text-xl font-semibold tracking-tight text-ink">{heading}</h2>
      <p class="text-base text-ink-secondary">{t('route.releaseSearch.description')}</p>
    </div>
    {#if releases}
      <span class="ml-auto text-sm text-ink-secondary">
        {tp('route.releaseSearch.resultCount', releases.length)}
      </span>
    {/if}
  </div>

  <ReleaseSearchControls
    bind:query
    bind:categories
    bind:indexerIDs
    {indexers}
    busy={loading}
    onsearch={runSearch}
    {contextLabel} />

  <IndexerErrors errors={failures} total={askedCount || undefined} />

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else}
    <ReleaseTable {releases} {loading} busyGUID={grabbingGUID} ongrab={grab} />
  {/if}
</div>
