<script lang="ts">
  /** Library filter chips. Active chip uses the accent tint, same as active nav. */
  import { STATUS, TONE_DOT, type FilterChip, type StatusKey } from '../status';

  interface Props {
    chips: FilterChip[];
    active: StatusKey | 'all';
    onselect: (key: StatusKey | 'all') => void;
  }

  let { chips, active, onselect }: Props = $props();
</script>

<div class="flex flex-wrap items-center gap-2" role="group" aria-label="Filter library">
  {#each chips as chip (chip.key)}
    {@const selected = chip.key === active}
    <button
      type="button"
      aria-pressed={selected}
      onclick={() => onselect(chip.key)}
      class="inline-flex h-7 items-center gap-2 rounded-full border px-3 text-sm transition-colors duration-150 ease-out
             {selected
        ? 'border-accent bg-accent-tint text-accent-text'
        : 'border-border bg-surface text-ink-secondary hover:bg-raised hover:text-ink'}">
      {#if chip.key !== 'all'}
        <span class="size-2 rounded-full {TONE_DOT[STATUS[chip.key].tone]}"></span>
      {/if}
      <span>{chip.label}</span>
      <span class="font-mono text-xs text-ink-muted">{chip.count}</span>
    </button>
  {/each}
</div>
