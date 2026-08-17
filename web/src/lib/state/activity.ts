/**
 * The write path for the stores the sidebar reads.
 *
 * A mutation this browser just made writes the store on this tick. Another
 * browser, or a background job, reaches the same stores through the live
 * stream. There is no badge poll.
 */

import type { MediaRequest, RequestStatus } from '../api/types';
import { downloads } from './downloads.svelte';
import { requests } from './requests.svelte';
import { system } from './system.svelte';
import { tasks } from './tasks.svelte';

/** A new (or merged) request: the pending badge updates on this tick. */
export function requestCreated(request: MediaRequest): void {
  requests.remember(request);
}

/**
 * An approve or dismiss. The pending badge drops immediately; an approval
 * that queued a search is watched at the queue-screen rate so the Queue
 * badge appears when the grab lands.
 */
export function requestDecided(
  id: number,
  status: Extract<RequestStatus, 'approved' | 'dismissed'>,
  opts: { expectDownload?: boolean } = {},
): void {
  requests.applyStatus(id, status);
  if (status === 'approved') {
    system.adjustCount('wanted', 1);
    void system.refresh();
  }
  if (opts.expectDownload) {
    downloads.watchSoon();
    tasks.watchSoon();
  }
}

/**
 * Inventory just changed: monitor toggles, removals, adds. The sidebar
 * counts come from system status, so they refresh on this tick.
 */
export function libraryChanged(opts: { expectDownload?: boolean } = {}): void {
  void system.refresh();
  if (opts.expectDownload) {
    downloads.watchSoon();
    tasks.watchSoon();
  }
}

/**
 * A scan-review row was matched or dismissed. The unmatched badge drops
 * immediately; a refresh then corrects Wanted and the library counts.
 */
export function scanReviewResolved(count = 1): void {
  if (count > 0) system.adjustCount('unmatched', -count);
  void system.refresh();
}

/** A search was queued: the footer should show it as soon as the job exists. */
export function searchQueued(count: number): void {
  if (count <= 0) return;
  tasks.watchSoon();
}
