<script lang="ts">
  /** Live and durable detail for one convert-for-TV job. */
  import { onMount } from 'svelte';
  import type { Conversion, ConversionStage } from '../api/types';
  import { conversionStateMeta, strategyLabel } from '../conversion';
  import { UNKNOWN, formatAge, formatDate, formatDuration } from '../format';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import ProgressBar from './ProgressBar.svelte';

  interface Props {
    conversion: Conversion;
    busy?: boolean;
    onclose: () => void;
    oncancel?: () => void;
    onretry?: () => void;
  }

  let {
    conversion,
    busy = false,
    onclose,
    oncancel,
    onretry,
  }: Props = $props();

  const drawerID = $props.id();
  const titleID = `${drawerID}-title`;

  const STAGE_LABELS: Record<ConversionStage, string> = {
    probing: 'Inspecting source',
    converting: 'Encoding media',
    verifying: 'Verifying output',
    installing: 'Installing output',
  };

  const FOCUSABLE =
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), ' +
    'textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

  let panel: HTMLElement;
  let now = $state(Date.now());
  let meta = $derived(conversionStateMeta(conversion.status));
  let filename = $derived(conversion.source_path.split('/').pop() || UNKNOWN);
  let stageLabel = $derived(conversion.stage ? STAGE_LABELS[conversion.stage] : null);
  let showProgress = $derived(
    conversion.progress !== undefined && (conversion.duration_seconds ?? 0) > 0,
  );
  let processedTime = $derived(
    (conversion.processed_seconds ?? 0) > 0
      ? formatDuration(conversion.processed_seconds ?? 0)
      : '0s',
  );
  let elapsed = $derived.by(() => {
    if (!conversion.started_at) return null;
    const started = Date.parse(conversion.started_at);
    if (Number.isNaN(started)) return null;
    return Math.max(0, (now - started) / 1000);
  });

  onMount(() => {
    const previousOverflow = document.body.style.overflow;
    const previousFocus = document.activeElement as HTMLElement | null;
    document.body.style.overflow = 'hidden';
    panel.focus();
    const timer = setInterval(() => (now = Date.now()), 1000);
    return () => {
      clearInterval(timer);
      document.body.style.overflow = previousOverflow;
      if (previousFocus?.isConnected) {
        previousFocus.focus();
      } else {
        queueMicrotask(() => {
          const pageTabs = document.querySelector<HTMLElement>(
            'main [role="group"][aria-label="Conversion work"]',
          );
          (
            pageTabs?.querySelector<HTMLElement>('button[aria-pressed="true"]') ??
            pageTabs?.querySelector<HTMLElement>('button:not([disabled])') ??
            document.querySelector<HTMLElement>('main button:not([disabled]), main a[href]')
          )?.focus();
        });
      }
    };
  });

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      onclose();
      return;
    }
    if (event.key !== 'Tab') return;

    const focusable = [...panel.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
      (element) => element.getAttribute('aria-hidden') !== 'true',
    );
    const first = focusable[0];
    const last = focusable.at(-1);
    if (!first || !last) {
      event.preventDefault();
      panel.focus();
      return;
    }

    const active = document.activeElement;
    const leavingBackward = event.shiftKey &&
      (active === first || active === panel || !panel.contains(active));
    const leavingForward = !event.shiftKey &&
      (active === last || active === panel || !panel.contains(active));
    if (!leavingBackward && !leavingForward) return;

    event.preventDefault();
    (event.shiftKey ? last : first).focus();
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="fixed inset-0 z-50 flex justify-end" aria-hidden="false">
  <button
    type="button"
    class="absolute inset-0 cursor-default bg-black/45"
    aria-label="Close conversion details"
    tabindex="-1"
    aria-hidden="true"
    onclick={onclose}></button>
  <div
    bind:this={panel}
    class="relative flex h-full w-full max-w-xl flex-col border-l border-border bg-surface shadow-2xl"
    role="dialog"
    aria-modal="true"
    aria-labelledby={titleID}
    tabindex="-1">
    <header class="flex items-start gap-3 border-b border-border px-5 py-4">
      <div class="min-w-0 flex-1">
        <p class="micro-label">Conversion details</p>
        <h2 id={titleID} class="mt-1 truncate text-lg font-semibold text-ink" title={filename}>
          {filename}
        </h2>
        <p
          class="mt-1 truncate font-mono text-xs text-ink-secondary"
          title={conversion.source_path || UNKNOWN}>
          {conversion.source_path || UNKNOWN}
        </p>
      </div>
      <Button variant="ghost" size="sm" title="Close conversion details" onclick={onclose}>
        <Icon name="x" size={16} />
        <span class="sr-only">Close</span>
      </Button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto">
      <section class="flex flex-col gap-4 border-b border-border px-5 py-5" aria-label="Conversion status">
        <div class="flex flex-wrap items-center gap-2">
          <Badge tone={meta.tone}>{meta.label}</Badge>
          <Badge mono tone="neutral" title="How this file is being converted">
            {strategyLabel(conversion.strategy)}
          </Badge>
          {#if stageLabel && !showProgress}
            <span class="text-sm text-ink-secondary">{stageLabel}</span>
          {/if}
        </div>

        {#if showProgress}
          <div class="flex flex-col gap-2">
            <div class="flex items-baseline justify-between gap-4">
              <span class="text-sm font-medium text-ink">{stageLabel}</span>
              <span class="font-mono text-sm text-ink">
                {Math.round(Math.max(0, Math.min(1, conversion.progress ?? 0)) * 100)}%
              </span>
            </div>
            <ProgressBar
              value={conversion.progress ?? 0}
              tone={meta.tone}
              label="{filename} conversion progress" />
            <p class="font-mono text-xs text-ink-secondary">
              {`${processedTime} / ${formatDuration(conversion.duration_seconds ?? 0)}`}
            </p>
          </div>
        {/if}

        {#if conversion.started_at}
          <dl class="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3">
            <div>
              <dt class="micro-label">Elapsed</dt>
              <dd class="mt-1 font-mono text-sm text-ink">
                {elapsed === null ? UNKNOWN : formatDuration(elapsed)}
              </dd>
            </div>
            <div>
              <dt class="micro-label">Remaining</dt>
              <dd class="mt-1 font-mono text-sm text-ink">
                {(conversion.eta_seconds ?? 0) > 0
                  ? formatDuration(conversion.eta_seconds!)
                  : UNKNOWN}
              </dd>
            </div>
            <div>
              <dt class="micro-label">Speed</dt>
              <dd class="mt-1 font-mono text-sm text-ink">
                {(conversion.speed ?? 0) > 0 ? `${conversion.speed!.toFixed(1)}x` : UNKNOWN}
              </dd>
            </div>
          </dl>
        {:else if conversion.status === 'running'}
          <p class="text-sm text-ink-secondary">
            Live timing is unavailable. Caravan may have restarted after this job began.
          </p>
        {/if}
      </section>

      <section class="flex flex-col gap-4 px-5 py-5" aria-label="Conversion process">
        <h3 class="micro-label">Process</h3>
        <dl class="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <dt class="text-xs text-ink-muted">Method</dt>
            <dd class="mt-1 text-sm text-ink">{strategyLabel(conversion.strategy)}</dd>
          </div>
          <div>
            <dt class="text-xs text-ink-muted">TV profile</dt>
            <dd class="mt-1 font-mono text-sm text-ink">{conversion.profile_id || UNKNOWN}</dd>
          </div>
          <div>
            <dt class="text-xs text-ink-muted">Queued</dt>
            <dd class="mt-1 text-sm text-ink" title={conversion.created_at}>
              {formatDate(conversion.created_at)}
              <span class="text-ink-secondary">({formatAge(conversion.created_at, now)} ago)</span>
            </dd>
          </div>
          <div>
            <dt class="text-xs text-ink-muted">Last update</dt>
            <dd class="mt-1 text-sm text-ink" title={conversion.updated_at}>
              {formatDate(conversion.updated_at)}
              <span class="text-ink-secondary">({formatAge(conversion.updated_at, now)} ago)</span>
            </dd>
          </div>
        </dl>

        {#if conversion.output_path && conversion.output_path !== conversion.source_path}
          <div>
            <p class="text-xs text-ink-muted">Output</p>
            <p class="mt-1 break-all font-mono text-xs text-ink" title={conversion.output_path}>
              {conversion.output_path}
            </p>
          </div>
        {/if}

        {#if conversion.error}
          <div class="rounded-md border border-danger/30 bg-danger/5 px-3 py-3">
            <p class="micro-label text-danger">Error</p>
            <p class="mt-1 break-words text-sm text-danger">{conversion.error}</p>
          </div>
        {/if}
      </section>
    </div>

    <footer class="flex flex-wrap items-center gap-2 border-t border-border px-5 py-3">
      <Button variant="ghost" size="sm" onclick={onclose}>Close</Button>
      <span class="flex-1"></span>
      {#if conversion.status === 'queued' && oncancel}
        <Button variant="secondary" size="sm" disabled={busy} onclick={oncancel}>Cancel</Button>
      {:else if (conversion.status === 'failed' || conversion.status === 'cancelled') && onretry}
        <Button variant="primary" size="sm" disabled={busy} onclick={onretry}>Retry</Button>
      {/if}
    </footer>
  </div>
</div>
