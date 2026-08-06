<script lang="ts">
  /**
   * Convert-for-TV workbench (SPEC §8, §11 `/convert`).
   *
   * Pending is the actionable library view. Active and Finished are the job
   * views. Job rows stay compact; their detail drawer shows live progress only
   * when the running Caravan process has ffmpeg timing to report.
   *
   * ffmpeg is optional. Without it, pending files and history stay readable,
   * while all conversion actions disappear or become unavailable.
   */
  import { onMount, onDestroy } from 'svelte';
  import { ApiError, api, errorText } from '../api/client';
  import type { Conversion, MediaFile } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import ConvertFileButton from '../components/ConvertFileButton.svelte';
  import ConversionDetailDrawer from '../components/ConversionDetailDrawer.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import PageTabs from '../components/PageTabs.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { activeConversions, conversionStateMeta, strategyLabel } from '../conversion';
  import { UNKNOWN, formatBytes, formatDate, truncateMiddle } from '../format';
  import { createSelection } from '../selection.svelte';
  import { page } from '../state/page.svelte';
  import { system } from '../state/system.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { TONE_DOT } from '../status';
  import { compatBadge } from '../tvcompat';

  /** A conversion outlives a poll tick by minutes, so polling is unhurried. */
  const POLL_MS = 5000;

  type ConvertTab = 'pending' | 'active' | 'finished';

  let tab = $state<ConvertTab>('pending');
  let pending = $state<MediaFile[] | null>(null);
  let conversions = $state<Conversion[] | null>(null);
  let error = $state<string | null>(null);
  let busyID = $state<number | null>(null);
  let timer: ReturnType<typeof setInterval> | null = null;
  let loadSequence = 0;
  let detailID = $state<number | null>(null);
  const selection = createSelection();
  let bulkBusy = $state(false);

  let ffmpeg = $derived(system.status?.ffmpeg_available ?? false);
  // Until the shell's first status fetch lands, "no ffmpeg" is not yet a fact.
  // Announcing it early would flash a banner that then disappears.
  let statusKnown = $derived(system.status !== null);
  let activeRows = $derived(activeConversions(conversions ?? []));
  let finishedRows = $derived(
    (conversions ?? []).filter((row) => !conversionStateMeta(row.status).active),
  );
  let jobRows = $derived(tab === 'active' ? activeRows : finishedRows);
  let detail = $derived(
    detailID === null
      ? null
      : (conversions ?? []).find((row) => row.id === detailID) ?? null,
  );

  $effect(() => {
    if (detailID !== null && conversions !== null && detail === null) detailID = null;
  });
  $effect(() => {
    if (statusKnown && !ffmpeg && selection.active) selection.clear();
  });

  let tabs = $derived<{ key: ConvertTab; label: string; count: number }[]>([
    { key: 'pending', label: 'Pending', count: pending?.length ?? 0 },
    { key: 'active', label: 'Active', count: activeRows.length },
    { key: 'finished', label: 'Finished', count: finishedRows.length },
  ]);

  async function load() {
    const sequence = ++loadSequence;
    try {
      const queue = await api.listConversionQueue();
      if (sequence !== loadSequence) return;
      pending = queue.pending;
      conversions = queue.conversions;
      error = null;
    } catch (err) {
      if (sequence !== loadSequence) return;
      error = errorText(err);
    }
  }

  onMount(() => {
    void load();
    timer = setInterval(() => void load(), POLL_MS);
  });

  onDestroy(() => {
    loadSequence += 1;
    if (timer) clearInterval(timer);
  });

  async function convertSelected() {
    const ids = [...selection.ids];
    if (ids.length === 0 || bulkBusy || !ffmpeg) return;

    bulkBusy = true;
    let handled = 0;
    const failed: number[] = [];
    try {
      for (const id of ids) {
        try {
          await api.convertMediaFile(id);
          handled++;
        } catch (err) {
          if (err instanceof ApiError && err.status === 409) handled++;
          else failed.push(id);
        }
      }

      selection.clear();
      for (const id of failed) selection.toggle(id);
      pushToast(
        failed.length === 0 ? `Queued ${ids.length}` : `Queued ${handled} of ${ids.length}`,
        failed.length === 0 ? 'neutral' : 'danger',
      );
      await load();
    } finally {
      bulkBusy = false;
    }
  }

  function onkeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && selection.active) selection.clear();
  }

  async function act(row: Conversion, action: () => Promise<unknown>, note: string) {
    busyID = row.id;
    try {
      await action();
      await load();
      pushToast(note, 'neutral');
    } catch (err) {
      pushToast(errorText(err), 'danger');
    } finally {
      busyID = null;
    }
  }

  function cancelDetail() {
    const row = detail;
    if (!row || row.status !== 'queued') return;
    void act(row, () => api.cancelConversion(row.id), 'Cancelled.');
  }

  function retryDetail() {
    const row = detail;
    if (!row || (row.status !== 'failed' && row.status !== 'cancelled')) return;
    void act(row, () => api.retryConversion(row.id), 'Queued again.');
  }

  $effect(() => {
    if (pending === null || conversions === null) {
      page.subtitle = null;
      return;
    }
    page.subtitle = `${pending.length} pending · ${activeRows.length} active`;
    return () => (page.subtitle = null);
  });
