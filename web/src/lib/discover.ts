/**
 * Pure helpers for the discover screens: where a card points, what a season
 * checklist has selected, and what the add/request modal's primary button
 * says. No DOM, no I/O — unit-tested in discover.test.ts.
 *
 * The season maths lives here rather than in AddRequestModal because it is the
 * part with edge cases (seasons already owned, seasons already requested,
 * whole-title vs partial) and it must be provable without mounting a dialog.
 */

import type { DiscoverItem, DiscoverSeason, MediaRequest, MediaType, MinAvailability } from './api/types';
import { UNKNOWN, seasonLabel } from './format';

/** Which half of the modal the user is in: adding for real, or asking for it. */
export type RequestMode = 'add' | 'request';

/**
 * What AddRequestModal did, handed back so the opener can patch its own view
 * instead of refetching a screen that only changed by one flag.
 */
export type AddRequestResult =
  | { kind: 'requested'; request: MediaRequest }
  | { kind: 'added'; mediaType: MediaType; libraryID: number };

/** A discover card points at the acquisition screen, keyed by TMDB id. */
export function discoverHref(item: Pick<DiscoverItem, 'media_type' | 'tmdb_id'>): string {
  return `/discover/${item.media_type}/${item.tmdb_id}`;
}

/** Where an owned title lives — the other id space (SPEC §11). */
export function libraryHref(mediaType: MediaType, libraryID: number): string {
  return mediaType === 'movie' ? `/movies/${libraryID}` : `/series/${libraryID}`;
}

/** The browse screen for one curated shelf. */
export function sourceHref(source: { type: string; id: number }): string {
  return `/discover/${source.type}/${source.id}`;
}

/** MOVIE / SERIES, for the mono chip on a mixed shelf. */
export function mediaTypeChip(mediaType: MediaType): string {
  return mediaType === 'movie' ? 'MOVIE' : 'SERIES';
}

/**
 * TMDB's 0-10 vote as one decimal, or null when there is no positive score
 * backed by at least one vote.
 */
export function ratingText(voteAverage: number, voteCount: number): string | null {
  if (!(voteCount > 0) || !Number.isFinite(voteAverage) || voteAverage <= 0) return null;
  return voteAverage.toFixed(1);
}

export interface RatingPresentation {
  text: string | null;
  title: string;
}

/**
 * A rating badge's visible score and explanation.
 *
 * A provider score is meaningful only when at least one person voted and the
 * title has a known, valid release date that is not in the future.
 */
export function ratingPresentation(
  voteAverage: number,
  voteCount: number,
  releaseDate: string,
  today: Date = new Date(),
): RatingPresentation {
  const rating = ratingText(voteAverage, voteCount);
  const dateMatch = /^(\d{4})-(\d{2})-(\d{2})$/.exec(releaseDate);
  if (!rating || !dateMatch || !Number.isFinite(today.getTime())) {
    return { text: null, title: 'Not yet rated' };
  }

  const [, yearText, monthText, dayText] = dateMatch;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const release = new Date(year, month - 1, day);
  const validReleaseDate =
    release.getFullYear() === year &&
    release.getMonth() === month - 1 &&
    release.getDate() === day;
  const released =
    validReleaseDate &&
    release.getTime() <= new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime();

  if (!released) return { text: null, title: 'Not yet rated' };

  const text = `${rating}/10`;
  return { text, title: `Rated ${text}` };
}

/** Leading year of a provider date ("2008-01-20" → 2008); 0 when unknown. */
export function yearOf(date: string | null | undefined): number {
  if (!date) return 0;
  const year = Number(date.slice(0, 4));
  return Number.isInteger(year) && year > 0 ? year : 0;
}

/** Runtime in minutes as "49 min"; the unknown placeholder at zero. */
export function runtimeText(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) return UNKNOWN;
  return `${Math.round(minutes)} min`;
}

/**
 * An ISO 639-1 code as a language name in the reader's locale ("en" →
 * "English"), the code itself when the runtime cannot name it, and the unknown
 * placeholder when the provider gave none.
 */
export function languageName(code: string): string {
  if (!code) return UNKNOWN;
  try {
    return new Intl.DisplayNames(undefined, { type: 'language' }).of(code) ?? code;
  } catch {
    return code;
  }
}

/** "12 EPS · 2022" — the mono line under a season row. */
export function seasonMeta(season: Pick<DiscoverSeason, 'episode_count' | 'air_date'>): string {
  const parts: string[] = [];
  if (season.episode_count > 0) parts.push(`${season.episode_count} EPS`);
  const year = yearOf(season.air_date);
  if (year > 0) parts.push(String(year));
  return parts.join(' · ');
}

/**
 * The seasons the checklist lets the user act on. A season the library already
 * holds is shown (with its badge) but never checkable: adding it again is a
 * no-op, and letting it be counted would make the footer lie.
 */
export function selectableSeasons(seasons: DiscoverSeason[]): DiscoverSeason[] {
  return seasons.filter((s) => !s.in_library);
}

/**
 * What is checked when the modal opens.
 *
 * Add mode takes everything the library is missing — that is what "add this
 * series" means. Request mode also skips seasons somebody already asked for:
 * re-requesting them merges into the same row and tells the user nothing.
 */
export function defaultSeasonSelection(
  seasons: DiscoverSeason[],
  mode: RequestMode,
): number[] {
  return selectableSeasons(seasons)
    .filter((s) => mode === 'add' || !s.requested)
    .map((s) => s.season_number);
}

