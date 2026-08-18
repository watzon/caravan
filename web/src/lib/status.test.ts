import { describe, expect, it } from 'vitest';
import type { Episode, MediaFile, Movie, Series } from './api/types';
import { STATUS, episodeStatus, movieStatus, seriesStatus } from './status';

const FILE: MediaFile = {
  id: 1,
  path: 'Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mp4',
  size: 1024,
  movie_id: 1,
  quality: '1080p',
  source: 'bluray',
  codec: 'x264',
  audio: 'AAC',
  release_group: 'GROUP',
  added_at: '2026-07-31T00:00:00Z',
  modified_at: '2026-07-31T00:00:00Z',
  compatibility: { verdict: 'compatible', reasons: [] },
};

function movie(overrides: Partial<Movie> = {}): Movie {
  return {
    id: 1,
    tmdb_id: 10378,
    imdb_id: '',
    title: 'Big Buck Bunny',
    sort_title: 'big buck bunny',
    year: 2008,
    overview: '',
    path: 'Movies/Big Buck Bunny (2008)',
    poster_path: '',
    poster_url: '',
    monitored: true,
    quality_profile_id: 0,
    library_id: 1,
    release_date: '2008-05-20',
    min_availability: 'released',
    added_at: '2026-07-31T00:00:00Z',
    updated_at: '2026-07-31T00:00:00Z',
    ...overrides,
  };
}

function series(overrides: Partial<Series> = {}): Series {
  return {
    id: 1,
    tmdb_id: 1,
    tvdb_id: 0,
    imdb_id: '',
    title: 'Planet Earth II',
    sort_title: 'planet earth ii',
    year: 2016,
    overview: '',
    status: 'Ended',
    path: 'TV/Planet Earth II (2016)',
    poster_path: '',
    poster_url: '',
    monitored: true,
    quality_profile_id: 0,
    library_id: 1,
    first_aired: '2016-11-06',
    added_at: '2026-07-31T00:00:00Z',
    updated_at: '2026-07-31T00:00:00Z',
    ...overrides,
  };
}

function episode(overrides: Partial<Episode> = {}): Episode {
  return {
    id: 1,
    series_id: 1,
    season_number: 1,
    episode_number: 1,
    tmdb_id: 0,
    title: 'Islands',
    overview: '',
    air_date: '2016-11-06',
    monitored: true,
    ...overrides,
  };
}

describe('movieStatus', () => {
  it('is downloaded when a file exists, whatever the monitored flag says', () => {
    expect(movieStatus(movie({ file: FILE }))).toBe('downloaded');
    expect(movieStatus(movie({ file: FILE, monitored: false }))).toBe('downloaded');
  });

  it('is wanted when monitored with no file', () => {
    expect(movieStatus(movie())).toBe('wanted');
  });

  it('is downloading when a grab is in flight and no file exists yet', () => {
    expect(movieStatus(movie({ downloading: true }))).toBe('downloading');
    expect(movieStatus(movie({ downloading: true, file: FILE }))).toBe('downloaded');
  });

  it('is unmonitored when unmonitored with no file', () => {
    expect(movieStatus(movie({ monitored: false }))).toBe('unmonitored');
  });
});

describe('seriesStatus', () => {
  it('is downloaded once every known episode has a file', () => {
    expect(seriesStatus(series({ episode_count: 6, episode_file_count: 6 }))).toBe('downloaded');
  });

  it('is incomplete on a partial season', () => {
    expect(seriesStatus(series({ episode_count: 6, episode_file_count: 2 }))).toBe('incomplete');
  });

  it('is downloading when a grab is in flight and the series is not complete', () => {
    expect(seriesStatus(series({ episode_count: 6, episode_file_count: 0, downloading: true }))).toBe(
      'downloading',
    );
    expect(seriesStatus(series({ episode_count: 6, episode_file_count: 2, downloading: true }))).toBe(
      'downloading',
    );
  });

  it('falls back to wanted/unmonitored when nothing is owned', () => {
    expect(seriesStatus(series({ episode_count: 6, episode_file_count: 0 }))).toBe('wanted');
    expect(
      seriesStatus(series({ episode_count: 6, episode_file_count: 0, monitored: false })),
    ).toBe('unmonitored');
  });

  it('does not claim downloaded when the episode count is unknown', () => {
    expect(seriesStatus(series())).toBe('wanted');
  });
});

describe('episodeStatus', () => {
  const now = Date.parse('2026-07-31T00:00:00Z');

  it('is downloaded when a file exists', () => {
    expect(episodeStatus(episode({ file: FILE }), now)).toBe('downloaded');
  });

  it('is missing when it aired, is monitored and has no file', () => {
    expect(episodeStatus(episode(), now)).toBe('missing');
  });

  it('is downloading when a grab is in flight', () => {
    expect(episodeStatus(episode({ downloading: true }), now)).toBe('downloading');
  });

  it('is unaired when the air date is in the future or unknown', () => {
    expect(episodeStatus(episode({ air_date: '2027-01-01' }), now)).toBe('unaired');
    expect(episodeStatus(episode({ air_date: '' }), now)).toBe('unaired');
  });

  it('is unaired even when the episode is not monitored', () => {
    expect(episodeStatus(episode({ air_date: '2027-01-01', monitored: false }), now)).toBe(
      'unaired',
    );
    expect(episodeStatus(episode({ air_date: '', monitored: false }), now)).toBe('unaired');
  });

  it('is unmonitored after the episode has aired with no file', () => {
    expect(episodeStatus(episode({ monitored: false }), now)).toBe('unmonitored');
  });
});

describe('status vocabulary', () => {
  it('maps colours the way DESIGN.md §2.3 specifies', () => {
    expect(STATUS.downloaded.tone).toBe('success'); // moss = present/healthy
    expect(STATUS.downloading.tone).toBe('accent'); // rust = in progress
    expect(STATUS.wanted.tone).toBe('accent'); // rust = wanted/active
    expect(STATUS.incomplete.tone).toBe('warning'); // amber = warning
    expect(STATUS.missing.tone).toBe('danger'); // red = missing
    expect(STATUS.unaired.tone).toBe('info'); // dusk blue = informational
  });
});
