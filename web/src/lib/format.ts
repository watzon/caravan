/** Pure display formatters. No DOM, no I/O — unit-tested in format.test.ts. */

import { currentLocale, translate } from './i18n.svelte';

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const;

/** The placeholder for "we do not know", used everywhere instead of blanks. */
export const UNKNOWN = '-';

/** Human byte size, base 1024, at most one decimal. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return UNKNOWN;
  if (bytes < 1024) {
    return translate('format.bytes', {
      value: new Intl.NumberFormat(currentLocale()).format(Math.round(bytes)),
      unit: 'B',
    });
  }

  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit++;
  }
  const rounded = Math.round(value * 10) / 10;
  const text = new Intl.NumberFormat(currentLocale(), {
    maximumFractionDigits: 1,
  }).format(rounded);
  return translate('format.bytes', { value: text, unit: UNITS[unit] ?? 'B' });
}

/**
 * RFC3339 (or plain date) string to "12 Mar 2016". The schema uses the empty
 * string for "unset", which renders as the unknown placeholder.
 */
export function formatDate(value: string | null | undefined): string {
  if (!value) return UNKNOWN;
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return UNKNOWN;
  const d = new Date(ms);
  return new Intl.DateTimeFormat(currentLocale(), {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    timeZone: 'UTC',
  }).format(d);
}

/**
 * RFC3339 string to "6 Nov 2016, 21:15". Unlike formatDate this renders in the
 * viewer's zone by default: a job timestamp answers "when did this happen to
 * me", where an air date is a calendar fact that must not shift overnight.
 * timeZone exists so tests can pin the output.
 */
export function formatDateTime(value: string | null | undefined, timeZone?: string): string {
  if (!value) return UNKNOWN;
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return UNKNOWN;
  return new Intl.DateTimeFormat(currentLocale(), {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hourCycle: 'h23',
    timeZone,
  }).format(new Date(ms));
}

/** True when the date is in the future (an unaired episode). */
export function isFuture(value: string | null | undefined, now: number = Date.now()): boolean {
  if (!value) return false;
  const ms = Date.parse(value);
  return !Number.isNaN(ms) && ms > now;
}

/**
 * Truncate in the middle, not at the end: for release names and paths the tail
 * carries the release group, which is exactly what users match on
 * (DESIGN.md §6).
 */
export function truncateMiddle(text: string, max: number): string {
  if (max <= 1) return text.length <= max ? text : '…';
  if (text.length <= max) return text;
  const keep = max - 1;
  const head = Math.ceil(keep / 2);
  const tail = keep - head;
  return `${text.slice(0, head)}…${tail === 0 ? '' : text.slice(text.length - tail)}`;
}

/** Season 0 is the specials season (SPEC §7). */
export function seasonLabel(number: number): string {
  return number === 0
    ? translate('format.season.specials')
    : translate('format.season.number', { number: String(number).padStart(2, '0') });
}

/** "S01E02" — the scene form, rendered in mono everywhere. */
export function episodeCode(season: number, episode: number): string {
  return `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`;
}

/** Title with year, the way library items are labelled: "Big Buck Bunny (2008)". */
export function titleWithYear(title: string, year: number): string {
  return year > 0 ? translate('format.titleWithYear', { title, year }) : title;
}

/** Parser confidence as a whole percent. */
export function formatConfidence(confidence: number): string {
  if (!Number.isFinite(confidence)) return UNKNOWN;
  return `${Math.round(Math.max(0, Math.min(1, confidence)) * 100)}%`;
}

const MINUTE_S = 60;
const HOUR_S = 60 * MINUTE_S;
const DAY_S = 24 * HOUR_S;

/**
 * How long ago a release was published, in the one unit that matters at that
 * scale — the release picker's AGE column is a glance, not a timestamp.
 * Anything in the future (an indexer with a skewed clock) reads as "now".
 */
