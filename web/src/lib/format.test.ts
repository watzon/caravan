import { describe, expect, it } from 'vitest';
import {
  UNKNOWN,
  episodeCode,
  formatAge,
  formatBytes,
  formatConfidence,
  formatDate,
  formatDateTime,
  formatDuration,
  formatInterval,
  formatRate,
  formatUntil,
  isFuture,
  seasonLabel,
  titleWithYear,
  truncateMiddle,
} from './format';

describe('formatBytes', () => {
  const cases: [number, string][] = [
    [0, '0 B'],
    [512, '512 B'],
    [1024, '1 KB'],
    [1536, '1.5 KB'],
    [1024 * 1024, '1 MB'],
    [1024 * 1024 * 1024 * 2.5, '2.5 GB'],
    [-1, UNKNOWN],
    [Number.NaN, UNKNOWN],
  ];

  for (const [input, want] of cases) {
    it(`formats ${input} as ${want}`, () => {
      expect(formatBytes(input)).toBe(want);
    });
  }
});

describe('formatDateTime', () => {
  it('renders the date with a 24-hour clock in the given zone', () => {
    expect(formatDateTime('2016-11-06T21:15:00Z', 'UTC')).toBe('6 Nov 2016, 21:15');
    expect(formatDateTime('2016-11-06T21:15:00Z', 'America/Denver')).toBe('6 Nov 2016, 14:15');
  });

  it('shows the unknown placeholder for empty or unparseable input', () => {
    expect(formatDateTime('')).toBe(UNKNOWN);
    expect(formatDateTime(null)).toBe(UNKNOWN);
    expect(formatDateTime('soon')).toBe(UNKNOWN);
  });
});

describe('formatDate', () => {
  it('renders RFC3339 in UTC', () => {
    expect(formatDate('2016-11-06T00:00:00Z')).toBe('6 Nov 2016');
  });

  it('renders a plain date', () => {
    expect(formatDate('2008-05-20')).toBe('20 May 2008');
  });

  it('treats the schema empty string as unknown', () => {
    expect(formatDate('')).toBe(UNKNOWN);
    expect(formatDate(null)).toBe(UNKNOWN);
    expect(formatDate('not a date')).toBe(UNKNOWN);
  });
});

describe('isFuture', () => {
  const now = Date.parse('2026-07-31T00:00:00Z');

  it('is true for an unaired episode', () => {
    expect(isFuture('2026-08-01T00:00:00Z', now)).toBe(true);
  });

  it('is false for an aired episode and for unset dates', () => {
    expect(isFuture('2026-07-30T00:00:00Z', now)).toBe(false);
    expect(isFuture('', now)).toBe(false);
  });
});

describe('truncateMiddle', () => {
  it('keeps the tail, because the release group lives there', () => {
    // Head and tail are both preserved; only the middle is elided.
    const out = truncateMiddle('Movies/Big Buck Bunny (2008)/file-GROUP.mkv', 20);
    expect(out).toHaveLength(20);
    expect(out.startsWith('Movies/Big')).toBe(true);
    expect(out.endsWith('GROUP.mkv')).toBe(true);
    expect(out).toContain('…');
  });

  it('leaves short text alone', () => {
    expect(truncateMiddle('short.mkv', 20)).toBe('short.mkv');
  });

  it('degrades to an ellipsis when there is no room', () => {
    expect(truncateMiddle('abcdef', 1)).toBe('…');
  });
});

describe('labels', () => {
  it('names season 0 Specials', () => {
    expect(seasonLabel(0)).toBe('Specials');
    expect(seasonLabel(1)).toBe('Season 01');
    expect(seasonLabel(12)).toBe('Season 12');
  });

  it('renders scene episode codes', () => {
    expect(episodeCode(1, 2)).toBe('S01E02');
    expect(episodeCode(0, 1)).toBe('S00E01');
  });

  it('renders Jellyfin-style titles', () => {
    expect(titleWithYear('Big Buck Bunny', 2008)).toBe('Big Buck Bunny (2008)');
    expect(titleWithYear('Untitled', 0)).toBe('Untitled');
  });

  it('clamps confidence to whole percents', () => {
    expect(formatConfidence(0.734)).toBe('73%');
    expect(formatConfidence(1.5)).toBe('100%');
    expect(formatConfidence(-1)).toBe('0%');
  });
});

