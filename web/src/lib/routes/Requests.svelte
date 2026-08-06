<script lang="ts">
  /**
   * Explore → Requests, which is two screens sharing one list (SPEC §11).
   *
   * An admin sees everyone's pending wishes and the two things that can happen
   * to one. Approve reopens the shared add/request modal in add mode prefilled
   * with the requested seasons, and submits through POST /requests/{id}/approve
   * — the add is what marks the row approved, so there is no second write to
   * get out of step with. Dismiss answers no; the row survives as history, and
   * the title can be requested again later.
   *
   * A scene row is the one exception on both counts, and the reason the row
   * helpers live in lib/requests.ts rather than inline here: it has no tmdb id
   * and so nowhere to link, and approving it takes the POST directly because
   * the modal has nothing to ask about it (see `approveScene`). Scene rows only
   * ever reach a caller the adult module is visible to — the server strips them
   * from everybody else's list — so there is no visibility branch in this file.
   *
   * A member sees only their own rows, and every status of them, so they can
   * watch a wish go from pending to approved. The only thing they may do to one
   * is cancel it while it is still pending — the same DELETE, under the name it
   * has when the row is yours. The server enforces both halves and the list it
   * hands back is already scoped, so nothing here filters by owner.
   */
  import { api, errorText } from '../api/client';
  import { metadataToast } from '../credentials';
  import type { MediaRequest } from '../api/types';
  import AddRequestModal from '../components/AddRequestModal.svelte';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Poster from '../components/Poster.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { availabilityLabel } from '../discover';
  import { formatDate, titleWithYear } from '../format';
  import {
    pendingRequests,
    requestFallbackIcon,
    requestHref,
    requestMediaChip,
    requestSeasonsLabel,
    requestStatusChip,
  } from '../requests';
  import { session } from '../state/session.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { REQUESTS_POLL_MS, requests } from '../state/requests.svelte';

  let approving = $state<MediaRequest | null>(null);
  let dismissing = $state<number | null>(null);
  /** The scene row whose approve call is in flight. */
  let approvingScene = $state<number | null>(null);

  $effect(() => requests.subscribe(REQUESTS_POLL_MS));

  let isAdmin = $derived(session.isAdmin);

  /**
   * An admin's list is a decision queue, so it holds only what is undecided. A
   * member's is the record of what they asked for, so it holds everything the
   * server gave them — which is already only their own rows.
   */
  let rows = $derived(isAdmin ? pendingRequests(requests.items) : (requests.items ?? []));

  /**
   * Approving a scene is a direct POST, not the add modal.
   *
   * The modal is TMDB-shaped all the way down — it fetches seasons for a tmdb
   * id and offers a quality profile and a root folder — and a scene has no tmdb
   * id and no seasons. The server wants nothing from the approver either: it
   * resolves the scene's SITE through the provider and adds that, so the only
   * thing to send is the approval. That also means the row does not simply
   * vanish on success — the site arrives holding the whole catalogue — so the
   * list is refetched rather than patched.
   */
  async function approveScene(request: MediaRequest) {
    approvingScene = request.id;
    try {
      await api.approveRequest(request.id, false);
      pushToast(`Approved ${request.title}`, 'success');
    } catch (err) {
      pushToast(metadataToast(err, isAdmin) ?? errorText(err), 'danger');
    } finally {
      approvingScene = null;
      void requests.refresh();
    }
  }

  async function dismiss(request: MediaRequest) {
    dismissing = request.id;
    try {
      await api.dismissRequest(request.id);
      requests.forget(request.id);
      pushToast(
        isAdmin ? `Dismissed ${request.title}` : `Cancelled ${request.title}`,
        'neutral',
      );
      // A member's list keeps every status, so the row belongs back on screen
      // as dismissed. forget() above is only what stops the click feeling slow.
      if (!isAdmin) void requests.refresh();
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
      title={isAdmin ? 'No pending requests' : 'No requests yet'}
      message={isAdmin
        ? 'Nothing is waiting on a decision. Requests made from Discover show up here until they are approved or dismissed.'
        : 'Anything you ask for from Discover shows up here, and stays until it is approved or turned down.'}>
      {#snippet action()}
        <Button variant="primary" href="/discover">Open Discover</Button>
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as request (request.id)}
        {@const statusChip = requestStatusChip(request.status)}
        {@const href = requestHref(request)}
        {@const label = titleWithYear(request.title, request.year)}
        <li class="flex items-center gap-3 rounded-md border border-border bg-surface p-3">
          <!-- A scene has nowhere to link (requestHref), so its poster and
               title are plain rather than an anchor to a route that would 404. -->
          {#if href}
            <a href={href} class="w-10 shrink-0" aria-label={label}>
              <Poster
                path={request.poster_url}
                alt=""
                fallbackIcon={requestFallbackIcon(request.media_type)} />
            </a>
          {:else}
            <div class="w-10 shrink-0">
              <Poster
                path={request.poster_url}
                alt=""
                fallbackIcon={requestFallbackIcon(request.media_type)} />
            </div>
          {/if}

          <div class="min-w-0 flex-1">
            {#if href}
              <a href={href} class="block truncate text-base font-medium text-ink">{label}</a>
            {:else}
              <p class="truncate text-base font-medium text-ink" title={label}>{label}</p>
            {/if}
            <p class="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
              <Badge mono tone="neutral">{requestMediaChip(request.media_type)}</Badge>
              <!-- Only on a member's list: an admin's holds pending rows only,
                   so the badge would say the same word on every one of them. -->
              {#if !isAdmin}
                <Badge tone={statusChip.tone}>{statusChip.label}</Badge>
              {/if}
              <span>{requestSeasonsLabel(request)}</span>
              {#if request.media_type === 'movie' && request.min_availability}
                <span class="text-ink-muted">·</span>
                <span>Wants: {availabilityLabel(request.min_availability)}</span>
              {/if}
              <span class="text-ink-muted">·</span>
              <span>Requested {formatDate(request.created_at)}</span>
              <!-- Empty for a row that predates accounts, one made while the
                   server ran open, or an asker since deleted. All three mean
                   the same thing to whoever is reading: nobody left to ask. -->
              {#if isAdmin && request.requested_by_username}
                <span class="text-ink-muted">·</span>
                <span>by {request.requested_by_username}</span>
              {/if}
            </p>
          </div>

          <div class="flex shrink-0 items-center gap-2">
            {#if isAdmin}
              {#if request.media_type === 'scene'}
                <Button
                  variant="primary"
                  size="sm"
                  disabled={dismissing === request.id || approvingScene === request.id}
                  onclick={() => void approveScene(request)}>
                  {approvingScene === request.id ? 'Approving…' : 'Approve'}
                </Button>
              {:else}
                <Button
                  variant="primary"
                  size="sm"
                  disabled={dismissing === request.id}
                  onclick={() => (approving = request)}>
                  Approve
                </Button>
              {/if}
              <Button
                variant="secondary"
                size="sm"
                disabled={dismissing === request.id}
                onclick={() => void dismiss(request)}>
                {dismissing === request.id ? 'Dismissing…' : 'Dismiss'}
              </Button>
            {:else if request.status === 'pending'}
              <Button
                variant="secondary"
                size="sm"
                disabled={dismissing === request.id}
                onclick={() => void dismiss(request)}>
                {dismissing === request.id ? 'Cancelling…' : 'Cancel'}
              </Button>
            {/if}
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<!-- The `!== 'scene'` is both a guard and the narrowing the modal's `mediaType`
     needs: its prop is `MediaType`, which is `RequestMediaType` minus the kind
     it cannot render. Scenes take the direct approve path above, so this only
     ever excludes a row that should never have got here. -->
{#if approving && approving.media_type !== 'scene'}
  <AddRequestModal
    mode="add"
    mediaType={approving.media_type}
    tmdbID={approving.tmdb_id}
    title={approving.title}
    year={approving.year}
    posterPath={approving.poster_path}
    preselect={approving.seasons}
    requestID={approving.id}
    initialMonitored={approving.monitored}
    initialAvailability={approving.min_availability}
    onclose={() => (approving = null)}
    ondone={() => {
      // Refetch rather than drop the row: an approval that granted fewer
      // seasons than were asked for leaves it pending for the remainder
      // (internal/api/library.go), and it has to keep showing that.
      void requests.refresh();
    }} />
{/if}
