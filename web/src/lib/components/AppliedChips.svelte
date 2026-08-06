<script lang="ts">
  /**
   * The applied-filter row: one chip per fact, each removable, and Clear all.
   *
   * It is the answer to "what am I actually looking at" — the pills above it
   * say what CAN be filtered, and this says what IS. Without it a filter set
   * three popovers ago is invisible, which is how people end up convinced the
   * catalogue is empty.
   *
   * `FilterChips.svelte` is a different component doing a different job: those
   * are the mutually exclusive status tabs on a list screen (one is always
   * active), and these are an additive set that is usually empty.
   */
  import type { AppliedChip } from '../explore';
  import type { Snippet } from 'svelte';
  import Icon from './Icon.svelte';

  interface Props {
    chips: AppliedChip[];
    onremove: (key: string) => void;
    onclear: () => void;
    /** Extra controls that belong beside the chips — the tags any/all mode. */
    trailing?: Snippet;
  }

  let { chips, onremove, onclear, trailing }: Props = $props();
</script>

{#if chips.length > 0 || trailing}
  <div class="flex flex-wrap items-center gap-2" role="group" aria-label="Applied filters">
    {#each chips as chip (chip.key)}
      <span
        class="inline-flex h-7 max-w-full items-center gap-1.5 rounded-full border border-accent
               bg-accent-tint pl-3 pr-1.5 font-mono text-xs text-accent-text">
        <span class="min-w-0 truncate" title={chip.label}>{chip.label}</span>
        <button
          type="button"
          aria-label="Remove filter {chip.label}"
          onclick={() => onremove(chip.key)}
          class="inline-flex size-4 shrink-0 items-center justify-center rounded-full
                 transition-colors duration-150 ease-out hover:bg-accent hover:text-ink-inverse">
          <Icon name="close" size={10} />
        </button>
      </span>
    {/each}

    {#if trailing}{@render trailing()}{/if}

    {#if chips.length > 0}
      <button
        type="button"
        onclick={onclear}
        class="text-sm text-ink-secondary underline-offset-2 transition-colors duration-150
               ease-out hover:text-ink hover:underline">
        Clear all
      </button>
    {/if}
  </div>
{/if}
