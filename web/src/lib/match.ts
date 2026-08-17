import type { Library, UnmatchedFile } from './api/types';
import { sceneMatchQuery } from './adult';

export interface TitleMatchStrategy {
  kind: 'title';
  mediaType: 'movie' | 'series';
  query: string;
  libraryID: number;
}

export interface SceneMatchStrategy {
  kind: 'scene';
  siteQuery: string;
  sceneDate: string;
  fallbackQuery: string;
  providers: string[];
}

export type UnmatchedMatchStrategy = TitleMatchStrategy | SceneMatchStrategy;

function libraryFor(file: UnmatchedFile, libraries: readonly Library[]): Library | undefined {
  if (!file.library_id) return undefined;
  return libraries.find((library) => library.id === file.library_id);
}

function providerChain(library: Library | undefined): string[] {
  if (!library) return [];
  if (library.providers.length > 0) return library.providers;
  return library.provider ? [library.provider] : [];
}

/**
 * The evidence and provider chain used to find candidates for one parked file.
 *
 * Providers own their search dialect behind the API. This function only chooses
 * the media-specific evidence: title text for movies and series (including an
 * AniList-backed TV library), or site plus release date for adult scenes.
 *
 * A scoped file returns null when its library is absent. Searching another
 * library's provider chain would offer candidates that the import cannot use.
 */
export function unmatchedMatchStrategy(
  file: UnmatchedFile,
  libraries: readonly Library[],
): UnmatchedMatchStrategy | null {
  const library = libraryFor(file, libraries);
  if (file.library_id !== 0 && !library) return null;

  const sceneShaped = library?.kind === 'adult' || Boolean(file.parsed.scene_date);
  if (sceneShaped) {
    if (library && library.kind !== 'adult') return null;
    const adultLibrary = library ?? libraries.find((candidate) =>
      candidate.kind === 'adult' && candidate.is_default
    );
    return {
      kind: 'scene',
      siteQuery: file.parsed.title,
      sceneDate: file.parsed.scene_date ?? '',
      fallbackQuery: sceneMatchQuery(file.path, file.parsed.title),
      providers: providerChain(adultLibrary),
    };
  }

  // A television library only ever holds series, so its kind decides. An ANIME
  // library holds both, so there the parse decides — which is the same rule the
  // scanner applies to a file under an anime root (internal/library/scan.go):
  // episode numbers make it a series, and everything else is a film.
  const mediaType = library?.kind === 'tv' || (file.parsed.episodes?.length ?? 0) > 0
    ? 'series'
    : 'movie';
  return {
    kind: 'title',
    mediaType,
    // The library's own chain is what should answer the manual search, so the
    // id travels for every kind whose items this dialog can match. Adult is
    // absent because a scene-shaped file left through the branch above.
    libraryID:
      library?.kind === 'movie' || library?.kind === 'tv' || library?.kind === 'anime'
        ? file.library_id
        : 0,
    query: file.parsed.title,
  };
}
