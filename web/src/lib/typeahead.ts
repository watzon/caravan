/**
 * The keyboard half of a search-as-you-type list, shared by the ⌘K add flow and
 * the add-a-site picker.
 *
 * Pure DOM and no runes, the same split router.ts/router.svelte.ts uses: the
 * reactive half lives in typeahead.svelte.ts and can only run inside a
 * component, while these can be tested against a plain document fragment.
 *
 * A result row is addressed as "a button inside a ul" rather than through a
 * registry of element refs, because that is the markup both callers already
 * write — a registry is a second description of the list, and second
 * descriptions drift.
 */

/** How long after the last keystroke a search waits before going out. */
export const DEBOUNCE_MS = 250;

/** The shortest query worth sending to a provider. */
export const MIN_QUERY = 2;

/**
 * Move focus to the first result, reporting whether there was one. The caller
 * decides what to do with the key when there was not — swallowing ArrowDown on
 * an empty list would trap the key in a field that has nothing below it.
 */
export function focusFirstResult(container: HTMLElement | null): boolean {
  const first = container?.querySelector<HTMLElement>('ul button');
  if (!first) return false;
  first.focus();
  return true;
}

/**
 * Walk the result buttons with Up/Down. Up from the first hands focus back to
 * the search field, so the whole list is reachable without leaving the arrows;
 * Down from the last stays put, because a wrap in a list you are reading top to
 * bottom reads as a jump to somewhere else.
 */
export function moveResultFocus(event: KeyboardEvent, container: HTMLElement | null): void {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
  const buttons = [...(container?.querySelectorAll<HTMLElement>('ul button') ?? [])];
  const index = buttons.indexOf(event.target as HTMLElement);
  if (index === -1) return;
  event.preventDefault();
  if (event.key === 'ArrowDown') {
    buttons[Math.min(index + 1, buttons.length - 1)]?.focus();
  } else if (index === 0) {
    container?.querySelector<HTMLElement>('input')?.focus();
  } else {
    buttons[index - 1]?.focus();
  }
}
