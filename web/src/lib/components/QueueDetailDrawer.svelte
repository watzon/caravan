<script lang="ts">
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
  import { api, ApiError, errorText } from '../api/client';
  import type { DownloadInsight, DownloadStatus } from '../api/types';
  import { UNKNOWN, formatBytes, formatDuration, formatRate, truncateMiddle } from '../format';
  import { downloadPhaseLabel, downloadStateMeta, engineLabel } from '../download';
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
    onremove: (deleteData: boolean) => void;
    onlimitsapplied?: () => Promise<void> | void;
  }

  let {
    download,
    busy = false,
    onclose,
    onpause,
    onresume,
    onremove,
    onlimitsapplied,
  }: Props = $props();

  let tab = $state<Tab>('peers');
  let insight = $state<DownloadInsight | null>(null);
  let insightError = $state<string | null>(null);
  let insightSupported = $state(true);
  let downKbps = $state('');
  let upKbps = $state('');
  let limitsForDownload = $state<string | null>(null);
  let applying = $state(false);
  let dialog = $state<HTMLElement | null>(null);

  $effect(() => {
    const previous = document.activeElement as HTMLElement | null;
    dialog?.focus();
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = '';
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

  $effect(() => {
    function onkeydown(event: KeyboardEvent) {
      if (event.key === 'Escape') onclose();
    }
    window.addEventListener('keydown', onkeydown);
    return () => window.removeEventListener('keydown', onkeydown);
  });

  async function applyLimits() {
    applying = true;
    try {
      const down = nonNegativeKbps(downKbps);
      const up = nonNegativeKbps(upKbps);
      await api.setDownloadLimits(download.id, down, up);
      downKbps = String(down);
      upKbps = String(up);
      await onlimitsapplied?.();
      pushToast('Rate limits applied.', 'success');
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
</script>

<div class="fixed inset-0 z-50 flex justify-end" aria-hidden="false">
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-bg/80"
    aria-label="Close download details"
    onclick={onclose}></button>

  <div
    bind:this={dialog}
    class="relative flex h-full w-full max-w-[440px] flex-col border-l border-border-strong bg-surface shadow-2xl"
    role="dialog"
    aria-modal="true"
    aria-label="Download details"
    tabindex="-1">
    <header class="flex items-start gap-3 border-b border-border px-5 py-4">
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <h2 class="min-w-0 truncate font-display text-xl font-semibold tracking-tight text-ink" title={download.name}>
            {download.name || UNKNOWN}
          </h2>
          <Badge tone={meta.tone}>{meta.label}</Badge>
        </div>
        <p class="mt-1 truncate font-mono text-xs text-ink-muted" title={download.name}>
          {download.name || UNKNOWN}
        </p>
      </div>
      <button
        type="button"
        class="shrink-0 rounded-sm p-1 text-ink-secondary transition-colors duration-150 hover:bg-raised hover:text-ink focus:outline-none focus:ring-2 focus:ring-accent"
        aria-label="Close download details"
        onclick={onclose}>
        <Icon name="close" />
      </button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto">
      <section class="flex flex-col gap-4 border-b border-border px-5 py-4" aria-label="Transfer status">
        <div class="flex items-end justify-between gap-4">
          <div>
            <p class="font-mono text-xl font-semibold text-ink">
              {Math.round(Math.max(0, Math.min(1, download.progress)) * 100)}%
            </p>
            <p class="mt-1 text-sm text-ink-secondary">
              {formatBytes(download.bytes_done)} of {download.size > 0 ? formatBytes(download.size) : UNKNOWN}
            </p>
          </div>
          <p class="font-mono text-sm text-ink-secondary">ETA {formatDuration(download.eta_seconds)}</p>
        </div>
        <ProgressBar value={download.progress} tone="info" label="{download.name} progress" />
        <!-- A Usenet download has no upload half, no share ratio and no piece
             availability. Showing those as 0.00 would read as a torrent that is
             seeding nothing rather than as a protocol that has no such thing. -->
        {#if usenet}
          <dl class="grid grid-cols-4 gap-2">
            <div class="min-w-0">
              <dt class="micro-label">Down</dt>
              <dd class="mt-1 truncate font-mono text-sm text-ink">{formatRate(download.down_rate)}</dd>
            </div>
            <div class="min-w-0">
              <dt class="micro-label">ETA</dt>
              <dd class="mt-1 truncate font-mono text-sm text-ink">{formatDuration(download.eta_seconds)}</dd>
            </div>
          </dl>
        {:else}
          <dl class="grid grid-cols-4 gap-2">
            <div class="min-w-0">
              <dt class="micro-label">Down</dt>
              <dd class="mt-1 truncate font-mono text-sm text-ink">{formatRate(download.down_rate)}</dd>
            </div>
            <div class="min-w-0">
              <dt class="micro-label">Up</dt>
              <dd class="mt-1 truncate font-mono text-sm text-ink">{formatRate(download.up_rate)}</dd>
            </div>
            <div class="min-w-0">
              <dt class="micro-label">Ratio</dt>
              <dd class="mt-1 font-mono text-sm text-ink">{download.ratio.toFixed(2)}</dd>
            </div>
            <div class="min-w-0">
              <dt class="micro-label">Availability</dt>
              <dd class="mt-1 font-mono text-sm text-ink">
                {insight ? insight.availability.toFixed(2) : UNKNOWN}
              </dd>
            </div>
          </dl>
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
                  ? `${insight.damaged_segments} segment${insight.damaged_segments === 1 ? '' : 's'} to reconstruct`
                  : 'Rebuilding damaged files from the release’s par2 volumes.'}
              {:else if download.phase === 'extracting'}
                Unpacking the release’s archives.
              {/if}
            </span>
          </p>
        {/if}
        <!-- Which client is holding this, and where it says the data is. For
             an external client that path is on the client's own machine, and
             it is the first thing to check when an import cannot read it. -->
        <dl class="grid grid-cols-4 gap-2">
          <div class="min-w-0">
            <dt class="micro-label">Client</dt>
            <dd class="mt-1 truncate text-sm text-ink" title="Which backend holds this download">
              {engineLabel(download)}
            </dd>
          </div>
          <div class="col-span-3 min-w-0">
            <dt class="micro-label">Location</dt>
            <dd class="mt-1 truncate font-mono text-sm text-ink-secondary" title={download.save_path}>
              {download.save_path || UNKNOWN}
            </dd>
          </div>
        </dl>
      </section>

      <div class="flex border-b border-border px-5" role="tablist" aria-label="Download detail sections">
        {#if usenet}
          <!-- One tab, and deliberately no Limits: the embedded Usenet engine
               implements no per-download rate control, so the tab could only
               ever have answered 400. -->
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'files'}
            class="-mb-px border-b-2 border-accent px-3 py-2 text-sm text-ink"
            onclick={() => (tab = 'files')}>
            Files{files.length ? ` (${files.length})` : ''}
          </button>
        {:else if insightSupported}
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'peers'}
            class="-mb-px border-b-2 px-3 py-2 text-sm transition-colors duration-150 {tab === 'peers' ? 'border-accent text-ink' : 'border-transparent text-ink-secondary hover:text-ink'}"
            onclick={() => (tab = 'peers')}>
            Peers{insight ? ` (${insight.peers.length})` : ''}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'trackers'}
            class="-mb-px border-b-2 px-3 py-2 text-sm transition-colors duration-150 {tab === 'trackers' ? 'border-accent text-ink' : 'border-transparent text-ink-secondary hover:text-ink'}"
            onclick={() => (tab = 'trackers')}>
            Trackers{insight ? ` (${insight.trackers.length})` : ''}
          </button>
        {/if}
        {#if !usenet}
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'limits'}
            class="-mb-px border-b-2 px-3 py-2 text-sm transition-colors duration-150 {tab === 'limits' ? 'border-accent text-ink' : 'border-transparent text-ink-secondary hover:text-ink'}"
            onclick={() => (tab = 'limits')}>
            Limits
          </button>
        {/if}
      </div>

      {#if usenet}
        <section class="px-5 py-4" aria-label="Files">
          {#if insightError}
            <p class="text-sm text-ink-secondary">File detail is unavailable: {insightError}</p>
          {:else if !insightSupported}
            <p class="text-sm text-ink-secondary">
              This download client does not report the files inside an NZB.
            </p>
          {:else if insight === null}
            <p class="text-sm text-ink-secondary">Loading files...</p>
          {:else if files.length === 0}
            <p class="text-sm text-ink-secondary">The engine is not reporting any files yet.</p>
          {:else}
            <!-- Segments, not bytes: an NZB's per-file size is the on-the-wire
                 total the poster declared, and a file's real progress is how
                 many of its articles are on disk. That is also the number par2
                 works in. -->
            <ul class="flex flex-col">
              {#each files as file (file.name)}
                <li class="flex flex-col gap-1.5 border-b border-border py-3 last:border-b-0">
                  <div class="flex items-center gap-2">
                    <p class="min-w-0 flex-1 truncate font-mono text-sm text-ink" title={file.name}>
                      {truncateMiddle(file.name || UNKNOWN, 48)}
                    </p>
                    {#if file.segments_failed > 0}
                      <Badge tone="danger">{file.segments_failed} missing</Badge>
                    {:else if file.complete}
                      <Badge tone="success">Complete</Badge>
                    {/if}
                    {#if file.par2}
                      <Badge tone="neutral">par2</Badge>
                    {/if}
                  </div>
                  <ProgressBar
                    value={file.segments > 0 ? file.segments_done / file.segments : 0}
                    tone={file.segments_failed > 0 ? 'warning' : file.complete ? 'success' : 'info'}
                    label="{file.name} progress" />
                  <p class="font-mono text-xs text-ink-muted">
                    {file.segments_done} / {file.segments} segments
                  </p>
                </li>
              {/each}
            </ul>
          {/if}
        </section>
      {:else if tab === 'peers' && insightSupported}
        <section class="px-5 py-4" aria-label="Peers">
          {#if insightError}
            <p class="text-sm text-ink-secondary">Insight is unavailable: {insightError}</p>
          {:else if insight === null}
            <p class="text-sm text-ink-secondary">Loading peers...</p>
          {:else if insight.peers.length === 0}
            <p class="text-sm text-ink-secondary">No peers are connected.</p>
          {:else}
            <div class="overflow-x-auto">
              <table class="w-full min-w-[360px] table-fixed text-left">
                <thead class="micro-label">
                  <tr>
                    <th class="w-[180px] pb-2 font-medium">Peer</th>
                    <th class="w-14 pb-2 font-medium">%</th>
                    <th class="w-[82px] pb-2 font-medium">Down</th>
                    <th class="pb-2 font-medium">Up</th>
                  </tr>
                </thead>
                <tbody>
                  {#each insight.peers as peer (peer.addr)}
                    <tr class="border-t border-border">
                      <td class="py-2 pr-2">
                        <p class="truncate font-mono text-sm text-ink" title={peer.addr}>{peer.addr}</p>
                        <p class="truncate text-xs text-ink-muted" title={peer.client}>{peer.client || UNKNOWN}</p>
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
        </section>
      {:else if tab === 'trackers' && insightSupported}
        <section class="px-5 py-4" aria-label="Trackers">
          {#if insight === null}
            <p class="text-sm text-ink-secondary">Open Peers to load tracker information.</p>
          {:else if insight.trackers.length === 0}
            <p class="text-sm text-ink-secondary">No trackers are configured.</p>
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
        </section>
      {:else}
        <section class="flex flex-col gap-6 px-5 py-5" aria-label="Limits">
          <div>
            <h3 class="micro-label">Rate limits</h3>
            <div class="mt-3 flex flex-col gap-3">
              <label class="grid grid-cols-[128px_minmax(0,1fr)] items-center gap-3 text-sm text-ink">
                <span>Download limit</span>
                <span class="relative">
                  <input
                    aria-label="Download limit"
                    type="number"
                    min="0"
                    step="1"
                    bind:value={downKbps}
                    class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 pr-12 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
                  <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-xs text-ink-muted">KB/s</span>
                </span>
              </label>
              <label class="grid grid-cols-[128px_minmax(0,1fr)] items-center gap-3 text-sm text-ink">
                <span>Upload limit</span>
                <span class="relative">
                  <input
                    aria-label="Upload limit"
                    type="number"
                    min="0"
                    step="1"
                    bind:value={upKbps}
                    class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 pr-12 font-mono text-sm text-ink focus:border-accent focus:outline-none" />
                  <span class="pointer-events-none absolute inset-y-0 right-3 flex items-center font-mono text-xs text-ink-muted">KB/s</span>
                </span>
              </label>
            </div>
            <p class="mt-2 text-xs text-ink-muted">0 is unlimited. Empty inherits the global limit from Settings.</p>
          </div>

          <div>
            <h3 class="micro-label">Seeding targets</h3>
            <p class="mt-3 text-sm text-ink-secondary">Stop at ratio [x], or after [days]. Either target stops seeding.</p>
          </div>

          <div class="flex flex-wrap justify-end gap-2">
            <Button variant="secondary" size="sm" disabled={applying} onclick={resetToGlobal}>
              Reset to global
            </Button>
            <Button variant="primary" size="sm" disabled={applying} onclick={applyLimits}>
              {applying ? 'Applying...' : 'Apply limits'}
            </Button>
          </div>
        </section>
      {/if}
    </div>

    <footer class="flex items-center gap-2 border-t border-border px-5 py-3">
      <Button variant="secondary" size="sm" disabled={busy} onclick={paused ? onresume : onpause}>
        <Icon name={paused ? 'play' : 'pause'} size={14} />
        {paused ? 'Resume' : 'Pause'}
      </Button>
      <span class="flex-1"></span>
      <Button variant="ghost" size="sm" disabled={busy} onclick={() => onremove(false)}>Remove</Button>
      <Button variant="danger" size="sm" disabled={busy} onclick={() => onremove(true)}>Remove + data</Button>
    </footer>
  </div>
</div>
