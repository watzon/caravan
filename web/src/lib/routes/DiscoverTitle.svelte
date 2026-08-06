<script lang="ts">
  /**
   * Explore → one title's acquisition screen.
   *
   * Deliberately not the library detail view: nothing here is about files. It
   * answers "do I have this, has somebody asked for it, and how do I get it",
   * and every one of those facts arrives decorated on the payload rather than
   * being pieced together from a second call.
   */
  import { onMount } from 'svelte';
  import { api, errorText, posterSrc } from '../api/client';
  import { metadataFault, type CredentialFault } from '../credentials';
  import type { DiscoverTitle, MediaType } from '../api/types';
  import AddRequestModal from '../components/AddRequestModal.svelte';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import DiscoverError from '../components/DiscoverError.svelte';
  import DiscoverShelf from '../components/DiscoverShelf.svelte';
  import Icon from '../components/Icon.svelte';
  import MetadataLinks from '../components/MetadataLinks.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import {
    canRequestSeason,
    languageName,
    libraryHref,
    ratingPresentation,
    runtimeText,
    seasonMeta,
    type RequestMode,
  } from '../discover';
  import { UNKNOWN, formatDate, seasonLabel } from '../format';
  import { discover } from '../state/discover.svelte';
  import { session } from '../state/session.svelte';

  interface Props {
    type: MediaType;
    tmdbID: number;
  }

  let { type, tmdbID }: Props = $props();

  let title = $state<DiscoverTitle | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  /** The credential fault behind the last failure, if that is what it was. */
  let fault = $state<CredentialFault | null>(null);

  /** Which modal is open, and (for a per-season Request) what it starts with. */
  let modal = $state<{ mode: RequestMode; preselect: number[] | null } | null>(null);

  async function load() {
    loading = true;
    try {
      title = await api.discoverTitle(type, tmdbID);
      error = null;
      fault = null;
    } catch (err) {
      error = errorText(err);
      fault = metadataFault(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  let rating = $derived(
    title
      ? ratingPresentation(title.vote_average, title.vote_count, title.date)
      : { text: null, title: 'Not yet rated' },
  );
  let genre = $derived(title?.genres[0] ?? '');
  let episodeTotal = $derived(
    (title?.seasons ?? []).reduce((sum, s) => sum + Math.max(0, s.episode_count), 0),
  );

  /**
   * The chip beside the eyebrow. Owned beats requested: once the title is in
   * the library its pending request has already been absorbed.
   */
  let stateChip = $derived.by(() => {
    const t = title;
    if (!t) return null;
    if (t.in_library) return { tone: 'success' as const, label: 'IN LIBRARY' };
    if (!t.requested) return null;
    const pending = (t.seasons ?? []).filter((s) => s.requested && !s.in_library);
    if (t.media_type === 'series' && pending.length === 1) {
      return { tone: 'warning' as const, label: `${seasonLabel(pending[0]!.season_number).toUpperCase()} REQUESTED` };
    }
    return { tone: 'warning' as const, label: 'REQUESTED' };
  });

  let metaParts = $derived.by(() => {
    const t = title;
    if (!t) return [] as string[];
    const parts: string[] = [];
    if (t.year > 0) parts.push(String(t.year));
    if (t.media_type === 'series') {
      if (t.seasons.length > 0) {
        parts.push(`${t.seasons.length} season${t.seasons.length === 1 ? '' : 's'}`);
      }
      if (episodeTotal > 0) parts.push(`${episodeTotal} episodes`);
    }
    if (t.runtime > 0) parts.push(runtimeText(t.runtime));
    // Who made it, under the name its media type uses: a network for a series,
    // a studio for a movie.
    if (t.network) parts.push(t.network);
    if (t.status) parts.push(t.status);
    return parts;
  });

  let links = $derived.by(() => {
    const t = title;
    if (!t) return [];
    const out = [
      { label: 'TMDB', href: `https://www.themoviedb.org/${t.media_type === 'movie' ? 'movie' : 'tv'}/${t.tmdb_id}` },
    ];
    if (t.imdb_id) out.push({ label: 'IMDb', href: `https://www.imdb.com/title/${t.imdb_id}/` });
    return out;
  });

  function open(mode: RequestMode, preselect: number[] | null = null) {
    modal = { mode, preselect };
  }
</script>

<div class="flex flex-col gap-8">
  <a
    href="/discover"
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    Back to Discover
  </a>

  {#if error}
    <DiscoverError message={error} {fault} onretry={load} />
  {:else if loading && title === null}
    <div class="flex gap-6">
      <Skeleton class="aspect-[2/3] w-52 rounded-md" />
      <div class="flex flex-1 flex-col gap-3">
        <Skeleton class="h-8 w-1/2" />
        <Skeleton class="h-4 w-1/3" />
        <Skeleton class="h-20 w-full" />
      </div>
    </div>
  {:else if title}
    <section class="relative overflow-hidden rounded-lg border border-border bg-surface">
      {#if title.backdrop_url}
        <img
          src={title.backdrop_url}
          alt=""
          class="absolute inset-0 size-full object-cover"
          decoding="async" />
      {/if}
      <div class="absolute inset-0 bg-linear-to-r from-bg via-bg/90 to-bg/40"></div>

      <div class="relative flex flex-col gap-6 p-6 md:flex-row">
        <div class="w-32 shrink-0 md:w-44">
          <Poster
            path={title.poster_url}
            alt={title.title}
            fallbackIcon={title.media_type === 'movie' ? 'film' : 'tv'} />
        </div>

        <div class="flex min-w-0 flex-1 flex-col gap-3">
          <p class="flex flex-wrap items-center gap-2 font-mono text-xs font-medium tracking-wide text-ink-muted">
            <span>{title.media_type === 'movie' ? 'MOVIE' : 'SERIES'}{genre ? ` · ${genre.toUpperCase()}` : ''}</span>
            {#if stateChip}
              <Badge mono tone={stateChip.tone}>{stateChip.label}</Badge>
            {/if}
          </p>

          <h2 class="font-display text-2xl font-bold tracking-tight text-ink" title={title.title}>
            {title.title}
          </h2>

          <p class="flex flex-wrap items-center gap-2 text-base text-ink-secondary">
            {#each metaParts as part, i (part + i)}
              {#if i > 0}<span class="text-ink-muted">·</span>{/if}
              <span>{part}</span>
            {/each}
            {#if metaParts.length === 0}<span>{UNKNOWN}</span>{/if}
          </p>

          <div class="mt-1 flex flex-wrap items-center gap-3">
            {#if title.in_library && session.isAdmin}
              <Button variant="primary" href={libraryHref(title.media_type, title.library_id)}>
                <Icon name="check" size={14} />
                Open in library
              </Button>
            {:else if title.in_library}
              <!-- The library screens are admin-only, so a member following
                   this would be bounced back to Discover. It is the screen's
                   only call to action for a title Caravan already has, which
                   makes a dead one worse than none: state the fact instead. -->
              <Badge tone="success">In library</Badge>
            {:else}
              <Button variant="primary" onclick={() => open('request')}>
                Request {title.media_type === 'movie' ? 'movie' : 'series'}
              </Button>
              <!-- Adding straight to the library picks a quality profile and a
                   root folder, which are the admin's calls. A member only asks. -->
              {#if session.isAdmin}
                <Button variant="secondary" onclick={() => open('add')}>Add to library</Button>
              {/if}
            {/if}
          </div>

          {#if !title.in_library && session.isAdmin}
            <p class="text-sm text-ink-muted">
              Direct add is available to admins · picks quality profile &amp; root folder
            </p>
          {/if}
        </div>
      </div>
    </section>

    <div class="flex flex-col gap-8 lg:flex-row">
      <div class="flex min-w-0 flex-1 flex-col gap-8">
        <section class="flex flex-col gap-2">
          <h3 class="font-display text-lg font-semibold tracking-tight text-ink">Overview</h3>
          <p class="max-w-3xl text-md text-ink-secondary">
            {title.overview || 'No overview available.'}
          </p>
        </section>

        {#if title.cast.length > 0}
          <section class="flex flex-col gap-3">
            <div class="flex items-baseline gap-3">
              <h3 class="font-display text-lg font-semibold tracking-tight text-ink">Cast</h3>
              <a
                href={`https://www.themoviedb.org/${title.media_type === 'movie' ? 'movie' : 'tv'}/${title.tmdb_id}/cast`}
                target="_blank"
                rel="noopener noreferrer"
                class="ml-auto text-sm text-accent-text transition-colors duration-150 ease-out hover:text-accent">
                Full cast &amp; crew
              </a>
            </div>
            <ul class="flex gap-4 overflow-x-auto pb-1">
              {#each title.cast.slice(0, 6) as member (member.tmdb_id)}
                <li class="flex w-24 shrink-0 flex-col items-center gap-2 text-center">
                  {#if posterSrc(member.profile_url)}
                    <img
                      src={posterSrc(member.profile_url)}
                      alt=""
                      loading="lazy"
                      decoding="async"
                      class="size-16 rounded-full object-cover" />
                  {:else}
                    <span
                      class="flex size-16 items-center justify-center rounded-full bg-raised
                             font-display text-md font-semibold text-ink-muted">
                      {member.name
                        .split(' ')
                        .map((w) => w.slice(0, 1))
                        .slice(0, 2)
                        .join('')
                        .toUpperCase()}
                    </span>
                  {/if}
                  <span class="w-full truncate text-sm text-ink" title={member.name}>{member.name}</span>
                  <span
                    class="w-full truncate text-xs text-ink-secondary"
                    title={member.character || UNKNOWN}>
                    {member.character || UNKNOWN}
                  </span>
                </li>
              {/each}
            </ul>
          </section>
        {/if}

        {#if title.seasons.length > 0}
          <section class="flex flex-col gap-3">
            <h3 class="font-display text-lg font-semibold tracking-tight text-ink">Seasons</h3>
            <ul class="flex flex-col divide-y divide-border overflow-hidden rounded-md border border-border">
              {#each title.seasons as season (season.season_number)}
                {@const meta = seasonMeta(season)}
                <li class="flex min-h-12 flex-wrap items-center gap-3 px-3 py-2">
                  <span class="text-base text-ink">{seasonLabel(season.season_number)}</span>
                  {#if meta}
                    <span class="font-mono text-xs text-ink-muted">{meta}</span>
                  {/if}
                  <span class="ml-auto shrink-0">
                    {#if season.in_library}
                      <Badge tone="success">In library</Badge>
                    {:else if season.requested}
                      <Badge tone="warning">Requested · pending approval</Badge>
                    {:else if canRequestSeason(title.in_library, season)}
                      <Button
                        variant="secondary"
                        size="sm"
                        onclick={() => open('request', [season.season_number])}>
                        Request
                      </Button>
                    {:else}
                      <!-- The series is ours, so POST /requests would answer
                           409 for the whole title. Say what is true and let
                           the library screen own the season. -->
                      <Badge tone="neutral">Not in library</Badge>
                    {/if}
                  </span>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
      </div>

      <aside class="flex w-full shrink-0 flex-col gap-4 rounded-lg border border-border bg-surface p-4 lg:w-[300px]">
        <div class="flex gap-3">
          <div class="flex flex-1 flex-col gap-1 rounded-md bg-raised p-3">
            <span class="micro-label">TMDB</span>
            <span class="font-mono text-md text-ink" title={rating.title}>{rating.text ?? rating.title}</span>
          </div>
        </div>

        <dl class="flex flex-col gap-3">
          {#if title.status}
            <div>
              <dt class="micro-label">Status</dt>
              <dd class="mt-1 text-sm text-ink">{title.status}</dd>
            </div>
          {/if}
          <div>
            <dt class="micro-label">{title.media_type === 'movie' ? 'Released' : 'First aired'}</dt>
            <dd class="mt-1 text-sm text-ink">{formatDate(title.date)}</dd>
          </div>
          {#if title.media_type === 'series'}
            <div>
              <dt class="micro-label">Last aired</dt>
              <dd class="mt-1 text-sm text-ink">{formatDate(title.last_aired)}</dd>
            </div>
          {/if}
          <div>
            <dt class="micro-label">{title.media_type === 'movie' ? 'Studio' : 'Network'}</dt>
            <dd class="mt-1 text-sm text-ink">{title.network || UNKNOWN}</dd>
          </div>
          {#if title.runtime > 0}
            <div>
              <dt class="micro-label">Runtime</dt>
              <dd class="mt-1 font-mono text-sm text-ink">{runtimeText(title.runtime)}</dd>
            </div>
          {/if}
          <div>
            <dt class="micro-label">Language</dt>
            <dd class="mt-1 text-sm text-ink">{languageName(title.language)}</dd>
          </div>
        </dl>

        {#if title.genres.length > 0}
          <div class="flex flex-wrap gap-1.5">
            {#each title.genres as name (name)}
              <Badge tone="neutral">{name}</Badge>
            {/each}
          </div>
        {/if}

        <MetadataLinks {links} />
      </aside>
    </div>

    <DiscoverShelf title="More like this" items={title.recommendations} showType />
  {/if}
</div>

{#if modal && title}
  <AddRequestModal
    mode={modal.mode}
    mediaType={title.media_type}
    tmdbID={title.tmdb_id}
    title={title.title}
    year={title.year}
    posterPath={title.poster_path}
    seasons={title.seasons}
    preselect={modal.preselect}
    onclose={() => (modal = null)}
    ondone={(result) => {
      // The screen's own facts changed; the cached discover shelves hold the
      // same title, so they are patched rather than refetched.
      if (result.kind === 'requested') {
        discover.markRequested(type, tmdbID);
      } else {
        discover.markInLibrary(type, tmdbID, result.libraryID);
      }
      void load();
    }} />
{/if}
