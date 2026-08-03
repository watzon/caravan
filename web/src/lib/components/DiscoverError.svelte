<script lang="ts">
  /**
   * The two ways discover fails, told apart (SPEC §13: failures are visible and
   * actionable). 503 is "no TMDB key" — a setup problem with a destination, not
   * something a retry will fix. Everything else is the provider being unhappy,
   * which is what the retry is for.
   */
  import Button from './Button.svelte';
  import EmptyState from './EmptyState.svelte';
  import LoadError from './LoadError.svelte';

  interface Props {
    message: string;
    /** ApiError status; 503 means no metadata provider is configured. */
    status?: number;
    onretry?: () => void;
  }

  let { message, status = 0, onretry }: Props = $props();
</script>

{#if status === 503}
  <EmptyState
    icon="settings"
    title="No metadata provider configured"
    message="Discover browses TMDB, so it needs an API key. Add one under Settings → Metadata and this screen fills in.">
    {#snippet action()}
      <Button variant="primary" href="/settings/metadata">Open metadata settings</Button>
    {/snippet}
  </EmptyState>
{:else}
  <LoadError {message} {onretry} />
{/if}
