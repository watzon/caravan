<script lang="ts">
  /** 2:3 poster with a graceful fallback — posters are the interface (DESIGN.md §2.1). */
  import { posterSrc } from '../api/client';
  import Icon, { type IconName } from './Icon.svelte';

  interface Props {
    path: string | null | undefined;
    alt: string;
    fallbackIcon?: IconName;
    class?: string;
  }

  let { path, alt, fallbackIcon = 'film', class: klass = '' }: Props = $props();

  let src = $derived(posterSrc(path));
  let failed = $state(false);
</script>

<div class="relative aspect-[2/3] w-full overflow-hidden rounded-md bg-raised {klass}">
  {#if src && !failed}
    <img
      {src}
      {alt}
      loading="lazy"
      decoding="async"
      class="size-full object-cover"
      onerror={() => (failed = true)} />
  {:else}
    <div class="flex size-full items-center justify-center text-ink-muted">
      <Icon name={fallbackIcon} size={28} />
    </div>
  {/if}
</div>
