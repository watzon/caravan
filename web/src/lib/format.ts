/** Pure display formatters. No DOM, no I/O — unit-tested in format.test.ts. */

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const;
const MONTHS = [
  'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun',
  'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec',
] as const;

/** The placeholder for "we do not know", used everywhere instead of blanks. */
export const UNKNOWN = '—';

/** Human byte size, base 1024, at most one decimal. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return UNKNOWN;
  if (bytes < 1024) return `${Math.round(bytes)} B`;

  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024;
    unit++;
  }
  const rounded = Math.round(value * 10) / 10;
  const text = Number.isInteger(rounded) ? String(rounded) : rounded.toFixed(1);
  return `${text} ${UNITS[unit]}`;
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
  return `${d.getUTCDate()} ${MONTHS[d.getUTCMonth()]} ${d.getUTCFullYear()}`;
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
  return number === 0 ? 'Specials' : `Season ${String(number).padStart(2, '0')}`;
}

/** "S01E02" — the scene form, rendered in mono everywhere. */
export function episodeCode(season: number, episode: number): string {
  return `S${String(season).padStart(2, '0')}E${String(episode).padStart(2, '0')}`;
}

/** Title with year, the way library items are labelled: "Big Buck Bunny (2008)". */
export function titleWithYear(title: string, year: number): string {
  return year > 0 ? `${title} (${year})` : title;
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
  if (seconds < HOUR_S) return `${Math.max(1, Math.floor(seconds / MINUTE_S))}m`;
  if (seconds < DAY_S) return `${Math.floor(seconds / HOUR_S)}h`;
  const days = Math.floor(seconds / DAY_S);
  if (days < 30) return `${days}d`;
  if (days < 365) return `${Math.floor(days / 30)}mo`;
  return `${Math.floor(days / 365)}y`;
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
  if (seconds <= 0) return 'now';
  if (seconds < HOUR_S) return `in ${Math.max(1, Math.round(seconds / MINUTE_S))}m`;
  if (seconds < DAY_S) return `in ${Math.round(seconds / HOUR_S)}h`;
  return `in ${Math.round(seconds / DAY_S)}d`;
}

/**
 * A recurring task's cadence. Minutes are how it is configured and stored, but
 * "360 min" is not how anyone thinks about six hours.
 */
export function formatInterval(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) return UNKNOWN;
  if (minutes < 60) return `${Math.round(minutes)} min`;
  const hours = minutes / 60;
  if (hours < 24) {
    const rounded = Math.round(hours * 10) / 10;
    return `${Number.isInteger(rounded) ? rounded : rounded.toFixed(1)} h`;
  }
  const days = Math.round((minutes / (24 * 60)) * 10) / 10;
  return `${Number.isInteger(days) ? days : days.toFixed(1)} d`;
}

/**
 * Transfer rate. A rate of zero is real (a queued or paused download moves no
 * bytes) but renders as the unknown placeholder rather than "0 B/s", which
 * reads as broken.
 */
export function formatRate(bytesPerSecond: number): string {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond <= 0) return UNKNOWN;
  return `${formatBytes(bytesPerSecond)}/s`;
}

/**
 * Coarse duration for queue ETAs. Engines report -1 for "unknown" and can
 * report absurd values for a stalled torrent, so anything past a year is
 * unknown too — an honest dash beats a confident lie.
 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0 || seconds > 365 * DAY_S) return UNKNOWN;
  if (seconds < MINUTE_S) return `${Math.round(seconds)}s`;
  if (seconds < HOUR_S) return `${Math.floor(seconds / MINUTE_S)}m`;
  if (seconds < DAY_S) {
    const hours = Math.floor(seconds / HOUR_S);
    const minutes = Math.floor((seconds % HOUR_S) / MINUTE_S);
    return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`;
  }
  const days = Math.floor(seconds / DAY_S);
  const hours = Math.floor((seconds % DAY_S) / HOUR_S);
  return hours === 0 ? `${days}d` : `${days}d ${hours}h`;
}
