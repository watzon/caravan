<script lang="ts">
  /**
   * Download queue (SPEC §5.1 "Download Manager", §11 `/downloads`).
   *
   * Polled while the screen is on top: the engine is the source of truth for
   * progress, and there is no push channel until a later phase. Removing a
   * download asks separately about the data, because "remove" and "delete my
   * media" must never be the same click (SPEC §13).
   */
  import { api, errorText } from '../api/client';
  import type { DownloadStatus } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Modal from '../components/Modal.svelte';
  import ProgressBar from '../components/ProgressBar.svelte';
  import QueueDetailDrawer from '../components/QueueDetailDrawer.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import {
    UNKNOWN,
    formatBytes,
    formatDuration,
    formatRate,
    truncateMiddle,
  } from '../format';
  import {
    canRetryDownload,
    downloadPhaseLabel,
    downloadStateMeta,
    engineLabel,
    isActiveDownload,
    isFinishedDownload,
    isSeedingDownload,
    sortDownloads,
  } from '../download';
  import { QUEUE_POLL_MS, downloads } from '../state/downloads.svelte';
  import { page } from '../state/page.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { TONE_DOT } from '../status';
  import { downloadItemHref } from '../library';
  import { useI18n } from '../i18n.svelte';

  const { t, tp } = useI18n();

  let busyID = $state<string | null>(null);
  let removing = $state<DownloadStatus | null>(null);
  let removeData = $state(false);
  /**
   * The open drawer is tracked by id, never by the row object: the store
   * replaces every row on each poll, so holding the object froze the drawer at
   * whatever progress, speed and phase it had when it was clicked while the
   * list under it kept moving.
   */
  let detailID = $state<string | null>(null);
  let detail = $derived(detailID === null ? null : (downloads.items ?? []).find((d) => d.id === detailID) ?? null);

  // A download that leaves the queue — removed, or gone from the engine — takes
  // its drawer with it rather than leaving a stale one that would reopen if the
  // id ever came back.
  $effect(() => {
    if (detailID !== null && downloads.items !== null && detail === null) detailID = null;
  });

  function openRemoveFromDetail(deleteData: boolean) {
    if (!detail) return;
    removing = detail;
    removeData = deleteData;
  }

  // Polling is scoped to this screen's lifetime - the store stops when the last
  // subscriber leaves, so navigating away costs nothing.
  $effect(() => downloads.subscribe(QUEUE_POLL_MS));

  async function act(
    download: DownloadStatus,
    action: () => Promise<unknown>,
    note: string,
  ) {
    busyID = download.id;
    try {
      await action();
      await downloads.refresh();
      pushToast(note, 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  function openRemove(download: DownloadStatus) {
    removing = download;
    removeData = false;
  }

  async function confirmRemove() {
    const target = removing;
    if (!target) return;
    const deleteData = removeData;
    busyID = target.id;
    try {
      await api.removeDownload(target.id, deleteData);
      downloads.forget(target.id);
      removing = null;
      detailID = null;
      pushToast(
        deleteData ? t('route.queue.removedAndDeleted') : t('route.queue.removedKeptData'),
        'neutral',
      );
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  /**
   * The default view is the acquiring queue: queued, downloading, failed, and
   * a mid-download pause. Seeding is a later stage — the file is already
   * here — so it has its own tab. Done hides completed imports and torrents
   * that finished and sit paused.
   */
  type QueueView = 'active' | 'seeding' | 'done' | 'all';
  let view = $state<QueueView>('active');

  let all = $derived(sortDownloads(downloads.items ?? []));
  let seedingRows = $derived(all.filter(isSeedingDownload));
  let doneRows = $derived(all.filter(isFinishedDownload));
  let activeRows = $derived(all.filter((d) => !isFinishedDownload(d) && !isSeedingDownload(d)));
  let rows = $derived(
    view === 'all' ? all : view === 'done' ? doneRows : view === 'seeding' ? seedingRows : activeRows,
  );

  let views = $derived<{ key: QueueView; label: string; count: number }[]>([
    { key: 'active', label: t('route.queue.active'), count: activeRows.length },
    { key: 'seeding', label: t('route.queue.seeding'), count: seedingRows.length },
    { key: 'done', label: t('route.queue.done'), count: doneRows.length },
    { key: 'all', label: t('route.queue.all'), count: all.length },
  ]);

  // The shared TopBar renders the page's subtitle: what the queue is doing,
  // in the same vocabulary the Paper queue header uses.
  $effect(() => {
    const items = downloads.items;
    if (!items) {
      page.subtitle = null;
      return;
    }
    const downloading = items.filter(isActiveDownload).length;
    const seeding = items.filter(isSeedingDownload).length;
    const failed = items.filter((item) => item.state === 'failed').length;
    const parts: string[] = [];
    if (downloading > 0) parts.push(t('route.queue.downloadingSubtitle', { count: downloading }));
    if (seeding > 0) parts.push(t('route.queue.seedingSubtitle', { count: seeding }));
    page.subtitle = t('route.queue.subtitle', {
      summary: parts.length > 0 ? parts.join(', ') : t('route.queue.idleSubtitle'),
      failed: failed ? t('route.queue.failedSubtitle', { count: failed }) : '',
    });
    return () => (page.subtitle = null);
  });
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <div class="flex flex-wrap items-center gap-2" role="group" aria-label={t('route.queue.filter')}>
      {#each views as tab (tab.key)}
        {@const selected = view === tab.key}
        <button
          type="button"
          aria-pressed={selected}
          onclick={() => (view = tab.key)}
          class="inline-flex h-7 items-center gap-2 rounded-full border px-3 text-sm transition-colors duration-150 ease-out
                 {selected
            ? 'border-accent bg-accent-tint text-accent-text'
            : 'border-border bg-surface text-ink-secondary hover:bg-raised hover:text-ink'}">
          <span>{tab.label}</span>
          <span class="font-mono text-xs text-ink-muted">{tab.count}</span>
        </button>
      {/each}
    </div>
    <div class="ml-auto flex items-center gap-2">
      <Button variant="secondary" onclick={() => downloads.refresh()}>
        <Icon name="refresh" size={14} />
        {t('route.queue.refresh')}
      </Button>
    </div>
  </div>

  {#if downloads.error && downloads.items === null}
    <LoadError message={downloads.error} onretry={() => downloads.refresh()} />
  {:else if downloads.loading && downloads.items === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 3 }) as _, i (i)}
        <Skeleton class="h-20 w-full rounded-md" />
      {/each}
    </div>
  {:else if all.length === 0}
    <EmptyState
      icon="download"
      title={t('route.queue.emptyTitle')}
      message={t('route.queue.emptyMessage')} />
  {:else if rows.length === 0 && view === 'seeding'}
    <EmptyState
      icon="download"
      title={t('route.queue.emptySeedingTitle')}
      message={t('route.queue.emptySeedingMessage')}>
      {#snippet action()}
        <Button variant="secondary" onclick={() => (view = 'all')}>{t('route.queue.showAll')}</Button>
      {/snippet}
    </EmptyState>
  {:else if rows.length === 0 && view === 'done'}
    <EmptyState
      icon="download"
      title={t('route.queue.emptyDoneTitle')}
      message={t('route.queue.emptyDoneMessage')}>
      {#snippet action()}
        <Button variant="secondary" onclick={() => (view = 'all')}>{t('route.queue.showAll')}</Button>
      {/snippet}
    </EmptyState>
  {:else if rows.length === 0}
    <EmptyState
      icon="download"
      title={t('route.queue.emptyActiveTitle')}
      message={seedingRows.length > 0
        ? tp('route.queue.emptyActiveSeedingMessage', seedingRows.length)
        : tp('route.queue.emptyActiveMessage', doneRows.length)}>
      {#snippet action()}
        {#if seedingRows.length > 0}
          <Button variant="secondary" onclick={() => (view = 'seeding')}>{t('route.queue.showSeeding')}</Button>
        {:else}
          <Button variant="secondary" onclick={() => (view = 'all')}>{t('route.queue.showAll')}</Button>
        {/if}
      {/snippet}
    </EmptyState>
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as download (download.id)}
        {@const meta = downloadStateMeta(download.state)}
        {@const paused = download.state === 'paused'}
        {@const phaseLabel = downloadPhaseLabel(download)}
        {@const finished = isFinishedDownload(download)}
        {@const itemHref = downloadItemHref(download)}
        {@const seedingContext = download.state === 'seeding' || (paused && download.progress >= 1)}
        {@const pauseLabel = paused
          ? seedingContext
            ? t('route.queue.resumeSeeding')
            : t('route.queue.resumeDownload')
          : seedingContext
            ? t('route.queue.pauseSeeding')
            : t('route.queue.pauseDownload')}
        <li class="relative flex flex-col gap-3 rounded-md border border-border bg-surface px-3 py-3 transition-colors duration-150 hover:border-border-strong">
          <button
            type="button"
            class="absolute inset-0 rounded-md focus:outline-none focus:ring-2 focus:ring-accent"
            aria-label={t('route.queue.openDetails', { name: download.name })}
            onclick={() => (detailID = download.id)}></button>
          <div class="relative z-10 flex pointer-events-none flex-wrap items-center gap-3">
            <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}" aria-hidden="true"></span>
            {#if itemHref}
              <a
                href={itemHref}
                class="pointer-events-auto min-w-0 flex-1 font-mono text-ink hover:text-accent-text hover:underline"
                title={download.name}>
                {truncateMiddle(download.name || UNKNOWN, 64)}
              </a>
            {:else}
              <span class="min-w-0 flex-1 font-mono text-ink" title={download.name}>
                {truncateMiddle(download.name || UNKNOWN, 64)}
              </span>
            {/if}
            <Badge tone={meta.tone}>{meta.label}</Badge>
            {#if phaseLabel}
              <Badge tone="info" title={t('route.queue.downloadStage')}>
                {phaseLabel}
              </Badge>
            {/if}
            <Badge mono tone="neutral" title={t('route.queue.downloadBackend')}>
              {engineLabel(download)}
            </Badge>

            <div class="pointer-events-auto flex shrink-0 items-center gap-2">
              {#if meta.active || paused}
                <Button
                  variant="ghost"
                  size="sm"
                  title={pauseLabel}
                  disabled={busyID === download.id}
                  onclick={(event) => {
                    event.stopPropagation();
                    void act(
                      download,
                      () =>
                        paused
                          ? api.resumeDownload(download.id)
                          : api.pauseDownload(download.id),
                      paused ? t('route.queue.resumed') : t('route.queue.paused'),
                    );
                  }}>
                  <Icon name={paused ? 'play' : 'pause'} size={14} />
                  <span class="sr-only">{pauseLabel}</span>
                </Button>
              {/if}
              {#if canRetryDownload(download)}
                <Button
                  variant="secondary"
                  size="sm"
                  title={t('route.queue.retryFailedStage')}
                  disabled={busyID === download.id}
                  onclick={(event) => {
                    event.stopPropagation();
                    void act(download, () => api.retryDownload(download.id), t('route.queue.retrying'));
                  }}>
                  <Icon name="refresh" size={14} />
                  <span class="sr-only">{t('route.queue.retry', { name: download.name })}</span>
                </Button>
              {/if}
              <Button
                variant="ghost"
                size="sm"
                disabled={busyID === download.id}
                onclick={(event) => {
                  event.stopPropagation();
                  openRemove(download);
                }}>
                <Icon name="trash" size={14} />
                <span class="sr-only">{t('route.queue.remove', { name: download.name })}</span>
              </Button>
            </div>
          </div>

          <ProgressBar
            value={download.progress}
            tone={meta.tone}
            label={t('route.queue.progress', { name: download.name })} />

          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-xs text-ink-secondary">
            <span>
              {formatBytes(download.bytes_done)} / {download.size > 0
                ? formatBytes(download.size)
                : UNKNOWN}
            </span>
            <span>{Math.round(Math.max(0, Math.min(1, download.progress)) * 100)}%</span>
            {#if finished}
              <span title={t('route.queue.transferFinished')}>{t('route.queue.complete')}</span>
            {:else if download.state === 'seeding'}
              <span title={t('route.queue.uploadRate')}>↑ {formatRate(download.up_rate)}</span>
              <span title={t('route.queue.shareRatio')}>
                {t('route.queue.ratio', { ratio: download.ratio.toFixed(2) })}
              </span>
            {:else}
              <span title={t('route.queue.downloadRate')}>↓ {formatRate(download.down_rate)}</span>
              <span title={t('route.queue.uploadRate')}>↑ {formatRate(download.up_rate)}</span>
              <span title={t('route.queue.timeRemaining')}>
                {t('route.queue.eta', { time: formatDuration(download.eta_seconds) })}
              </span>
              {#if download.ratio > 0}
                <span title={t('route.queue.shareRatio')}>
                  {t('route.queue.ratio', { ratio: download.ratio.toFixed(2) })}
                </span>
              {/if}
            {/if}
          </div>

          {#if download.error}
            <p class="text-sm text-danger">{download.error}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>

{#if detail}
  {@const open = detail}
  <QueueDetailDrawer
    download={open}
    busy={busyID === open.id}
    onclose={() => (detailID = null)}
    onpause={() => void act(open, () => api.pauseDownload(open.id), t('route.queue.paused'))}
    onresume={() => void act(open, () => api.resumeDownload(open.id), t('route.queue.resumed'))}
    onretry={() => void act(open, () => api.retryDownload(open.id), t('route.queue.retrying'))}
    onremove={openRemoveFromDetail}
    onlimitsapplied={() => downloads.refresh()} />
{/if}

{#if removing}
  {@const target = removing}
  <Modal title={t('route.queue.removeTitle')} width="max-w-lg" onclose={() => (removing = null)}>
    <div class="flex flex-col gap-4 p-4">
      <p class="font-mono text-sm text-ink" title={target.name || UNKNOWN}>{truncateMiddle(target.name || UNKNOWN, 72)}</p>
      <p class="text-base text-ink-secondary">{t('route.queue.removeWarning')}</p>

      <label class="flex items-center gap-3 rounded-md border border-border bg-raised px-3 py-2">
        <input
          type="checkbox"
          bind:checked={removeData}
          class="size-4 accent-danger" />
        <span class="text-base text-ink">{t('route.queue.deleteData')}</span>
      </label>
    </div>

    {#snippet footer()}
      <Button variant="ghost" onclick={() => (removing = null)}>{t('route.queue.cancel')}</Button>
      <Button
        variant={removeData ? 'danger' : 'primary'}
        disabled={busyID === target.id}
        onclick={confirmRemove}>
        {removeData ? t('route.queue.removeAndDelete') : t('route.queue.removeButton')}
      </Button>
    {/snippet}
  </Modal>
{/if}
