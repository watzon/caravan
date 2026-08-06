import { describe, expect, it, vi } from 'vitest';
import {
  ROUTES,
  isAdultRoute,
  matchPath,
  matchRoutes,
  memberAllowedRoute,
  normalizePath,
  numericParam,
  ordinalParam,
  splitLocation,
} from './router';
import { navigate, router, startRouter } from './router.svelte';

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
    expect(matchRoutes(ROUTES, '/definitely-not-a-route')).toBeNull();
  });

  it('resolves the movie release picker without shadowing the detail route', () => {
    expect(matchRoutes(ROUTES, '/movies/42')?.pattern).toBe('/movies/:id');
    expect(matchRoutes(ROUTES, '/movies/42/search')).toEqual({
      pattern: '/movies/:id/search',
      params: { id: '42' },
    });
  });

  it('resolves season and episode pickers', () => {
    expect(matchRoutes(ROUTES, '/series/7/search/2')).toEqual({
      pattern: '/series/:id/search/:season',
      params: { id: '7', season: '2' },
    });
    expect(matchRoutes(ROUTES, '/series/7/search/2/5')).toEqual({
      pattern: '/series/:id/search/:season/:episode',
      params: { id: '7', season: '2', episode: '5' },
    });
  });

  it('resolves the queue', () => {
    expect(matchRoutes(ROUTES, '/queue')?.pattern).toBe('/queue');
  });

  // The index is Discover now: it is a route in its own right, not a redirect
  // to the library.
  it('resolves the index and the discover screens', () => {
    expect(matchRoutes(ROUTES, '/')?.pattern).toBe('/');
    expect(matchRoutes(ROUTES, '/discover')?.pattern).toBe('/discover');
    expect(matchRoutes(ROUTES, '/requests')?.pattern).toBe('/requests');
  });

  /**
   * The filtered scopes (phase 12). They are two-segment paths and the title
   * screens are three-segment ones, so /discover/movies can never be read as
   * "the movie whose id is 'movies'".
   */
  it('resolves the filtered scopes without shadowing the title screens', () => {
    expect(matchRoutes(ROUTES, '/discover/movies')?.pattern).toBe('/discover/movies');
    expect(matchRoutes(ROUTES, '/discover/series')?.pattern).toBe('/discover/series');
    expect(matchRoutes(ROUTES, '/discover/adult')?.pattern).toBe('/discover/adult');
    expect(matchRoutes(ROUTES, '/discover/movie/78')?.pattern).toBe('/discover/movie/:tmdbId');
  });

  /**
   * A filter is a query string, and the router matches paths. A scope's
   * filtered URL therefore resolves to the same screen as its bare one — which
   * is the whole reason filters can live there without a route per filter.
   */
  it('matches a filtered scope as the scope it is', () => {
    expect(matchRoutes(ROUTES, '/discover/movies?genres=878&people=1245')).toEqual({
      pattern: '/discover/movies',
      params: {},
    });
  });

  it('keeps the browse shelves apart from the title screens', () => {
    expect(matchRoutes(ROUTES, '/discover/network/213')).toEqual({
      pattern: '/discover/network/:id',
      params: { id: '213' },
    });
    expect(matchRoutes(ROUTES, '/discover/studio/41077')).toEqual({
      pattern: '/discover/studio/:id',
      params: { id: '41077' },
    });
  });

  // These ids are TMDB's, not the library's — /discover/series/1396 and
  // /series/1396 are different titles entirely.
  it('resolves a discover title by media type', () => {
    expect(matchRoutes(ROUTES, '/discover/movie/78')).toEqual({
      pattern: '/discover/movie/:tmdbId',
      params: { tmdbId: '78' },
    });
    expect(matchRoutes(ROUTES, '/discover/series/1396')).toEqual({
      pattern: '/discover/series/:tmdbId',
      params: { tmdbId: '1396' },
    });
  });
});

/**
 * The second, independent gate on the adult screens (PLAN phase 9 track 5).
 *
 * `memberAllowedRoute` answers a question about a ROLE; this answers one about
 * a per-account grant that an admin also has to have been given. Both have to
 * hold, and the pair is what keeps an admin who switched the module OFF away
 * from screens their role would otherwise open.
 */
