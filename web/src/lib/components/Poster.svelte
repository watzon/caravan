<script lang="ts">
  /** 2:3 poster with a graceful fallback — posters are the interface (DESIGN.md §2.1). */
  import { posterSrc } from '../api/client';
  import Icon, { type IconName } from './Icon.svelte';

  interface Props {
    path: string | null | undefined;
    /** Provider artwork URL to try when the local file fails to load. */
    fallback?: string | null | undefined;
    alt: string;
    fallbackIcon?: IconName;
    /**
     * 'cover' fills the frame — right for real posters. 'contain' floats the
     * image at its own aspect on the surface, padded — for artwork that is a
     * mark rather than a poster (site logos are square or wide, and
     * cover-cropping one into portrait produces a blurry stretch).
     */
    fit?: 'cover' | 'contain';
    /**
     * 'poster' is the 2:3 movie/series shape. 'video' is 16:9, for tiles whose
     * artwork is a wide mark — a banner logo in a tall frame is mostly empty
     * frame.
     */
    aspect?: 'poster' | 'video';
    class?: string;
  }

  let {
    path,
    fallback = undefined,
    alt,
    fallbackIcon = 'film',
    fit = 'cover',
    aspect = 'poster',
    class: klass = '',
  }: Props = $props();

  // The source chain is local artwork first, provider artwork second, icon
  // last. A local poster whose file is gone (deleted library tree, stale
  // row) must degrade to the provider URL, not straight to the icon.
  let sources = $derived(
    [posterSrc(path), posterSrc(fallback)].filter((s): s is string => !!s),
  );
  let stage = $state(0);
  let src = $derived(stage < sources.length ? sources[stage] : null);

  $effect(() => {
    // A new item resets the chain.
    path;
    fallback;
    stage = 0;
  });
</script>

<div
  class="relative {aspect === 'video'
    ? 'aspect-video'
    : 'aspect-[2/3]'} w-full overflow-hidden rounded-md bg-raised {klass}">
  {#if src}
    <img
      {src}
      {alt}
      loading="lazy"
      decoding="async"
      class="size-full {fit === 'contain' ? 'object-contain p-4' : 'object-cover'}"
      onerror={() => (stage += 1)} />
  {:else}
    <div
      class="flex size-full items-center justify-center text-ink-muted"
      role={alt ? 'img' : undefined}
      aria-label={alt || undefined}>
      <Icon name={fallbackIcon} size={28} />
    </div>
  {/if}
</div>
