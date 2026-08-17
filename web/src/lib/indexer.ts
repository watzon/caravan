/**
 * Indexer settings helpers (SPEC §5.1 "Indexer Clients").
 *
 * Category ids are edited as free text because that is how indexers document
 * them ("2000,2040"), and stored as numbers because that is what the Torznab
 * query takes.
 *
 * Pure — unit-tested in indexer.test.ts.
 */

import type {
  Indexer,
  IndexerCategory,
  IndexerContent,
  IndexerDefinition,
  IndexerKind,
  IndexerPrivacy,
  IndexerType,
} from './api/types';
import { translate } from './i18n.svelte';

export const INDEXER_TYPES: { value: IndexerType; label: string; help: string }[] = [
  {
    value: 'torznab',
    get label() { return translate('indexer.type.torznab.label'); },
    get help() { return translate('indexer.type.torznab.help'); },
  },
  {
    value: 'newznab',
    get label() { return translate('indexer.type.newznab.label'); },
    get help() { return translate('indexer.type.newznab.help'); },
  },
];

export const INDEXER_KINDS: {
  value: IndexerKind;
  label: string;
  help: string;
}[] = [
  {
    value: 'torrent',
    get label() { return translate('indexer.kind.torrent.label'); },
    get help() { return translate('indexer.kind.torrent.help'); },
  },
  {
    value: 'usenet',
    get label() { return translate('indexer.kind.usenet.label'); },
    get help() { return translate('indexer.kind.usenet.help'); },
  },
  {
    value: 'generic',
    get label() { return translate('indexer.kind.generic.label'); },
    get help() { return translate('indexer.kind.generic.help'); },
  },
];

export const INDEXER_CONTENT: IndexerContent[] = [
  'movies',
  'tv',
  'anime',
  'audio',
  'books',
  'adult',
  'pc',
  'other',
];

export interface CatalogFilters {
  privacy?: readonly string[];
  languages?: readonly string[];
  content?: readonly string[];
}

/** Filter a catalog list the way the add-indexer picker does. */
export function filterDefinitions(
  definitions: readonly IndexerDefinition[],
  query: string,
  filters: CatalogFilters = {},
): IndexerDefinition[] {
  const q = query.trim().toLowerCase();
  const privacy = new Set((filters.privacy ?? []).filter(Boolean));
  const languages = new Set((filters.languages ?? []).filter(Boolean));
  const content = new Set((filters.content ?? []).filter(Boolean));
  return definitions.filter((def) => {
    if (privacy.size > 0 && !privacy.has(def.privacy)) return false;
    if (languages.size > 0 && !languages.has(def.language)) return false;
    if (content.size > 0 && !(def.content ?? []).some((tag) => content.has(tag))) return false;
    if (q === '') return true;
    return (
      def.id.toLowerCase().includes(q) ||
      def.name.toLowerCase().includes(q) ||
      def.description.toLowerCase().includes(q)
    );
  });
}

export function catalogPrivacyValues(definitions: readonly IndexerDefinition[]): IndexerPrivacy[] {
  const order: IndexerPrivacy[] = ['public', 'private', 'semi-private'];
  const seen = new Set(definitions.map((def) => def.privacy));
  return order.filter((value) => seen.has(value));
}

export function catalogLanguages(definitions: readonly IndexerDefinition[]): string[] {
  return [...new Set(definitions.map((def) => def.language).filter(Boolean))].sort((a, b) =>
    a.localeCompare(b),
  );
}

export function catalogContentValues(definitions: readonly IndexerDefinition[]): IndexerContent[] {
  const seen = new Set<string>();
  for (const def of definitions) {
    for (const tag of def.content ?? []) seen.add(tag);
  }
  return INDEXER_CONTENT.filter((tag) => seen.has(tag));
}

export function toggleFilterValue(selected: readonly string[], id: string): string[] {
  return selected.includes(id) ? selected.filter((value) => value !== id) : [...selected, id];
}

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

/** Append /api to a published site address unless it is already a feed. */
export function feedURLFromBase(base: string): string {
  const trimmed = base.trim().replace(/\/+$/, '');
  if (!trimmed) return '';
  const lower = trimmed.toLowerCase();
  if (lower.endsWith('/api') || lower.includes('/torznab') || lower.includes('/newznab')) {
    return trimmed;
  }
  return `${trimmed}/api`;
}

/**
 * Value for the add-indexer URL field. Local definitions keep the tracker base
 * because Caravan executes them; remote presets become the API endpoint the
 * Torznab/Newznab client calls.
 */