</script>
<svelte:window {onkeydown} />

<div class="flex flex-col gap-6">
  <div class="flex items-center gap-2">
    <div class="min-w-0 flex-1">
      <PageTabs
        {tabs}
        active={tab}
        onchange={(key) => (tab = key)}
        ariaLabel="Conversion work" />
    </div>
    <Button variant="secondary" size="sm" onclick={() => void load()}>
      <Icon name="refresh" size={14} />
      Refresh
    </Button>
  </div>

  <p class="max-w-3xl text-base text-ink-secondary">
    Find files that need work, track active jobs, and review finished conversions. A conversion
    can copy streams into a compatible container or re-encode them when needed. The original
    stays in place until the new file passes verification.
  </p>

  {#if statusKnown && !ffmpeg}
    <Banner
      tone="info"
      icon="warning"
      title="ffmpeg is not installed"
      message="Caravan does not bundle ffmpeg. Install ffmpeg and ffprobe, then restart Caravan to convert files. Everything else keeps working. The TV-compatibility badges stay informational." />
  {/if}

  {#if error && (pending === null || conversions === null)}
    <LoadError message={error} onretry={load} />
  {:else if pending === null || conversions === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 3 }) as _, i (i)}
        <Skeleton class="h-20 w-full rounded-md" />
      {/each}
    </div>
  {:else if tab === 'pending'}
    {#if pending.length === 0}
      <EmptyState
        icon="refresh"
        title="No files need conversion"
        message="Every current library file either matches the active TV profile, has no known compatibility problem, or is already being converted." />
    {:else}
      <ul class="flex flex-col gap-2" aria-label="Files pending conversion">
        {#each pending as file (file.id)}
          {@const meta = compatBadge(file.compatibility)}
          <li
            class="group/row relative flex flex-col gap-2 rounded-md border border-border bg-surface
                   px-3 py-3 transition-colors duration-150 hover:border-border-strong
                   {selection.has(file.id) ? 'ring-2 ring-accent' : ''}">
            {#if ffmpeg && selection.active}
              <button
                type="button"
                class="absolute inset-0 z-10 rounded-md focus:outline-none focus:ring-2 focus:ring-accent"
                aria-label="{selection.has(file.id) ? 'Deselect' : 'Select'} {file.path.split('/').pop() || UNKNOWN}"
                aria-pressed={selection.has(file.id)}
                onclick={() => selection.toggle(file.id)}></button>
            {/if}
            <div
              class="relative z-20 flex flex-wrap items-center gap-3
                     {selection.active ? 'pointer-events-none' : ''}">
              {#if ffmpeg}
                {#if selection.active}
                  <span
                    class="flex size-5 shrink-0 items-center justify-center rounded-full border
                           {selection.has(file.id)
                      ? 'border-accent bg-accent text-ink-inverse'
                      : 'border-border-strong bg-bg text-transparent'}"
                    aria-hidden="true">
                    <Icon name="check" size={12} />
                  </span>
                {:else}
                  <button
                    type="button"
                    class="pointer-events-auto flex size-5 shrink-0 items-center justify-center rounded-full
                           border border-border-strong bg-bg text-ink-secondary opacity-0
                           transition-opacity duration-150 ease-out hover:border-accent hover:text-accent
                           focus-visible:opacity-100 group-hover/row:opacity-100
                           group-focus-within/row:opacity-100 pointer-coarse:opacity-100"
                    aria-label="Select {file.path.split('/').pop() || UNKNOWN}"
                    aria-pressed="false"
                    onclick={() => selection.toggle(file.id)}>
                    <Icon name="check" size={12} />
                  </button>
                {/if}
              {/if}
              <span class="size-2 shrink-0 rounded-full {TONE_DOT.warning}" aria-hidden="true"></span>
              <span class="min-w-0 flex-1 font-mono text-ink" title={file.path}>
                {truncateMiddle(file.path || UNKNOWN, 64)}
              </span>
              {#if meta}
                <Badge
                  mono
                  tone={meta.tone}
                  title={meta.title.replace('copy-only remux', 'copy-only conversion')}>
                  {meta.label.replace('REMUX', 'CONVERT')}
                </Badge>
              {/if}
              {#if !selection.active}
                <ConvertFileButton {file} onqueued={() => void load()} />
              {/if}
            </div>
            <div
              class="relative z-20 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm
                     text-ink-secondary {selection.active ? 'pointer-events-none' : ''}">
              <span class="font-mono text-xs">{formatBytes(file.size)}</span>
              {#if file.compatibility.reasons.length}
                <span>{file.compatibility.reasons.join(' · ')}</span>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  {:else if jobRows.length === 0}
    <EmptyState
      icon="refresh"
      title={tab === 'active' ? 'No active conversions' : 'No finished conversions'}
      message={tab === 'active'
        ? 'No conversions are queued or running. Files that need work are listed under Pending.'
        : 'Completed, failed, and cancelled conversion jobs will appear here.'} />
  {:else}
    <ul class="flex flex-col gap-2" aria-label={tab === 'active' ? 'Active conversions' : 'Finished conversions'}>
      {#each jobRows as row (row.id)}
        {@const meta = conversionStateMeta(row.status)}
        <li
          class="relative flex flex-col gap-2 rounded-md border border-border bg-surface px-3 py-3
                 transition-colors duration-150 hover:border-border-strong">
          <button
            type="button"
            class="absolute inset-0 rounded-md focus:outline-none focus:ring-2 focus:ring-accent"
            aria-label="Open conversion details for {row.source_path.split('/').pop() || UNKNOWN}"
            onclick={() => (detailID = row.id)}></button>
          <div class="pointer-events-none relative z-10 flex flex-wrap items-center gap-3">
            <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}" aria-hidden="true"></span>
            <span class="min-w-0 flex-1 font-mono text-ink" title={row.source_path}>
              {truncateMiddle(row.source_path || UNKNOWN, 64)}
            </span>
            <Badge tone={meta.tone}>{meta.label}</Badge>
            <Badge mono tone="neutral" title="How this file is being converted">
              {row.strategy === 'remux' ? 'Convert (stream copy)' : strategyLabel(row.strategy)}
            </Badge>

            <div class="pointer-events-auto flex shrink-0 items-center gap-2">
              {#if row.status === 'queued'}
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busyID === row.id}
                  onclick={(event) => {
                    event.stopPropagation();
                    void act(row, () => api.cancelConversion(row.id), 'Cancelled.');
                  }}>
                  Cancel
                </Button>
              {:else if (row.status === 'failed' || row.status === 'cancelled') && ffmpeg}
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busyID === row.id}
                  title="Try this conversion again"
                  onclick={(event) => {
                    event.stopPropagation();
                    void act(row, () => api.retryConversion(row.id), 'Queued again.');
                  }}>
                  Retry
                </Button>
              {/if}
            </div>
          </div>

          <div class="pointer-events-none relative z-10 flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-xs text-ink-secondary">
            <span title="TV profile this conversion targets">{row.profile_id || UNKNOWN}</span>
            <span>{formatDate(row.updated_at)}</span>
            {#if row.output_path && row.output_path !== row.source_path}
              <span class="min-w-0 truncate" title={row.output_path}>
                → {truncateMiddle(row.output_path, 56)}
              </span>
            {/if}
          </div>

          {#if row.error}
            <p class="pointer-events-none relative z-10 text-sm text-danger">{row.error}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#if tab === 'pending' && ffmpeg && selection.active}
    <div class="pointer-events-none fixed bottom-6 left-60 right-0 z-40 flex justify-center">
      <div
        class="pointer-events-auto flex items-center gap-1 rounded-lg border border-border-strong
               bg-overlay py-1.5 pl-4 pr-1.5 shadow-2xl"
        role="group"
        aria-label="Selection actions">
        <span class="mr-2 whitespace-nowrap text-base font-medium text-ink">
          {selection.count} selected
        </span>
        <Button
          variant="primary"
          size="sm"
          disabled={bulkBusy}
          onclick={() => void convertSelected()}>
          <Icon name="refresh" size={14} />
          Convert selected
        </Button>
        <span class="mx-1 h-5 w-px bg-border" aria-hidden="true"></span>
        <Button
          variant="ghost"
          size="sm"
          disabled={bulkBusy}
          onclick={() => selection.clear()}
          title="Clear selection">
          <Icon name="close" size={14} />
          <span class="sr-only">Clear selection</span>
        </Button>
      </div>
    </div>
  {/if}

  {#if detail}
    <ConversionDetailDrawer
      conversion={detail}
      busy={busyID === detail.id}
      onclose={() => (detailID = null)}
      oncancel={detail.status === 'queued' ? cancelDetail : undefined}
      onretry={(detail.status === 'failed' || detail.status === 'cancelled') && ffmpeg
        ? retryDetail
        : undefined} />
  {/if}
</div>
