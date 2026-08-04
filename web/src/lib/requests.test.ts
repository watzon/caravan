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
  requestFallbackIcon,
  requestHref,
  requestMediaChip,
  requestSeasonsLabel,
  requestStatusChip,
} from './requests';

/**
 * A scene request as the server hands one back: named by its stash id, with a
 * `tmdb_id` of 0 and no seasons. Those two zeroes are what every case below
 * turns on — a helper that forgets a scene builds a link to /discover/scene/0.
 */
function scene(id: number, extra: Partial<MediaRequest> = {}): MediaRequest {
  return request(id, {
    media_type: 'scene',
    tmdb_id: 0,
    stash_id: `stash-${id}`,
    ...extra,
  });
}

function request(id: number, extra: Partial<MediaRequest> = {}): MediaRequest {
  return {
    id,
    media_type: 'series',
    tmdb_id: 1000 + id,
    stash_id: '',
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

  /**
   * A scene carries no seasons, so without its own case it falls through to the
   * series branch and reads "All seasons" — which misdescribes both the ask and
   * what approving it does (it adds the site).
   */
  it('calls a scene a scene rather than every season of one', () => {
    expect(requestSeasonsLabel(scene(1))).toBe('Scene');
    expect(requestSeasonsLabel(scene(1, { seasons: [] }))).toBe('Scene');
  });
});

describe('requestMediaChip', () => {
  it('gives each of the three kinds its own word', () => {
    expect(requestMediaChip('movie')).toBe('MOVIE');
    expect(requestMediaChip('series')).toBe('SERIES');
    // The bug this pins: a two-way `movie ? … : 'SERIES'` labels a scene SERIES.
    expect(requestMediaChip('scene')).toBe('SCENE');
  });
});

describe('requestFallbackIcon', () => {
  it('does not let a posterless scene pass for a television series', () => {
    expect(requestFallbackIcon('movie')).toBe('film');
    expect(requestFallbackIcon('series')).toBe('tv');
    expect(requestFallbackIcon('scene')).toBe('flame');
  });
});

describe('requestHref', () => {
  it('links the TMDB kinds at their discover screen', () => {
    expect(requestHref(request(1, { media_type: 'movie', tmdb_id: 78 }))).toBe('/discover/movie/78');
    expect(requestHref(request(1, { media_type: 'series', tmdb_id: 1396 }))).toBe(
      '/discover/series/1396',
    );
  });

  /**
   * The whole reason `RequestMediaType` is not `MediaType` widened. A scene's
   * tmdb id is 0, so the old shared path built `/discover/scene/0` — a route
   * that does not exist, on a row whose poster and title were both anchors to
   * it. Null is "render this as text".
   */
  it('sends a scene nowhere, because there is nowhere to send it', () => {
    expect(requestHref(scene(1))).toBeNull();
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
