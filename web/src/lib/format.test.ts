import { describe, expect, it } from 'vitest';
import {
  UNKNOWN,
  episodeCode,
  formatBytes,
  formatConfidence,
  formatDate,
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
