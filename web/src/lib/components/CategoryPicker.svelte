<script lang="ts">
  /**
   * Category tree picker (indexer settings). Renders the tree the indexer
   * advertises in its capabilities document as checkboxes; the selection is
   * the flat id list the configuration stores. Checking a parent selects its
   * whole subtree explicitly — indexers are not required to expand parent ids
   * server-side (AnimeTosho does not), so the ids a search sends must be the
   * ids the user picked.
   *
   * Selection logic lives in lib/indexer.ts, pure and unit-tested; this
   * component only renders.
   */
  import type { IndexerCategory } from '../api/types';
  import { allCategoryIds, selectionState, toggleCategory, unknownCategoryIds } from '../indexer';
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    tree: IndexerCategory[];
    selected: number[];
    onchange: (ids: number[]) => void;
  }

  let { tree, selected, onchange }: Props = $props();

  let picked = $derived(new Set(selected));
  let advertised = $derived(allCategoryIds(tree));
  let unknown = $derived(unknownCategoryIds(picked, tree));
  let allSelected = $derived(advertised.length > 0 && advertised.every((id) => picked.has(id)));
  const { t, tp } = useI18n();
</script>

<div class="flex flex-col gap-2 rounded-md border border-border-strong bg-raised p-3">
  <div class="flex flex-wrap items-center gap-2">
    <p class="text-sm text-ink-secondary">
      {selected.length === 0
        ? t('component.categoryPicker.noneSelected')
        : tp('component.categoryPicker.selected', selected.length)}
    </p>
    <div class="ml-auto flex gap-1">
      <Button
        variant="ghost"
        size="sm"
        disabled={allSelected}
        onclick={() => onchange([...new Set([...advertised, ...unknown])])}>
        {t('component.actions.selectAll')}
      </Button>
      <Button variant="ghost" size="sm" disabled={selected.length === 0} onclick={() => onchange([])}>
        {t('component.actions.clear')}
      </Button>
    </div>
  </div>

  {#snippet row(node: IndexerCategory, depth: number)}
    {@const state = selectionState(node, picked)}
    {@const label = node.name || t('component.categoryPicker.categoryFallback', { id: node.id })}
    <li>
      <button
        type="button"
        role="checkbox"
        aria-checked={state === 'all' ? 'true' : state === 'some' ? 'mixed' : 'false'}
        onclick={() => onchange([...toggleCategory(node, picked)])}
        class="flex w-full items-center gap-2 rounded-sm py-1.5 pr-2 text-left transition-colors duration-150 ease-out hover:bg-overlay"
        style="padding-left: {8 + depth * 24}px">
        <span
          class="flex size-4 shrink-0 items-center justify-center rounded-sm border
                 {state === 'none'
            ? 'border-border-strong bg-surface'
            : 'border-accent bg-accent text-ink-inverse'}">
          {#if state === 'all'}
            <Icon name="check" size={12} />
          {:else if state === 'some'}
            <Icon name="minus" size={12} />
          {/if}
        </span>
        <span
          class="min-w-0 flex-1 truncate text-sm {state === 'none' ? 'text-ink-secondary' : 'text-ink'}"
          title={label}>
          {label}
        </span>
        <span class="shrink-0 font-mono text-xs text-ink-muted">{node.id}</span>
      </button>
    </li>
    {#each node.subcats as sub (sub.id)}
      {@render row(sub, depth + 1)}
    {/each}
  {/snippet}

  <ul class="flex max-h-64 flex-col gap-0.5 overflow-y-auto">
    {#each tree as node (node.id)}
      {@render row(node, 0)}
    {/each}
  </ul>

  {#if unknown.length > 0}
    <div class="flex flex-wrap items-center gap-1.5 border-t border-border pt-2">
      <span class="text-xs text-ink-secondary">{t('component.categoryPicker.unadvertised')}</span>
      {#each unknown as id (id)}
        <button
          type="button"
          aria-label={t('component.categoryPicker.removeSelection', { id })}
          onclick={() => onchange(selected.filter((x) => x !== id))}
          class="flex h-5 items-center gap-1 rounded-sm bg-warning-tint px-1.5 font-mono text-xs
                 text-warning hover:text-ink"
          title={t('component.categoryPicker.removeSelection', { id })}>
          {id}
          <Icon name="close" size={10} />
        </button>
      {/each}
    </div>
  {/if}
</div>
