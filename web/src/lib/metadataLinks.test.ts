/**
 * Provider link construction: every id the library holds gets a link, absent
 * ids are omitted rather than producing dead URLs.
 */
import { describe, expect, it } from 'vitest';
import { episodeLink, movieLinks, seriesLinks, siteLinks } from './metadataLinks';

describe('movieLinks', () => {
  it('links TMDB and IMDb when both ids exist', () => {
    expect(movieLinks({ tmdb_id: 10378, imdb_id: 'tt1254207' })).toEqual([
      { label: 'TMDB', href: 'https://www.themoviedb.org/movie/10378' },
      { label: 'IMDb', href: 'https://www.imdb.com/title/tt1254207/' },
    ]);
  });

  it('omits providers whose id is missing', () => {
    expect(movieLinks({ tmdb_id: 0, imdb_id: '' })).toEqual([]);
    expect(movieLinks({ tmdb_id: 10378, imdb_id: '' }).map((l) => l.label)).toEqual(['TMDB']);
  });
});

describe('seriesLinks', () => {
  it('links TMDB, IMDb and TVDB when every id exists', () => {
    expect(seriesLinks({ tmdb_id: 312949, tvdb_id: 473423, imdb_id: 'tt39551330' })).toEqual([
      { label: 'TMDB', href: 'https://www.themoviedb.org/tv/312949' },
      { label: 'IMDb', href: 'https://www.imdb.com/title/tt39551330/' },
      { label: 'TVDB', href: 'https://www.thetvdb.com/dereferrer/series/473423' },
    ]);
  });

  it('omits providers whose id is missing', () => {
    expect(seriesLinks({ tmdb_id: 312949, tvdb_id: 0, imdb_id: '' }).map((l) => l.label)).toEqual([
      'TMDB',
    ]);
  });
});

describe('siteLinks', () => {
  it('names the known stash-box endpoints by their wordmark', () => {
    expect(siteLinks({ provider_url: 'https://theporndb.net/sites/abc' })).toEqual([
      { label: 'TPDB', href: 'https://theporndb.net/sites/abc' },
    ]);
    expect(siteLinks({ provider_url: 'https://stashdb.org/studios/abc' })).toEqual([
      { label: 'StashDB', href: 'https://stashdb.org/studios/abc' },
    ]);
  });

  it('falls back to the hostname for an endpoint it has no wordmark for', () => {
    expect(siteLinks({ provider_url: 'https://www.example.test/studios/abc' })).toEqual([
      { label: 'example.test', href: 'https://www.example.test/studios/abc' },
    ]);
  });

  it('omits the chip when the server derived no page', () => {
    expect(siteLinks({ provider_url: '' })).toEqual([]);
  });
});

describe('episodeLink', () => {
  it('routes by season and episode number under the series', () => {
    expect(episodeLink(312949, 1, 3)).toBe(
      'https://www.themoviedb.org/tv/312949/season/1/episode/3',
    );
  });

  it('handles season 0, which is Specials rather than "unset"', () => {
    expect(episodeLink(312949, 0, 13)).toBe(
      'https://www.themoviedb.org/tv/312949/season/0/episode/13',
    );
  });

  it('returns null without a series TMDB id', () => {
    expect(episodeLink(0, 1, 1)).toBeNull();
  });
});
