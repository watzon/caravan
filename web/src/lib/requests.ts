/**
 * Pure helpers for the requests screen and the nav badge it feeds. No DOM, no
 * I/O — unit-tested in requests.test.ts.
 *
 * The store holds every request (pending, approved, dismissed) so history stays
 * one fetch away; what the badge and the list want is derived here rather than
 * baked into the fetch, which keeps the derivation testable.
 */

import type { MediaRequest } from './api/types';
import { seasonLabel } from './format';

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
 * What was asked for. A null `seasons` means the whole title — every movie
 * request, and a series request that covered all of them.
 */
export function requestSeasonsLabel(request: MediaRequest): string {
  if (request.media_type === 'movie') return 'Movie';
  const seasons = request.seasons;
  if (seasons === null || seasons.length === 0) return 'All seasons';
  if (seasons.length === 1) return seasonLabel(seasons[0] as number);
  return `${seasons.length} seasons · ${seasons.map(seasonLabel).join(', ')}`;
}
