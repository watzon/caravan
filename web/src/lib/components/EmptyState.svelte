<script lang="ts">
  /**
   * DESIGN.md §6: every list ships an empty state — icon, one sentence, one
   * action. This is the visible-failure philosophy from SPEC §13 applied to
   * "nothing here yet".
   */
  import type { Snippet } from 'svelte';
  import Icon, { type IconName } from './Icon.svelte';

  interface Props {
    icon: IconName;
    title: string;
    message: string;
    action?: Snippet;
    class?: string;
  }

  let { icon, title, message, action, class: klass = '' }: Props = $props();
</script>

<div
  class="flex flex-col items-center justify-center gap-3 rounded-lg border border-border
         bg-surface px-6 py-16 text-center {klass}">
  <span class="flex size-12 items-center justify-center rounded-full bg-raised text-ink-muted">
    <Icon name={icon} size={22} />
  </span>
  <h2 class="font-display text-lg font-semibold text-ink">{title}</h2>
  <p class="max-w-md text-base text-ink-secondary">{message}</p>
  {#if action}
    <div class="mt-2">{@render action()}</div>
  {/if}
</div>
