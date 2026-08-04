<script lang="ts">
  /**
   * DESIGN.md §5: status dot top-left of the poster, title + year below in
   * 13px, hover raises a hairline ring, no zoom gimmicks. The card is one link
   * whose accessible name is the title (DESIGN.md §7).
   *
   * Selection starts on the card itself: with `ontoggle` set, hovering or
   * focusing reveals a check circle over the poster's top-right corner (always
   * shown on coarse pointers, which have no hover). The circle is a sibling of
   * the link, not a child — interactive elements do not nest. Once a selection
   * is active (`selectable`) the card becomes a toggle button instead: a link
   * that silently does not navigate is a lie to the keyboard and to
   * middle-click, and the circle drops to a decoration because the whole card
   * is the control.
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
    /** See Poster: 'contain' for mark-shaped artwork like site logos. */
    posterFit?: 'cover' | 'contain';
    /** See Poster: 'video' for tiles whose artwork is a wide mark. */
    posterAspect?: 'poster' | 'video';
    /** A selection is active: render as its toggle rather than a link. */
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
    posterFit = 'cover',
    posterAspect = 'poster',
    selectable = false,
    selected = false,
    ontoggle,
  }: Props = $props();

  const SHELL = 'group/card flex w-full flex-col gap-2 rounded-md text-left focus:outline-none';

  const CIRCLE = `flex size-5 items-center justify-center rounded-full border
    transition-opacity duration-150 ease-out`;
</script>

{#snippet card()}
  <div
    class="relative rounded-md ring-1 transition-[box-shadow] duration-150 ease-out
           {selected
      ? 'ring-2 ring-accent'
      : 'ring-transparent group-hover/card:ring-border-strong group-focus-visible/card:ring-accent'}">
    <Poster
      path={posterPath}
      fallback={posterUrl}
      alt=""
      {fallbackIcon}
      fit={posterFit}
      aspect={posterAspect} />

    <!-- flex, not inline: line-height would stretch the circle into a pill. -->
    <span
      class="absolute left-2 top-2 flex items-center justify-center rounded-full
             border border-border-strong bg-bg p-1.5">
      <StatusDot {status} showLabel={false} />
    </span>

    {#if selectable}
      <span
        class="{CIRCLE} absolute right-2 top-2
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
  <div class="group/card relative">
    <a {href} class={SHELL} aria-label={titleWithYear(title, year)}>
      {@render card()}
    </a>
    {#if ontoggle}
      <button
        type="button"
        class="{CIRCLE} absolute right-2 top-2 z-10 border-border-strong bg-bg text-ink-secondary
               opacity-0 hover:border-accent hover:text-accent focus-visible:opacity-100
               group-hover/card:opacity-100 group-focus-within/card:opacity-100 pointer-coarse:opacity-100"
        aria-label="Select {titleWithYear(title, year)}"
        onclick={() => ontoggle?.()}>
        <Icon name="check" size={12} />
      </button>
    {/if}
  </div>
{/if}
