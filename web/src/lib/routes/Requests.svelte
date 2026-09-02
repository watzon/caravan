<script lang="ts">
  /**
   * Explore → Requests, which is two screens sharing one list (SPEC §11).
   *
   * An admin sees a decision screen split into awaiting approval and approved
   * history. Approve reopens the shared add/request modal in add mode prefilled
   * with the requested seasons, and submits through POST /requests/{id}/approve.
   * The add is what marks the row approved, so there is no second write to get
   * out of step with. Dismiss answers no; the row survives as history, and the
   * title can be requested again later.
   *
   * A scene row is the exception on both counts, and the reason the row helpers
   * live in lib/requests.ts rather than inline here: it has no tmdb id and so
   * nowhere to link, and approving it takes the POST directly because the modal
   * has nothing to ask about it (see `approveScene`). The server strips scene
   * rows from the list of anybody the adult module is invisible to, so there is
   * no visibility branch in this file.
   *
   * A member sees only their own rows, in every status, and may cancel one
   * while it is still pending. The server enforces both halves and the list it
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
  import PageTabs from '../components/PageTabs.svelte';
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
  import { requestDecided } from '../state/activity';
  import { requests } from '../state/requests.svelte';
  import { useI18n } from '../i18n.svelte';

  const { t } = useI18n();

  type Tab = 'pending' | 'approved';

  const TABS: { key: Tab; label: string }[] = [
    { key: 'pending', label: t('route.requests.pendingTab') },
    { key: 'approved', label: t('route.requests.approvedTab') },
  ];

  let approving = $state<MediaRequest | null>(null);
  let dismissing = $state<number | null>(null);
  /**
   * Scene rows whose approve call is in flight. A single id would drop the
   * "Approving" state from the first row the moment a second was clicked.
   */
  let approvingScenes = $state<number[]>([]);
  let tab = $state<Tab>('pending');

  function isApprovingScene(id: number): boolean {
    return approvingScenes.includes(id);
  }

  // One snapshot on open. After that the live stream (admin) or a
  // local write (member) updates the shared store — no interval.
  $effect(() => {
    void requests.refresh();
  });

  let isAdmin = $derived(session.isAdmin);
  let pending = $derived(pendingRequests(requests.items));
  let approved = $derived((requests.items ?? []).filter((request) => request.status === 'approved'));
  let tabs = $derived(
    TABS.map((item) => ({
      ...item,
      count: requests.items === null ? null : item.key === 'pending' ? pending.length : approved.length,
    })),
  );

  /**
   * An admin's screen separates the decision queue from decisions already
   * made. A member's is the record of what they asked for, so it holds
   * everything the server gave them — which is already only their own rows.
   */
  let rows = $derived(
    isAdmin ? (tab === 'pending' ? pending : approved) : (requests.items ?? []),
  );

  /**
   * Approving a scene is a direct POST, not the add modal.
   *
   * The modal is TMDB-shaped all the way down — it fetches seasons for a tmdb
   * id and offers a quality profile and a root folder — and a scene has no tmdb
   * id and no seasons. The server resolves its site through the provider, so
   * there is nothing for the approver to choose. Unlike the modal path, this
   * approval cannot partially grant a scene request; drop it immediately while
   * the refresh verifies the server's final list.
   */
  async function approveScene(request: MediaRequest) {
    if (isApprovingScene(request.id)) return;
    approvingScenes = [...approvingScenes, request.id];
    try {
      const result = await api.approveRequest(request.id, true);
      requestDecided(request.id, 'approved', { expectDownload: result.search_queued });
      pushToast(
        result.search_queued
          ? t('route.requests.approvedQueued', { title: request.title })
          : t('route.requests.approved', { title: request.title }),
        'success',
      );
    } catch (err) {
      pushToast(metadataToast(err, isAdmin) ?? errorText(err), 'danger');
    } finally {
      approvingScenes = approvingScenes.filter((id) => id !== request.id);
      void requests.refresh();
    }
  }

  async function dismiss(request: MediaRequest) {
    dismissing = request.id;
    try {
      await api.dismissRequest(request.id);
      requestDecided(request.id, 'dismissed');
      if (isAdmin) requests.forget(request.id);
      pushToast(
        isAdmin
          ? t('route.requests.dismissed', { title: request.title })
          : t('route.requests.cancelled', { title: request.title }),
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
  {#if isAdmin}
    <PageTabs
      {tabs}
      active={tab}
      onchange={(key) => (tab = key)}
      ariaLabel={t('route.requests.filter')} />
  {/if}

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
      title={isAdmin
        ? tab === 'pending'
          ? t('route.requests.emptyPendingTitle')
          : t('route.requests.emptyApprovedTitle')
        : t('route.requests.emptyTitle')}
      message={isAdmin
        ? tab === 'pending'
          ? t('route.requests.emptyPendingMessage')
          : t('route.requests.emptyApprovedMessage')
        : t('route.requests.emptyMessage')}>
      {#snippet action()}
        <Button variant="primary" href="/discover">{t('route.requests.openDiscover')}</Button>
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
              <a href={href} class="block truncate text-base font-medium text-ink" title={label}>{label}</a>
            {:else}
              <p class="truncate text-base font-medium text-ink" title={label}>{label}</p>
            {/if}
            <p class="flex flex-wrap items-center gap-2 text-sm text-ink-secondary">
              <Badge mono tone="neutral">{requestMediaChip(request.media_type)}</Badge>
              <!-- Only on a member's list: the admin tabs carry decisions at
                   either stage, so a status badge would repeat the tab. -->
              {#if !isAdmin}
                <Badge tone={statusChip.tone}>{statusChip.label}</Badge>
              {/if}
              <span>{requestSeasonsLabel(request)}</span>
              {#if request.media_type === 'movie' && request.min_availability}
                <span class="text-ink-muted">·</span>
                <span>{t('route.requests.wants', { availability: availabilityLabel(request.min_availability) })}</span>
              {/if}
              <span class="text-ink-muted">·</span>
              <span>{t('route.requests.requested', { date: formatDate(request.created_at) })}</span>
              <!-- Empty for a row that predates accounts, one made while the
                   server ran open, or an asker since deleted. All three mean
                   the same thing to whoever is reading: nobody left to ask. -->
              {#if isAdmin && request.requested_by_username}
                <span class="text-ink-muted">·</span>
                <span>{t('route.requests.by', { username: request.requested_by_username })}</span>
              {/if}
            </p>
          </div>

          <div class="flex shrink-0 items-center gap-2">
            {#if isAdmin && tab === 'pending'}
              {#if request.media_type === 'scene'}
                <Button
                  variant="primary"
                  size="sm"
                  disabled={dismissing === request.id || isApprovingScene(request.id)}
                  onclick={() => void approveScene(request)}>
                  {isApprovingScene(request.id) ? t('route.requests.approving') : t('route.requests.approve')}
                </Button>
              {:else}
                <Button
                  variant="primary"
                  size="sm"
                  disabled={dismissing === request.id}
                  onclick={() => (approving = request)}>
                  {t('route.requests.approve')}
                </Button>
              {/if}
              <Button
                variant="secondary"
                size="sm"
                disabled={dismissing === request.id}
                onclick={() => void dismiss(request)}>
                {dismissing === request.id ? t('route.requests.dismissing') : t('route.requests.dismiss')}
              </Button>
            {:else if request.status === 'pending'}
              <Button
                variant="secondary"
                size="sm"
                disabled={dismissing === request.id}
                onclick={() => void dismiss(request)}>
                {dismissing === request.id ? t('route.requests.cancelling') : t('route.requests.cancel')}
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
      if (approving) requestDecided(approving.id, 'approved');
      void requests.refresh();
    }} />
{/if}
