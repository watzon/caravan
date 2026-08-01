/**
 * Indexer settings helpers (SPEC §5.1 "Indexer Clients").
 *
 * Category ids are edited as free text because that is how indexers document
 * them ("2000,2040"), and stored as numbers because that is what the Torznab
 * query takes.
 *
 * Pure — unit-tested in indexer.test.ts.
 */

import type { IndexerCategory, IndexerType } from './api/types';

export const INDEXER_TYPES: { value: IndexerType; label: string; help: string }[] = [
  { value: 'torznab', label: 'Torznab', help: 'Torrent indexer (Jackett, Prowlarr, private trackers).' },
  { value: 'newznab', label: 'Newznab', help: 'Usenet indexer (NZBGeek, DrunkenSlug, …).' },
];

/**
 * Parse a user-typed category list. Unparseable entries are dropped rather than
 * rejected — a trailing comma is a typo, not an error worth blocking a save on
 * — and duplicates collapse so the list is stable across edits.
 */
export function parseCategories(text: string): number[] {
  const seen = new Set<number>();
  for (const part of text.split(/[,\s]+/)) {
    if (part === '') continue;
    const n = Number(part);
    if (Number.isInteger(n) && n >= 0) seen.add(n);
  }
  return [...seen];
}

/** Render stored category ids back into the text the form edits. */
export function formatCategories(categories: readonly number[] | null | undefined): string {
  return (categories ?? []).join(', ');
}

/* ----------------------------------------------------------------------------
 * Category-tree selection (the picker in IndexerSettings). The tree comes from
 * the indexer's own capabilities document; the selection is stored as the flat
 * id list IndexerConfig.Categories already is. All pure — the component only
 * renders.
 * ------------------------------------------------------------------------- */

/** Every id in the tree, parents and children, in document order. */
export function allCategoryIds(tree: readonly IndexerCategory[]): number[] {
  const out: number[] = [];
  for (const node of tree) {
    out.push(node.id, ...allCategoryIds(node.subcats));
  }
  return out;
}

/**
 * A node's checkbox state against the current selection: parents report
 * 'some' when the subtree is only partially selected, so the checkbox can
 * render indeterminate.
 */
export function selectionState(
  node: IndexerCategory,
  selected: ReadonlySet<number>,
): 'all' | 'some' | 'none' {
  const ids = allCategoryIds([node]);
  const picked = ids.filter((id) => selected.has(id)).length;
  if (picked === 0) return 'none';
  if (picked === ids.length) return 'all';
  return 'some';
}

/**
 * Toggle a node. A parent toggles its whole subtree — checking it selects
 * every descendant (indexers like AnimeTosho do not expand parent ids
 * server-side, so the ids must be explicit), and unchecking a partially
 * selected parent clears the subtree rather than inverting it.
 */
export function toggleCategory(
  node: IndexerCategory,
  selected: ReadonlySet<number>,
): Set<number> {
  const next = new Set(selected);
  const ids = allCategoryIds([node]);
  if (selectionState(node, selected) === 'all') {
    for (const id of ids) next.delete(id);
  } else {
    for (const id of ids) next.add(id);
  }
  return next;
}

/**
 * Selected ids the tree does not advertise — manual entries from before the
 * picker existed, or an indexer that changed its caps. They render as removable
 * extras so a save never silently drops them.
 */
export function unknownCategoryIds(
  selected: ReadonlySet<number>,
  tree: readonly IndexerCategory[],
): number[] {
  const known = new Set(allCategoryIds(tree));
  return [...selected].filter((id) => !known.has(id)).sort((a, b) => a - b);
}

/**
 * Why this indexer config cannot be saved, or null when it can. The server
 * validates too; this exists so the user is told before a round trip.
 */
export function validateIndexer(input: { name: string; url: string }): string | null {
  if (input.name.trim() === '') return 'Give the indexer a name.';
  const url = input.url.trim();
  if (url === '') return 'The indexer needs a base URL.';
  if (!/^https?:\/\//i.test(url)) return 'The URL must start with http:// or https://.';
  return null;
}
