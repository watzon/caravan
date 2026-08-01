<script lang="ts">
  import { dismissToast, toasts } from '../state/toast.svelte';
  import { TONE_DOT, TONE_TINT } from '../status';
  import Icon from './Icon.svelte';
</script>

<div class="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-80 flex-col gap-2" aria-live="polite">
  {#each toasts.items as toast (toast.id)}
    <div
      class="pointer-events-auto flex items-center gap-3 overflow-hidden rounded-md border border-border {TONE_TINT[
        toast.tone
      ]}">
      <span class="w-0.5 self-stretch {TONE_DOT[toast.tone]}"></span>
      <p class="flex-1 py-3 text-base text-ink">{toast.message}</p>
      <button
        type="button"
        class="p-3 text-ink-secondary transition-colors duration-150 hover:text-ink"
        aria-label="Dismiss notification"
        onclick={() => dismissToast(toast.id)}>
        <Icon name="close" size={14} />
      </button>
    </div>
  {/each}
</div>
