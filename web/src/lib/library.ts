/**
 * What a library kind means to the SPA: which shelf screen answers for it, and
 * which items it may hold.
 *
 * Pure — unit-tested in library.test.ts. The glyph half of the same question
 * lives in Icon.svelte's module block, because the list of names this build can
 * draw IS that module.
 */

import { tick } from 'svelte';
import type { LibraryKind, SessionLibrary, SessionUser } from './api/types';

/**
 * The screen that lists a shelf of this kind.
 *
 * One map rather than a ternary per call site: the sidebar builds hrefs from
 * it, and the anime screen exists precisely because a kind gained a path. A
 * kind this build does not know has no screen, which is why the answer is
 * optional rather than a guess.
 */
const KIND_PATHS: Record<string, string> = {
  movie: '/movies',
  tv: '/series',
  anime: '/anime',
  adult: '/adult',
};

export function libraryPath(kind: string): string | undefined {
  return KIND_PATHS[kind];
}

/** The shelf URL for one library: `/l/{slug}`, with adult staying on `/adult`. */
export function shelfHref(
  lib: Pick<SessionLibrary, 'id' | 'kind' | 'name' | 'slug'> | null | undefined,
): string {
  if (!lib) return '/movies';
  if (lib.kind === 'adult') return KIND_PATHS.adult ?? '/adult';
  if (lib.slug) return `/l/${encodeURIComponent(lib.slug)}`;
  const path = libraryPath(lib.kind);
  if (!path) return '/movies';
  return lib.id > 0 ? `${path}?library=${lib.id}` : path;
}

export function sessionLibraryByID(
  user: SessionUser | null,
  id: number,
): SessionLibrary | undefined {
  if (id <= 0) return undefined;
  return (user?.libraries ?? []).find((l) => l.id === id);
}

export function sessionLibraryBySlug(
  user: SessionUser | null,
  slug: string,
): SessionLibrary | undefined {
  if (!slug) return undefined;
  return (user?.libraries ?? []).find((l) => l.slug === slug);
}

/**
 * Where a detail page's back link should go, and what it should say.
 *
 * The item names a library; the session names that library. The fallback is
 * the kind root the screen used to hard-code, used only when the session has
 * not loaded the shelf yet.
 */
export function shelfBack(
  user: SessionUser | null,
  libraryID: number,
  fallback: { href: string; label: string },
): { href: string; label: string } {
  const lib = sessionLibraryByID(user, libraryID);
  if (!lib) return fallback;
  return { href: shelfHref(lib), label: lib.name };
}

/**
 * The order the sidebar groups library rows in: films, television, anime.
 *
 * Adult is absent on purpose — it does not get a row per library, it collapses
 * into the single /adult entry the module has always had.
 */
export const LIBRARY_KIND_ORDER: LibraryKind[] = ['movie', 'tv', 'anime'];

/**
 * Whether a library of `libKind` may hold an item whose own vocabulary is
 * `itemKind`. Mirrors core.LibraryKindAccepts (internal/core/library.go), which
 * is what the add, move and resolve paths on the server enforce.
 *
 *   - a library always accepts its own vocabulary;
 *   - an anime library also accepts films and television series, because it is
 *     the one shelf that speaks two vocabularies at once;
 *   - a television library accepts a row already filed as anime, which is what
 *     makes the anime shelf somewhere a series can be moved OFF as well as onto.
 *
 * Nothing widens into or out of `adult`: a site is identified by a stash-box id
 * rather than by a catalogue, and a shelf whose promise is absence is not
 * somewhere an ordinary series may drift into.
 *
 * The SPA offers rather than enforces — the server refuses a bad target either
 * way — but offering a target the server will refuse is a 400 the user cannot
 * read, so the two rules are stated the same way.
 */
export function libraryKindAccepts(libKind: string, itemKind: string): boolean {
  if (libKind === itemKind) return true;
  if (libKind === 'anime') return itemKind === 'movie' || itemKind === 'tv';
  return libKind === 'tv' && itemKind === 'anime';
}

/**
 * The ids of the session's libraries of one kind.
 *
 * /auth/me is the only library list a member has, and it is already active-only
 * and access-filtered, so this is what an unfiltered shelf screen uses to decide
 * which items are "mine" without asking the admin-only GET /libraries.
 */
export function sessionLibraryIDs(user: SessionUser | null, kind: LibraryKind): number[] {
  return (user?.libraries ?? []).filter((l) => l.kind === kind).map((l) => l.id);
}

/**
 * Kind order for a combined-surface library filter. Adult is included here
 * because Wanted and Calendar mix every shelf this session can see, including
 * the adult one when it is granted.
 */
const FILTER_KIND_ORDER: LibraryKind[] = [...LIBRARY_KIND_ORDER, 'adult'];

function filterKindRank(kind: LibraryKind): number {
  const index = FILTER_KIND_ORDER.indexOf(kind);
  return index < 0 ? FILTER_KIND_ORDER.length : index;
}

