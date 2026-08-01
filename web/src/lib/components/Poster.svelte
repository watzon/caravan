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
    class?: string;
  }

  let {
    path,
    fallback = undefined,
    alt,
    fallbackIcon = 'film',
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

<div class="relative aspect-[2/3] w-full overflow-hidden rounded-md bg-raised {klass}">
  {#if src}
    <img
      {src}
      {alt}
      loading="lazy"
      decoding="async"
      class="size-full object-cover"
      onerror={() => (stage += 1)} />
  {:else}
    <div class="flex size-full items-center justify-center text-ink-muted">
      <Icon name={fallbackIcon} size={28} />
    </div>
  {/if}
</div>
