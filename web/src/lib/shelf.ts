/**
 * The sorting and library-scoping the three shelf grids share.
 *
 * Movies, Series and Anime each render their own template — their chips,
 * empty states and card notes genuinely differ — but "added, then title",
 * "title", "status, then title" and "only this library's rows" are the same
 * four rules in all three. They live here so a third screen was an import
 * rather than a third copy that drifts.
 *
 * Pure — unit-tested in shelf.test.ts.
 */

export type ShelfSortKey = 'title' | 'added' | 'status';

/** The fields every shelf row is sorted and scoped by, whatever else it carries. */
export interface ShelfItem {
  id: number;
  title: string;
  sort_title: string;
  added_at: string;
  /**
   * The library that owns the row. The server stamps every one; the field is
   * optional here only because the three grids share this shape with the
   * lighter DTOs a test builds. See `inLibrary`.
   */
  library_id?: number;
}

/**
 * Read `?sort=`. Anything unrecognised is "added", which is the shelf's
 * default and the spelling that carries no query parameter at all.
 */
export function readShelfSort(value: string | null): ShelfSortKey {
  return value === 'title' || value === 'status' ? value : 'added';
}

/**
 * Read `?library=`. 0 means "no filter" — the plain /movies and /series URLs
 * keep meaning "every visible item of the kind" — so a missing, malformed or
 * non-positive value all answer the same way.
 */
export function readLibraryFilter(params: URLSearchParams): number {
  const raw = params.get('library');
  if (raw === null) return 0;
  const n = Number(raw);
  return Number.isInteger(n) && n > 0 ? n : 0;
}

/**
 * Whether a row belongs to the filtered library. Strict equality on
 * `library_id`, which is the whole rule: the server stamps every movie and
 * every series with its shelf, so there is no row to rescue and no reason for
 * a grid to adopt one. `store.CountLibraryItems` counts strictly by id too,
 * which is what keeps a shelf's grid and the badge above it agreeing.
 */
export function inLibrary(item: { library_id?: number }, libraryID: number): boolean {
  return (item.library_id ?? 0) === libraryID;
}

/** `all`, narrowed to one library; the unfiltered list when `libraryID` is 0. */
export function filterByLibrary<T extends { library_id?: number }>(
  items: readonly T[],
  libraryID: number,
): T[] {
  if (libraryID === 0) return [...items];
  return items.filter((item) => inLibrary(item, libraryID));
}

/**
 * Title order: the sort title when there is one, then the display title, then
 * the id. The last two are tie-breakers rather than decoration — a stable order
 * is what stops two same-titled rows swapping places on every re-render.
 */
export function compareShelfTitle(a: ShelfItem, b: ShelfItem): number {
  return (
    (a.sort_title || a.title).localeCompare(b.sort_title || b.title) ||
    a.title.localeCompare(b.title) ||
    a.id - b.id
  );
}

/**
 * Sort a shelf. `rank` grades a row for the 'status' order — each screen has
 * its own chip vocabulary, so the ranking is the caller's and the ordering is
 * shared. Returns a new array; the input is never mutated.
 */
export function sortShelf<T extends ShelfItem>(
  items: readonly T[],
  sort: ShelfSortKey,
  rank: (item: T) => number,
): T[] {
  return [...items].sort((a, b) => {
    if (sort === 'added') return b.added_at.localeCompare(a.added_at) || compareShelfTitle(a, b);
    if (sort === 'status') return rank(a) - rank(b) || compareShelfTitle(a, b);
    return compareShelfTitle(a, b);
  });
}
