<script lang="ts">
  /**
   * The two ways discover fails, told apart (SPEC §13: failures are visible and
   * actionable). A credential fault — no TMDB key, or one the provider refused
   * — is a setup problem with a destination, not something a retry will fix.
   * Everything else is the provider being unhappy, which is what the retry is
   * for.
   *
   * "No key" and "key refused" need different sentences because they need
   * different actions: enter one, or correct one. A 503 with no error code
   * still reads as absent, which is what every such route meant before the code
   * existed.
   */
  import type { CredentialFault } from '../credentials';
  import CredentialEmptyState from './CredentialEmptyState.svelte';
  import LoadError from './LoadError.svelte';

  interface Props {
    message: string;
    /** The credential fault the failed call reported, if it reported one. */
    fault?: CredentialFault | null;
    onretry?: () => void;
  }

  let { message, fault = null, onretry }: Props = $props();
</script>

{#if fault}
  <CredentialEmptyState {fault} />
{:else}
  <LoadError {message} {onretry} />
{/if}
