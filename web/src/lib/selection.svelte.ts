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
export interface Selection<ID extends string | number = number> {
  readonly active: boolean;
  readonly ids: ID[];
  readonly count: number;
  has(id: ID): boolean;
  toggle(id: ID): void;
  /** Replace the held ids. An empty list deactivates the selection. */
  replace(ids: readonly ID[]): void;
  /** Drop the whole selection, which also deactivates it. */
  clear(): void;
}

export function createSelection<ID extends string | number = number>(): Selection<ID> {
  let ids = $state<ID[]>([]);

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
    has: (id) => ids.includes(id),
    toggle(id) {
      ids = ids.includes(id) ? ids.filter((other) => other !== id) : [...ids, id];
    },
    replace(next) {
      ids = [...next];
    },
    clear() {
      ids = [];
    },
  };
}
