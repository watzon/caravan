<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * Detail drawer for one active download.
   *
   * It is protocol-specific, because the two protocols have almost nothing in
   * common past a progress bar: a torrent has peers, trackers, a share ratio,
   * piece availability and an upload limit worth setting, and a Usenet download
   * has none of those. It has files being assembled out of segments, and stages
   * — repair, unpack — that a torrent never enters. Showing a Usenet download a
   * Limits tab the embedded engine answers 400 for is the bug this split fixes.
   */
  import { downloadItemHref } from '../library';
  import { api, ApiError, errorText } from '../api/client';
  import type { DownloadInsight, DownloadStatus } from '../api/types';
  import { UNKNOWN, formatBytes, formatDuration, formatRate, truncateMiddle } from '../format';
  import { canRetryDownload, downloadPhaseLabel, downloadStateMeta, engineLabel } from '../download';
  import { QUEUE_POLL_MS } from '../state/downloads.svelte';
  import { pushToast } from '../state/toast.svelte';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import ProgressBar from './ProgressBar.svelte';

  type Tab = 'peers' | 'trackers' | 'limits' | 'files';

  interface Props {
    download: DownloadStatus;
    busy?: boolean;
    onclose: () => void;
    onpause: () => void;
    onresume: () => void;
    /** Offered only for a failed download an engine can retry. */
    onretry?: () => void;
    onremove: (deleteData: boolean) => void;
    onlimitsapplied?: () => Promise<void> | void;
  }

  let {
    download,
    busy = false,
    onclose,
    onpause,
    onresume,
    onretry,
    onremove,
    onlimitsapplied,
  }: Props = $props();

  const drawerID = $props.id();
  const titleID = `${drawerID}-title`;
  const peersTabID = `${drawerID}-peers-tab`;
  const peersPanelID = `${drawerID}-peers-panel`;
  const trackersTabID = `${drawerID}-trackers-tab`;
  const trackersPanelID = `${drawerID}-trackers-panel`;
  const limitsTabID = `${drawerID}-limits-tab`;
  const limitsPanelID = `${drawerID}-limits-panel`;
  const filesTabID = `${drawerID}-files-tab`;
  const filesPanelID = `${drawerID}-files-panel`;
  const downLimitID = `${drawerID}-download-limit`;
  const upLimitID = `${drawerID}-upload-limit`;
  const limitsHelpID = `${drawerID}-limits-help`;
  const FOCUSABLE_SELECTOR = [
    'a[href]',
    'button:not([disabled])',
    'input:not([disabled]):not([type="hidden"])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    '[tabindex]:not([tabindex="-1"])',
  ].join(',');

  let tab = $state<Tab>('peers');
  let insight = $state<DownloadInsight | null>(null);
  let insightError = $state<string | null>(null);
  let insightSupported = $state(true);
  let downKbps = $state('');
  let upKbps = $state('');
  let limitsForDownload = $state<string | null>(null);
  let applying = $state(false);
  let dialog = $state<HTMLElement | null>(null);

  function focusableElements() {
    return Array.from(dialog?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ?? []).filter(
      (element) =>
        element.tabIndex >= 0 &&
        !element.closest('[aria-hidden="true"], [hidden], [inert]') &&
        element.getAttribute('aria-disabled') !== 'true',
    );
  }

  $effect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialog?.focus();
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = previousOverflow;
      previous?.focus?.();
    };
  });



  $effect(() => {
    if (limitsForDownload === download.id) return;
    downKbps = String(Math.round((download.max_down_rate ?? 0) / 1024));
    upKbps = String(Math.round((download.max_up_rate ?? 0) / 1024));
    limitsForDownload = download.id;
  });
  let meta = $derived(downloadStateMeta(download.state));
  let paused = $derived(download.state === 'paused');
  let seeding = $derived(download.state === 'seeding');
  let retryable = $derived(canRetryDownload(download) && onretry !== undefined);
  // A server older than the protocol field only ever ran a torrent engine, so
  // its downloads read as torrents — which is exactly what they were.
  let usenet = $derived(download.protocol === 'usenet');
  let phaseLabel = $derived(downloadPhaseLabel(download));
  let files = $derived(insight?.files ?? []);

  // Each protocol opens on its own first tab. It re-runs only when the protocol
  // changes, so a user who has switched to Trackers stays there across polls.
  $effect(() => {
    tab = usenet ? 'files' : 'peers';
  });

  function nonNegativeKbps(value: string): number {
    const parsed = Number(value);
    return Number.isFinite(parsed) && parsed > 0 ? Math.round(parsed) : 0;
  }

  async function loadInsight(signal?: AbortSignal) {
    try {
      insight = await api.downloadInsight(download.id, signal);
      insightError = null;
    } catch (err) {
      if (signal?.aborted) return;
      if (err instanceof ApiError && (err.status === 400 || err.status === 501)) {
        insightSupported = false;
        // Fall back to the only other tab the protocol has. A Usenet download
        // has no Limits tab to fall back to — the Files panel says so instead.
        tab = usenet ? 'files' : 'limits';
        return;
      }
      insightError = errorText(err);
    }
  }

  // Torrent insight is peer chatter nobody is looking at unless the Peers tab
  // is open. Usenet insight is the download's own file and repair state, which
  // changes underneath the drawer whichever tab is showing, so it is polled for
  // as long as the drawer is open.
  $effect(() => {
    if (!insightSupported) return;
    if (!usenet && tab !== 'peers') return;
    const controller = new AbortController();
    void loadInsight(controller.signal);
    const interval = window.setInterval(() => void loadInsight(), QUEUE_POLL_MS);
    return () => {
      controller.abort();
      window.clearInterval(interval);
    };
  });

  function onkeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.stopPropagation();
      onclose();
      return;
    }
    if (event.key !== 'Tab') return;
    if (document.activeElement !== dialog && !dialog?.contains(document.activeElement)) return;

    const focusable = focusableElements();
    if (focusable.length === 0) {
      event.preventDefault();
      dialog?.focus();
      return;
    }
    const active = document.activeElement as HTMLElement | null;
    const index = focusable.indexOf(active as HTMLElement);
    const atBoundary = event.shiftKey
      ? index <= 0
      : index === -1 || index === focusable.length - 1;
    if (!atBoundary) return;
    event.preventDefault();
    focusable[event.shiftKey ? focusable.length - 1 : 0]?.focus();
  }

  function ontabkeydown(event: KeyboardEvent) {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    const tabs = [...(dialog?.querySelectorAll<HTMLButtonElement>('[role="tab"]') ?? [])];
    if (tabs.length === 0) return;
    const index = tabs.indexOf(event.currentTarget as HTMLButtonElement);
    let next: number;
    if (event.key === 'Home') {
      next = 0;
    } else if (event.key === 'End') {
      next = tabs.length - 1;
    } else if (event.key === 'ArrowRight') {
      next = (index + 1) % tabs.length;
    } else {
      next = (index - 1 + tabs.length) % tabs.length;
    }
    event.preventDefault();
    tabs[next]?.focus();
    tabs[next]?.click();
  }

  async function applyLimits() {
    applying = true;
    try {
      const down = nonNegativeKbps(downKbps);
      const up = nonNegativeKbps(upKbps);
      await api.setDownloadLimits(download.id, down, up);
      downKbps = String(down);
      upKbps = String(up);
      await onlimitsapplied?.();
      pushToast(t('component.queueDetailDrawer.rateLimitsApplied'), 'success');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      applying = false;
    }
  }

  async function resetToGlobal() {
    downKbps = '0';
    upKbps = '0';
    await applyLimits();
  }

  const { t, tp } = useI18n();

  let itemHref = $derived(downloadItemHref(download));
  let itemLabel = $derived(
    download.movie_id
      ? t('component.queueDetailDrawer.openMovie')
      : download.series_kind === 'adult'
        ? t('component.queueDetailDrawer.openSite')
        : t('component.queueDetailDrawer.openSeries'),
  );
