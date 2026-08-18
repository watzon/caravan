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
  import type {
    Indexer,
    IndexerError,
    Movie,
    Release,
    ReleasesResponse,
    Series,
  } from '../api/types';
  import Icon from '../components/Icon.svelte';
  import IndexerErrors from '../components/IndexerErrors.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Modal from '../components/Modal.svelte';
  import ReleaseSearchControls from '../components/ReleaseSearchControls.svelte';
  import ReleaseTable from '../components/ReleaseTable.svelte';
  import { episodeCode, seasonLabel } from '../format';
  import { sceneNumber } from '../adult';
  import { libraryChanged } from '../state/activity';
  import { movieSeed, seriesSeed, sceneSeed } from '../searchseed';
  import { pushToast } from '../state/toast.svelte';
  import { useI18n, type TranslationKey } from '../i18n.svelte';

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

  /**
   * The route may say series while the row is a site — Wanted used to link
   * scenes that way. The item's own kind wins so the box and the heading
   * do not flash the television spelling and then rewrite it.
   */
  let asSite = $derived(kind === 'site' || series?.kind === 'adult');
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

  /** Rows the expression's own filters hid. 0 — nothing was hidden. */
  let filteredCount = $state(0);
  let helpOpen = $state(false);

  /**
   * The syntax cheatsheet's worked examples. The expressions are literal — they
   * are the language, not prose — so only the line explaining each is
   * translated.
   */
  const syntaxExamples: { code: string; note: TranslationKey }[] = [
    { code: 'site:"Vixen" date:2026-01-19', note: 'route.releaseSearch.syntaxExampleSite' },
    { code: 'title:"Dune" year:2021 -hdtv', note: 'route.releaseSearch.syntaxExampleTitle' },
    { code: 'quality:1080p codec:x265', note: 'route.releaseSearch.syntaxExampleQuality' },
    { code: 'foo OR bar', note: 'route.releaseSearch.syntaxExampleOr' },
  ];

  /** Where the "back" link points. */
  let itemHref = $derived.by(() => {
    if (kind === 'movie') return `/movies/${id}`;
    if (asSite) return `/adult/sites/${id}`;
    return `/series/${id}`;
  });

  let heading = $derived.by((): string => {
    if (kind === 'movie') return movie?.title ?? t('route.releaseSearch.movie');
    const title = series?.title ?? (asSite ? t('route.releaseSearch.site') : t('route.releaseSearch.series'));
    if (season < 0) return title;
    if (asSite) {
      return episode >= 0 ? `${title} · ${season} · ${sceneNumber(episode)}` : `${title} · ${season}`;
    }
    if (episode >= 0) return `${title} · ${episodeCode(season, episode)}`;
    return `${title} · ${seasonLabel(season)}`;
  });

  let contextLabel = $derived(
    t('route.releaseSearch.context', {
      kind: kind === 'movie'
        ? t('route.releaseSearch.movie')
        : asSite
          ? t('route.releaseSearch.site')
          : t('route.releaseSearch.series'),
      heading,
    }),
  );

  /**
   * The library whose quality profile scores a free-text re-search, so an
   * edited query ranks rows the same way the derived one did. 0 — an item the
   * screen has not loaded yet — falls back to the store-wide default.
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

  /**
   * The client-side twin of the server's seed, written into the box the moment
   * the item arrives. The fan-out is the slow half of this screen, and a box
   * that sits empty for its whole duration reads as broken. The server's
   * `search_expression` replaces this when the search lands; the twins spell
   * the identical string (their tests pin it), so the hand-off is invisible.
   */
  function seedFromItem() {
    let seed = '';
    if (kind === 'movie' && movie) {
      seed = movieSeed(movie.title, movie.year);
    } else if (asSeries && series) {
      if (asSite) {
        const found = series.seasons?.find((s) => s.season_number === season);
        const scene = found?.episodes?.find((e) => e.episode_number === episode);
        seed = sceneSeed(series.title, scene?.air_date ?? '', scene?.title ?? '');
      } else {
        seed = seriesSeed(series.title, season, episode);
      }
    }
    if (seed === '') return;
    derivedQuery = seed;
    query = seed;
  }

  async function load() {
    loading = true;
    releases = null;
    resetAnswerNotes();
    try {
      // The item load is what gives the screen a title AND the box its seed;
      // the search is the slow half, so it starts first and lands last.
      const searchPromise = perItemSearch();
      if (!asSeries) {
        movie = await api.getMovie(id);
      } else {
        series = await api.getSeries(id);
      }
      seedFromItem();
      applyResponse(await searchPromise);
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
   *
   * The seed is the query-language spelling when the server has one: it is the
   * same search written in the language the box speaks, so widening it is an
   * edit rather than a retype. `query` is only the first raw string sent
   * upstream, which for a scene is half of what was asked.
   */
  function applyResponse(found: ReleasesResponse, seed = true) {
    releases = found.releases;
    failures = found.errors ?? [];
    filteredCount = found.filtered ?? 0;
    askedCount = countAsked();
    if (!seed) return;
    derivedQuery = found.search_expression ?? found.query;
    query = derivedQuery;
  }

  /**
   * How many indexers this search reached. Snapshotted with the answer rather
   * than derived at render time: the rail is editable, so "2 of 5 answered"
   * must keep meaning the search on screen after the user ticks a sixth box.
   * Zero — the indexer list has not arrived — reads as "we do not know".
   */
  let askedCount = $state(0);

  /**
   * Drop everything that describes the answer being replaced. A stale "3 hidden
   * by your filters" over a fresh result set is a lie, and a failed search must
   * leave no note at all.
   */
  function resetAnswerNotes() {
    filteredCount = 0;
  }

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
    resetAnswerNotes();
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
          ...(episode >= 0 ? { episode } : {}),
        });
      }
      pushToast(t('route.releaseSearch.grabbed', { title: release.title }), 'success');
      if (releases) {
        releases = releases.map((row) =>
          row.guid === release.guid ? { ...row, queue_state: 'downloading' } : row,
        );
      }
      libraryChanged({ expectDownload: true });
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
    onhelp={() => (helpOpen = true)}
    {contextLabel} />

  <IndexerErrors errors={failures} total={askedCount || undefined} />

  {#if filteredCount > 0}
    <!-- Hidden, not lost: every one of these rows is cached, so loosening the
         expression brings it straight back. -->
    <p data-filtered-note class="text-sm text-ink-muted">
      {tp('route.releaseSearch.filteredCount', filteredCount)}
    </p>
  {/if}

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else}
    <ReleaseTable {releases} {loading} busyGUID={grabbingGUID} ongrab={grab} />
  {/if}
</div>

{#if helpOpen}
  <Modal title={t('route.releaseSearch.syntaxHelp')} width="max-w-2xl" onclose={() => (helpOpen = false)}>
    <div
      data-syntax-help
      class="flex flex-col gap-2 p-4 text-sm text-ink-secondary">
      <p>{t('route.releaseSearch.syntaxIntro')}</p>
      <ul class="flex flex-col gap-1">
        {#each syntaxExamples as example (example.code)}
          <li class="flex flex-wrap items-baseline gap-2">
            <code class="rounded-sm bg-raised px-1.5 py-0.5 font-mono text-ink">{example.code}</code>
            <span>{t(example.note)}</span>
          </li>
        {/each}
      </ul>
      <p>{t('route.releaseSearch.syntaxOperators')}</p>
      <p>{t('route.releaseSearch.syntaxFields')}</p>
    </div>
  </Modal>
{/if}
