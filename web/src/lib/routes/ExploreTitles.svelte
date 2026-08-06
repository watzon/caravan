<script lang="ts">
  /**
   * Explore → Movies / Series: the whole TMDB catalogue behind a filter rail
   * (PLAN phase 12 tasks 4-6).
   *
   * One component for both scopes, because they are one screen: the same grid,
   * the same paging, the same chips, and exactly one pill that differs. That
   * difference is not cosmetic — TMDB's /discover/tv has no cast/crew/people
   * parameter and the API answers 400 rather than ignoring one, so the Cast &
   * crew pill exists on movies and the Network pill on series, and neither is
   * ever rendered where it cannot be honoured. A single component makes that
   * one visible line instead of a divergence between two files.
   *
   * THE FILTER IS THE URL. Nothing here holds filter state: the rail writes a
   * new address and this reads it back, so a filtered view is a link, a reload
   * restores it, and Back steps through the filters somebody tried.
   */
  import type { DiscoverItem, DiscoverScopePage, MediaType } from '../api/types';
  import { api, errorText } from '../api/client';
  import { metadataFault, type CredentialFault } from '../credentials';
  import AppliedChips from '../components/AppliedChips.svelte';
  import Button from '../components/Button.svelte';
  import DiscoverCard from '../components/DiscoverCard.svelte';
  import DiscoverError from '../components/DiscoverError.svelte';
  import Dropdown from '../components/Dropdown.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ExploreScopes from '../components/ExploreScopes.svelte';
  import FilterOptions from '../components/FilterOptions.svelte';
  import FilterPill from '../components/FilterPill.svelte';
  import FilterRange from '../components/FilterRange.svelte';
  import FilterTypeahead from '../components/FilterTypeahead.svelte';
  import PosterGrid from '../components/PosterGrid.svelte';
  import PosterGridSkeleton from '../components/PosterGridSkeleton.svelte';
  import Toggle from '../components/Toggle.svelte';
  import {
    clearedTitleFilter,
    languageOptions,
    parseTitleFilter,
    removeTitleChip,
    titleApiQuery,
    titleChips,
    titleFilterHref,
    toggleRef,
    TITLE_SORTS,
    type FilterRef,
    type TitleFilter,
  } from '../explore';
  import { navigate, router } from '../router.svelte';
  import { discover } from '../state/discover.svelte';

  interface Props {
    mediaType: MediaType;
  }

  let { mediaType }: Props = $props();

  /** The dropdown takes {id, name}; TITLE_SORTS carries the provider pair too. */
  const SORT_CHOICES = TITLE_SORTS.map((option) => ({ id: option.key, name: option.label }));

  /** The year pill writes whole years; the API takes the days they bound. */
  const YEAR_START = '-01-01';
  const YEAR_END = '-12-31';

  let filter = $derived(parseTitleFilter(mediaType, router.params));
  let chips = $derived(titleChips(mediaType, filter));

  let items = $state<DiscoverItem[]>([]);
  let page = $state<DiscoverScopePage | null>(null);
  let loading = $state(true);
  let loadingMore = $state(false);
  let error = $state<string | null>(null);
  let fault = $state<CredentialFault | null>(null);

  /** The genre vocabulary, which differs per media type and is fetched once. */
  let genres = $state<{ id: string; name: string }[]>([]);
  let genresLoading = $state(false);

  let inFlight: AbortController | null = null;

  /**
   * The question currently on screen, as a string. The effect below keys off
   * it, so a filter change reloads and a hide-in-library flick does not — that
   * one is a view over the answer, not a new question.
   */
  let question = $derived(JSON.stringify(titleApiQuery(mediaType, filter, 1)));

  async function load(pageNumber: number) {
    inFlight?.abort();
    const controller = new AbortController();
    inFlight = controller;
    if (pageNumber <= 1) loading = true;
    else loadingMore = true;
    try {
      const fetched = await api.discoverScope(
        mediaType,
        titleApiQuery(mediaType, filter, pageNumber),
        controller.signal,
      );
      page = fetched;
      // Pages append: this is one long grid, not N screens. The merge dedupes
      // because the grid is keyed by (media_type, tmdb_id) and TMDB really does
      // hand back a page twice at its own ceiling.
      items = pageNumber <= 1 ? fetched.items : mergeItems(items, fetched.items);
      error = null;
      fault = null;
    } catch (err) {
      if (controller.signal.aborted) return;
      error = errorText(err);
      fault = metadataFault(err);
    } finally {
      if (!controller.signal.aborted) {
        loading = false;
        loadingMore = false;
      }
    }
  }

  function itemKey(item: DiscoverItem): string {
    return `${item.media_type}-${item.tmdb_id}`;
  }

  function mergeItems(existing: DiscoverItem[], fetched: DiscoverItem[]): DiscoverItem[] {
    const seen = new Set(existing.map(itemKey));
    return [...existing, ...fetched.filter((item) => !seen.has(itemKey(item)))];
  }

  // Every filter change is a fresh first page, including the first render.
  $effect(() => {
    void question;
    items = [];
    page = null;
    void load(1);
  });

  // The curated network list lives on the Featured payload, which the store
  // already caches — the series scope borrows it rather than paying for a
  // second copy, and does not load it at all on the movie scope.
  let networkOptions = $derived(
    (discover.home?.networks ?? []).map((source) => ({ id: String(source.id), name: source.name })),
  );

  $effect(() => {
    if (mediaType !== 'series') return;
    void discover.load();
  });

  $effect(() => {
    const type = mediaType;
    genresLoading = true;
    const controller = new AbortController();
    void api
      .discoverGenres(type, controller.signal)
      .then((answer) => {
        genres = answer.genres.map((g) => ({ id: String(g.tmdb_id), name: g.name }));
      })
      // A genre list that failed to load leaves the pill empty and says so;
      // it must not take the results down with it.
      .catch(() => {})
      .finally(() => {
        genresLoading = false;
      });
    return () => controller.abort();
  });

  /** Replace the address. `replace` so the Back button is not a filter undo log. */
  function apply(next: TitleFilter) {
    navigate(titleFilterHref(mediaType, next), { replace: true });
  }

  function toggleGenre(id: string) {
    const known = genres.find((g) => g.id === id);
    apply({ ...filter, genres: toggleRef(filter.genres, { id, name: known?.name ?? '' }) });
  }

  function toggleNetwork(id: string) {
    const known = networkOptions.find((n) => n.id === id);
    apply({ ...filter, networks: toggleRef(filter.networks, { id, name: known?.name ?? '' }) });
  }

  function setLanguage(code: string) {
    apply({ ...filter, language: filter.language === code ? '' : code });
  }

  function setYear(which: 'from' | 'to', year: number) {
    const value = year > 0 ? `${year}${which === 'from' ? YEAR_START : YEAR_END}` : '';
    apply({ ...filter, [which]: value });
  }

  function yearOf(date: string): number {
    return date === '' ? 0 : Number(date.slice(0, 4));
  }

  let visible = $derived(filter.hideOwned ? items.filter((item) => !item.in_library) : items);
  let hasMore = $derived(page !== null && page.page < page.total_pages);
  let nextPage = $derived((page?.page ?? 0) + 1);
  let noun = $derived(mediaType === 'movie' ? 'movie' : 'series');
  let sortKey = $derived(filter.sort);
