/**
 * Pure helpers for the requests screen and the nav badge it feeds. No DOM, no
 * I/O — unit-tested in requests.test.ts.
 *
 * The store holds every request (pending, approved, dismissed) so history stays
 * one fetch away; what the badge and the list want is derived here rather than
 * baked into the fetch, which keeps the derivation testable.
 */

import type { MediaRequest, RequestStatus } from './api/types';
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
  const seasons = request.seasons;
  if (seasons === null || seasons.length === 0) return 'All seasons';
  if (seasons.length === 1) return seasonLabel(seasons[0] as number);
  return `${seasons.length} seasons · ${seasons.map(seasonLabel).join(', ')}`;
}
