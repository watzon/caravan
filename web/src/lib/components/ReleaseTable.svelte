<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * The release result table, its skeleton, and its empty state.
   *
   * Lifted out of ReleaseSearch so the per-item picker and the universal search
   * render one table. Every result is grabbable, including the flagged ones —
   * the UI de-emphasizes a bad release, it does not decide for the user (SPEC
   * §13).
   *
   * A row is a specimen strip: the release name keeps its tail (DESIGN.md §6),
   * indexer/age/size live on a facts line, and the action cell IS the row state
   * — Grab, Downloading, or Downloaded — rather than a chip fighting a button.
   */
  import type { Release } from '../api/types';
  import { UNKNOWN, formatAge, formatBytes } from '../format';
  import { isFlagged, releaseFlags, releaseScore, sortReleases } from '../release';
  import { compatBadge } from '../tvcompat';
  import Badge from './Badge.svelte';
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import Icon from './Icon.svelte';
  import MiddleEllipsis from './MiddleEllipsis.svelte';
  import Skeleton from './Skeleton.svelte';
  const { t, tp } = useI18n();

  interface Props {
    /** Null before the first search; an empty array is "searched, found nothing". */
    releases: Release[] | null;
    loading: boolean;
    /** Keyed by GUID, not row id: an uncached result has id 0. */
    busyGUID: string | null;
    /** The row button's verb. The universal search grabs into a chosen target. */
    grabLabel?: string;
    ongrab: (release: Release) => void;
    emptyMessage?: string;
    /** Rows shown before "Show more"; a fan-out can return hundreds. */
    pageSize?: number;
  }

  let {
    releases,
    loading,
    busyGUID,
    grabLabel = t('component.releaseTable.grabDefault'),
    ongrab,
    emptyMessage = t('component.releaseTable.emptyDefault'),
    pageSize = 50,
  }: Props = $props();

  let sorted = $derived(sortReleases(releases ?? []));

  /**
   * The page window. It slices AFTER the sort, so "show more" only ever
   * reveals worse rows than the ones on screen — a cut before sorting could
   * hide the best release behind a button. A new result set starts back at
   * one page; the search that produced it is a different question.
   */
  let visibleCount = $state(pageSize);
  $effect(() => {
    releases;
    visibleCount = pageSize;
  });
  let rows = $derived(sorted.slice(0, visibleCount));
  let remaining = $derived(Math.max(0, sorted.length - visibleCount));

  /** Indexer, age, size, and torrent swarm — the facts the old columns spent a whole cell each on. */
  function facts(release: Release): string {
    const parts: string[] = [];
    if (release.indexer) parts.push(release.indexer);
    const age = formatAge(release.published_at);
    if (age && age !== UNKNOWN) parts.push(age);
    if (release.size > 0) parts.push(formatBytes(release.size));
    if (release.protocol === 'torrent') {
      parts.push(`${release.seeders}/${release.leechers}`);
    }
    return parts.join(' · ');
  }

  function rowTone(release: Release, best: boolean): string {
    if (release.queue_state === 'downloaded') return 'bg-success-tint';
    if (release.queue_state === 'downloading' || best) return 'bg-accent-tint';
    return 'hover:bg-raised';
  }

  function railTone(release: Release, best: boolean): string {
    if (release.queue_state === 'downloaded') return 'bg-success';
    if (release.queue_state === 'downloading' || best) return 'bg-accent';
    return '';
  }
</script>

