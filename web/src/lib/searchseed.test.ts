import { describe, expect, it } from 'vitest';
import { movieSeed, seriesSeed, sceneSeed } from './searchseed';

/**
 * Every expectation here is the exact string internal/searchql's seed builder
 * writes (pinned by its own Go tests). If one of these fails after a server
 * change, the twins have drifted and the box will visibly rewrite itself when
 * the search lands — update both sides together.
 */
describe('search seed twins', () => {
  it('spells a movie the way the server does', () => {
    expect(movieSeed('Dune', 2021)).toBe('title:"Dune" year:2021');
    expect(movieSeed('Big Buck Bunny', 0)).toBe('title:"Big Buck Bunny"');
  });

  it('spells a series the way the server does', () => {
    expect(seriesSeed('Some Show', 1, 2)).toBe('title:"Some Show" season:1 episode:2');
    expect(seriesSeed('Some Show', 1, 0)).toBe('title:"Some Show" season:1');
    expect(seriesSeed('Some Show', -1, 0)).toBe('title:"Some Show"');
  });

  it('spells a scene the way the server does, both variants included', () => {
    expect(sceneSeed('Creampie Thais', '2026-06-14T00:00:00Z', 'Moie')).toBe(
      '(site:"Creampie Thais" date:2026-06-14) OR "Creampie Thais Moie"',
    );
    expect(sceneSeed('Creampie Thais', '2026-06-14', '')).toBe(
      'site:"Creampie Thais" date:2026-06-14',
    );
    expect(sceneSeed('Creampie Thais', '', 'Moie')).toBe('"Creampie Thais Moie"');
    expect(sceneSeed('Creampie Thais', '', '!!!')).toBe('site:"Creampie Thais"');
  });

  it('escapes embedded quotes like the server', () => {
    expect(movieSeed('The "Real" Story', 1999)).toBe('title:"The \\"Real\\" Story" year:1999');
  });
});
