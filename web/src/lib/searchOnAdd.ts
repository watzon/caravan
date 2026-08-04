/**
 * "Start searching immediately" is sticky per browser: it is a habit, not a
 * per-item decision, and Sonarr/Radarr users expect the box to remember. It
 * defaults on — adding something you do not want searched is the rarer case.
 *
 * The discover add/request modal is what still reads it. The ⌘K add dialog
 * (AddItemModal) does not: its search box is nested under "Add and monitor",
 * and both start off and are re-chosen per add — a remembered "monitor and
 * search everything" is the accident that pairing is there to prevent.
 */

const SEARCH_ON_ADD_KEY = 'caravan.searchOnAdd';

export function readSearchOnAdd(): boolean {
  try {
    return window.localStorage.getItem(SEARCH_ON_ADD_KEY) !== '0';
  } catch {
    // Private mode, or storage disabled: the default still applies.
    return true;
  }
}

export function writeSearchOnAdd(next: boolean): void {
  try {
    window.localStorage.setItem(SEARCH_ON_ADD_KEY, next ? '1' : '0');
  } catch {
    // The in-memory choice still governs this add.
  }
}
