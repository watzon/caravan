<script lang="ts">
  /**
   * The indexers that did not answer.
   *
   * A release fan-out returns partial results by design: one dead tracker must
   * not blank a screen the other three filled. But a short list of results has
   * two very different explanations — "that is all there is" and "half your
   * indexers are down" — and only one of them is worth acting on. This is what
   * tells them apart, so a missing release is never silently a missing indexer
   * (SPEC §13).
   *
   * It renders nothing when every indexer answered: a healthy fan-out is not
   * news.
   */
  import type { IndexerError } from '../api/types';
  import Banner from './Banner.svelte';

  interface Props {
    errors: IndexerError[];
    /**
     * How many indexers were asked. It turns "2 failed" into "3 of 5
     * answered", which is the number that says whether the results are worth
     * trusting. Omitted when the caller does not know.
     */
    total?: number;
  }

  let { errors, total }: Props = $props();

  let title = $derived.by(() => {
    if (total !== undefined && total >= errors.length) {
      const answered = total - errors.length;
      return `${answered} of ${total} indexers answered`;
    }
    return `${errors.length} ${errors.length === 1 ? 'indexer' : 'indexers'} did not answer`;
  });

  // Named one per line rather than summarised: which indexer failed and why is
  // the whole point, and there are never many of them.
  let message = $derived(
    errors.map((e) => `${e.indexer || `Indexer ${e.indexer_id}`}: ${e.error}`).join(' · '),
  );
</script>

{#if errors.length > 0}
  <Banner tone="warning" icon="warning" {title} {message} />
{/if}
