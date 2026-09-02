/**
 * The universal search screen's URL.
 *
 * A search is a filter over a screen, not a screen of its own, so it lives in
 * the query string — the same split router.ts documents for the explore scopes.
 * That makes a search shareable and reload-proof, which matters more here than
 * anywhere else in the app: a fan-out over every enabled indexer is slow and
 * remote, and "what exactly did you search for" is the first question anybody
 * asks about a result.
 *
 * Pure — unit-tested in search.test.ts.
 */

export interface ReleaseSearchQuery {
  q: string;
  /** Indexer category ids; empty searches genuinely unfiltered. */
  cats: number[];
  /** Restrict to these indexers; empty asks every enabled one. */
  indexers: number[];
}

export const EMPTY_RELEASE_SEARCH: ReleaseSearchQuery = { q: '', cats: [], indexers: [] };

/**
 * Read a comma-separated id list. Unparseable entries are dropped rather than
 * failing the whole URL: a hand-edited query string is a typo, not a reason to
 * refuse to search at all.
 */
function idList(raw: string | null): number[] {
  if (!raw) return [];
  const seen: number[] = [];
  for (const part of raw.split(',')) {
    const n = Number(part.trim());
    if (Number.isInteger(n) && n > 0 && !seen.includes(n)) seen.push(n);
  }
  return seen;
}

/** Parse `?q=&cats=&indexers=` off the router's params. */
export function parseReleaseSearch(params: URLSearchParams): ReleaseSearchQuery {
  return {
    q: (params.get('q') ?? '').trim(),
    cats: idList(params.get('cats')),
    indexers: idList(params.get('indexers')),
  };
}

/**
 * The address of a search. An empty query answers the bare path, so pressing
 * Search on an empty box does not leave `?q=` behind for the next reload to
 * read back.
 */
export function releaseSearchHref(query: ReleaseSearchQuery): string {
  const params = new URLSearchParams();
  const q = query.q.trim();
  if (q !== '') params.set('q', q);
  if (query.cats.length > 0) params.set('cats', query.cats.join(','));
  if (query.indexers.length > 0) params.set('indexers', query.indexers.join(','));
  const search = params.toString();
  return search === '' ? '/search' : `/search?${search}`;
}