export function indexerFormURL(def: {
  kind: string;
  definition_id?: string;
  url: string;
  url_placeholder: string;
  urls?: readonly string[];
}): string {
  const base = def.urls?.[0] || def.url || '';
  if (base) return def.definition_id ? base.replace(/\/+$/, '') : feedURLFromBase(base);
  return def.url_placeholder || '';
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

export interface CategoryGroup {
  id: number;
  name: string;
  selected: boolean;
  children: { id: number; name: string }[];
}

/**
 * Group selected category ids under their advertised top-level parent. A
 * descendant keeps the path below that parent, so deeper trees stay readable
 * without repeating the parent name on every item.
 */
export function categoryGroups(
  categories: readonly number[],
  tree: readonly IndexerCategory[],
): CategoryGroup[] {
  const selected = new Set(categories);
  const known = new Set(allCategoryIds(tree));
  const groups: CategoryGroup[] = [];

  const collectChildren = (
    nodes: readonly IndexerCategory[],
    parentName: string,
    path: readonly string[] = [],
  ): { id: number; name: string }[] => {
    const children: { id: number; name: string }[] = [];
    for (const node of nodes) {
      const fullName = node.name || translate('indexer.category.number', { id: node.id });
      const prefix = `${parentName}/`;
      const segment = fullName.startsWith(prefix) ? fullName.slice(prefix.length) : fullName;
      const childPath = [...path, segment];
      if (selected.has(node.id)) children.push({ id: node.id, name: childPath.join(' / ') });
      children.push(...collectChildren(node.subcats, fullName, childPath));
    }
    return children;
  };

  for (const node of tree) {
    const name = node.name || translate('indexer.category.number', { id: node.id });
    const children = collectChildren(node.subcats, name);
    if (selected.has(node.id) || children.length > 0) {
      groups.push({ id: node.id, name, selected: selected.has(node.id), children });
    }
  }

  const unknown = categories
    .filter((id) => !known.has(id))
    .map((id) => ({ id, name: translate('indexer.category.number', { id }) }));
  if (unknown.length > 0) {
    groups.push({
      id: -1,
      name: translate('indexer.category.notAdvertised'),
      selected: false,
      children: unknown,
    });
  }
  return groups;
}

/* ----------------------------------------------------------------------------
 * Category blocks for the free-text search rail. Unlike the picker above, this
 * is not one indexer's advertised tree: a universal search fans out over every
 * enabled indexer at once, and there is no tree that is true of all of them.
 * The Newznab/Torznab top-level blocks are the vocabulary they DO share, so
 * they are what the rail offers.
 * ------------------------------------------------------------------------- */

/** The standard Newznab/Torznab top-level blocks, in their numeric order. */
export const STANDARD_CATEGORY_BLOCKS: readonly { id: number; name: string }[] = [
  { id: 2000, get name() { return translate('indexer.category.movies'); } },
  { id: 3000, get name() { return translate('indexer.category.audio'); } },
  { id: 4000, get name() { return translate('indexer.category.pc'); } },
  { id: 5000, get name() { return translate('indexer.category.tv'); } },
  { id: 6000, get name() { return translate('indexer.category.adult'); } },
  { id: 7000, get name() { return translate('indexer.category.books'); } },
  { id: 8000, get name() { return translate('indexer.category.other'); } },
];

/** The adult block, mirroring core.AdultCategoryBase (internal/core/release.go). */
export const ADULT_CATEGORY_BASE = 6000;

/** Whether an id falls in the adult block — core.IsAdultCategory's twin. */
export function isAdultCategory(id: number): boolean {
  return id >= ADULT_CATEGORY_BASE && id < ADULT_CATEGORY_BASE + 1000;
}

/**
 * What the search rail offers as categories: the standard blocks, plus every
 * id somebody has already configured on an indexer.
 *
 * The union matters because a private tracker's ids are frequently outside the
 * standard blocks entirely (a 100000-series scheme is common), and an option
 * list that only knew the blocks would make those indexers unsearchable by
 * category. A configured id keeps its block's name when it has one, and is
 * otherwise named by its number — the id is what the indexer documents.
 *
 * `adult` is the module's visibility, not a preference: with the module absent
 * the XXX block is not on the list at all. The server strips adult categories
 * out of the request anyway (handleSearchReleases); this is the courtesy half.
 */
export function searchCategoryOptions(
  indexers: readonly Indexer[],
  adult: boolean,
): { id: number; name: string }[] {
  const named = new Map<number, string>();
  for (const block of STANDARD_CATEGORY_BLOCKS) {
    if (!adult && isAdultCategory(block.id)) continue;
    named.set(block.id, `${block.id} ${block.name}`);
  }
  for (const indexer of indexers) {
    for (const id of indexer.categories) {
      if (!adult && isAdultCategory(id)) continue;
      if (!named.has(id)) named.set(id, String(id));
    }
  }
  return [...named.entries()]
    .map(([id, name]) => ({ id, name }))
    .sort((a, b) => a.id - b.id);
}

/**
 * Why this indexer config cannot be saved, or null when it can. The server
 * validates too; this exists so the user is told before a round trip.
 */
export function validateIndexer(input: {
  name: string;
  url: string;
  apiKey?: string;
  requiresAPIKey?: boolean;
  hasStoredKey?: boolean;
}): string | null {
  if (input.name.trim() === '') return translate('validation.indexer.name');
  const url = input.url.trim();
  if (url === '') return translate('validation.indexer.urlRequired');
  if (!/^https?:\/\//i.test(url)) return translate('validation.indexer.urlScheme');
  if (input.requiresAPIKey && !input.hasStoredKey && (input.apiKey ?? '').trim() === '') {
    return translate('validation.indexer.apiKey');
  }
  return null;
}

/** Host label for a catalog mirror, falling back to the raw URL. */
export function urlHost(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}