/** Add or remove one season, keeping the list sorted so labels read in order. */
export function toggleSeason(selected: readonly number[], season: number): number[] {
  return selected.includes(season)
    ? selected.filter((n) => n !== season)
    : [...selected, season].sort((a, b) => a - b);
}

/** Every selectable season — what the "Select all" link produces. */
export function allSeasonNumbers(seasons: DiscoverSeason[]): number[] {
  return selectableSeasons(seasons).map((s) => s.season_number);
}

/** True when the link should read "Deselect all" rather than "Select all". */
export function allSelected(seasons: DiscoverSeason[], selected: readonly number[]): boolean {
  const selectable = allSeasonNumbers(seasons);
  return selectable.length > 0 && selectable.every((n) => selected.includes(n));
}

/**
 * The `seasons` field POST /requests should carry: omitted (the whole title)
 * when every season the provider knows about is checked, the sorted list
 * otherwise. Sending [] is treated identically to omitting it, so undefined is
 * the honest way to say "all of it".
 */
export function requestSeasons(
  seasons: DiscoverSeason[],
  selected: readonly number[],
): number[] | undefined {
  if (seasons.length === 0) return undefined;
  const wanted = [...selected].sort((a, b) => a - b);
  if (wanted.length >= seasons.length) return undefined;
  return wanted;
}

/**
 * The `seasons` field POST /library/series should carry: omitted when the add
 * covers the whole series, the sorted list otherwise.
 *
 * The endpoint reads the list as "monitor exactly these", so a season the
 * library already holds is always included — an add must never quietly
 * unmonitor a season nobody asked it about.
 */
export function addSeasons(
  seasons: DiscoverSeason[],
  selected: readonly number[],
): number[] | undefined {
  if (seasons.length === 0) return undefined;
  const wanted = seasons
    .filter((s) => s.in_library || selected.includes(s.season_number))
    .map((s) => s.season_number)
    .sort((a, b) => a - b);
  if (wanted.length >= seasons.length) return undefined;
  return wanted;
}

/**
 * Whether a season row on the detail screen may offer a Request button.
 *
 * POST /requests refuses anything already tracked with 409 "already in the
 * library" — the check is on the title, not the season — so offering the
 * button on an owned series would be a guaranteed error toast. The season is
 * reported as missing instead, and the library screen is where it is managed.
 */
export function canRequestSeason(
  titleInLibrary: boolean,
  season: Pick<DiscoverSeason, 'in_library' | 'requested'>,
): boolean {
  return !titleInLibrary && !season.in_library && !season.requested;
}

/** "A", "A and B", "A, B and C". */
function joinList(names: string[]): string {
  if (names.length <= 1) return names[0] ?? '';
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
}

/**
 * The helper line under the checklist when adding will silently resolve
 * somebody's pending request. Add mode only: in request mode the merge is the
 * point, not a side effect worth warning about.
 */
export function absorbNote(
  seasons: DiscoverSeason[],
  selected: readonly number[],
  mode: RequestMode,
): string | null {
  if (mode !== 'add') return null;
  const names = seasons
    .filter((s) => !s.in_library && s.requested && selected.includes(s.season_number))
    .map((s) => seasonLabel(s.season_number));
  if (names.length === 0) return null;
  return names.length === 1
    ? `Adding ${names[0]} will absorb its pending request and mark it approved.`
    : `Adding ${joinList(names)} will absorb their pending requests and mark them approved.`;
}

/** The modal's primary button. The count is checked seasons, never total. */
export function submitLabel(
  mode: RequestMode,
  mediaType: MediaType,
  count: number,
): string {
  const verb = mode === 'add' ? 'Add' : 'Request';
  if (mediaType === 'movie') return `${verb} movie`;
  if (count <= 0) return `${verb} series`;
  return `${verb} ${count} season${count === 1 ? '' : 's'}`;
}

/** "Breaking Bad · 5 seasons · 62 episodes"; movies get "Title · 1982". */
export function modalSubtitle(
  mediaType: MediaType,
  title: string,
  year: number,
  seasons: DiscoverSeason[],
): string {
  if (mediaType === 'movie') return year > 0 ? `${title} · ${year}` : title;
  const parts = [title];
  if (seasons.length > 0) {
    parts.push(`${seasons.length} season${seasons.length === 1 ? '' : 's'}`);
  }
  const episodes = seasons.reduce((sum, s) => sum + Math.max(0, s.episode_count), 0);
  if (episodes > 0) parts.push(`${episodes} episode${episodes === 1 ? '' : 's'}`);
  return parts.join(' · ');
}

/**
 * The minimum-availability vocabulary, in temporal order. One list feeds both
 * the modal's select and the requests screen's label so the words cannot
 * drift apart.
 */
export const AVAILABILITY_OPTIONS: {
  value: MinAvailability;
  label: string;
  hint: string;
}[] = [
  { value: 'announced', label: 'Announced', hint: 'Search as soon as it is added.' },
  { value: 'in_cinemas', label: 'In cinemas', hint: 'Wait for the theatrical release.' },
  { value: 'released', label: 'Released', hint: 'Wait for a home release — digital or disc.' },
];

/** The user-facing name of an availability stage; "" for the empty default. */
export function availabilityLabel(value: string): string {
  return AVAILABILITY_OPTIONS.find((option) => option.value === value)?.label ?? '';
}

/** The one-line explanation of an availability stage shown under the select. */
export function availabilityHint(value: MinAvailability): string {
  return AVAILABILITY_OPTIONS.find((option) => option.value === value)?.hint ?? '';
}
