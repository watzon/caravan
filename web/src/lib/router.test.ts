import { describe, expect, it } from 'vitest';
import { ROUTES, matchPath, matchRoutes, normalizePath, numericParam } from './router';

describe('normalizePath', () => {
  const cases: [string, string][] = [
    ['', '/'],
    ['/', '/'],
    ['movies', '/movies'],
    ['/movies/', '/movies'],
    ['/movies//', '/movies'],
    ['/movies?filter=wanted', '/movies'],
    ['/movies#top', '/movies'],
  ];

  for (const [input, want] of cases) {
    it(`normalizes ${JSON.stringify(input)} to ${want}`, () => {
      expect(normalizePath(input)).toBe(want);
    });
  }
});

describe('matchPath', () => {
  it('matches a literal route', () => {
    expect(matchPath('/movies', '/movies')).toEqual({});
  });

  it('captures params', () => {
    expect(matchPath('/movies/:id', '/movies/42')).toEqual({ id: '42' });
  });

  it('decodes param values', () => {
    expect(matchPath('/movies/:id', '/movies/a%20b')).toEqual({ id: 'a b' });
  });

  it('rejects a different segment count', () => {
    expect(matchPath('/movies/:id', '/movies')).toBeNull();
    expect(matchPath('/movies', '/movies/42')).toBeNull();
  });

  it('rejects a different literal', () => {
    expect(matchPath('/movies/:id', '/series/42')).toBeNull();
  });
});

describe('matchRoutes', () => {
  it('prefers the literal route over the param route', () => {
    // '/movies' is listed before '/movies/:id', so a bare list URL never
    // resolves to a detail screen with an empty id.
    expect(matchRoutes(ROUTES, '/movies')?.pattern).toBe('/movies');
  });

  it('resolves detail routes', () => {
    expect(matchRoutes(ROUTES, '/series/7')).toEqual({
      pattern: '/series/:id',
      params: { id: '7' },
    });
  });

  it('returns null for an unknown path', () => {
    expect(matchRoutes(ROUTES, '/calendar')).toBeNull();
  });
});

describe('numericParam', () => {
  it('parses positive integers', () => {
    expect(numericParam({ id: '42' }, 'id')).toBe(42);
  });

  it('rejects anything else', () => {
    expect(numericParam({ id: 'abc' }, 'id')).toBe(0);
    expect(numericParam({ id: '-1' }, 'id')).toBe(0);
    expect(numericParam({ id: '1.5' }, 'id')).toBe(0);
    expect(numericParam({}, 'id')).toBe(0);
  });
});
