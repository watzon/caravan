import { describe, expect, it } from 'vitest';
import type { Library, UnmatchedFile } from './api/types';
import { unmatchedMatchStrategy } from './match';

function library(overrides: Partial<Library> = {}): Library {
  return {
    id: 4,
    kind: 'movie',
    name: 'Movies',
    icon: '',
    root_path: 'library/Movies',
    provider: 'tmdb',
    providers: ['tmdb'],
    is_default: false,
    item_count: 0,
    active: true,
    restricted: false,
    dlna_visible: true,
    route_torrent: '',
    route_usenet: '',
    quality_profile_id: 0,
    indexers: [],
    ...overrides,
  };
}

function file(overrides: Partial<UnmatchedFile> = {}): UnmatchedFile {
  return {
    id: 1,
    path: 'incomplete/Some.Release/file.mkv',
    size: 1,
    parsed: {
      title: 'Some Release',
      year: 2026,
      season: 0,
      episodes: [],
      quality: '',
      source: '',
      codec: '',
      audio: '',
      bit_depth: 0,
      group: '',
      proper: false,
      repack: false,
      edition: '',
      confidence: 0.5,
    },
    reason: 'no match',
    library_id: 0,
    seen_at: '2026-08-11T00:00:00Z',
    ...overrides,
  };
}

describe('unmatchedMatchStrategy', () => {
  it('uses title search for an unscoped movie guess', () => {
    expect(unmatchedMatchStrategy(file(), [])).toEqual({
      kind: 'title',
      mediaType: 'movie',
      query: 'Some Release',
      libraryID: 0,
    });
  });

  it('lets a TV library and its provider chain override a movie-shaped parse', () => {
    const anime = library({
      id: 8,
      kind: 'tv',
      name: 'Anime',
      provider: 'anilist',
      providers: ['anilist', 'tmdb'],
    });
    expect(unmatchedMatchStrategy(file({ library_id: 8 }), [anime])).toEqual({
      kind: 'title',
      mediaType: 'series',
      query: 'Some Release',
      libraryID: 8,
    });
  });

  it('uses the adult site, date, fallback text, and full target provider chain', () => {
    const adult = library({
      id: 3,
      kind: 'adult',
      name: 'Adult',
      provider: 'stashbox',
      providers: ['stashbox', 'stashbox:fansdb'],
    });
    const parked = file({
      library_id: 3,
      path: 'incomplete/AfricanCasting.20.01.26.Scarlet.XXX.1080p.MP4-WRB/hash.mp4',
      parsed: {
        ...file().parsed,
        title: 'AfricanCasting',
        scene_date: '2020-01-26T00:00:00Z',
      },
    });
    expect(unmatchedMatchStrategy(parked, [adult])).toEqual({
      kind: 'scene',
      siteQuery: 'AfricanCasting',
      sceneDate: '2020-01-26T00:00:00Z',
      fallbackQuery: 'Scarlet',
      providers: ['stashbox', 'stashbox:fansdb'],
    });
  });

  it('uses the default adult library chain for an unscoped scene', () => {
    const adult = library({
      id: 3,
      kind: 'adult',
      name: 'Adult',
      provider: 'stashbox',
      providers: ['stashbox', 'stashbox:fansdb'],
      is_default: true,
    });
    const parked = file({
      parsed: {
        ...file().parsed,
        scene_date: '2020-01-26T00:00:00Z',
      },
    });
    expect(unmatchedMatchStrategy(parked, [adult])).toMatchObject({
      kind: 'scene',
      providers: ['stashbox', 'stashbox:fansdb'],
    });
  });

  it('refuses to search a different chain when a scoped library is missing', () => {
    expect(unmatchedMatchStrategy(file({ library_id: 99 }), [])).toBeNull();
  });
});
