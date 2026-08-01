<script lang="ts">
  /** DESIGN.md §5: page title left, global search (⌘K) and system health right. */
  import type { Snippet } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import { system } from '../state/system.svelte';
  import { TONE_DOT, type Tone } from '../status';

  interface Props {
    title: string;
    onsearch: () => void;
    actions?: Snippet;
  }

  let { title, onsearch, actions }: Props = $props();

  let health = $derived.by((): { tone: Tone; label: string } => {
    if (system.error) return { tone: 'danger', label: 'Server unreachable' };
    const s = system.status;
    if (!s) return { tone: 'neutral', label: 'Checking' };
    if (s.dirty) return { tone: 'danger', label: 'Dirty shutdown' };
    if (s.engine_health === 'ok') return { tone: 'success', label: 'Healthy' };
    if (s.engine_health === 'degraded') return { tone: 'warning', label: 'Degraded' };
    return { tone: 'danger', label: s.engine_health || 'Unknown' };
  });

  const isMac =
    typeof navigator !== 'undefined' && /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent);
</script>

<header
  class="sticky top-0 z-30 flex h-16 items-center gap-4 border-b border-border bg-bg/95 px-6 backdrop-blur">
  <h1 class="flex-1 truncate font-display text-xl font-semibold tracking-tight text-ink">
    {title}
  </h1>

  {#if actions}
    {@render actions()}
  {/if}

  <button
    type="button"
    onclick={onsearch}
    class="flex h-8 items-center gap-2 rounded-md border border-border-strong bg-raised px-3
           text-base text-ink-muted transition-colors duration-150 ease-out hover:bg-overlay hover:text-ink-secondary">
    <Icon name="search" size={14} />
    <span class="hidden sm:inline">Add movie or series</span>
    <kbd class="ml-2 rounded-sm bg-surface px-1.5 py-0.5 font-mono text-xs text-ink-muted">
      {isMac ? '⌘' : 'Ctrl'}K
    </kbd>
  </button>

  <div class="flex items-center gap-2" title="System health">
    <span class="size-2 shrink-0 rounded-full {TONE_DOT[health.tone]}"></span>
    <span class="hidden text-sm text-ink-secondary lg:inline">{health.label}</span>
  </div>
</header>
