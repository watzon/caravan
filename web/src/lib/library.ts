/**
 * What a library kind means to the SPA: which shelf screen answers for it, and
 * which items it may hold.
 *
 * Pure — unit-tested in library.test.ts. The glyph half of the same question
 * lives in Icon.svelte's module block, because the list of names this build can
 * draw IS that module.
 */

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
