<script lang="ts">
  /**
   * Explore → Requests: the pending wishes, and the two things that can happen
   * to one.
   *
   * Approve reopens the shared add/request modal in add mode prefilled with the
   * requested seasons, and submits through POST /requests/{id}/approve — the
   * add is what marks the row approved, so there is no second write to get out
   * of step with. Dismiss answers no; the row survives as history, and the
   * title can be requested again later.
   */
  import { api, errorText } from '../api/client';
  import type { MediaRequest } from '../api/types';
  import AddRequestModal from '../components/AddRequestModal.svelte';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { availabilityLabel, discoverHref } from '../discover';
  import { formatDate, titleWithYear } from '../format';
  import { pendingRequests, requestSeasonsLabel } from '../requests';
  import { pushToast } from '../state/toast.svelte';
  import { REQUESTS_POLL_MS, requests } from '../state/requests.svelte';

  let approving = $state<MediaRequest | null>(null);
  let dismissing = $state<number | null>(null);

  $effect(() => requests.subscribe(REQUESTS_POLL_MS));

  let rows = $derived(pendingRequests(requests.items));

  async function dismiss(request: MediaRequest) {
    dismissing = request.id;
    try {
      await api.dismissRequest(request.id);
      requests.forget(request.id);
      pushToast(`Dismissed ${request.title}`, 'neutral');
    } catch (err) {
      // 409 means it stopped being pending while this screen was open: the
      // list is stale, so refetch rather than guess.
      pushToast(errorText(err), 'danger');
      void requests.refresh();
    } finally {
      dismissing = null;
    }
  }
</script>

<div class="flex flex-col gap-6">
  {#if requests.error && requests.items === null}
    <LoadError message={requests.error} onretry={() => void requests.refresh()} />
  {:else if requests.loading && requests.items === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 3 }) as _, i (i)}
        <Skeleton class="h-16 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="inbox"
      title="No pending requests"
      message="Nothing is waiting on a decision. Requests made from Discover show up here until they are approved or dismissed.">
      {#snippet action()}
        <Button variant="primary" href="/discover">Open Discover</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as request (request.id)}
        <li class="flex items-center gap-3 rounded-md border border-border bg-surface p-3">
          <a
            href={discoverHref(request)}
            class="w-10 shrink-0"
            aria-label={titleWithYear(request.title, request.year)}>
            <Poster
              path={request.poster_url}
              alt=""
              fallbackIcon={request.media_type === 'movie' ? 'film' : 'tv'} />
          </a>

          <div class="min-w-0 flex-1">
            <a href={discoverHref(request)} class="block truncate text-base font-medium text-ink">
              {titleWithYear(request.title, request.year)}
            </a>
            <p class="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
              <Badge mono tone="neutral">
                {request.media_type === 'movie' ? 'MOVIE' : 'SERIES'}
              </Badge>
              <span>{requestSeasonsLabel(request)}</span>
              {#if request.media_type === 'movie' && request.min_availability}
                <span class="text-ink-muted">·</span>
                <span>Wants: {availabilityLabel(request.min_availability)}</span>
              {/if}
              <span class="text-ink-muted">·</span>
              <span>Requested {formatDate(request.created_at)}</span>
            </p>
          </div>

          <div class="flex shrink-0 items-center gap-2">
            <Button
              variant="primary"
              size="sm"
              disabled={dismissing === request.id}
              onclick={() => (approving = request)}>
              Approve
            </Button>
            <Button
              variant="secondary"
              size="sm"
              disabled={dismissing === request.id}
              onclick={() => void dismiss(request)}>
              {dismissing === request.id ? 'Dismissing…' : 'Dismiss'}
            </Button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>

{#if approving}
  <AddRequestModal
    mode="add"
    mediaType={approving.media_type}
    tmdbID={approving.tmdb_id}
    title={approving.title}
    year={approving.year}
    posterPath={approving.poster_path}
    preselect={approving.seasons}
    requestID={approving.id}
    initialAvailability={approving.min_availability}
    onclose={() => (approving = null)}
    ondone={() => {
      // Refetch rather than drop the row: an approval that granted fewer
      // seasons than were asked for leaves it pending for the remainder
      // (internal/api/library.go), and it has to keep showing that.
      void requests.refresh();
    }} />
{/if}
