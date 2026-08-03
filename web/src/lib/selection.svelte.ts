/**
 * Selection for the library grids. Movies and Series share it so the two
 * pages cannot drift on what "selected" means or on when a selection is
 * cleared.
 *
 * There is no separate mode flag: a selection is active exactly while it holds
 * something. The first card's check starts it, clearing it ends it — a "0
 * selected" state with an action bar and nothing to act on cannot exist.
 *
 * Ids are an array rather than a Set: a library grid holds hundreds of items,
 * not millions, and an array is what `$state` tracks without a reactive
 * wrapper.
 */
export interface Selection {
  readonly active: boolean;
  readonly ids: number[];
  readonly count: number;
  has(id: number): boolean;
  toggle(id: number): void;
  /** Drop the whole selection, which also deactivates it. */
  clear(): void;
}

export function createSelection(): Selection {
  let ids = $state<number[]>([]);

  return {
    get active() {
      return ids.length > 0;
    },
    get ids() {
      return ids;
    },
    get count() {
      return ids.length;
    },
    has: (id: number) => ids.includes(id),
    toggle(id: number) {
      ids = ids.includes(id) ? ids.filter((other) => other !== id) : [...ids, id];
    },
    clear() {
      ids = [];
    },
  };
}