describe('isAdultRoute', () => {
  it('names every adult screen, and is derived rather than listed', () => {
    for (const pattern of [
      '/adult',
      '/adult/scenes',
      '/adult/sites/:id',
      // Phase 12: scene browsing moved next to the other two catalogues, so an
      // adult screen now lives outside /adult for the first time.
      '/discover/adult',
    ] as const) {
      expect(isAdultRoute(pattern), pattern).toBe(true);
    }
    // Derived from the path, so a route added under /adult tomorrow is gated by
    // having been added rather than by somebody remembering to name it twice.
    // The scene picker is filed here for exactly that reason: it could have
    // lived under /series/:id/search, which works — a site IS a series row —
    // and would have been a screen the adult gate could not see.
    //
    // /discover/adult is the ONE route the prefix rule cannot reach, so it is
    // named in router.ts and pinned here: this list is the whole gate, and a
    // future adult screen filed somewhere the rule misses fails this test
    // rather than shipping ungated.
    expect(ROUTES.filter(isAdultRoute)).toEqual([
      '/discover/adult',
      '/adult',
      '/adult/scenes',
      '/adult/sites/:id',
      '/adult/sites/:id/search',
      '/adult/sites/:id/search/:year',
      '/adult/sites/:id/search/:year/:number',
    ]);
  });

  it('gates nothing else in the app', () => {
    for (const pattern of ROUTES) {
      if (pattern === '/adult' || pattern.startsWith('/adult/')) continue;
      if (pattern === '/discover/adult') continue;
      expect(isAdultRoute(pattern), pattern).toBe(false);
    }
  });

  /**
   * The sibling scopes are NOT adult routes. /discover/adult being named by
   * hand is the risk this covers: a rule spelled out rather than derived is a
   * rule that can be spelled too widely, and gating /discover/movies would put
   * the whole of Explore behind a grant nobody has.
   */
  it('leaves the other explore scopes alone', () => {
    for (const pattern of ['/discover', '/discover/movies', '/discover/series'] as const) {
      expect(isAdultRoute(pattern), pattern).toBe(false);
    }
  });

  /**
   * The two gates are independent and both are required. A member is not barred
   * by their ROLE from the three adult READS — the server's own allowlist names
   * the same three — which is exactly why the grant has to be checked
   * separately.
   */
  it('is a separate question from the member allowlist', () => {
    for (const pattern of [
      '/adult',
      '/adult/scenes',
      '/adult/sites/:id',
      '/discover/adult',
    ] as const) {
      expect(memberAllowedRoute(pattern), pattern).toBe(true);
    }
  });

  /**
   * The other half of the same point: being an adult route does not make a
   * route a member's. The scene picker grabs releases, which is an admin write,
   * and the server keeps the release and grab routes admin-only — so a granted
   * member needs BOTH gates to say yes, and here one of them says no.
   */
  it('keeps the scene picker out of the member allowlist', () => {
    for (const pattern of [
      '/adult/sites/:id/search',
      '/adult/sites/:id/search/:year',
      '/adult/sites/:id/search/:year/:number',
    ] as const) {
      expect(isAdultRoute(pattern), pattern).toBe(true);
      expect(memberAllowedRoute(pattern), pattern).toBe(false);
    }
  });
});

