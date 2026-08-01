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