</script>

<svelte:window {onkeydown} />

<div class="fixed inset-0 z-50 flex justify-end" aria-hidden="false">
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-bg/80"
    tabindex="-1"
    aria-hidden="true"
    onclick={onclose}></button>

  <div
    bind:this={dialog}
    class="relative flex h-full w-full max-w-[440px] flex-col border-l border-border-strong bg-surface shadow-2xl"
    role="dialog"
    aria-modal="true"
    aria-labelledby={titleID}
    tabindex="-1">
    <header class="flex items-start gap-3 border-b border-border px-5 py-4">
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <h2
            id={titleID}
            class="min-w-0 truncate font-display text-xl font-semibold tracking-tight text-ink"
            title={download.name || UNKNOWN}>
            {download.name || UNKNOWN}
          </h2>
          <Badge tone={meta.tone}>{meta.label}</Badge>
        </div>
        <p class="mt-1 truncate font-mono text-xs text-ink-muted" title={download.name || UNKNOWN}>
          {download.name || UNKNOWN}
        </p>
        {#if itemHref}
          <a
            href={itemHref}
            class="mt-2 inline-flex text-sm font-medium text-accent-text hover:underline">
            {itemLabel}
          </a>
        {/if}
      </div>
      <button
        type="button"
        class="shrink-0 rounded-sm p-1 text-ink-secondary transition-colors duration-150 hover:bg-raised hover:text-ink"
        aria-label={t('component.queueDetailDrawer.closeDownloadDetails')}
        onclick={onclose}>
        <Icon name="close" />
      </button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto">
      <section class="flex flex-col gap-4 border-b border-border px-5 py-4" aria-label={t('component.queueDetailDrawer.transferStatus')}>
        <div class="flex items-end justify-between gap-4">
          <div>
            <p class="font-mono text-xl font-semibold text-ink">
              {Math.round(Math.max(0, Math.min(1, download.progress)) * 100)}%
            </p>
            <p class="mt-1 text-sm text-ink-secondary">
              {formatBytes(download.bytes_done)} of {download.size > 0 ? formatBytes(download.size) : UNKNOWN}
            </p>
          </div>
          {#if seeding}
            <p class="font-mono text-sm text-ink-secondary" title={t('component.queueDetailDrawer.ratio')}>
              {t('component.queueDetailDrawer.ratio')} {download.ratio.toFixed(2)}
            </p>
          {:else}
            <p class="font-mono text-sm text-ink-secondary">ETA {formatDuration(download.eta_seconds)}</p>
          {/if}
        </div>
        <ProgressBar value={download.progress} tone={meta.tone} label="{download.name || UNKNOWN} progress" />
        <!-- A Usenet download has no upload half, no share ratio and no piece
             availability. Showing those as 0.00 would read as a torrent that is
             seeding nothing rather than as a protocol that has no such thing. -->
        {#if usenet}
          <dl class="grid grid-cols-2 gap-2">
            <div class="min-w-0">
              <dt class="micro-label">{t('component.queueDetailDrawer.down')}</dt>
              <dd class="mt-1 truncate font-mono text-sm text-ink" title={formatRate(download.down_rate)}>
                {formatRate(download.down_rate)}
              </dd>
            </div>
            <div class="min-w-0">
              <dt class="micro-label">{t('component.queueDetailDrawer.eta')}</dt>
              <dd class="mt-1 truncate font-mono text-sm text-ink" title={formatDuration(download.eta_seconds)}>
                {formatDuration(download.eta_seconds)}
              </dd>
            </div>
          </dl>
        {:else}
          <dl class="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <div class="min-w-0">
              <dt class="micro-label">{t('component.queueDetailDrawer.down')}</dt>
              <dd class="mt-1 truncate font-mono text-sm text-ink" title={formatRate(download.down_rate)}>
                {formatRate(download.down_rate)}
              </dd>
            </div>
            <div class="min-w-0">
              <dt class="micro-label">{t('component.queueDetailDrawer.up')}</dt>
              <dd class="mt-1 truncate font-mono text-sm text-ink" title={formatRate(download.up_rate)}>
                {formatRate(download.up_rate)}
              </dd>
            </div>
            <div class="min-w-0">
              <dt class="micro-label">{t('component.queueDetailDrawer.ratio')}</dt>
              <dd class="mt-1 font-mono text-sm text-ink">{download.ratio.toFixed(2)}</dd>
            </div>
            <div class="min-w-0">
              <dt class="micro-label">{t('component.queueDetailDrawer.availability')}</dt>
              <dd class="mt-1 font-mono text-sm text-ink">
                {insight ? insight.availability.toFixed(2) : UNKNOWN}
              </dd>
            </div>
          </dl>
        {/if}

        <!-- Why nothing is moving. A queued download whose size is already
             known is waiting on the concurrency cap rather than on a magnet's
             metadata, and saying so is the difference between "my queue works"
             and "my queue is stuck". -->
        {#if download.state === 'queued' && (usenet || download.size > 0)}
          <p class="rounded-sm border border-border bg-raised px-3 py-2 text-sm text-ink-secondary">
            {t('component.queueDetailDrawer.waitingForAFreeDownloadSlotRaiseTheLimitUnder')}
            <a
              class="text-accent-text hover:underline"
              href="/settings/downloads#download-concurrency">{t('component.queueDetailDrawer.settingsDownloadsConcurrency')}</a
            >, or pause something that is running.
          </p>
        {/if}

        <!-- Which stage the engine is in, when it is one the state badge cannot
             express. par2 reports no live progress, so repair is described by
             what it is working on rather than by a percentage nothing
             measures. -->
        {#if usenet && phaseLabel}
          <p
            class="flex items-center gap-2 rounded-sm border border-border bg-raised px-3 py-2 text-sm text-ink-secondary"
            title={insight?.damaged_files?.length
              ? `Damaged: ${insight.damaged_files.join(', ')}`
              : undefined}>
            <Badge tone="info">{phaseLabel}</Badge>
            <span>
              {#if download.phase === 'repairing'}
                {insight?.damaged_segments
                  ? tp('component.queueDetailDrawer.rebuildSegments', insight.damaged_segments)
                  : t('component.queueDetailDrawer.rebuildingFiles')}
              {:else if download.phase === 'extracting'}
                {t('component.queueDetailDrawer.unpackingArchives')}
              {/if}
            </span>
          </p>
        {/if}
        <!-- Which client is holding this, and where it says the data is. For
             an external client that path is on the client's own machine, and
             it is the first thing to check when an import cannot read it. -->
        <dl class="grid grid-cols-1 gap-2 sm:grid-cols-4">
          <div class="min-w-0">
            <dt class="micro-label">{t('component.queueDetailDrawer.client')}</dt>
            <dd class="mt-1 truncate text-sm text-ink" title={engineLabel(download)}>
              {engineLabel(download)}
            </dd>
          </div>
          <div class="min-w-0 sm:col-span-3">
            <dt class="micro-label">{t('component.queueDetailDrawer.location')}</dt>
            <dd class="mt-1 truncate font-mono text-sm text-ink-secondary" title={download.save_path || UNKNOWN}>
              {download.save_path || UNKNOWN}
            </dd>
          </div>
        </dl>
      </section>

      <div class="flex border-b border-border px-5" role="tablist" aria-label={t('component.queueDetailDrawer.downloadDetailSections')}>
        {#if usenet}
          <!-- One tab, and deliberately no Limits: the embedded Usenet engine
               implements no per-download rate control, so the tab could only
               ever have answered 400. -->
          <button
            id={filesTabID}
            type="button"
            role="tab"
            aria-selected={tab === 'files'}
            aria-controls={filesPanelID}
            tabindex={tab === 'files' ? 0 : -1}
            onkeydown={ontabkeydown}
            class="-mb-px border-b-2 border-accent px-3 py-2 text-sm text-ink"
            onclick={() => (tab = 'files')}>
            Files{files.length ? ` (${files.length})` : ''}
          </button>
        {:else if insightSupported}
          <button
            id={peersTabID}
            type="button"
            role="tab"
            aria-selected={tab === 'peers'}
            aria-controls={peersPanelID}
            tabindex={tab === 'peers' ? 0 : -1}
            onkeydown={ontabkeydown}
            class="-mb-px border-b-2 px-3 py-2 text-sm transition-colors duration-150 {tab === 'peers' ? 'border-accent text-ink' : 'border-transparent text-ink-secondary hover:text-ink'}"
            onclick={() => (tab = 'peers')}>
            Peers{insight ? ` (${insight.peers.length})` : ''}
          </button>
          <button
            id={trackersTabID}
            type="button"
            role="tab"
            aria-selected={tab === 'trackers'}
            aria-controls={trackersPanelID}
            tabindex={tab === 'trackers' ? 0 : -1}
            onkeydown={ontabkeydown}
            class="-mb-px border-b-2 px-3 py-2 text-sm transition-colors duration-150 {tab === 'trackers' ? 'border-accent text-ink' : 'border-transparent text-ink-secondary hover:text-ink'}"
            onclick={() => (tab = 'trackers')}>
            Trackers{insight ? ` (${insight.trackers.length})` : ''}
          </button>
        {/if}
        {#if !usenet}
          <button
            id={limitsTabID}
            type="button"
            role="tab"
            aria-selected={tab === 'limits'}
            aria-controls={limitsPanelID}
            tabindex={tab === 'limits' ? 0 : -1}
            onkeydown={ontabkeydown}
            class="-mb-px border-b-2 px-3 py-2 text-sm transition-colors duration-150 {tab === 'limits' ? 'border-accent text-ink' : 'border-transparent text-ink-secondary hover:text-ink'}"
            onclick={() => (tab = 'limits')}>
            {t('component.queueDetailDrawer.limits')}
          </button>
        {/if}
      </div>

      {#if usenet}
        <div id={filesPanelID} role="tabpanel" aria-labelledby={filesTabID} class="px-5 py-4">
          {#if insightError}
            <p class="text-sm text-ink-secondary">File detail is unavailable: {insightError}</p>
          {:else if !insightSupported}
            <p class="text-sm text-ink-secondary">
              {t('component.queueDetailDrawer.thisDownloadClientDoesNotReportTheFilesInsideAnNzb')}
            </p>
          {:else if insight === null}
            <p class="text-sm text-ink-secondary">{t('component.queueDetailDrawer.loadingFiles')}</p>
          {:else if files.length === 0}
            <p class="text-sm text-ink-secondary">{t('component.queueDetailDrawer.theEngineIsNotReportingAnyFilesYet')}</p>
          {:else}
            <!-- Segments, not bytes: an NZB's per-file size is the on-the-wire
                 total the poster declared, and a file's real progress is how
                 many of its articles are on disk. That is also the number par2
                 works in. -->
            <ul class="flex flex-col">
              {#each files as file (file.name)}
                <li class="flex flex-col gap-1.5 border-b border-border py-3 last:border-b-0">
                  <div class="flex items-center gap-2">
                    <p class="min-w-0 flex-1 truncate font-mono text-sm text-ink" title={file.name || UNKNOWN}>
                      {truncateMiddle(file.name || UNKNOWN, 48)}
                    </p>
                    {#if file.segments_failed > 0}
                      <Badge tone="danger">{file.segments_failed} missing</Badge>
                    {:else if file.complete}
                      <Badge tone="success">{t('component.queueDetailDrawer.complete')}</Badge>
                    {/if}
                    {#if file.par2}
                      <Badge tone="neutral">par2</Badge>
                    {/if}
                  </div>
                  <ProgressBar
                    value={file.segments > 0 ? file.segments_done / file.segments : 0}
                    tone={file.segments_failed > 0 ? 'warning' : file.complete ? 'success' : 'info'}
                    label="{file.name || UNKNOWN} progress" />
                  <p class="font-mono text-xs text-ink-muted">
                    {file.segments_done} / {file.segments} segments
                  </p>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {:else if tab === 'peers' && insightSupported}
        <div id={peersPanelID} role="tabpanel" aria-labelledby={peersTabID} class="px-5 py-4">
          {#if insightError}
            <p class="text-sm text-ink-secondary">Insight is unavailable: {insightError}</p>
          {:else if insight === null}
            <p class="text-sm text-ink-secondary">{t('component.queueDetailDrawer.loadingPeers')}</p>
          {:else if insight.peers.length === 0}
            <p class="text-sm text-ink-secondary">{t('component.queueDetailDrawer.noPeersAreConnected')}</p>
          {:else}
            <div class="overflow-x-auto">
              <table class="w-full min-w-[360px] table-fixed text-left">
                <thead class="micro-label">
                  <tr>
                    <th class="w-[180px] pb-2 font-medium">{t('component.queueDetailDrawer.peer')}</th>
                    <th class="w-14 pb-2 font-medium">%</th>
                    <th class="w-[82px] pb-2 font-medium">{t('component.queueDetailDrawer.down')}</th>
                    <th class="pb-2 font-medium">{t('component.queueDetailDrawer.up')}</th>
                  </tr>
                </thead>
                <tbody>
                  {#each insight.peers as peer (peer.addr)}
                    <tr class="border-t border-border">
                      <td class="py-2 pr-2">
                        <p class="truncate font-mono text-sm text-ink" title={peer.addr}>{peer.addr}</p>
                        <p class="truncate text-xs text-ink-muted" title={peer.client || UNKNOWN}>{peer.client || UNKNOWN}</p>
                      </td>
                      <td class="py-2 font-mono text-sm text-ink">{Math.round(peer.progress * 100)}%</td>
                      <td class="py-2 font-mono text-sm text-ink">{formatRate(peer.down_rate)}</td>
                      <td class="py-2 font-mono text-sm text-ink">{formatRate(peer.up_rate)}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </div>
      {:else if tab === 'trackers' && insightSupported}
        <div id={trackersPanelID} role="tabpanel" aria-labelledby={trackersTabID} class="px-5 py-4">
          {#if insight === null}
            <p class="text-sm text-ink-secondary">{t('component.queueDetailDrawer.openPeersToLoadTrackerInformation')}</p>
          {:else if insight.trackers.length === 0}
            <p class="text-sm text-ink-secondary">{t('component.queueDetailDrawer.noTrackersAreConfigured')}</p>
          {:else}
            <ul class="flex flex-col">
              {#each insight.trackers as tracker (tracker.url)}
                <li class="flex items-center gap-3 border-b border-border py-3 last:border-b-0">
                  <p class="min-w-0 flex-1 truncate font-mono text-sm text-ink" title={tracker.url}>{tracker.url}</p>
                  <Badge tone="neutral">{tracker.status || UNKNOWN}</Badge>
                  <span class="shrink-0 font-mono text-xs text-ink-secondary">
                    {tracker.seeders} S / {tracker.leechers} L
                  </span>
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      {:else}
        <div
          id={limitsPanelID}
          role="tabpanel"
          aria-labelledby={limitsTabID}
          class="flex flex-col gap-6 px-5 py-5">
          <div>
            <h3 class="micro-label">{t('component.queueDetailDrawer.rateLimits')}</h3>
            <div class="mt-3 flex flex-col gap-3">
              <label
                for={downLimitID}
                class="grid grid-cols-[128px_minmax(0,1fr)] items-center gap-3 text-sm text-ink">
                <span>{t('component.queueDetailDrawer.downloadLimit')}<span class="sr-only"> {t('component.queueDetailDrawer.inKbS')}</span></span>
                <span class="relative">
                  <input
                    id={downLimitID}
                    aria-describedby={limitsHelpID}
                    type="number"
                    min="0"
                    step="1"
                    bind:value={downKbps}
                    class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 pr-12 font-mono text-sm text-ink focus:border-accent" />
                  <span
                    aria-hidden="true"
                    class="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-xs text-ink-muted">KB/s</span>
                </span>
              </label>
              <label
                for={upLimitID}
                class="grid grid-cols-[128px_minmax(0,1fr)] items-center gap-3 text-sm text-ink">
                <span>{t('component.queueDetailDrawer.uploadLimit')}<span class="sr-only"> {t('component.queueDetailDrawer.inKbS')}</span></span>
                <span class="relative">
                  <input
                    id={upLimitID}
                    aria-describedby={limitsHelpID}
                    type="number"
                    min="0"
                    step="1"
                    bind:value={upKbps}
                    class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 pr-12 font-mono text-sm text-ink focus:border-accent" />
                  <span
                    aria-hidden="true"
                    class="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-xs text-ink-muted">KB/s</span>
                </span>
              </label>
            </div>
            <p id={limitsHelpID} class="mt-2 text-xs text-ink-muted">
              0 is unlimited. Empty inherits the global limit from
              <a
                class="text-accent-text hover:underline"
                href="/settings/downloads#download-concurrency">{t('component.queueDetailDrawer.settingsDownloadsConcurrency')}</a
              >.
            </p>
          </div>

          <div>
            <h3 class="micro-label">{t('component.queueDetailDrawer.seedingTargets')}</h3>
            <p class="mt-3 text-sm text-ink-secondary">{t('component.queueDetailDrawer.stopAtRatioXOrAfterDaysEitherTargetStopsSeeding')}</p>
          </div>

          <div class="flex flex-wrap justify-end gap-2">
            <Button variant="secondary" size="sm" disabled={applying} onclick={resetToGlobal}>
              {t('component.queueDetailDrawer.resetToGlobal')}
            </Button>
            <Button variant="primary" size="sm" disabled={applying} onclick={applyLimits}>
              {applying ? t('component.queueDetailDrawer.applying') : t('component.queueDetailDrawer.applyLimits')}
            </Button>
          </div>
        </div>
      {/if}
    </div>

    <footer class="flex flex-wrap items-center gap-2 border-t border-border px-5 py-3">
      <!-- A failed download has nothing to pause. What it has is a stage that
           went wrong and the fifteen gigabytes it already fetched, so the one
           action worth offering is trying that stage again. -->
      {#if retryable}
        <Button variant="primary" size="sm" disabled={busy} onclick={onretry}>
          <Icon name="refresh" size={14} />
          {t('component.queueDetailDrawer.retry')}
        </Button>
      {:else}
        <Button variant="secondary" size="sm" disabled={busy} onclick={paused ? onresume : onpause}>
          <Icon name={paused ? 'play' : 'pause'} size={14} />
          {paused ? 'Resume' : 'Pause'}
        </Button>
      {/if}
      <span class="flex-1"></span>
      <Button variant="ghost" size="sm" disabled={busy} onclick={() => onremove(false)}>{t('component.queueDetailDrawer.remove')}</Button>
      <Button variant="danger" size="sm" disabled={busy} onclick={() => onremove(true)}>{t('component.queueDetailDrawer.removeData')}</Button>
    </footer>
  </div>
</div>