describe('ordinalParam', () => {
  it('accepts zero, because season 0 is Specials', () => {
    expect(ordinalParam({ season: '0' }, 'season')).toBe(0);
    expect(ordinalParam({ season: '12' }, 'season')).toBe(12);
  });

  it('answers -1 for anything unparseable, never 0', () => {
    expect(ordinalParam({ season: 'abc' }, 'season')).toBe(-1);
    expect(ordinalParam({ season: '-1' }, 'season')).toBe(-1);
    expect(ordinalParam({ season: '1.5' }, 'season')).toBe(-1);
    expect(ordinalParam({}, 'season')).toBe(-1);
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

/**
 * The path/query/fragment split. `normalizePath` cuts query and fragment
 * strings because the ROUTE table has no room for either. `splitLocation`
 * keeps both parts that navigation must retain.
 */
describe('splitLocation', () => {
  const cases: [string, { path: string; search: string; hash: string }][] = [
    ['/discover/movies', { path: '/discover/movies', search: '', hash: '' }],
    // '?' with nothing after it is the same URL as no query string at all, and
    // must compare equal or navigate() would push a history entry for a no-op.
    ['/discover/movies?', { path: '/discover/movies', search: '', hash: '' }],
    [
      '/discover/movies?genres=878&people=1245',
      { path: '/discover/movies', search: 'genres=878&people=1245', hash: '' },
    ],
    // Repeated keys survive: two people is two parameters, not one joined.
    [
      '/discover/movies?people=1&people=2',
      { path: '/discover/movies', search: 'people=1&people=2', hash: '' },
    ],
    ['/discover/movies#top', { path: '/discover/movies', search: '', hash: '#top' }],
    [
      '/settings/downloads?tab=engine#download-clients',
      { path: '/settings/downloads', search: 'tab=engine', hash: '#download-clients' },
    ],
    [
      '/discover/movies/?genres=878',
      { path: '/discover/movies', search: 'genres=878', hash: '' },
    ],
    ['/adult/scenes', { path: '/adult/scenes', search: '', hash: '' }],
  ];

  for (const [input, want] of cases) {
    it(`splits ${JSON.stringify(input)}`, () => {
      expect(splitLocation(input)).toEqual(want);
    });
  }

  it('leaves normalizePath dropping the query, which is what routing wants', () => {
    expect(normalizePath('/discover/movies?genres=878')).toBe('/discover/movies');
  });
});

describe('router document navigation', () => {
  it('leaves download and same-origin API links to the browser', () => {
    navigate('/router-test-start', { replace: true });
    const stop = startRouter();
    const pushState = vi.spyOn(window.history, 'pushState');
    const intercepted: boolean[] = [];
    const stopDocumentNavigation = (event: MouseEvent) => {
      intercepted.push(event.defaultPrevented);
      event.preventDefault();
    };
    document.addEventListener('click', stopDocumentNavigation);
    const links = [
      '<a href="/movies" download>Movie export</a>',
      '<a href="/api/v1/system/backup">Download backup</a>',
      '<a href="/api/v1/quality-profiles/export">Export profiles</a>',
    ];

    try {
      for (const markup of links) {
        const container = document.createElement('div');
        container.innerHTML = markup;
        const link = container.firstElementChild as HTMLAnchorElement;
        document.body.appendChild(link);
        const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
        link.dispatchEvent(event);
        link.remove();

        expect(intercepted.at(-1), link.textContent).toBe(false);
      }
      expect(pushState).not.toHaveBeenCalled();
      expect(router.path).toBe('/router-test-start');
    } finally {
      pushState.mockRestore();
      document.removeEventListener('click', stopDocumentNavigation);
      stop();
    }
  });

  it('keeps settings fragments, then scrolls the rendered anchor into view', async () => {
    navigate('/router-test-start', { replace: true });
    const target = document.createElement('section');
    target.id = 'download-clients';
    const scrollIntoView = vi.fn();
    Object.defineProperty(target, 'scrollIntoView', { configurable: true, value: scrollIntoView });
    document.body.appendChild(target);
    const link = document.createElement('a');
    link.href = '/settings/downloads#download-clients';
    document.body.appendChild(link);
    const stop = startRouter();

    try {
      const event = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 });
      link.dispatchEvent(event);
      await Promise.resolve();

      expect(event.defaultPrevented).toBe(true);
      expect(router.path).toBe('/settings/downloads');
      expect(router.hash).toBe('#download-clients');
      expect(window.location.pathname).toBe('/settings/downloads');
      expect(window.location.hash).toBe('#download-clients');
      expect(scrollIntoView).toHaveBeenCalledOnce();

      navigate('/settings');
      expect(router.path).toBe('/settings');
      expect(router.hash).toBe('');
      expect(window.location.hash).toBe('');
    } finally {
      stop();
      link.remove();
      target.remove();
    }
  });
});