{#if loading}
  <div class="overflow-hidden rounded-md border border-border">
    {#each Array.from({ length: 6 }) as _, i (i)}
      <div class="flex items-center gap-3 border-b border-border px-3 py-3 last:border-b-0">
        <div class="min-w-0 flex-1">
          <Skeleton class="h-4 w-3/4" />
          <Skeleton class="mt-1.5 h-3 w-1/3" />
        </div>
        <Skeleton class="h-5 w-14 rounded-sm" />
        <Skeleton class="h-4 w-10" />
        <Skeleton class="h-7 w-16 rounded-md" />
      </div>
    {/each}
  </div>
{:else if sorted.length === 0}
  <EmptyState icon="search" title={t('component.releaseTable.noReleasesFound')} message={emptyMessage}>
    {#snippet action()}
      <Button variant="secondary" href="/settings">{t('component.releaseTable.openIndexerSettings')}</Button>
    {/snippet}
  </EmptyState>
{:else}
  <div class="overflow-x-auto rounded-md border border-border">
    <table class="w-full min-w-[40rem] border-collapse text-sm">
      <thead>
        <!-- w-full on the name + w-[1%] on the rest gives leftover width to
             the title. max-w-0 on the cell is what lets MiddleEllipsis shrink. -->
        <tr class="bg-surface text-left">
          <th class="micro-label w-full px-3 py-2 font-semibold">{t('component.releaseTable.release')}</th>
          <th class="micro-label w-[1%] px-3 py-2 font-semibold">{t('component.releaseTable.quality')}</th>
          <th class="micro-label w-[1%] px-3 py-2 text-right font-semibold">{t('component.releaseTable.score')}</th>
          <th class="micro-label w-[1%] px-3 py-2 text-right font-semibold">{t('component.releaseTable.grab')}</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as release, index (release.guid || `${release.indexer_id}:${release.title}`)}
          {@const flags = releaseFlags(release)}
          {@const tv = compatBadge(release.compatibility)}
          {@const flagged = isFlagged(release)}
          {@const best = index === 0}
          {@const queued = release.queue_state}
          {@const rail = railTone(release, best)}
          <tr
            class="border-t border-border align-middle transition-colors duration-150
                   {rowTone(release, best)}">
            <td class="relative w-full max-w-0 px-3 py-2.5" title={release.title}>
              {#if rail}
                <span class="pointer-events-none absolute inset-y-0 left-0 w-0.5 {rail}" aria-hidden="true"></span>
              {/if}
              <div class="font-mono text-ink">
                <MiddleEllipsis text={release.title} />
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-ink-secondary">
                {#if best}
                  <span class="font-semibold uppercase tracking-wide text-accent-text">
                    {t('component.releaseTable.best')}
                  </span>
                {/if}
                <span>{facts(release)}</span>
              </div>
            </td>

            <td class="w-[1%] px-3 py-2.5">
              <div class="flex flex-wrap items-center gap-1.5">
                {#if release.parsed.quality && release.parsed.quality !== 'unknown'}
                  <Badge mono>{release.parsed.quality}</Badge>
                {/if}
                {#if release.parsed.source && release.parsed.source !== 'unknown'}
                  <Badge mono>{release.parsed.source}</Badge>
                {/if}
                {#if release.parsed.codec}
                  <Badge mono>{release.parsed.codec}</Badge>
                {/if}
                {#if release.parsed.proper}
                  <Badge mono tone="success">{t('component.releaseTable.proper')}</Badge>
                {/if}
                {#if release.parsed.repack}
                  <Badge mono tone="success">{t('component.releaseTable.repack')}</Badge>
                {/if}
                {#each flags as flag (flag.key)}
                  <Badge mono tone={flag.tone} title={flag.title}>{flag.label}</Badge>
                {/each}
                {#if tv}
                  <Badge mono tone={tv.tone} title={tv.title}>{tv.label}</Badge>
                {/if}
              </div>
            </td>

            <td
              class="w-[1%] whitespace-nowrap px-3 py-2.5 text-right font-mono {best ? 'text-accent-text' : 'text-ink-secondary'}"
              title={t('component.releaseTable.scoreHint')}>
              {releaseScore(release)}
            </td>

            <td class="w-[1%] whitespace-nowrap px-3 py-2.5">
              <div class="flex flex-col items-end gap-1">
                {#if queued === 'downloading'}
                  <span class="inline-flex items-center gap-1.5 text-sm font-medium text-accent-text">
                    <Icon name="download" size={14} />
                    {t('component.releaseTable.downloading')}
                  </span>
                {:else if queued === 'downloaded'}
                  <span class="inline-flex items-center gap-1.5 text-sm font-medium text-success">
                    <Icon name="check" size={14} />
                    {t('component.releaseTable.downloaded')}
                  </span>
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busyGUID !== null}
                    title={flagged ? flags.map((f) => f.title).join(' ') : undefined}
                    onclick={() => ongrab(release)}>
                    {busyGUID === release.guid
                      ? t('component.releaseTable.grabbing')
                      : t('component.releaseTable.grabAgain')}
                  </Button>
                {:else}
                  <Button
                    variant={best ? 'primary' : 'secondary'}
                    size="sm"
                    disabled={busyGUID !== null}
                    title={flagged ? flags.map((f) => f.title).join(' ') : undefined}
                    onclick={() => ongrab(release)}>
                    <Icon name="download" size={14} />
                    {busyGUID === release.guid ? t('component.releaseTable.grabbing') : grabLabel}
                  </Button>
                {/if}
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if remaining > 0}
    <div class="mt-2 flex items-center justify-center gap-3">
      <Button
        variant="secondary"
        size="sm"
        onclick={() => (visibleCount += pageSize)}>
        {tp('component.releaseTable.showMore', Math.min(pageSize, remaining))}
      </Button>
      <span class="text-sm text-ink-muted">
        {t('component.releaseTable.showingOf', { shown: String(rows.length), total: String(sorted.length) })}
      </span>
    </div>
  {/if}
{/if}
