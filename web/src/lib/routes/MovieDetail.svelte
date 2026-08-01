<script lang="ts">
  /** Movie detail (DESIGN.md §4: 32px display item title, machine text in mono). */
  import { onMount } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Movie } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import StatusDot from '../components/StatusDot.svelte';
  import Toggle from '../components/Toggle.svelte';
  import { UNKNOWN, formatBytes, formatDate, truncateMiddle } from '../format';
  import { pushToast } from '../state/toast.svelte';
  import { movieStatus } from '../status';

  interface Props {
    id: number;
  }

  let { id }: Props = $props();

  let movie = $state<Movie | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let savingMonitored = $state(false);

  async function load() {
    loading = true;
    try {
      movie = await api.getMovie(id);
      error = null;
    } catch (err) {
      error = errorText(err);
    } finally {
      loading = false;
    }
  }

  onMount(load);

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
</script>

<div class="flex flex-col gap-6">
  <a
    href="/movies"
    class="inline-flex w-fit items-center gap-2 text-base text-ink-secondary transition-colors duration-150 hover:text-ink">
    <Icon name="back" size={14} />
    Movies
  </a>

  {#if error}
    <LoadError message={error} onretry={load} />
  {:else if loading && movie === null}
    <div class="flex gap-6">
      <Skeleton class="aspect-[2/3] w-52 rounded-md" />
      <div class="flex flex-1 flex-col gap-3">
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
                <span>Released {formatDate(movie.release_date)}</span>
              {/if}
            </p>
          </div>
          <div class="flex items-center gap-3">
            <Button variant="primary" size="sm" href="/movies/{movie.id}/search">
              <Icon name="search" size={14} />
              Search
            </Button>
            <Toggle
              checked={movie.monitored}
              label="Monitored"
              disabled={savingMonitored}
              onchange={setMonitored} />
          </div>
        </div>

        <p class="max-w-3xl text-md text-ink-secondary">
          {movie.overview || 'No overview available.'}
        </p>

        <dl class="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <div>
            <dt class="micro-label">Folder</dt>
            <dd class="mt-1 truncate font-mono text-sm text-ink" title={movie.path}>
              {movie.path || UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">TMDB id</dt>
            <dd class="mt-1 font-mono text-sm text-ink">
              {movie.tmdb_id > 0 ? movie.tmdb_id : UNKNOWN}
            </dd>
          </div>
          <div>
            <dt class="micro-label">Added</dt>
            <dd class="mt-1 text-sm text-ink">{formatDate(movie.added_at)}</dd>
          </div>
        </dl>
      </div>
    </div>

    <section class="flex flex-col gap-3">
      <h3 class="text-lg font-semibold text-ink">File</h3>

      {#if !file}
        <EmptyState
          icon="folder"
          title="No file imported"
          message="Caravan has no media file for this movie yet. Run a library scan after copying the file into the storage root.">
          {#snippet action()}
            <Button variant="secondary" href="/scan-review">Open scan review</Button>
          {/snippet}
        </EmptyState>
      {:else}
        <div class="overflow-x-auto rounded-md border border-border">
          <table class="w-full min-w-[640px] border-collapse text-sm">
            <thead>
              <tr class="bg-surface text-left">
                <th class="micro-label px-3 py-2 font-semibold">Path</th>
                <th class="micro-label px-3 py-2 font-semibold">Quality</th>
                <th class="micro-label px-3 py-2 font-semibold">Source</th>
                <th class="micro-label px-3 py-2 font-semibold">Codec</th>
                <th class="micro-label px-3 py-2 font-semibold">Audio</th>
                <th class="micro-label px-3 py-2 text-right font-semibold">Size</th>
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
                <td class="px-3 py-2 text-right font-mono text-ink-secondary">
                  {formatBytes(file.size)}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      {/if}
    </section>
  {/if}
</div>
