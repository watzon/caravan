<script lang="ts">
  /**
   * Provider link chips for a detail header: small external links to the
   * item's TMDB/IMDb/TVDB pages. Wordmark text rather than brand logos keeps
   * the bundle owned (DESIGN.md §8) and the row consistent with the badge
   * vocabulary.
   */
  import type { MetadataLink } from '../metadataLinks';
  import Icon from './Icon.svelte';

  interface Props {
    links: MetadataLink[];
  }

  let { links }: Props = $props();
</script>

{#if links.length > 0}
  <div class="flex flex-wrap items-center gap-1.5">
    {#each links as link (link.label)}
      <a
        href={link.href}
        target="_blank"
        rel="noopener noreferrer"
        title={`Open on ${link.label}`}
        class="inline-flex h-6 items-center gap-1 rounded-full border border-border bg-surface px-2.5
               font-mono text-xs text-ink-secondary transition-colors duration-150 ease-out
               hover:border-accent hover:bg-accent-tint hover:text-accent-text">
        {link.label}
        <Icon name="link" size={10} />
      </a>
    {/each}
  </div>
{/if}
