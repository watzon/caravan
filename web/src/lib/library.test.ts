import { describe, expect, it } from 'vitest';
import {
  LIBRARY_KIND_ORDER,
  downloadItemHref,
  isLibraryItemTarget,
  libraryItemAnchor,
  libraryItemHref,
  parseLibraryItemHash,
  libraryKindAccepts,
  libraryPath,
  sessionLibraryByID,
  sessionLibraryBySlug,
  sessionLibraryIDs,
  sessionFilterLibraries,
  matchesLibraryFilter,
  shelfBack,
  shelfHref,
} from './library';
import type { SessionLibrary, SessionUser } from './api/types';

function user(libraries: SessionUser['libraries']): SessionUser {
  return { username: 'root', role: 'admin', open: false, adult: false, libraries };
}

function shelf(over: Partial<SessionLibrary> & { id: number }): SessionLibrary {
  return { kind: 'movie', name: `Library ${over.id}`, icon: '', ...over };
}

describe('libraryKindAccepts', () => {
  it('accepts a library its own vocabulary', () => {
    expect(libraryKindAccepts('movie', 'movie')).toBe(true);
    expect(libraryKindAccepts('tv', 'tv')).toBe(true);
    expect(libraryKindAccepts('anime', 'anime')).toBe(true);
    expect(libraryKindAccepts('adult', 'adult')).toBe(true);
  });

  it('lets an anime library hold films and television series', () => {
    expect(libraryKindAccepts('anime', 'movie')).toBe(true);
    expect(libraryKindAccepts('anime', 'tv')).toBe(true);
  });

  it('lets a television library take a series back off the anime shelf', () => {
    expect(libraryKindAccepts('tv', 'anime')).toBe(true);
  });

  it('refuses the mismatches the server refuses, adult in both directions', () => {
    expect(libraryKindAccepts('movie', 'tv')).toBe(false);
    expect(libraryKindAccepts('tv', 'movie')).toBe(false);
    expect(libraryKindAccepts('movie', 'anime')).toBe(false);
    expect(libraryKindAccepts('anime', 'adult')).toBe(false);
    expect(libraryKindAccepts('adult', 'anime')).toBe(false);
    expect(libraryKindAccepts('adult', 'movie')).toBe(false);
    expect(libraryKindAccepts('movie', 'adult')).toBe(false);
  });
});

describe('libraryPath', () => {
  it('names the screen each kind is listed on', () => {
    expect(libraryPath('movie')).toBe('/movies');
    expect(libraryPath('tv')).toBe('/series');
    expect(libraryPath('anime')).toBe('/anime');
    expect(libraryPath('adult')).toBe('/adult');
  });

  it('answers nothing for a kind this build has no screen for', () => {
    expect(libraryPath('audiobook')).toBeUndefined();
  });
});

describe('LIBRARY_KIND_ORDER', () => {
  it('groups the sidebar films, television, anime — and never adult', () => {
    expect(LIBRARY_KIND_ORDER).toEqual(['movie', 'tv', 'anime']);
  });
});

describe('sessionLibraryIDs', () => {
  it('picks out the ids of one kind', () => {
    const session = user([
      { id: 1, kind: 'movie', name: 'Movies', slug: 'movies', icon: '' },
      { id: 3, kind: 'anime', name: 'Anime', slug: 'anime', icon: '' },
      { id: 5, kind: 'anime', name: 'Films', slug: 'films', icon: 'star' },
      { id: 2, kind: 'tv', name: 'Series', slug: 'series', icon: '' },
    ]);
    expect(sessionLibraryIDs(session, 'anime')).toEqual([3, 5]);
    expect(sessionLibraryIDs(session, 'movie')).toEqual([1]);
  });

  it('reads an unknown identity, and a server too old to send the list, as none', () => {
    expect(sessionLibraryIDs(null, 'anime')).toEqual([]);
    expect(sessionLibraryIDs(user(undefined), 'anime')).toEqual([]);
  });
});

describe('shelfHref', () => {
  it('addresses a named shelf as /l/{slug}', () => {
    expect(shelfHref(shelf({ id: 3, kind: 'anime', name: 'Anime', slug: 'anime' }))).toBe(
      '/l/anime',
    );
    expect(shelfHref(shelf({ id: 4, kind: 'movie', name: 'Kids', slug: 'kids' }))).toBe('/l/kids');
  });

  it('keeps the adult module on /adult, whatever the slug', () => {
    expect(shelfHref(shelf({ id: 9, kind: 'adult', name: 'Adult', slug: 'adult' }))).toBe('/adult');
  });

  it('falls back to the kind path plus ?library= when the slug is missing', () => {
    expect(shelfHref(shelf({ id: 4, kind: 'movie', name: 'Kids' }))).toBe('/movies?library=4');
    expect(shelfHref(shelf({ id: 3, kind: 'anime', name: 'Anime' }))).toBe('/anime?library=3');
  });
});

