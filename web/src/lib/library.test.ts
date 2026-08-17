import { describe, expect, it } from 'vitest';
import {
  LIBRARY_KIND_ORDER,
  libraryKindAccepts,
  libraryPath,
  sessionLibraryByID,
  sessionLibraryBySlug,
  sessionLibraryIDs,
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