export function formatAge(value: string | null | undefined, now: number = Date.now()): string {
  if (!value) return UNKNOWN;
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return UNKNOWN;

  const seconds = Math.max(0, Math.round((now - ms) / 1000));
  if (seconds < HOUR_S) {
    return translate('format.age.minute', {
      count: Math.max(1, Math.floor(seconds / MINUTE_S)),
    });
  }
  if (seconds < DAY_S) {
    return translate('format.age.hour', { count: Math.floor(seconds / HOUR_S) });
  }
  const days = Math.floor(seconds / DAY_S);
  if (days < 30) return translate('format.age.day', { count: days });
  if (days < 365) return translate('format.age.month', { count: Math.floor(days / 30) });
  return translate('format.age.year', { count: Math.floor(days / 365) });
}

/**
 * The other half of formatAge: how long until something scheduled comes due.
 * A time that has already passed reads as "now" rather than a negative wait —
 * the queue polls on its own clock, so "due" and "started" are a moment apart.
 */
export function formatUntil(value: string | null | undefined, now: number = Date.now()): string {
  if (!value) return UNKNOWN;
  const ms = Date.parse(value);
  if (Number.isNaN(ms)) return UNKNOWN;

  const seconds = Math.round((ms - now) / 1000);
  if (seconds <= 0) return translate('format.until.now');
  if (seconds < HOUR_S) {
    return translate('format.until.minute', {
      count: Math.max(1, Math.round(seconds / MINUTE_S)),
    });
  }
  if (seconds < DAY_S) {
    return translate('format.until.hour', { count: Math.round(seconds / HOUR_S) });
  }
  return translate('format.until.day', { count: Math.round(seconds / DAY_S) });
}

/**
 * A recurring task's cadence. Minutes are how it is configured and stored, but
 * "360 min" is not how anyone thinks about six hours.
 */
export function formatInterval(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) return UNKNOWN;
  if (minutes < 60) {
    return translate('format.interval.minute', { count: Math.round(minutes) });
  }
  const hours = minutes / 60;
  if (hours < 24) {
    const rounded = Math.round(hours * 10) / 10;
    return translate('format.interval.hour', {
      count: new Intl.NumberFormat(currentLocale(), {
        maximumFractionDigits: 1,
      }).format(rounded),
    });
  }
  const days = Math.round((minutes / (24 * 60)) * 10) / 10;
  return translate('format.interval.day', {
    count: new Intl.NumberFormat(currentLocale(), {
      maximumFractionDigits: 1,
    }).format(days),
  });
}

/**
 * Transfer rate. A rate of zero is real (a queued or paused download moves no
 * bytes) but renders as the unknown placeholder rather than "0 B/s", which
 * reads as broken.
 */
export function formatRate(bytesPerSecond: number): string {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return UNKNOWN;
  return translate('format.rate.perSecond', { rate: formatBytes(bytesPerSecond) });
}

/**
 * Coarse duration for queue ETAs. Engines report -1 for "unknown" and can
 * report absurd values for a stalled torrent, so anything past a year is
 * unknown too — an honest dash beats a confident lie.
 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0 || seconds > 365 * DAY_S) return UNKNOWN;
  if (seconds < MINUTE_S) {
    return translate('format.duration.second', { count: Math.round(seconds) });
  }
  if (seconds < HOUR_S) {
    return translate('format.duration.minute', {
      count: Math.floor(seconds / MINUTE_S),
    });
  }
  if (seconds < DAY_S) {
    const hours = Math.floor(seconds / HOUR_S);
    const minutes = Math.floor((seconds % HOUR_S) / MINUTE_S);
    return minutes === 0
      ? translate('format.duration.hour', { count: hours })
      : translate('format.duration.hourMinute', { hours, minutes });
  }
  const days = Math.floor(seconds / DAY_S);
  const hours = Math.floor((seconds % DAY_S) / HOUR_S);
  return hours === 0
    ? translate('format.duration.day', { count: days })
    : translate('format.duration.dayHour', { days, hours });
}
