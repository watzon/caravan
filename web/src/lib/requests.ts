/**
 * Pure helpers for the requests screen and the nav badge it feeds. No DOM, no
 * I/O — unit-tested in requests.test.ts.
 *
 * The store holds every request (pending, approved, dismissed) so history stays
 * one fetch away; what the badge and the list want is derived here rather than
 * baked into the fetch, which keeps the derivation testable.
 */

import type { MediaRequest, RequestMediaType, RequestStatus } from './api/types';
import { discoverHref } from './discover';
import { seasonLabel } from './format';
import type { Tone } from './status';

/** Requests still waiting on a decision, newest first (the server's order). */
export function pendingRequests(requests: MediaRequest[] | null): MediaRequest[] {
  return (requests ?? []).filter((r) => r.status === 'pending');
}

/**
 * What the sidebar badge shows. Only pending rows count: an approved request
 * became a library item and a dismissed one was answered, so neither is work
 * waiting on the user.
 */
export function pendingRequestCount(requests: MediaRequest[] | null): number {
  return pendingRequests(requests).length;
}

/**
 * How a request's own status reads, in the shared status vocabulary
 * (DESIGN.md §2.3). Only a member's list shows it: an admin's list is pending
 * rows and nothing else, so the badge there would say the same word every time.
 */
export function requestStatusChip(status: RequestStatus): { label: string; tone: Tone } {
  switch (status) {
    case 'approved':
      return { label: 'Approved', tone: 'success' };
    case 'dismissed':
      return { label: 'Dismissed', tone: 'neutral' };
    default:
      return { label: 'Pending', tone: 'warning' };
  }
}

/**
 * What was asked for. A null `seasons` means the whole title — every movie
 * request, and a series request that covered all of them.
 */
export function requestSeasonsLabel(request: MediaRequest): string {
  if (request.media_type === 'movie') return 'Movie';
  // A scene is not a season ask and never carries one — the server rejects
  // `seasons` on a scene request outright. Without this case it would fall
  // through to the series branch and read "All seasons", which is both wrong
  // and a promise about what approving it does: approving a scene adds the
  // SITE, not a season of anything.
  if (request.media_type === 'scene') return 'Scene';
  const seasons = request.seasons;
  if (seasons === null || seasons.length === 0) return 'All seasons';
  if (seasons.length === 1) return seasonLabel(seasons[0] as number);
  return `${seasons.length} seasons · ${seasons.map(seasonLabel).join(', ')}`;
}

/**
 * The mono chip on a request row. It is `discover.mediaTypeChip` plus the third
 * kind, and it is a separate function rather than that one widened because that
 * one is reached from TMDB-shaped screens where 'scene' cannot occur — widening
 * it would ask every one of those callers to handle a case they cannot get.
 */
export function requestMediaChip(mediaType: RequestMediaType): string {
  switch (mediaType) {
    case 'movie':
      return 'MOVIE';
    case 'scene':
      return 'SCENE';
    default:
      return 'SERIES';
  }
}

/**
 * The placeholder behind a row with no poster. A scene gets the flame the rest
 * of the adult module uses, so a posterless scene row does not masquerade as a
 * television series.
 */
export function requestFallbackIcon(mediaType: RequestMediaType): 'film' | 'tv' | 'flame' {
  switch (mediaType) {
    case 'movie':
      return 'film';
    case 'scene':
      return 'flame';
    default:
      return 'tv';
  }
}

/**
 * Where a request row links, or null when it links nowhere.
 *
 * Null is the scene answer and the reason `RequestMediaType` exists: /discover
 * is TMDB-shaped down to its ids, a scene's `tmdb_id` is 0, and `discoverHref`
 * on one builds `/discover/scene/0` — a route that does not exist. There is no
 * per-scene screen to point at instead (the adult routes are the site grid, one
 * site, and the scene search), so the row renders as text and says so honestly
 * rather than offering a link that lands nowhere.
 */
export function requestHref(request: MediaRequest): string | null {
  if (request.media_type === 'scene') return null;
  return discoverHref({ media_type: request.media_type, tmdb_id: request.tmdb_id });
}
