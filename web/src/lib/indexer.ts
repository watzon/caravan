/**
 * Indexer settings helpers (SPEC §5.1 "Indexer Clients").
 *
 * Category ids are edited as free text because that is how indexers document
 * them ("2000,2040"), and stored as numbers because that is what the Torznab
 * query takes.
 *
 * Pure — unit-tested in indexer.test.ts.
 */

import type { IndexerType } from './api/types';

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
