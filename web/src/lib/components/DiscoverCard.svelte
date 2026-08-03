<script lang="ts">
  /**
   * A discover poster card: the library's PosterCard vocabulary (2:3 poster,
   * hairline ring on hover, 13px title) with the two things that differ on this
   * side of the app — a media-type chip on mixed shelves, and an availability
   * chip that answers "do I already have this, or has somebody already asked".
   *
   * It is not PosterCard with more props: that card's whole bottom-left corner
   * is the owned file's quality, and its status dot is a library state. Nothing
   * on a discover card has a file yet.
   */
  import type { DiscoverItem } from '../api/types';
  import { discoverHref, mediaTypeChip, ratingText } from '../discover';
  import { UNKNOWN, titleWithYear } from '../format';
  import Badge from './Badge.svelte';
  import Icon from './Icon.svelte';
  import Poster from './Poster.svelte';

  interface Props {
    item: DiscoverItem;
    /** Show the MOVIE/SERIES chip — only meaningful on a mixed shelf. */
    showType?: boolean;
  }

  let { item, showType = false }: Props = $props();

  let rating = $derived(ratingText(item.vote_average));
</script>

<a
  href={discoverHref(item)}
  aria-label={titleWithYear(item.title, item.year)}
  class="group/card flex w-full flex-col gap-2 rounded-md text-left focus:outline-none">
  <div
    class="relative rounded-md ring-1 ring-transparent transition-[box-shadow] duration-150 ease-out
           group-hover/card:ring-border-strong group-focus-visible/card:ring-accent">
    <Poster
      path={item.poster_url}
      alt=""
      fallbackIcon={item.media_type === 'movie' ? 'film' : 'tv'} />

    {#if showType}
      <span class="absolute left-2 top-2">
        <Badge mono tone="neutral">{mediaTypeChip(item.media_type)}</Badge>
      </span>
    {/if}

    <!-- Owned beats requested: once it is in the library the request is moot
         (the add absorbed it server-side). -->
    {#if item.in_library}
      <span class="absolute bottom-2 left-2">
        <Badge tone="success">
          <span class="inline-flex items-center gap-1"><Icon name="check" size={10} />IN LIBRARY</span>
        </Badge>
      </span>
    {:else if item.requested}
      <span class="absolute bottom-2 left-2">
        <Badge tone="warning">
          <span class="inline-flex items-center gap-1"><Icon name="clock" size={10} />REQUESTED</span>
        </Badge>
      </span>
    {/if}
  </div>

  <div class="min-w-0">
    <p class="truncate text-sm font-medium text-ink" title={item.title}>{item.title}</p>
    <p class="flex items-center justify-between gap-2 text-sm text-ink-secondary">
      <span>{item.year > 0 ? item.year : UNKNOWN}</span>
      {#if rating}
        <span class="inline-flex shrink-0 items-center gap-1 font-mono text-xs text-ink-muted">
          <Icon name="star" size={10} />{rating}
        </span>
      {/if}
    </p>
  </div>
</a>