</script>

<div class="flex flex-col gap-6">
  <!-- No match count, unlike the adult scope: TMDB's filtered page carries a
       page count and not a result total, and a number nobody can trust is worse
       than none. -->
  <ExploreScopes
    active={mediaType === 'movie' ? 'movies' : 'series'}
    note="The whole catalogue, filtered your way." />

  <div class="flex flex-wrap items-center gap-2">
    <FilterPill label="Genre" applied={filter.genres.length > 0}>
      {#snippet children()}
        <FilterOptions
          options={genres}
          selected={filter.genres.map((r) => r.id)}
          onselect={toggleGenre}
          loading={genresLoading}
          emptyText="No genres — the provider did not answer." />
      {/snippet}
    </FilterPill>

    {#if mediaType === 'movie'}
      <FilterPill label="Cast & crew" applied={filter.people.length > 0} width="w-72">
        {#snippet children()}
          <FilterTypeahead
            search={async (q, signal) =>
              (await api.discoverPeople(q, signal)).map((p) => ({
                id: String(p.tmdb_id),
                name: p.name,
                hint: p.department,
              }))}
            selected={filter.people}
            ontoggle={(ref: FilterRef) => apply({ ...filter, people: toggleRef(filter.people, ref) })}
            placeholder="Search people…"
            ariaLabel="Search cast and crew" />
        {/snippet}
      </FilterPill>
    {:else}
      <!-- A list, not a typeahead: TMDB has no network search endpoint (only
           /search/company, whose ids are company ids and would filter nothing),
           so the choices are the networks Caravan already curates on Featured.
           A typeahead here would be a search box that cannot search. -->
      <FilterPill label="Network" applied={filter.networks.length > 0} width="w-56">
        {#snippet children()}
          <FilterOptions
            options={networkOptions}
            selected={filter.networks.map((r) => r.id)}
            onselect={toggleNetwork}
            loading={discover.loading && networkOptions.length === 0}
            emptyText="No networks — the provider did not answer." />
        {/snippet}
      </FilterPill>
    {/if}

    <FilterPill label="Studio" applied={filter.companies.length > 0} width="w-72">
      {#snippet children()}
        <FilterTypeahead
          search={async (q, signal) =>
            (await api.discoverCompanies(q, signal)).map((c) => ({
              id: String(c.tmdb_id),
              name: c.name,
              hint: c.country,
            }))}
          selected={filter.companies}
          ontoggle={(ref: FilterRef) =>
            apply({ ...filter, companies: toggleRef(filter.companies, ref) })}
          placeholder="Search studios…"
          ariaLabel="Search studios" />
      {/snippet}
    </FilterPill>

    <FilterPill label="Keyword" applied={filter.keywords.length > 0} width="w-72">
      {#snippet children()}
        <FilterTypeahead
          search={async (q, signal) =>
            (await api.discoverKeywords(q, signal)).map((k) => ({
              id: String(k.tmdb_id),
              name: k.name,
            }))}
          selected={filter.keywords}
          ontoggle={(ref: FilterRef) =>
            apply({ ...filter, keywords: toggleRef(filter.keywords, ref) })}
          placeholder="Search keywords…"
          ariaLabel="Search keywords" />
      {/snippet}
    </FilterPill>

    <FilterPill label="Year" applied={filter.from !== '' || filter.to !== ''}>
      {#snippet children()}
        <FilterRange
          minValue={yearOf(filter.from)}
          minLabel="From"
          onmin={(value) => setYear('from', value)}
          maxValue={yearOf(filter.to)}
          maxLabel="To"
          onmax={(value) => setYear('to', value)}
          placeholder="2019" />
      {/snippet}
    </FilterPill>

    <FilterPill label="Runtime" applied={filter.runtimeMin > 0 || filter.runtimeMax > 0}>
      {#snippet children()}
        <FilterRange
          minValue={filter.runtimeMin}
          minLabel="Min"
          onmin={(value) => apply({ ...filter, runtimeMin: value })}
          maxValue={filter.runtimeMax}
          maxLabel="Max"
          onmax={(value) => apply({ ...filter, runtimeMax: value })}
          placeholder="90"
          hint={mediaType === 'series' ? 'Minutes per episode.' : 'Minutes.'} />
      {/snippet}
    </FilterPill>

    <FilterPill label="Rating" applied={filter.ratingMin > 0}>
      {#snippet children()}
        <FilterRange
          minValue={filter.ratingMin}
          minLabel="At least"
          onmin={(value) => apply({ ...filter, ratingMin: Math.min(10, value) })}
          placeholder="7"
          max={10}
          hint="Out of 10 — halves count, so 7.5 is a filter." />
      {/snippet}
    </FilterPill>

    <FilterPill label="Language" applied={filter.language !== ''} width="w-56">
      {#snippet children()}
        <FilterOptions
          options={languageOptions().map((l) => ({ id: l.code, name: l.label }))}
          selected={filter.language === '' ? [] : [filter.language]}
          onselect={setLanguage} />
      {/snippet}
    </FilterPill>

    <div class="ml-auto flex items-center gap-3">
      <Toggle
        checked={filter.hideOwned}
        label="Hide in library"
        onchange={(next) => apply({ ...filter, hideOwned: next })} />
      <Dropdown
        label="Sort"
        options={SORT_CHOICES}
        value={sortKey}
        onselect={(id) => apply({ ...filter, sort: id })} />
    </div>
  </div>

  <AppliedChips
    {chips}
    onremove={(key) => apply(removeTitleChip(filter, key))}
    onclear={() => apply(clearedTitleFilter(filter))} />

  {#if error && items.length === 0}
    <DiscoverError message={error} {fault} onretry={() => void load(1)} />
  {:else if loading && items.length === 0}
    <PosterGridSkeleton />
  {:else if visible.length === 0}
    <EmptyState
      icon="compass"
      title="Nothing matches"
      message={chips.length === 0
        ? `The provider returned no ${noun} results.`
        : 'No title matches every filter. Try removing one.'} />
  {:else}
    <PosterGrid>
      {#each visible as item (itemKey(item))}
        <DiscoverCard {item} />
      {/each}
    </PosterGrid>
  {/if}

  {#if error && items.length > 0}
    <!-- `page` only advances on success, so a retry targets the page that
         failed rather than the last one that worked. -->
    <DiscoverError message={error} {fault} onretry={() => void load(nextPage)} />
  {/if}

  {#if hasMore && !error}
    <div class="flex justify-center">
      <Button variant="secondary" disabled={loadingMore} onclick={() => void load(nextPage)}>
        {loadingMore ? 'Loading…' : 'Load more'}
      </Button>
    </div>
  {/if}
</div>
