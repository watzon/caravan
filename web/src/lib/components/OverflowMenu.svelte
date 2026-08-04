<script lang="ts">
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

  let open = $state(false);
  let trigger = $state<HTMLButtonElement | null>(null);
  let menu = $state<HTMLElement | null>(null);

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

  /** Down/Up walk the items; the browser's own Tab order still works. */
  function onmenukeydown(event: KeyboardEvent) {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    const buttons = [...(menu?.querySelectorAll<HTMLElement>('button:not(:disabled)') ?? [])];
    if (buttons.length === 0) return;
    event.preventDefault();
    const index = buttons.indexOf(document.activeElement as HTMLElement);
    const next = event.key === 'ArrowDown' ? index + 1 : index - 1;
    buttons[Math.max(0, Math.min(next, buttons.length - 1))]?.focus();
  }

  $effect(() => {
    if (!open) return;
    // Focus the first item on open: a menu you have to Tab into is a menu the
    // keyboard cannot really reach.
    menu?.querySelector<HTMLElement>('button:not(:disabled)')?.focus();
  });
</script>

<svelte:window {onkeydown} onpointerdowncapture={onpointerdown} />

<div class="relative">
  <button
    bind:this={trigger}
    type="button"
    aria-haspopup="menu"
    aria-expanded={open}
    aria-label="More actions for {subject}"
    title="More actions"
    onclick={() => (open = !open)}
    class="inline-flex size-8 shrink-0 items-center justify-center rounded-md border
           border-transparent text-ink-secondary transition-colors duration-150 ease-out
           hover:bg-raised hover:text-ink
           {open ? 'bg-raised text-ink' : ''}">
    <Icon name="more" size={16} />
  </button>

  {#if open}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div
      bind:this={menu}
      role="menu"
      aria-label="More actions"
      onkeydown={onmenukeydown}
      class="absolute right-0 top-full z-30 mt-1 min-w-44 overflow-hidden rounded-lg border
             border-border-strong bg-overlay py-1 shadow-2xl">
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
