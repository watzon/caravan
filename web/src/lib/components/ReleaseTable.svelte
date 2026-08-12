<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * The release result table, its skeleton, and its empty state (plan part B7).
   *
   * Lifted out of ReleaseSearch unchanged so the per-item picker and the
   * universal search render one table rather than two that drift. Every result
   * is grabbable, including the flagged ones — the UI de-emphasizes a bad
   * release, it does not decide for the user (SPEC §13).
   *
   * Scoring, flags and TV compatibility stay where they were: pure helpers in
   * release.ts and tvcompat.ts, called from the row.
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
  }

  let {
    releases,
    loading,
    busyGUID,
    grabLabel = t('component.releaseTable.grabDefault'),
    ongrab,
    emptyMessage = t('component.releaseTable.emptyDefault'),
  }: Props = $props();

  let rows = $derived(sortReleases(releases ?? []));

</script>

{#if loading}
  <div class="overflow-hidden rounded-md border border-border">
    {#each Array.from({ length: 6 }) as _, i (i)}
      <div class="flex items-center gap-3 border-b border-border px-3 py-3 last:border-b-0">
        <Skeleton class="h-4 w-24" />
        <Skeleton class="h-4 flex-1" />
        <Skeleton class="h-4 w-12" />
        <Skeleton class="h-4 w-16" />
        <Skeleton class="h-4 w-14" />
        <Skeleton class="h-7 w-16 rounded-md" />
      </div>
    {/each}
  </div>
{:else if rows.length === 0}
  <EmptyState icon="search" title={t('component.releaseTable.noReleasesFound')} message={emptyMessage}>
    {#snippet action()}
      <Button variant="secondary" href="/settings">{t('component.releaseTable.openIndexerSettings')}</Button>
    {/snippet}
  </EmptyState>
{:else}
  <div class="overflow-x-auto rounded-md border border-border">
    <table class="w-full min-w-[1000px] border-collapse text-sm">
      <thead>
        <tr class="bg-surface text-left">
          <th class="micro-label px-3 py-2 font-semibold">{t('component.releaseTable.source')}</th>
          <th class="micro-label px-3 py-2 font-semibold">{t('component.releaseTable.release')}</th>
          <th class="micro-label px-3 py-2 font-semibold">{t('component.releaseTable.age')}</th>
          <th class="micro-label px-3 py-2 text-right font-semibold">{t('component.releaseTable.size')}</th>
          <th class="micro-label px-3 py-2 text-right font-semibold">{t('component.releaseTable.peers')}</th>
          <th class="micro-label px-3 py-2 font-semibold">{t('component.releaseTable.quality')}</th>
          <th class="micro-label px-3 py-2 text-right font-semibold">{t('component.releaseTable.score')}</th>
          <th class="micro-label px-3 py-2 text-right font-semibold">{t('component.releaseTable.grab')}</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as release, index (release.guid || `${release.indexer_id}:${release.title}`)}
          {@const flags = releaseFlags(release)}
          {@const tv = compatBadge(release.compatibility)}
          {@const flagged = isFlagged(release)}
          {@const best = index === 0}
          <tr
            class="relative border-t border-border align-top transition-colors duration-150
                   {best ? 'bg-accent-tint' : 'hover:bg-raised'} {flagged ? 'opacity-60' : ''}">
            <td class="px-3 py-3">
              <span class="flex items-center gap-2">
                {#if best}
                  <span class="h-4 w-0.5 shrink-0 rounded-full bg-accent" aria-hidden="true"></span>
                  <span class="text-xs font-semibold uppercase tracking-wide text-accent-text">{t('component.releaseTable.best')}</span>
                {/if}
                <span class="truncate text-ink-secondary" title={release.indexer}>
                  {release.indexer || UNKNOWN}
                </span>
              </span>
            </td>

            <td class="w-full max-w-0 px-3 py-3 font-mono text-ink" title={release.title}>
              <MiddleEllipsis text={release.title} />
            </td>

            <td class="px-3 py-3 text-ink-secondary">{formatAge(release.published_at)}</td>

            <td class="px-3 py-3 text-right font-mono text-ink-secondary">
              {release.size > 0 ? formatBytes(release.size) : UNKNOWN}
            </td>

            <td class="px-3 py-3 text-right font-mono">
              {#if release.protocol === 'torrent'}
                <span class={release.seeders > 0 ? 'text-success' : 'text-danger'}>
                  {release.seeders}
                </span>
                <span class="text-ink-muted">/{release.leechers}</span>
              {:else}
                <span class="text-ink-muted">{UNKNOWN}</span>
              {/if}
            </td>

            <td class="px-3 py-3">
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

            <td class="px-3 py-3 text-right font-mono {best ? 'text-accent-text' : 'text-ink-secondary'}">
              {releaseScore(release)}
            </td>

            <td class="px-3 py-3">
              <div class="flex justify-end">
                <Button
                  variant={best ? 'primary' : 'secondary'}
                  size="sm"
                  disabled={busyGUID !== null}
                  title={flagged ? flags.map((f) => f.title).join(' ') : undefined}
                  onclick={() => ongrab(release)}>
                  <Icon name="download" size={14} />
                  {busyGUID === release.guid ? t('component.releaseTable.grabbing') : grabLabel}
                </Button>
              </div>
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}
