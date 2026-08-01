<script lang="ts">
  /**
   * DESIGN.md §5: status dot top-left of the poster, title + year below in
   * 13px, hover raises a hairline ring, no zoom gimmicks. The card is one link
   * whose accessible name is the title (DESIGN.md §7).
   */
  import { titleWithYear } from '../format';
  import type { StatusKey } from '../status';
  import Badge from './Badge.svelte';
  import Poster from './Poster.svelte';
  import StatusDot from './StatusDot.svelte';
  import type { IconName } from './Icon.svelte';

  interface Props {
    href: string;
    title: string;
    year: number;
    posterPath: string | null | undefined;
    /** Provider artwork URL used when the local poster fails to load. */
    posterUrl?: string | null;
    status: StatusKey;
    /** Quality of the owned file, or a short summary line ("6 / 10 episodes"). */
    quality?: string | null;
    note?: string | null;
    fallbackIcon?: IconName;
  }

  let {
    href,
    title,
    year,
    posterPath,
    posterUrl = undefined,
    status,
    quality,
    note,
    fallbackIcon = 'film',
  }: Props = $props();
</script>

<a
  {href}
  class="group flex flex-col gap-2 rounded-md focus:outline-none"
  aria-label={titleWithYear(title, year)}>
  <div
    class="relative rounded-md ring-1 ring-transparent transition-[box-shadow] duration-150 ease-out
           group-hover:ring-border-strong group-focus-visible:ring-accent">
    <Poster path={posterPath} fallback={posterUrl} alt="" {fallbackIcon} />

    <span class="absolute left-2 top-2 rounded-full bg-bg/70 p-1.5">
      <StatusDot {status} showLabel={false} />
    </span>

    {#if quality}
      <span class="absolute bottom-2 left-2">
        <Badge mono tone="neutral">{quality}</Badge>
      </span>
    {/if}
  </div>

  <div class="min-w-0">
    <p class="truncate text-sm font-medium text-ink" title={title}>{title}</p>
    <p class="truncate text-sm text-ink-secondary">
      {year > 0 ? year : '—'}{note ? ` · ${note}` : ''}
    </p>
  </div>
</a>
