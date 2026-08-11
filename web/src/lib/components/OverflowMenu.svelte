<script lang="ts">
  import { useI18n } from '../i18n.svelte';
  /**
   * The ⋯ menu at the end of a detail page's action row: the actions that are
   * real but rare, kept out of the row where a red trash button used to sit one
   * mis-click from the search buttons.
   *
   * No dropdown primitive existed to extend — the app's other transient
   * surfaces are the Modal and SelectActions' pinned bar, neither of which is
   * an anchored menu — so this is it, written once and shared by every detail
   * page. It deliberately holds no state about WHAT the items do: a caller
   * passes items, and closing is this component's job.
   *
   * Dismissal is all three ways a menu has to close, because a menu that stays
   * open after the thing it opened is a menu floating over a modal: Escape,
   * a click anywhere outside, and choosing an item. Focus goes back to the
   * trigger every time, so a keyboard user is never dropped at the top of the
   * document.
   */
  import type { MenuItem } from '../menu';
  import Icon from './Icon.svelte';

  interface Props {
    items: MenuItem[];
    /** Named in the trigger's accessible label, e.g. "Dune". */
    subject: string;
  }

  let { items, subject }: Props = $props();

  const overflowMenuID = $props.id();
  const menuID = `${overflowMenuID}-menu`;

  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);
  let menu = $state<HTMLElement | null>(null);
  let focusLastOnOpen = $state(false);
  let menuLeft = $state(0);
  let menuTop = $state(0);

  function close(refocus = true) {
    if (!open) return;
    open = false;
    if (refocus) trigger?.focus();
  }

  function choose(item: MenuItem) {
    if (item.disabled) return;
    close();
    // After the menu is gone: an item that opens a dialog must not have this
    // one still on top of it.
    item.onselect();
  }

  function toggle() {
    if (open) {
      close();
      return;
    }
    focusLastOnOpen = false;
    open = true;
  }

  function ontriggerkeydown(event: KeyboardEvent) {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    focusLastOnOpen = event.key === 'ArrowUp';
    if (open) {
      const buttons = [...(menu?.querySelectorAll<HTMLElement>('button:not(:disabled)') ?? [])];
      buttons[focusLastOnOpen ? buttons.length - 1 : 0]?.focus();
      return;
    }
    open = true;
  }

  function positionMenu() {
    if (!open || !trigger || !menu) return;
    const gutter = 16;
    const maxWidth = Math.max(0, window.innerWidth - gutter * 2);
    const width = Math.min(menu.offsetWidth || Math.min(176, maxWidth), maxWidth);
    const triggerRect = trigger.getBoundingClientRect();
    const maxLeft = Math.max(gutter, window.innerWidth - width - gutter);
    menuLeft = Math.round(Math.min(Math.max(triggerRect.right - width, gutter), maxLeft));
    menuTop = Math.round(triggerRect.bottom + 4);
  }

  // Pointer-down rather than click, so a menu never survives the press that
  // started somewhere else — and capture, so it fires before the thing under
  // the pointer reacts.
  function onpointerdown(event: PointerEvent) {
    if (!open) return;
    const target = event.target as Node;
    if (trigger?.contains(target) || menu?.contains(target)) return;
    close(false);
  }

  function onkeydown(event: KeyboardEvent) {
    if (!open || event.key !== 'Escape') return;
    // Stopped so a menu inside a dialog does not close the dialog too.
    event.stopPropagation();
    close();
  }

  /** Arrow keys, Home, and End keep focus within the menu; Tab dismisses it. */
  function onmenukeydown(event: KeyboardEvent) {
    if (event.key === 'Tab') {
      close();
      return;
    }
    const buttons = [...(menu?.querySelectorAll<HTMLElement>('button:not(:disabled)') ?? [])];
    if (buttons.length === 0) return;
    const index = buttons.indexOf(document.activeElement as HTMLElement);
    let next: number;
    if (event.key === 'ArrowDown') {
      next = (index + 1) % buttons.length;
    } else if (event.key === 'ArrowUp') {
      next = (index - 1 + buttons.length) % buttons.length;
    } else if (event.key === 'Home') {
      next = 0;
    } else if (event.key === 'End') {
      next = buttons.length - 1;
    } else {
      return;
    }
    event.preventDefault();
    buttons[next]?.focus();
  }

  $effect(() => {
    positionMenu();
    if (!open) return;
    // Focus an item on open: a menu you have to Tab into is a menu the
    // keyboard cannot really reach. ArrowUp opens at the last item.
    const buttons = [...(menu?.querySelectorAll<HTMLElement>('button:not(:disabled)') ?? [])];
    buttons[focusLastOnOpen ? buttons.length - 1 : 0]?.focus();
  });

  $effect(() => {
    if (!open) return;
    // The app scrolls an inner main panel, not the window. A fixed menu would
    // otherwise stay behind after its trigger moved, so any scroll dismisses it.
    const dismissOnScroll = () => {
      if (!open) return;
      open = false;
      trigger?.focus({ preventScroll: true });
    };
    document.addEventListener('scroll', dismissOnScroll, true);
    window.addEventListener('scroll', dismissOnScroll, true);
    return () => {
      document.removeEventListener('scroll', dismissOnScroll, true);
      window.removeEventListener('scroll', dismissOnScroll, true);
    };
  });

  const { t, tp } = useI18n();
</script>

<svelte:window
  {onkeydown}
  onpointerdowncapture={onpointerdown}
  onresize={positionMenu} />

<div class="relative">
  <button
    bind:this={trigger}
    type="button"
    aria-haspopup="menu"
    aria-expanded={open}
    aria-controls={menuID}
    aria-label={t('component.overflowMenu.moreActionsFor', { subject })}
    title={t('component.overflowMenu.moreActionsFor', { subject })}
    onkeydown={ontriggerkeydown}
    onclick={toggle}
    class="inline-flex size-8 shrink-0 items-center justify-center rounded-md border
           border-transparent text-ink-secondary transition-colors duration-150 ease-out
           hover:bg-raised hover:text-ink
           {open ? 'bg-raised text-ink' : ''}">
    <Icon name="more" size={16} />
  </button>

  {#if open}
    <div
      id={menuID}
      bind:this={menu}
      role="menu"
      tabindex="-1"
      aria-label={t('component.overflowMenu.moreActionsFor', { subject })}
      onkeydown={onmenukeydown}
      class="fixed z-30 min-w-44 max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg border
             border-border-strong bg-overlay py-1 shadow-2xl"
      style="left: {menuLeft}px; top: {menuTop}px">
      {#each items as item (item.label)}
        <button
          type="button"
          role="menuitem"
          disabled={item.disabled}
          onclick={() => choose(item)}
          class="block w-full px-3 py-1.5 text-left text-sm transition-colors duration-150
                 ease-out disabled:opacity-50
                 {item.danger ? 'text-danger hover:bg-danger/10' : 'text-ink hover:bg-raised'}">
          {item.label}
        </button>
      {/each}
    </div>
  {/if}
</div>