/**
 * The shelves a Wanted or Calendar library filter may offer, grouped by kind.
 *
 * /auth/me is already active-only and access-filtered, so this is the same
 * list the sidebar draws from — GET /libraries stays admin-only.
 */
export function sessionFilterLibraries(user: SessionUser | null): SessionLibrary[] {
  return [...(user?.libraries ?? [])].sort(
    (a, b) => filterKindRank(a.kind) - filterKindRank(b.kind) || a.id - b.id,
  );
}

/**
 * Whether a row belongs on a combined surface given the libraries the caller
 * checked. An empty selection means every library — that is the default, and
 * unchecking the last box returns there rather than hiding everything.
 */
export function matchesLibraryFilter(
  libraryID: number | undefined,
  selected: readonly number[],
): boolean {
  if (selected.length === 0) return true;
  return libraryID != null && selected.includes(libraryID);
}

/**
 * The library item a shared surface (Wanted, Calendar, Queue, Convert) can
 * name. Zero and missing ids are the same: this download or file is not
 * matched to that kind of row.
 */
export interface LibraryItemRef {
  movie_id?: number;
  series_id?: number;
  /** `tv`, `anime` or `adult`. Missing is treated as television. */
  series_kind?: string;
  season_number?: number;
  episode_number?: number;
}

export interface LibraryItemOrdinal {
  adult: boolean;
  season: number;
  episode: number;
}

/**
 * Element id a series episode or adult scene row wears so a hash can find it.
 *
 * Television uses season and episode numbers (`s1e1`), not the episode row
 * id. A row id of 15 is not S01E15, and hashing it sent people to the wrong
 * episode. Adult uses the release year and scene number (`y2026n24`).
 */
export function libraryItemAnchor(item: {
  series_kind?: string;
  season_number: number;
  episode_number: number;
}): string {
  if (item.series_kind === 'adult') return `y${item.season_number}n${item.episode_number}`;
  return `s${item.season_number}e${item.episode_number}`;
}

/** Parse a library-item hash, or null when it is not one. */
export function parseLibraryItemHash(hash: string): LibraryItemOrdinal | null {
  const raw = hash.startsWith('#') ? hash.slice(1) : hash;
  const adult = /^y(\d+)n(\d+)$/i.exec(raw);
  if (adult) {
    return { adult: true, season: Number(adult[1]), episode: Number(adult[2]) };
  }
  const tv = /^s(\d+)e(\d+)$/i.exec(raw);
  if (tv) {
    return { adult: false, season: Number(tv[1]), episode: Number(tv[2]) };
  }
  return null;
}

export function isLibraryItemTarget(
  hash: string,
  item: { series_kind?: string; season_number: number; episode_number: number },
): boolean {
  const target = parseLibraryItemHash(hash);
  if (!target) return false;
  const adult = item.series_kind === 'adult';
  return target.adult === adult && target.season === item.season_number && target.episode === item.episode_number;
}

/** Row treatment when this episode or scene is the hash target. */
export const LIBRARY_ITEM_TARGET_CLASS = 'bg-accent-tint ring-2 ring-inset ring-accent';

/**
 * The detail URL for a library item. A movie goes to `/movies/:id`. A
 * television or anime series goes to `/series/:id`. An adult site goes to
 * `/adult/sites/:id`. An episode or scene hash is appended when the caller
 * named the season and episode numbers, so the detail page can scroll to
 * that row.
 */
export function libraryItemHref(item: LibraryItemRef): string | undefined {
  const movieID = item.movie_id ?? 0;
  if (movieID > 0) return `/movies/${movieID}`;

  const seriesID = item.series_id ?? 0;
  if (seriesID <= 0) return undefined;

  const adult = item.series_kind === 'adult';
  const base = adult ? `/adult/sites/${seriesID}` : `/series/${seriesID}`;
  const season = item.season_number;
  const episode = item.episode_number;
  if (season === undefined || episode === undefined || season < 0 || episode < 0) return base;
  return `${base}#${libraryItemAnchor({ series_kind: item.series_kind, season_number: season, episode_number: episode })}`;
}

/**
 * Queue rows carry every episode a grab covers. A single-episode grab can
 * scroll to that row when the server named its season and episode numbers.
 * A season pack opens the series or site instead of guessing.
 */
export function downloadItemHref(item: {
  movie_id?: number;
  series_id?: number;
  series_kind?: string;
  season_number?: number;
  episode_number?: number;
}): string | undefined {
  return libraryItemHref({
    movie_id: item.movie_id,
    series_id: item.series_id,
    series_kind: item.series_kind,
    season_number: item.season_number,
    episode_number: item.episode_number,
  });
}

/**
 * Scroll to a hash target after the destination page has rendered. The router
 * tries on navigate, but a series or site page loads its rows asynchronously,
 * so the element is not there yet.
 */
export async function revealHashTarget(id: string): Promise<void> {
  if (!id) return;
  await tick();
  document.getElementById(id)?.scrollIntoView?.({ block: 'center' });
}
