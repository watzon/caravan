<script lang="ts">
  /**
   * A popover body that searches a provider: cast & crew, studio, keyword,
   * performer, tag, site.
   *
   * The interaction is the ⌘K dialog's, so the machinery is the ⌘K dialog's:
   * `createTypeahead` for the debounce, the cancellation and the one-answer-on-
   * screen rule, and `moveResultFocus` for the arrows. Cancellation is the part
   * that matters here — the rail's popovers are opened and abandoned constantly
   * and every abandoned search is a request the server can stop reading (it
   * answers 499 and logs nothing else), which only works if the signal really
   * is wired through.
   *
   * Picking does NOT close the popover: "these two tags" is one thought, and a
   * popover that shut after the first would make the second a fresh trip.
   */
  import { createTypeahead } from '../typeahead.svelte';
  import { moveResultFocus, MIN_QUERY } from '../typeahead';
  import type { FilterRef } from '../explore';
  import { hasRef } from '../explore';
  import Icon from './Icon.svelte';

  interface Option {
    id: string;
    name: string;
    /** The line that tells two people with the same name apart. */
    hint?: string;
  }

  interface Props {
    /** One search. The signal is aborted the moment a newer keystroke lands. */
    search: (query: string, signal: AbortSignal) => Promise<Option[]>;
    selected: readonly FilterRef[];
    ontoggle: (ref: FilterRef) => void;
    placeholder: string;
    /** Named in the field's accessible label: "Search performers". */
    ariaLabel: string;
  }

  let { search, selected, ontoggle, placeholder, ariaLabel }: Props = $props();

  const typeahead = createTypeahead<Option[]>({
    run: (query, signal) => search(query, signal),
    blank: () => [],
  });

  let container = $state<HTMLElement | null>(null);

  /**
   * The chosen refs the current search does not show. Without them, picking two
   * people and then typing a third name would leave the first two with no
   * visible way back off — the chips row is the other one, but it is a scroll
   * away and the tick is where the eye already is.
   */
  let offscreen = $derived(selected.filter((ref) => !typeahead.results.some((o) => o.id === ref.id)));
</script>

<div bind:this={container} class="flex flex-col gap-2">
  <input
    type="search"
    {placeholder}
    aria-label={ariaLabel}
    bind:value={typeahead.query}
    onkeydown={(event) => {
      if (event.key !== 'ArrowDown') return;
      const first = container?.querySelector<HTMLElement>('ul button');
      if (!first) return;
      event.preventDefault();
      first.focus();
    }}
    class="h-9 w-full rounded-sm border border-border-strong bg-raised px-3 text-md text-ink
           placeholder:text-ink-muted focus:border-accent focus:outline-none" />

  {#if typeahead.error}
    <p class="px-1 text-sm text-danger">{typeahead.error}</p>
  {:else if typeahead.loading}
    <p class="px-1 text-sm text-ink-muted">Searching…</p>
  {:else if typeahead.idle}
    <p class="px-1 text-sm text-ink-muted">Type at least {MIN_QUERY} characters.</p>
  {:else if typeahead.results.length === 0}
    <p class="px-1 text-sm text-ink-muted">No matches for “{typeahead.trimmed}”.</p>
  {/if}

  {#if typeahead.results.length > 0 || offscreen.length > 0}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <ul
      class="-mx-1 flex max-h-64 flex-col overflow-y-auto"
      onkeydown={(event) => moveResultFocus(event, container)}>
      {#each typeahead.results as option (option.id)}
        {@const on = hasRef(selected, option.id)}
        <li>
          <button
            type="button"
            aria-pressed={on}
            onclick={() => ontoggle({ id: option.id, name: option.name })}
            class="flex w-full items-center justify-between gap-2 rounded-sm px-2 py-1.5 text-left
                   transition-colors duration-150 ease-out
                   {on ? 'bg-accent-tint text-accent-text' : 'text-ink hover:bg-raised'}">
            <span class="min-w-0">
              <span class="block truncate text-base">{option.name}</span>
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

      {#each offscreen as ref (ref.id)}
        <li>
          <button
            type="button"
            aria-pressed="true"
            onclick={() => ontoggle(ref)}
            class="flex w-full items-center justify-between gap-2 rounded-sm bg-accent-tint px-2
                   py-1.5 text-left text-base text-accent-text">
            <span class="truncate">{ref.name || ref.id}</span>
            <Icon name="check" size={12} />
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>
