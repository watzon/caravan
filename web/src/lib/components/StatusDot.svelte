<script lang="ts">
  /**
   * DESIGN.md §6/§7: 8px circle + label. Status is conveyed by dot AND text,
   * never colour alone — when the label is hidden it moves to a title/sr-only.
   */
  import { STATUS, TONE_DOT, type StatusKey } from '../status';

  interface Props {
    status: StatusKey;
    showLabel?: boolean;
    class?: string;
  }

  let { status, showLabel = true, class: klass = '' }: Props = $props();
  let meta = $derived(STATUS[status]);
</script>

<span class="inline-flex items-center gap-2 {klass}" title={showLabel ? undefined : meta.label}>
  <span class="size-2 shrink-0 rounded-full {TONE_DOT[meta.tone]}"></span>
  {#if showLabel}
    <span class="text-sm text-ink-secondary">{meta.label}</span>
  {:else}
    <span class="sr-only">{meta.label}</span>
  {/if}
</span>
