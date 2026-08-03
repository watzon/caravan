/**
 * Select mode for the library grids. Movies and Series share it so the two
 * pages cannot drift on what "selected" means or on when a selection is
 * cleared.
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
  /** Enter select mode with nothing selected. */
  enter(): void;
  /** Leave select mode, discarding the selection. */
  exit(): void;
}

export function createSelection(): Selection {
  let active = $state(false);
  let ids = $state<number[]>([]);

  return {
    get active() {
      return active;
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
    enter() {
      // Entering clears: a selection that survived a previous session of select
      // mode would act on items the user can no longer see highlighted.
      ids = [];
      active = true;
    },
    exit() {
      ids = [];
      active = false;
    },
  };
}
