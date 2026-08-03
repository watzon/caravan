/**
 * What the requests screen lists and what the sidebar badge counts. The badge
 * is derived rather than fetched narrowly, so the derivation is the thing worth
 * proving: an approved or dismissed row must never inflate it.
 */
import { describe, expect, it } from 'vitest';
import type { MediaRequest, RequestStatus } from './api/types';
import {
  pendingRequestCount,
  pendingRequests,
  requestSeasonsLabel,
  requestStatusChip,
} from './requests';

function request(id: number, extra: Partial<MediaRequest> = {}): MediaRequest {
  return {
    id,
    media_type: 'series',
    tmdb_id: 1000 + id,
    title: `Title ${id}`,
    year: 2020,
    poster_path: '',
    poster_url: '',
    seasons: null,
    min_availability: '',
    requested_by_username: '',
    status: 'pending' as RequestStatus,
    created_at: '2026-08-01T00:00:00Z',
    updated_at: '2026-08-01T00:00:00Z',
    ...extra,
  };
}

describe('pending requests', () => {
  const rows = [
    request(1),
    request(2, { status: 'approved' }),
    request(3, { status: 'dismissed' }),
    request(4),
  ];

  it('counts only what is still waiting on a decision', () => {
    expect(pendingRequestCount(rows)).toBe(2);
    expect(pendingRequests(rows).map((r) => r.id)).toEqual([1, 4]);
  });

  // A zero badge renders nothing, so "no data yet" and "nothing pending" must
  // both answer 0 rather than throw.
  it('reads an unloaded list as zero', () => {
    expect(pendingRequestCount(null)).toBe(0);
    expect(pendingRequestCount([])).toBe(0);
  });
});

describe('requestSeasonsLabel', () => {
  it('says what was actually asked for', () => {
    expect(requestSeasonsLabel(request(1, { media_type: 'movie' }))).toBe('Movie');
    // null is the whole title, and so is an empty list (the server treats them
    // identically on the way in).
    expect(requestSeasonsLabel(request(1, { seasons: null }))).toBe('All seasons');
    expect(requestSeasonsLabel(request(1, { seasons: [] }))).toBe('All seasons');
    expect(requestSeasonsLabel(request(1, { seasons: [2] }))).toBe('Season 02');
    expect(requestSeasonsLabel(request(1, { seasons: [1, 2] }))).toBe(
      '2 seasons · Season 01, Season 02',
    );
  });
});

describe('requestStatusChip', () => {
  // Only a member's list renders this, and it is the one place they learn that
  // a wish was granted — so an approved row must not read as a quiet nothing.
  it('gives each status its own word and tone', () => {
    expect(requestStatusChip('pending')).toEqual({ label: 'Pending', tone: 'warning' });
    expect(requestStatusChip('approved')).toEqual({ label: 'Approved', tone: 'success' });
    expect(requestStatusChip('dismissed')).toEqual({ label: 'Dismissed', tone: 'neutral' });
  });
});
