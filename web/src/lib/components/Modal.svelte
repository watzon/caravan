<script lang="ts">
  /** Dialog on --color-overlay, radius-lg (DESIGN.md §3/§6). */
  import { tick } from 'svelte';
  import type { Snippet } from 'svelte';
  import { useI18n } from '../i18n.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    title: string;
    onclose: () => void;
    /**
     * Prevents accidental dismissal by the modal chrome. Caller-owned Save and
     * Cancel actions still decide for themselves whether to call `onclose`.
     */
    dirty?: boolean;
    /** Tailwind max-width class; modals are content-sized, not full-bleed. */
    width?: string;
    children: Snippet;
    footer?: Snippet;
  }

  let { title, onclose, dirty = false, width = 'max-w-2xl', children, footer }: Props = $props();
  const { t } = useI18n();

  const modalID = $props.id();
  const titleID = `${modalID}-title`;
  const discardDescriptionID = `${modalID}-discard-description`;
  const FOCUSABLE_SELECTOR = [
    'a[href]',
    'area[href]',
    'button:not([disabled])',
    'input:not([disabled]):not([type="hidden"])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    'iframe',
    'object',
    'embed',
    '[contenteditable]:not([contenteditable="false"])',
    '[tabindex]:not([tabindex="-1"])',
  ].join(',');

  let dialog = $state<HTMLElement | null>(null);
  let confirmingDiscard = $state(false);
  let closeAttemptFocus = $state<HTMLElement | null>(null);

  function focusableElements() {
    return Array.from(dialog?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ?? []).filter(
      (element) =>
        element.tabIndex >= 0 &&
        !element.closest('[aria-hidden="true"], [hidden], [inert]') &&
        element.getAttribute('aria-disabled') !== 'true',
    );
  }

  async function requestClose() {
    if (confirmingDiscard) return;
    if (!dirty) {
      onclose();
      return;
    }

    closeAttemptFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    confirmingDiscard = true;
    await tick();
    if (confirmingDiscard) {
      dialog
        ?.querySelector<HTMLButtonElement>('[data-modal-discard-actions] button')
        ?.focus();
    }
  }

  async function keepEditing() {
    confirmingDiscard = false;
    await tick();
    if (closeAttemptFocus?.isConnected) {
      closeAttemptFocus.focus();
      return;
    }
    (dialog?.querySelector<HTMLElement>('[autofocus]') ?? dialog)?.focus();
  }

  function discardChanges() {
    onclose();
  }

  function onkeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.stopPropagation();
      requestClose();
      return;
    }

    if (event.key !== 'Tab') return;

    const focusable = focusableElements();
    if (focusable.length === 0) {
      event.preventDefault();
      event.stopPropagation();
      dialog?.focus();
      return;
    }

    const activeElement = document.activeElement as HTMLElement | null;
    const activeIndex = focusable.indexOf(activeElement as HTMLElement);
    const movingBackward = event.shiftKey;
    const atBoundary = movingBackward
      ? activeIndex <= 0
      : activeIndex === -1 || activeIndex === focusable.length - 1;

    if (!atBoundary) return;

    event.preventDefault();
    event.stopPropagation();
    focusable[movingBackward ? focusable.length - 1 : 0].focus();
  }

  $effect(() => {
    const previous = document.activeElement as HTMLElement | null;
    // Native autofocus is unreliable on dynamically inserted nodes, and
    // focusing the dialog shell would steal it anyway — so honor a child's
    // autofocus here and fall back to the shell.
    (dialog?.querySelector<HTMLElement>('[autofocus]') ?? dialog)?.focus();
    // Restore rather than clear on close: a confirm opened from inside an
    // editor stacks two modals, and clearing would unlock page scroll while
    // the editor underneath is still open.
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = previousOverflow;
      previous?.focus?.();
    };
  });

</script>

<div class="fixed inset-0 z-50 flex items-start justify-center p-4 pt-16 sm:p-6 sm:pt-24">
  <div
    class="absolute inset-0 cursor-default bg-bg/80"
    aria-hidden="true"
    data-modal-backdrop
    onclick={requestClose}>
  </div>

  <div
    bind:this={dialog}
    role="dialog"
    aria-modal="true"
    aria-labelledby={titleID}
    aria-describedby={confirmingDiscard ? discardDescriptionID : undefined}
    tabindex="-1"
    {onkeydown}
    class="relative flex max-h-full w-full {width} flex-col overflow-hidden rounded-lg
           border border-border-strong bg-overlay shadow-2xl">
    <header class="flex items-center justify-between gap-4 border-b border-border px-4 py-3">
      <h2
        id={titleID}
        class="min-w-0 flex-1 truncate font-display text-md font-semibold tracking-tight text-ink"
        title={confirmingDiscard ? t('component.modal.discardTitle') : title}>
        {confirmingDiscard ? t('component.modal.discardTitle') : title}
      </h2>
      {#if !confirmingDiscard}
        <button
          type="button"
          class="rounded-sm p-1 text-ink-secondary transition-colors duration-150 hover:bg-raised hover:text-ink"
          aria-label={t('component.actions.close')}
          onclick={requestClose}>
          <Icon name="close" />
        </button>
      {/if}
    </header>

    {#if confirmingDiscard}
      <div data-modal-discard-confirmation class="min-h-0 flex-1 overflow-y-auto p-4">
        <p id={discardDescriptionID} class="text-base text-ink-secondary">
          {t('component.modal.discardDescription')}
        </p>
      </div>
      <footer
        data-modal-discard-actions
        class="flex flex-wrap items-center justify-end gap-2 border-t border-border px-4 py-3">
        <Button variant="secondary" onclick={keepEditing}>{t('component.modal.keepEditing')}</Button>
        <Button variant="danger" onclick={discardChanges}>{t('component.modal.discardChanges')}</Button>
      </footer>
    {:else}
      <div class="min-h-0 flex-1 overflow-y-auto">{@render children()}</div>

      {#if footer}
        <footer class="flex flex-wrap items-center justify-end gap-2 border-t border-border px-4 py-3">
          {@render footer()}
        </footer>
      {/if}
    {/if}
  </div>
</div>