describe('formatAge', () => {
  const now = Date.parse('2026-07-31T12:00:00Z');
  const ago = (ms: number) => new Date(now - ms).toISOString();

  const cases: [string, string][] = [
    [ago(30 * 1000), '1m'],
    [ago(45 * 60 * 1000), '45m'],
    [ago(5 * 60 * 60 * 1000), '5h'],
    [ago(3 * 24 * 60 * 60 * 1000), '3d'],
    [ago(60 * 24 * 60 * 60 * 1000), '2mo'],
    [ago(800 * 24 * 60 * 60 * 1000), '2y'],
  ];

  for (const [input, want] of cases) {
    it(`renders ${input} as ${want}`, () => {
      expect(formatAge(input, now)).toBe(want);
    });
  }

  it('reads a skewed indexer clock as brand new rather than negative', () => {
    expect(formatAge(new Date(now + 60_000).toISOString(), now)).toBe('1m');
  });

  it('treats unset and unparseable dates as unknown', () => {
    expect(formatAge('', now)).toBe(UNKNOWN);
    expect(formatAge(null, now)).toBe(UNKNOWN);
    expect(formatAge('not a date', now)).toBe(UNKNOWN);
  });
});

describe('formatRate', () => {
  it('renders a live rate per second', () => {
    expect(formatRate(1024)).toBe('1 KB/s');
    expect(formatRate(1.5 * 1024 * 1024)).toBe('1.5 MB/s');
  });

  it('renders no movement as unknown rather than 0 B/s', () => {
    expect(formatRate(0)).toBe(UNKNOWN);
    expect(formatRate(-1)).toBe(UNKNOWN);
    expect(formatRate(Number.NaN)).toBe(UNKNOWN);
  });
});

describe('formatDuration', () => {
  const cases: [number, string][] = [
    [0, '0s'],
    [45, '45s'],
    [90, '1m'],
    [3600, '1h'],
    [3600 + 5 * 60, '1h 5m'],
    [2 * 86400, '2d'],
    [2 * 86400 + 3600 * 3, '2d 3h'],
  ];

  for (const [input, want] of cases) {
    it(`renders ${input}s as ${want}`, () => {
      expect(formatDuration(input)).toBe(want);
    });
  }

  it('reads the engine unknown sentinel as unknown', () => {
    // internal/core.DownloadStatus.ETASeconds is -1 when the engine cannot say.
    expect(formatDuration(-1)).toBe(UNKNOWN);
  });

  it('refuses to render a stalled torrent absurd ETA', () => {
    expect(formatDuration(400 * 86400)).toBe(UNKNOWN);
    expect(formatDuration(Number.POSITIVE_INFINITY)).toBe(UNKNOWN);
  });
});

describe('formatUntil', () => {
  const now = Date.parse('2026-08-04T12:00:00Z');
  const ahead = (ms: number) => new Date(now + ms).toISOString();

  const cases: [string, string][] = [
    [ahead(5 * 60 * 1000), 'in 5m'],
    [ahead(30 * 1000), 'in 1m'],
    [ahead(6 * 60 * 60 * 1000), 'in 6h'],
    [ahead(2 * 24 * 60 * 60 * 1000), 'in 2d'],
  ];

  for (const [input, want] of cases) {
    it(`renders ${input} as ${want}`, () => {
      expect(formatUntil(input, now)).toBe(want);
    });
  }

  it('reads a time that has already passed as due now', () => {
    // The queue polls on its own clock: overdue and starting are the same thing.
    expect(formatUntil(ahead(-60_000), now)).toBe('now');
    expect(formatUntil(ahead(0), now)).toBe('now');
  });

  it('has nothing to say about an unset or unparseable time', () => {
    expect(formatUntil('', now)).toBe(UNKNOWN);
    expect(formatUntil(null, now)).toBe(UNKNOWN);
    expect(formatUntil('soon', now)).toBe(UNKNOWN);
  });
});

describe('formatInterval', () => {
  const cases: [number, string][] = [
    [15, '15 min'],
    [59, '59 min'],
    [60, '1 h'],
    [90, '1.5 h'],
    [360, '6 h'],
    [720, '12 h'],
    [1440, '1 d'],
    [10080, '7 d'],
  ];

  for (const [input, want] of cases) {
    it(`renders ${input} minutes as ${want}`, () => {
      expect(formatInterval(input)).toBe(want);
    });
  }

  it('refuses to render a cadence that cannot be one', () => {
    expect(formatInterval(0)).toBe(UNKNOWN);
    expect(formatInterval(-5)).toBe(UNKNOWN);
    expect(formatInterval(Number.NaN)).toBe(UNKNOWN);
  });
});