describe('sessionFilterLibraries', () => {
  it('groups by kind, movies first, adult last, then by id', () => {
    expect(
      sessionFilterLibraries(
        user([
          shelf({ id: 9, kind: 'adult', name: 'Adult' }),
          shelf({ id: 4, kind: 'movie', name: 'Kids' }),
          shelf({ id: 3, kind: 'anime', name: 'Anime' }),
          shelf({ id: 1, kind: 'movie', name: 'Movies' }),
          shelf({ id: 2, kind: 'tv', name: 'Series' }),
        ]),
      ).map((library) => library.id),
    ).toEqual([1, 4, 2, 3, 9]);
  });

  it('treats a missing library list as empty, not as "no libraries exist"', () => {
    expect(sessionFilterLibraries(user(undefined))).toEqual([]);
    expect(sessionFilterLibraries(null)).toEqual([]);
  });
});

describe('matchesLibraryFilter', () => {
  it('shows every row when nothing is checked', () => {
    expect(matchesLibraryFilter(1, [])).toBe(true);
    expect(matchesLibraryFilter(undefined, [])).toBe(true);
    expect(matchesLibraryFilter(0, [])).toBe(true);
  });

  it('keeps only the checked libraries', () => {
    expect(matchesLibraryFilter(1, [1, 2])).toBe(true);
    expect(matchesLibraryFilter(3, [1, 2])).toBe(false);
    expect(matchesLibraryFilter(undefined, [1])).toBe(false);
    expect(matchesLibraryFilter(0, [1])).toBe(false);
  });
});

describe('shelfBack', () => {
  const session = user([
    shelf({ id: 1, kind: 'movie', name: 'Movies', slug: 'movies' }),
    shelf({ id: 3, kind: 'anime', name: 'Anime', slug: 'anime' }),
  ]);

  it('points at the item\'s own library, by name', () => {
    expect(shelfBack(session, 3, { href: '/series', label: 'Series' })).toEqual({
      href: '/l/anime',
      label: 'Anime',
    });
  });

  it('keeps the kind-root fallback when the session does not know the shelf', () => {
    expect(shelfBack(session, 99, { href: '/series', label: 'Series' })).toEqual({
      href: '/series',
      label: 'Series',
    });
    expect(sessionLibraryByID(session, 3)?.slug).toBe('anime');
    expect(sessionLibraryBySlug(session, 'movies')?.id).toBe(1);
  });
});

describe('libraryItemHref', () => {
  it('routes movies, television, anime and adult sites to their detail pages', () => {
    expect(libraryItemHref({ movie_id: 7 })).toBe('/movies/7');
    expect(libraryItemHref({ series_id: 3 })).toBe('/series/3');
    expect(libraryItemHref({ series_id: 3, series_kind: 'tv' })).toBe('/series/3');
    expect(libraryItemHref({ series_id: 4, series_kind: 'anime' })).toBe('/series/4');
    expect(libraryItemHref({ series_id: 9, series_kind: 'adult' })).toBe('/adult/sites/9');
  });

  it('hashes a single episode or scene by season and number, never by row id', () => {
    expect(libraryItemHref({ series_id: 3, season_number: 1, episode_number: 1 })).toBe(
      '/series/3#s1e1',
    );
    expect(
      libraryItemHref({ series_id: 9, series_kind: 'adult', season_number: 2026, episode_number: 24 }),
    ).toBe('/adult/sites/9#y2026n24');
    expect(libraryItemAnchor({ season_number: 1, episode_number: 1 })).toBe('s1e1');
    expect(libraryItemAnchor({ series_kind: 'adult', season_number: 2026, episode_number: 24 })).toBe(
      'y2026n24',
    );
    expect(libraryItemHref({ series_id: 3, episode_number: 15 })).toBe('/series/3');
    expect(parseLibraryItemHash('#s1e1')).toEqual({ adult: false, season: 1, episode: 1 });
    expect(parseLibraryItemHash('#y2026n24')).toEqual({ adult: true, season: 2026, episode: 24 });
    expect(isLibraryItemTarget('#s1e1', { season_number: 1, episode_number: 1 })).toBe(true);
    expect(isLibraryItemTarget('#s1e1', { season_number: 1, episode_number: 15 })).toBe(false);
  });

  it('treats a missing or zero id as unmatched', () => {
    expect(libraryItemHref({})).toBeUndefined();
    expect(libraryItemHref({ movie_id: 0, series_id: 0 })).toBeUndefined();
  });

  it('prefers the movie when both ids are present', () => {
    expect(libraryItemHref({ movie_id: 7, series_id: 3 })).toBe('/movies/7');
  });
});

describe('downloadItemHref', () => {
  it('scrolls only when the grab named a season and episode', () => {
    expect(
      downloadItemHref({ series_id: 3, series_kind: 'tv', season_number: 1, episode_number: 1 }),
    ).toBe('/series/3#s1e1');
    expect(downloadItemHref({ series_id: 3, series_kind: 'tv' })).toBe('/series/3');
    expect(downloadItemHref({ movie_id: 7 })).toBe('/movies/7');
    expect(downloadItemHref({})).toBeUndefined();
  });
});
