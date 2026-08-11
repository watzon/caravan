<script lang="ts">
  /**
   * Explore → Discover: the landing screen, and what `/` now shows.
   *
   * Everything on it is pre-decorated by GET /discover (in_library, requested),
   * so no card cross-references a second call to know its own state. The
   * payload is cached in the discover store because it costs three sequential
   * TMDB round trips — bouncing into a title and back must not pay twice.
   */
  import { onMount } from 'svelte';
  import type { DiscoverItem, DiscoverSource } from '../api/types';
  import AddRequestModal from '../components/AddRequestModal.svelte';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import DiscoverError from '../components/DiscoverError.svelte';
  import DiscoverShelf from '../components/DiscoverShelf.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import ExploreScopes from '../components/ExploreScopes.svelte';
  import Icon from '../components/Icon.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { useI18n } from '../i18n.svelte';
  import {
    discoverHref,
    libraryHref,
    mediaTypeChip,
    ratingPresentation,
    sourceHref,
    type RequestMode,
  } from '../discover';
  import { discover } from '../state/discover.svelte';
  import { session } from '../state/session.svelte';

  const { t } = useI18n();
  /** The billboard title the modal is open for, in whichever mode the role allows. */
  let acquiring = $state<DiscoverItem | null>(null);

  /**
   * Putting a title straight into the library is an admin's to do; a member
   * asks for it. One button either way — offering both to somebody who cannot
   * use one of them is a door painted on a wall.
   */
  let mode = $derived<RequestMode>(session.isAdmin ? 'add' : 'request');

  onMount(() => void discover.load());

  let home = $derived(discover.home);
  let hero = $derived<DiscoverItem | null>(home?.trending[0] ?? null);
  let heroRating = $derived(
    hero ? ratingPresentation(hero.vote_average, hero.vote_count, hero.date) : null,
  );

  /** The billboard's one call to action, under whichever verb the role gets. */
  let heroAction = $derived(
    hero === null
      ? ''
      : mode === 'add'
        ? t(hero.media_type === 'movie' ? 'route.discover.addMovie' : 'route.discover.addSeries')
        : t(hero.media_type === 'movie' ? 'route.discover.requestMovie' : 'route.discover.requestSeries'),
  );

  /** Trending shelf minus the billboard, so #1 is not on screen twice. */
  let trendingRest = $derived(home ? home.trending.slice(1) : []);

  function metaLine(item: DiscoverItem): string {
    return t('route.discover.metaLine', {
      year: item.year,
      type: t(item.media_type === 'movie' ? 'route.discover.movie' : 'route.discover.series'),
    });
  }
</script>

