import { describe, expect, it } from 'vitest';
import { parseReleaseSearch, releaseSearchHref } from './search';

function parse(search: string) {
  return parseReleaseSearch(new URLSearchParams(search));
}

describe('parseReleaseSearch', () => {
  it('reads an empty query string as an empty search', () => {
    expect(parse('')).toEqual({ q: '', cats: [], indexers: [] });
  });

  it('reads the query, categories and indexers', () => {
    expect(parse('q=blade+runner&cats=2000,5000&indexers=1,3')).toEqual({
      q: 'blade runner',
      cats: [2000, 5000],
      indexers: [1, 3],
    });
  });

  it('trims the query so a stray space is not a different search', () => {
    expect(parse('q=%20dune%20').q).toBe('dune');
  });

  it('drops entries a hand-edited URL got wrong rather than refusing to search', () => {
    expect(parse('q=x&cats=2000,abc,-5,').cats).toEqual([2000]);
  });

  it('collapses duplicate ids so a list stays stable across edits', () => {
    expect(parse('q=x&indexers=1,1,2').indexers).toEqual([1, 2]);
  });
});

describe('releaseSearchHref', () => {
  it('answers the bare path for an empty search', () => {
    expect(releaseSearchHref({ q: '', cats: [], indexers: [] })).toBe('/search');
  });

  it('omits the filters that are not set', () => {
    expect(releaseSearchHref({ q: 'dune', cats: [], indexers: [] })).toBe('/search?q=dune');
  });

  it('joins ids the way the API takes them', () => {
    expect(releaseSearchHref({ q: 'dune', cats: [2000], indexers: [1, 2] })).toBe(
      '/search?q=dune&cats=2000&indexers=1%2C2',
    );
  });

  it('round-trips through parseReleaseSearch', () => {
    const query = { q: 'blade runner 2049', cats: [2000, 5000], indexers: [3] };
    const href = releaseSearchHref(query);
    expect(parse(href.slice(href.indexOf('?') + 1))).toEqual(query);
  });
});
