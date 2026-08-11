<script lang="ts">
  import type { IndexerCategory } from '../api/types';
  import { categoryGroups } from '../indexer';
  import { useI18n } from '../i18n.svelte';
  import Badge from './Badge.svelte';

  interface Props {
    tree: IndexerCategory[];
    selected: number[];
    label: string;
    tone?: 'accent' | 'neutral';
  }

  let { tree, selected, label, tone = 'neutral' }: Props = $props();
  let groups = $derived(categoryGroups(selected, tree));
  const { t } = useI18n();
</script>

<div class="flex flex-wrap items-start gap-3" role="list" aria-label={label}>
  {#each groups as group (group.id)}
    <div
      role="listitem"
      data-category-group={group.id}
      data-parent-selected={group.selected || undefined}
      class="flex w-fit max-w-full flex-col gap-1.5 rounded-md border border-border-strong bg-raised p-2">
      <span
        class="flex items-center gap-2 text-base font-semibold
               {group.selected
          ? tone === 'accent'
            ? 'text-accent-text'
            : 'text-ink'
          : 'text-ink-secondary'}"
        title={group.selected
          ? t('component.categorySummary.selectedGroup', { name: group.name })
          : t('component.categorySummary.group', { name: group.name })}>
        <span
          aria-hidden="true"
          class="size-1.5 shrink-0 rounded-full
                 {group.selected ? 'bg-accent' : 'border border-border-strong bg-surface'}">
        </span>
        {group.name}
        <span class="sr-only">{group.selected ? t('component.categorySummary.parentSelected') : t('component.categorySummary.categoryGroup')}</span>
      </span>

      {#if group.children.length > 0}
        <div class="flex min-w-0 items-stretch">
          <span
            aria-hidden="true"
            class="ml-1 mr-2 w-2.5 shrink-0 rounded-bl-md border-b border-l border-border-strong">
          </span>
          <div class="flex min-w-0 flex-wrap items-center gap-1 py-0.5">
            {#each group.children as category (category.id)}
              <Badge {tone} title={`${group.name} / ${category.name}`}>{category.name}</Badge>
            {/each}
          </div>
        </div>
      {/if}
    </div>
  {/each}
</div>
