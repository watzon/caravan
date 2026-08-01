/**
 * The single status vocabulary (DESIGN.md §2.3): moss = present/healthy,
 * rust = wanted/active, amber = below cutoff/warning, red = failed/missing,
 * dusk blue = informational. Badges, dots and table rows all read from here so
 * the same state never gets two different colours.
 *
 * Pure — unit-tested in status.test.ts.
 */

import type { Episode, Movie, Series } from './api/types';
import { isFuture } from './format';

export type Tone = 'success' | 'warning' | 'danger' | 'accent' | 'info' | 'neutral';

export type StatusKey =
  | 'downloaded'
  | 'incomplete'
  | 'wanted'
  | 'missing'
  | 'unaired'
  | 'unmonitored';

export interface StatusMeta {
  label: string;
  tone: Tone;
}

export const STATUS: Record<StatusKey, StatusMeta> = {
  downloaded: { label: 'Downloaded', tone: 'success' },
  incomplete: { label: 'Incomplete', tone: 'warning' },
  wanted: { label: 'Wanted', tone: 'accent' },
  missing: { label: 'Missing', tone: 'danger' },
  unaired: { label: 'Unaired', tone: 'info' },
  unmonitored: { label: 'Unmonitored', tone: 'neutral' },
};

/** One chip in the library filter row. */
export interface FilterChip {
  key: StatusKey | 'all';
  label: string;
  count: number;
}

/** Filter chips on the library grids map 1:1 onto status keys. */
export const MOVIE_FILTERS: StatusKey[] = ['downloaded', 'wanted', 'unmonitored'];
export const SERIES_FILTERS: StatusKey[] = [
  'downloaded',
  'incomplete',
  'wanted',
  'unmonitored',
];

export function movieStatus(movie: Movie): StatusKey {
  if (movie.file) return 'downloaded';
  if (!movie.monitored) return 'unmonitored';
  return 'wanted';
}

export function seriesStatus(series: Series): StatusKey {
  const total = series.episode_count ?? 0;
  const owned = series.episode_file_count ?? 0;
  if (total > 0 && owned >= total) return 'downloaded';
  if (owned > 0) return 'incomplete';
  if (!series.monitored) return 'unmonitored';
  return 'wanted';
}

export function episodeStatus(episode: Episode, now: number = Date.now()): StatusKey {
  if (episode.file) return 'downloaded';
  if (!episode.monitored) return 'unmonitored';
  if (!episode.air_date || isFuture(episode.air_date, now)) return 'unaired';
  return 'missing';
}

/** Tailwind classes per tone, resolved from tokens only (DESIGN.md §3). */
export const TONE_DOT: Record<Tone, string> = {
  success: 'bg-success',
  warning: 'bg-warning',
  danger: 'bg-danger',
  accent: 'bg-accent',
  info: 'bg-info',
  neutral: 'bg-ink-muted',
};

export const TONE_TEXT: Record<Tone, string> = {
  success: 'text-success',
  warning: 'text-warning',
  danger: 'text-danger',
  accent: 'text-accent-text',
  info: 'text-info',
  neutral: 'text-ink-secondary',
};

export const TONE_TINT: Record<Tone, string> = {
  success: 'bg-success-tint',
  warning: 'bg-warning-tint',
  danger: 'bg-danger-tint',
  accent: 'bg-accent-tint',
  info: 'bg-info-tint',
  neutral: 'bg-raised',
};
