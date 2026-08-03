<script lang="ts">
  /**
   * DESIGN.md §5: status dot top-left of the poster, title + year below in
   * 13px, hover raises a hairline ring, no zoom gimmicks. The card is one link
   * whose accessible name is the title (DESIGN.md §7).
   *
   * In the grids' select mode the card becomes a toggle button instead: a link
   * that silently does not navigate is a lie to the keyboard and to
   * middle-click. Without `selectable` — everywhere else — it is the plain link
   * it has always been.
   */
  import { titleWithYear } from '../format';
  import type { StatusKey } from '../status';
  import Badge from './Badge.svelte';
  import Icon from './Icon.svelte';
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
    /** Render as a selection toggle rather than a link. */
    selectable?: boolean;
    selected?: boolean;
    ontoggle?: () => void;
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
    selectable = false,
    selected = false,
    ontoggle,
  }: Props = $props();

  const SHELL = 'group flex flex-col gap-2 rounded-md text-left focus:outline-none';
</script>

{#snippet card()}
  <div
    class="relative rounded-md ring-1 transition-[box-shadow] duration-150 ease-out
           {selected
      ? 'ring-2 ring-accent'
      : 'ring-transparent group-hover:ring-border-strong group-focus-visible:ring-accent'}">
    <Poster path={posterPath} fallback={posterUrl} alt="" {fallbackIcon} />

    <!-- flex, not inline: line-height would stretch the circle into a pill. -->
    <span
      class="absolute left-2 top-2 flex items-center justify-center rounded-full
             border border-border-strong bg-bg p-1.5">
      <StatusDot {status} showLabel={false} />
    </span>

    {#if selectable}
      <span
        class="absolute right-2 top-2 flex size-5 items-center justify-center rounded-full border
               {selected
          ? 'border-accent bg-accent text-ink-inverse'
          : 'border-border-strong bg-bg text-transparent'}">
        <Icon name="check" size={12} />
      </span>
    {/if}

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
{/snippet}

{#if selectable}
  <button
    type="button"
    class={SHELL}
    aria-pressed={selected}
    aria-label={titleWithYear(title, year)}
    onclick={() => ontoggle?.()}>
    {@render card()}
  </button>
{:else}
  <a {href} class={SHELL} aria-label={titleWithYear(title, year)}>
    {@render card()}
  </a>
{/if}
