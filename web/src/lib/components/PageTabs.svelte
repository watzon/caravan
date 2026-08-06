<script lang="ts" generics="T extends string">
  /**
   * The one tab strip for in-page section switching (Wanted, History,
   * Settings). Active tab gets the accent underline; the strip itself carries
   * the hairline separator, so tabs sit -mb-px over it.
   */
  interface Props {
    tabs: { key: T; label: string; count?: number | null }[];
    active: T;
    onchange: (key: T) => void;
    ariaLabel: string;
  }

  let { tabs, active, onchange, ariaLabel }: Props = $props();
</script>

<div class="flex gap-2 border-b border-border" role="group" aria-label={ariaLabel}>
  {#each tabs as item (item.key)}
    <button
      type="button"
      aria-pressed={active === item.key}
      onclick={() => onchange(item.key)}
      class="-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-base transition-colors duration-150 ease-out
             {active === item.key
        ? 'border-accent text-accent-text'
        : 'border-transparent text-ink-secondary hover:text-ink'}">
      {item.label}
      {#if item.count != null}
        <span class="text-xs {active === item.key ? 'text-accent-text' : 'text-ink-muted'}">
          {item.count}
        </span>
      {/if}
    </button>
  {/each}
</div>
