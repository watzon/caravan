<script lang="ts">
  /**
   * A popover body that is a list of known options: the genre checklist, the
   * language picker, the site-scope ladder.
   *
   * It handles both cardinalities because they differ by one line — a
   * multi-select toggles and stays open, a single-select replaces and closes —
   * and two components that differed only there would drift in the parts that
   * are the same: the tick, the row height, the empty state, the arrow keys.
   *
   * Options are `{ id, name }` whatever they came from, so the caller converts
   * once at the boundary rather than this knowing about four provider shapes.
   */
  import { moveResultFocus } from '../typeahead';
  import Icon from './Icon.svelte';

  interface Option {
    id: string;
    name: string;
    /** The second line, when two options need telling apart. */
    hint?: string;
  }

  interface Props {
    options: Option[];
    /** Ids currently chosen. A single-select passes at most one. */
    selected: readonly string[];
    onselect: (id: string) => void;
    /** "No genres" — what to say when the provider gave nothing. */
    emptyText?: string;
    loading?: boolean;
  }

  let { options, selected, onselect, emptyText = 'Nothing to choose from', loading = false }: Props =
    $props();

  let list = $state<HTMLElement | null>(null);
</script>

<div bind:this={list} class="flex flex-col gap-1">
  {#if loading}
    <p class="px-2 py-1.5 text-sm text-ink-muted">Loading…</p>
  {:else if options.length === 0}
    <p class="px-2 py-1.5 text-sm text-ink-muted">{emptyText}</p>
  {:else}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <ul
      class="-mx-1 flex max-h-64 flex-col overflow-y-auto"
      onkeydown={(event) => moveResultFocus(event, list)}>
      {#each options as option (option.id)}
        {@const on = selected.includes(option.id)}
        <li>
          <button
            type="button"
            aria-pressed={on}
            onclick={() => onselect(option.id)}
            class="flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left
                   text-base transition-colors duration-150 ease-out
                   {on ? 'bg-accent-tint text-accent-text' : 'text-ink hover:bg-raised'}">
            <span class="min-w-0">
              <span class="block truncate">{option.name}</span>
              {#if option.hint}
                <span class="block truncate text-sm text-ink-muted">{option.hint}</span>
              {/if}
            </span>
            {#if on}
              <Icon name="check" size={12} />
            {/if}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>
