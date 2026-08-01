<script lang="ts">
  /**
   * The ⌘K add flow (SPEC §9 steps 1-2): TMDB-backed search, then "add to
   * library" with monitoring on. Also reusable as a plain picker for the
   * scan-review manual match, where `onpick` replaces the add call.
   */
  import { untrack } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { MovieMeta, SearchResults, SeriesMeta } from '../api/types';
  import { navigate } from '../router.svelte';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import LoadError from './LoadError.svelte';
  import Modal from './Modal.svelte';
  import Poster from './Poster.svelte';
  import Skeleton from './Skeleton.svelte';
  import TextInput from './TextInput.svelte';

  interface Props {
    onclose: () => void;
    /** Restrict the picker to one kind (scan review knows what it parsed). */
    kind?: 'movie' | 'series' | null;
    /** Prefill for the manual-match flow. */
    initialQuery?: string;
    title?: string;
    /**
     * When supplied the modal picks instead of adding: the caller decides what
     * to do with the chosen TMDB id.
     */
    onpick?: (kind: 'movie' | 'series', tmdbID: number) => Promise<void> | void;
  }

  let {
    onclose,
    kind: fixedKind = null,
    initialQuery = '',
    title = 'Add to library',
    onpick,
  }: Props = $props();

  const DEBOUNCE_MS = 250;
  const MIN_QUERY = 2;

  // Both props seed local state once: the modal is remounted per use, so
  // reading them untracked is the intent, not an oversight.
  let query = $state(untrack(() => initialQuery));
  let kind = $state<'movie' | 'series'>(untrack(() => fixedKind) ?? 'movie');
  let results = $state<SearchResults>({ movies: [], series: [] });
  let loading = $state(false);
  let error = $state<string | null>(null);
  let busyID = $state<number | null>(null);

  let rows = $derived<(MovieMeta | SeriesMeta)[]>(
    kind === 'movie' ? results.movies : results.series,
  );

  $effect(() => {
    const q = query.trim();
    const k = kind;
    if (q.length < MIN_QUERY) {
      results = { movies: [], series: [] };
      loading = false;
      error = null;
      return;
    }

    const controller = new AbortController();
    loading = true;
    const timer = setTimeout(async () => {
      try {
        const found = await api.search(q, k, controller.signal);
        results = { movies: found.movies ?? [], series: found.series ?? [] };
        error = null;
      } catch (err) {
        if (controller.signal.aborted) return;
        error = errorText(err);
      } finally {
        if (!controller.signal.aborted) loading = false;
      }
    }, DEBOUNCE_MS);

    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  });

  function yearOf(row: MovieMeta | SeriesMeta): number {
    return row.year;
  }

  async function choose(row: MovieMeta | SeriesMeta) {
    busyID = row.tmdb_id;
    try {
      if (onpick) {
        await onpick(kind, row.tmdb_id);
        return;
      }
      if (kind === 'movie') {
        const added = await api.addMovie({ tmdb_id: row.tmdb_id, monitored: true });
        pushToast(`Added ${added.title}`, 'success');
        onclose();
        navigate(`/movies/${added.id}`);
      } else {
        const added = await api.addSeries({ tmdb_id: row.tmdb_id, monitored: true });
        pushToast(`Added ${added.title}`, 'success');
        onclose();
        navigate(`/series/${added.id}`);
      }
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }
</script>

<Modal {title} {onclose}>
  <div class="flex flex-col gap-4 p-4">
    {#if !fixedKind}
      <div class="flex gap-2" role="tablist" aria-label="Search type">
        {#each [{ key: 'movie', label: 'Movies' }, { key: 'series', label: 'Series' }] as tab (tab.key)}
          <button
            type="button"
            role="tab"
            aria-selected={kind === tab.key}
            onclick={() => (kind = tab.key as 'movie' | 'series')}
            class="h-7 rounded-full border px-3 text-sm transition-colors duration-150 ease-out
                   {kind === tab.key
              ? 'border-accent bg-accent-tint text-accent-text'
              : 'border-border bg-surface text-ink-secondary hover:bg-raised hover:text-ink'}">
            {tab.label}
          </button>
        {/each}
      </div>
    {/if}

    <TextInput
      bind:value={query}
      type="search"
      autofocus
      placeholder={kind === 'movie' ? 'Search TMDB for a movie…' : 'Search TMDB for a series…'}
      ariaLabel="Search TMDB" />

    {#if error}
      <LoadError message={error} />
    {:else if loading}
      <div class="flex flex-col gap-2">
        {#each Array.from({ length: 4 }) as _, i (i)}
          <div class="flex items-center gap-3 rounded-md border border-border p-2">
            <Skeleton class="h-[72px] w-12 rounded-sm" />
            <div class="flex flex-1 flex-col gap-2">
              <Skeleton class="h-4 w-1/2" />
              <Skeleton class="h-3 w-3/4" />
            </div>
          </div>
        {/each}
      </div>
    {:else if query.trim().length < MIN_QUERY}
      <EmptyState
        icon="search"
        title="Search the metadata provider"
        message="Type at least two characters to look up a title on TMDB." />
    {:else if rows.length === 0}
      <EmptyState
        icon="search"
        title="No matches"
        message="TMDB returned nothing for “{query.trim()}”. Try the original-language title, or add the year." />
    {:else}
      <ul class="flex flex-col gap-2">
        {#each rows as row (row.tmdb_id)}
          <li class="flex items-start gap-3 rounded-md border border-border p-2 transition-colors duration-150 ease-out hover:bg-raised">
            <div class="w-12 shrink-0">
              <Poster
                path={row.poster_url}
                alt=""
                fallbackIcon={kind === 'movie' ? 'film' : 'tv'} />
            </div>
            <div class="min-w-0 flex-1">
              <p class="flex items-center gap-2 text-base font-medium text-ink">
                <span class="truncate">{row.title}</span>
                {#if yearOf(row) > 0}
                  <Badge mono>{yearOf(row)}</Badge>
                {/if}
              </p>
              <p class="line-clamp-2 text-sm text-ink-secondary">
                {row.overview || 'No overview available.'}
              </p>
            </div>
            <Button
              variant="primary"
              size="sm"
              disabled={busyID !== null}
              onclick={() => choose(row)}>
              {busyID === row.tmdb_id ? 'Working…' : onpick ? 'Match' : 'Add'}
            </Button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</Modal>