{#snippet tiles(label: string, sources: DiscoverSource[])}
  {#if sources.length > 0}
    <section class="flex flex-col gap-3">
      <h2 class="font-display text-lg font-semibold tracking-tight text-ink">{label}</h2>
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        {#each sources as source (source.id)}
          <a
            href={sourceHref(source)}
            title={source.name}
            class="flex h-14 min-w-0 items-center justify-center rounded-md border border-border bg-surface px-3
                   text-center text-sm text-ink-secondary transition-colors duration-150 ease-out
                   hover:border-border-strong hover:bg-raised hover:text-ink">
            <span class="line-clamp-2">{source.name}</span>
          </a>
        {/each}
      </div>
    </section>
  {/if}
{/snippet}

<div class="flex flex-col gap-8">
  <!-- Featured is one of four scopes now (PLAN phase 12 task 4), so the row is
       here too: this screen is where somebody arrives, and it is the only place
       the other three are announced. -->
  <ExploreScopes active="featured" />

  {#if discover.error}
    <DiscoverError
      message={discover.error}
      fault={discover.fault}
      onretry={() => void discover.load(true)} />
  {:else if discover.loading && home === null}
    <Skeleton class="h-64 w-full rounded-lg" />
    <div class="flex gap-4">
      {#each Array.from({ length: 6 }) as _, i (i)}
        <div class="flex w-40 shrink-0 flex-col gap-2">
          <Skeleton class="aspect-[2/3] w-full rounded-md" />
          <Skeleton class="h-4 w-3/4" />
        </div>
      {/each}
    </div>
  {:else if home}
    {#if hero}
      <section
        class="relative overflow-hidden rounded-lg border border-border bg-surface"
        aria-label={t('route.discover.trendingFirst')}>
        {#if hero.backdrop_url}
          <img
            src={hero.backdrop_url}
            alt=""
            class="absolute inset-0 size-full object-cover"
            loading="eager"
            decoding="async" />
        {:else}
          <!-- No artwork: the initial on the app's own ground, the same
               fallback the library posters use rather than a stock image. -->
          <div class="absolute inset-0 flex items-center justify-end bg-raised pr-12">
            <span class="font-display text-[8rem] font-bold leading-none text-border-strong">
              {hero.title.slice(0, 1).toUpperCase()}
            </span>
          </div>
        {/if}
        <div class="absolute inset-0 bg-linear-to-r from-bg via-bg/85 to-bg/20"></div>

        <div class="relative flex max-w-2xl flex-col gap-3 px-6 py-10">
          <p class="font-mono text-xs font-medium tracking-wide text-warning">
            {t('route.discover.trendingFirst')} · {mediaTypeChip(hero.media_type)}
          </p>
          <h2 class="font-display text-2xl font-bold tracking-tight text-ink" title={hero.title}>
            {hero.title}
          </h2>
          <p class="text-base text-ink-secondary" title={metaLine(hero)}>{metaLine(hero)}</p>
          <p
            class="line-clamp-2 max-w-xl text-base text-ink-secondary"
            title={hero.overview || t('route.discover.noOverview')}>
            {hero.overview || t('route.discover.noOverview')}
          </p>
          <div class="mt-1 flex flex-wrap items-center gap-3">
            {#if hero.in_library && session.isAdmin}
              <Button variant="primary" href={libraryHref(hero.media_type, hero.library_id)}>
                <Icon name="check" size={14} />
                {t('route.discover.inLibrary')}
              </Button>
            {:else if hero.in_library}
              <!-- /movies/:id and /series/:id are admin screens, so this link
                   would bounce a member straight back here — from the
                   billboard it would read as a button that does nothing. The
                   fact is still worth saying, so it is said rather than
                   linked. -->
              <Badge tone="success">{t('route.discover.inLibrary')}</Badge>
            {:else}
              <Button variant="primary" onclick={() => (acquiring = hero)}>
                <Icon name="plus" size={14} />
                {heroAction}
              </Button>
            {/if}
            <Button variant="secondary" href={discoverHref(hero)}>{t('route.discover.details')}</Button>
            {#if heroRating}
              <Badge mono tone="neutral" title={heroRating.title}>
                <span class="inline-flex items-center gap-1">
                  <Icon name="star" size={12} />
                  {#if heroRating.text}
                    {heroRating.text}
                  {:else}
                    <span class="sr-only">{heroRating.title}</span>
                  {/if}
                </span>
              </Badge>
            {/if}
          </div>
        </div>
      </section>
    {/if}

    <DiscoverShelf title={t('route.discover.trendingWeek')} items={trendingRest} showType />
    <DiscoverShelf title={t('route.discover.popularMovies')} items={home.popular_movies} />
    <DiscoverShelf title={t('route.discover.popularSeries')} items={home.popular_series} />

    <!-- The curated lists ARE the whole shelf (internal/api/discover.go), so
         there is no "all networks" screen to link to. -->
    {@render tiles(t('route.discover.browseByNetwork'), home.networks)}
    {@render tiles(t('route.discover.browseByStudio'), home.studios)}

    {#if home.trending.length === 0 && home.popular_movies.length === 0 && home.popular_series.length === 0}
      <EmptyState
        icon="compass"
        title={t('route.discover.emptyTitle')}
        message={t('route.discover.emptyMessage')} />
    {/if}
  {/if}
</div>

{#if acquiring}
  <AddRequestModal
    {mode}
    mediaType={acquiring.media_type}
    tmdbID={acquiring.tmdb_id}
    title={acquiring.title}
    year={acquiring.year}
    posterPath={acquiring.poster_path}
    onclose={() => (acquiring = null)}
    ondone={(result) => {
      const item = acquiring;
      if (!item) return;
      // The cached shelves hold this same title, so they are patched rather
      // than refetched — the home payload costs three TMDB round trips.
      if (result.kind === 'added') {
        discover.markInLibrary(item.media_type, item.tmdb_id, result.libraryID);
      } else {
        discover.markRequested(item.media_type, item.tmdb_id);
      }
    }} />
{/if}
