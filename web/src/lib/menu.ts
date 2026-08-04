/**
 * What an overflow menu is made of. It lives in a plain module rather than in
 * OverflowMenu.svelte so both the component and the pages that fill it — and
 * the tests that build one — describe an item the same way.
 */
export interface MenuItem {
  label: string;
  onselect: () => void;
  /** Destructive items are red, the way the button they replaced was. */
  danger?: boolean;
  disabled?: boolean;
}
