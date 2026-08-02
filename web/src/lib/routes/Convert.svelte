<script lang="ts">
  /**
   * Convert-for-TV queue (SPEC §8, §11 `/convert`).
   *
   * Modelled on the download queue, with one deliberate difference: there is no
   * progress bar. The server reports a coarse state machine, and a bar that
   * only ever shows 0% or 100% claims a precision nobody has.
   *
   * ffmpeg is optional. When the server does not have it, the screen still
   * lists what happened while it did — uninstalling ffmpeg must not erase
   * history — and every action is replaced by one informational banner.
   */
  import { onMount, onDestroy } from 'svelte';
  import { api, errorText } from '../api/client';
  import type { Conversion } from '../api/types';
  import Badge from '../components/Badge.svelte';
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import EmptyState from '../components/EmptyState.svelte';
  import Icon from '../components/Icon.svelte';
  import LoadError from '../components/LoadError.svelte';
  import Skeleton from '../components/Skeleton.svelte';
  import { activeConversions, conversionStateMeta, strategyLabel } from '../conversion';
  import { UNKNOWN, formatDate, truncateMiddle } from '../format';
  import { page } from '../state/page.svelte';
  import { system } from '../state/system.svelte';
  import { pushToast } from '../state/toast.svelte';
  import { TONE_DOT } from '../status';

  /** A conversion outlives a poll tick by minutes, so polling is unhurried. */
  const POLL_MS = 5000;

  let rows = $state<Conversion[] | null>(null);
  let error = $state<string | null>(null);
  let busyID = $state<number | null>(null);
  let timer: ReturnType<typeof setInterval> | null = null;

  let ffmpeg = $derived(system.status?.ffmpeg_available ?? false);
  // Until the shell's first status fetch lands, "no ffmpeg" is not yet a fact.
  // Announcing it early would flash a banner that then disappears.
  let statusKnown = $derived(system.status !== null);

  async function load() {
    try {
      rows = await api.listConversions();
      error = null;
    } catch (err) {
      error = errorText(err);
    }
  }

  onMount(() => {
    void load();
    timer = setInterval(() => void load(), POLL_MS);
  });

  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

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

  $effect(() => {
    const items = rows;
    if (!items) {
      page.subtitle = null;
      return;
    }
    const failed = items.filter((item) => item.status === 'failed').length;
    page.subtitle = `${activeConversions(items).length} active${failed ? ` · ${failed} failed` : ''}`;
    return () => (page.subtitle = null);
  });
</script>

<div class="flex flex-col gap-6">
  <div class="flex flex-wrap items-center gap-3">
    <p class="max-w-3xl text-base text-ink-secondary">
      Files being made playable on your TV profile. A remux swaps the container and copies the
      streams untouched; a transcode re-encodes and is much slower. The original is only
      removed once the new file has been checked.
    </p>
    <div class="ml-auto flex items-center gap-2">
      <Button variant="secondary" onclick={() => void load()}>
        <Icon name="refresh" size={14} />
        Refresh
      </Button>
    </div>
  </div>

  {#if statusKnown && !ffmpeg}
    <Banner
      tone="info"
      icon="warning"
      title="ffmpeg is not installed"
      message="Caravan does not bundle ffmpeg. Install ffmpeg and ffprobe, then restart Caravan to convert files. Everything else keeps working — the TV-compatibility badges stay informational." />
  {/if}

  {#if error && rows === null}
    <LoadError message={error} onretry={load} />
  {:else if rows === null}
    <div class="flex flex-col gap-2">
      {#each Array.from({ length: 3 }) as _, i (i)}
        <Skeleton class="h-16 w-full rounded-md" />
      {/each}
    </div>
  {:else if rows.length === 0}
    <EmptyState
      icon="refresh"
      title="Nothing to convert"
      message="Open a movie or episode. When a file will not play on your TV profile, its file row offers a Convert button." />
  {:else}
    <ul class="flex flex-col gap-2">
      {#each rows as row (row.id)}
        {@const meta = conversionStateMeta(row.status)}
        <li
          class="flex flex-col gap-2 rounded-md border border-border bg-surface px-3 py-3
                 transition-colors duration-150 hover:border-border-strong">
          <div class="flex flex-wrap items-center gap-3">
            <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}" aria-hidden="true"></span>
            <span class="min-w-0 flex-1 font-mono text-ink" title={row.source_path}>
              {truncateMiddle(row.source_path || UNKNOWN, 64)}
            </span>
            <Badge tone={meta.tone}>{meta.label}</Badge>
            <Badge mono tone="neutral" title="How this file is being converted">
              {strategyLabel(row.strategy)}
            </Badge>

            <div class="flex shrink-0 items-center gap-2">
              {#if row.status === 'queued'}
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busyID === row.id}
                  onclick={() => void act(row, () => api.cancelConversion(row.id), 'Cancelled.')}>
                  Cancel
                </Button>
              {:else if row.status === 'failed' || row.status === 'cancelled'}
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={busyID === row.id || (statusKnown && !ffmpeg)}
                  title={!statusKnown || ffmpeg
                    ? 'Try this conversion again'
                    : 'ffmpeg is not installed'}
                  onclick={() => void act(row, () => api.retryConversion(row.id), 'Queued again.')}>
                  Retry
                </Button>
              {/if}
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 font-mono text-xs text-ink-secondary">
            <span title="TV profile this conversion targets">{row.profile_id || UNKNOWN}</span>
            <span>{formatDate(row.updated_at)}</span>
            {#if row.output_path && row.output_path !== row.source_path}
              <span class="min-w-0 truncate" title={row.output_path}>
                → {truncateMiddle(row.output_path, 56)}
              </span>
            {/if}
          </div>

          {#if row.error}
            <p class="text-sm text-danger">{row.error}</p>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</div>
