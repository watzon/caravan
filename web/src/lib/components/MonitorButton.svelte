<script lang="ts">
  /**
   * The monitored flag as a 32px icon toggle, on every detail page.
   *
   * It replaced a switch with a "Monitored" label beside it. The label was the
   * only thing in the action row that spelled its state in words, and it cost
   * more width than the three buttons around it; a bookmark that is either lit
   * or not says the same thing in a quarter of the space, and the word survives
   * where a word actually helps — the accessible name and the tooltip, which
   * both say what a click will DO rather than only what is true now.
   *
   * It is a button, not a checkbox or a switch: the pages it sits on treat
   * monitoring as an action with a server round trip behind it, and a control
   * that flips instantly and then reverts on failure would be a lie. The state
   * is reported with aria-pressed, which is what a toggle button uses.
   *
   * There was no icon-toggle primitive to extend — Toggle.svelte is the labeled
   * switch this replaces, and Button.svelte has no pressed state — so this is
   * the one, shared by all three detail pages so they cannot drift.
   */
  import { useI18n } from '../i18n.svelte';
  import Icon from './Icon.svelte';

  interface Props {
    monitored: boolean;
    /** Named in the accessible label, e.g. "Dune" — never just "this item". */
    subject: string;
    disabled?: boolean;
    onchange: (next: boolean) => void;
  }

  let { monitored, subject, disabled = false, onchange }: Props = $props();
  const { t } = useI18n();

  // What a click does, not merely what is true: a control whose name is its
  // state leaves a screen-reader user to guess what pressing it means.
  let label = $derived(
    monitored
      ? t('component.monitor.stop', { subject })
      : t('component.monitor.start', { subject }),
  );
</script>

<button
  type="button"
  aria-pressed={monitored}
  aria-label={label}
  title={label}
  {disabled}
  onclick={() => onchange(!monitored)}
  class="inline-flex size-8 shrink-0 items-center justify-center rounded-md border
         transition-colors duration-150 ease-out disabled:opacity-50
         {monitored
    ? 'border-accent bg-accent-tint text-accent-text'
    : 'border-transparent text-ink-muted hover:bg-raised hover:text-ink'}">
  <Icon name="bookmark" size={16} class={monitored ? 'fill-current' : ''} />
</button>
