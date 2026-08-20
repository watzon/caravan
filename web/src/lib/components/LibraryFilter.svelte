<script lang="ts">
  /**
   * Multi-select library filter for combined surfaces (Wanted, Calendar).
   *
   * Empty means every library this session can see. Checking a shelf narrows
   * the list; checking several keeps those shelves together. It is not a
   * single-library toggle: the wanted and calendar screens mix kinds, and the
   * point is to show movies, or shows, or both.
   *
   * Hidden when there is nothing to choose between. One shelf would only
   * offer "all" versus that shelf, which is the same list.
   */
  import type { LibraryKind } from '../api/types';
  import { sessionFilterLibraries } from '../library';
  import { useI18n } from '../i18n.svelte';
  import { session } from '../state/session.svelte';
  import FilterOptions from './FilterOptions.svelte';
  import FilterPill from './FilterPill.svelte';

  interface Props {
    selected: readonly number[];
    onchange: (ids: number[]) => void;
    shape?: 'pill' | 'box';
  }

  let { selected, onchange, shape = 'box' }: Props = $props();
  const { t, tp } = useI18n();

  let libraries = $derived(sessionFilterLibraries(session.user));
  let options = $derived(
    libraries.map((library) => {
      const kind = kindLabel(library.kind);
      return {
        id: String(library.id),
        name: library.name,
        hint: kind === library.name ? undefined : kind,
      };
    }),
  );
  let selectedIds = $derived(selected.map(String));
  let selectedName = $derived(
    selected.length === 1 ? libraries.find((library) => library.id === selected[0])?.name : undefined,
  );
  let label = $derived(
    selected.length === 0
      ? t('component.libraryFilter.all')
      : (selectedName ?? tp('component.libraryFilter.selected', selected.length)),
  );

  function kindLabel(kind: LibraryKind): string {
    switch (kind) {
      case 'movie':
        return t('component.libraries.kindMovie');
      case 'tv':
        return t('component.libraries.kindSeries');
      case 'anime':
        return t('component.libraries.kindAnime');
      case 'adult':
        return t('component.libraries.kindAdult');
    }
  }

  function toggle(id: string) {
    const value = Number(id);
    onchange(
      selected.includes(value) ? selected.filter((libraryID) => libraryID !== value) : [...selected, value],
    );
  }
</script>

{#if libraries.length > 1}
  <FilterPill {label} applied={selected.length > 0} {shape} width="w-64">
    {#snippet children()}
      <FilterOptions
        {options}
        selected={selectedIds}
        onselect={toggle}
        emptyText={t('component.libraryFilter.empty')} />
    {/snippet}
  </FilterPill>
{/if}
