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
  import { discoverHref, mediaTypeChip, ratingPresentation } from '../discover';
  import { UNKNOWN, titleWithYear } from '../format';
  import { useI18n } from '../i18n.svelte';
  import Badge from './Badge.svelte';
  import Icon from './Icon.svelte';
  import Poster from './Poster.svelte';

  interface Props {
    item: DiscoverItem;
    /** Show the MOVIE/SERIES chip — only meaningful on a mixed shelf. */
    showType?: boolean;
  }

  let { item, showType = false }: Props = $props();
  const { t } = useI18n();

  let rating = $derived(ratingPresentation(item.vote_average, item.vote_count, item.date));
  let accessibleLabel = $derived.by(() => {
    const parts = [
      item.year > 0
        ? titleWithYear(item.title, item.year)
        : t('component.discoverCard.yearUnknown', { title: item.title }),
    ];
    if (showType) parts.push(mediaTypeChip(item.media_type));
    parts.push(rating.title);
    if (item.in_library) parts.push(t('component.status.inLibrary'));
    else if (item.requested) parts.push(t('component.status.requested'));
    return parts.join(', ');
  });
</script>

<a
  href={discoverHref(item)}
  aria-label={accessibleLabel}
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
          <span class="inline-flex items-center gap-1"><Icon name="check" size={10} />{t('component.status.inLibrary')}</span>
        </Badge>
      </span>
    {:else if item.requested}
      <span class="absolute bottom-2 left-2">
        <Badge tone="warning">
          <span class="inline-flex items-center gap-1"><Icon name="clock" size={10} />{t('component.status.requested')}</span>
        </Badge>
      </span>
    {/if}
  </div>

  <div class="min-w-0">
    <p class="truncate text-sm font-medium text-ink" title={item.title}>{item.title}</p>
    <p class="flex items-center justify-between gap-2 text-sm text-ink-secondary">
      <span>{item.year > 0 ? item.year : UNKNOWN}</span>
      <Badge mono tone="neutral" title={rating.title}>
        <span class="inline-flex items-center gap-1">
          <Icon name="star" size={10} />
          {#if rating.text}
            {rating.text}
          {:else}
            <span class="sr-only">{rating.title}</span>
          {/if}
        </span>
      </Badge>
    </p>
  </div>
</a>
