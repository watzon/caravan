/**
 * The filter model (PLAN phase 12 task 5).
 *
 * Three properties are worth proving and only one of them is visible on screen:
 *
 *  1. ROUND TRIP. A filter serialised into the URL and parsed back is the same
 *     filter. This is what makes a filtered view shareable and reload-proof,
 *     and it is the kind of thing that breaks silently — a control keeps
 *     working, and only the link somebody sent comes back wrong.
 *  2. THE API QUERY. Every filter the rail offers reaches the endpoint as a
 *     parameter that endpoint allowlists, and NOTHING else does. The server
 *     answers 400 to an unknown parameter, so a client-only value that leaked
 *     through would break the whole screen rather than one filter.
 *  3. THE SEAM. `people` is a movie question and `networks` is a series one;
 *     neither is ever sent to the scope that refuses it.
 */
import { describe, expect, it } from 'vitest';
import {
  EMPTY_SCENE_FILTER,
  EMPTY_TITLE_FILTER,
  clearedSceneFilter,
  clearedTitleFilter,
  durationBadge,
  exploreScopeHref,
  matchCountLine,
  parseRef,
  parseSceneFilter,
  parseTitleFilter,
  refLabel,
  refParam,
  removeSceneChip,
  removeTitleChip,
  sceneApiQuery,
  sceneChips,
  sceneFilterHref,
  sceneFilterQuery,
  titleApiQuery,
  titleChips,
  titleFilterHref,
  titleFilterQuery,
  toggleRef,
  visibleScopes,
  type SceneFilter,
  type TitleFilter,
} from './explore';
import type { MediaType } from './api/types';

/* ---------------------------------------------------------------------------
 * The scope row.
 * ------------------------------------------------------------------------ */

describe('the scope row', () => {
  it('addresses Featured as /discover itself', () => {
    expect(exploreScopeHref('featured')).toBe('/discover');
    expect(exploreScopeHref('movies')).toBe('/discover/movies');
    expect(exploreScopeHref('series')).toBe('/discover/series');
    expect(exploreScopeHref('adult')).toBe('/discover/adult');
  });

  /**
   * The Adult pill is ABSENT without the grant, not disabled. A greyed-out pill
   * announces that the module exists, which is exactly the trace phase 9
   * promised not to leave.
   */
  it('omits the adult pill entirely without the grant', () => {
    expect(visibleScopes(false).map((s) => s.key)).toEqual(['featured', 'movies', 'series']);
    expect(visibleScopes(true).map((s) => s.key)).toEqual([
      'featured',
      'movies',
      'series',
      'adult',
    ]);
  });
});

/* ---------------------------------------------------------------------------
 * Refs.
 * ------------------------------------------------------------------------ */

describe('refs', () => {
  it('reads a bare id and an id:name', () => {
    expect(parseRef('878')).toEqual({ id: '878', name: '' });
    expect(parseRef('878:Sci-Fi')).toEqual({ id: '878', name: 'Sci-Fi' });
  });

  /**
   * A name may hold a colon ("Star Wars: A New Hope" as a keyword) and a
   * stash-box id may not, so the FIRST colon is the separator and every one
   * after it belongs to the name.
   */
  it('splits on the first colon only', () => {
    expect(parseRef('12:Star Wars: A New Hope')).toEqual({
      id: '12',
      name: 'Star Wars: A New Hope',
    });
  });

  it('reads a stash-box uuid as opaquely as a TMDB integer', () => {
    const uuid = '3f7d1c6a-1111-4a2b-9c3d-000000000001';
    expect(parseRef(`${uuid}:Vixen`)).toEqual({ id: uuid, name: 'Vixen' });
  });

  it('refuses a value with no id', () => {
    expect(parseRef('')).toBeNull();
    expect(parseRef(':Sci-Fi')).toBeNull();
  });

  it('round-trips through the URL spelling', () => {
    for (const raw of ['878', '878:Sci-Fi', '12:Star Wars: A New Hope']) {
      expect(refParam(parseRef(raw)!)).toBe(raw);
    }
  });

  it('falls back to the id when the URL carried no name', () => {
    expect(refLabel({ id: '878', name: '' })).toBe('878');
    expect(refLabel({ id: '878', name: 'Sci-Fi' })).toBe('Sci-Fi');
  });

  it('treats refs as a set keyed by id', () => {
    const one = { id: '1', name: 'A' };
    const two = { id: '2', name: 'B' };
    expect(toggleRef([one], two)).toEqual([one, two]);
    // Picking the same person twice is one filter, not two.
    expect(toggleRef([one, two], { id: '1', name: 'A' })).toEqual([two]);
  });
});

/* ---------------------------------------------------------------------------
 * Round trip — the shareable-URL property.
 * ------------------------------------------------------------------------ */

/** Everything the movie rail can set at once. */
const FULL_MOVIE_FILTER: TitleFilter = {
  genres: [
    { id: '878', name: 'Sci-Fi' },
    { id: '28', name: 'Action' },
  ],
  keywords: [{ id: '9715', name: 'superhero' }],
  companies: [{ id: '41077', name: 'A24' }],
  networks: [],
  people: [{ id: '1245', name: 'Pedro Pascal' }],
  from: '2019-01-01',
  to: '2024-12-31',
  runtimeMin: 60,
  runtimeMax: 120,
  ratingMin: 7.5,
  language: 'ja',
  sort: 'rating',
  hideOwned: true,
};

const FULL_SCENE_FILTER: SceneFilter = {
  text: 'poolside',
  site: { id: '84060', name: 'Vixen' },
  scope: 'network',
  performers: [
    { id: '1', name: 'Sienna Vale' },
    { id: '2', name: 'Mara Solis' },
  ],
  performersAll: true,
  tags: [
    { id: '70', name: 'Outdoor' },
    { id: '71', name: 'Threesome' },
  ],
  tagsAll: true,
  year: 2026,
  duration: 40,
  sort: 'duration',
  hideOwned: true,
};

function reparseTitle(filter: TitleFilter, mediaType: MediaType = 'movie'): TitleFilter {
  return parseTitleFilter(mediaType, new URLSearchParams(titleFilterQuery(mediaType, filter)));
}

function reparseScene(filter: SceneFilter): SceneFilter {
  return parseSceneFilter(new URLSearchParams(sceneFilterQuery(filter)));
}

describe('URL round trip', () => {
  it('restores a full movie filter from its own query string', () => {
    expect(reparseTitle(FULL_MOVIE_FILTER)).toEqual(FULL_MOVIE_FILTER);
  });

  it('restores a full series filter from its own query string', () => {
    const series: TitleFilter = {
      ...FULL_MOVIE_FILTER,
      people: [],
      networks: [{ id: '213', name: 'Netflix' }],
      sort: 'title',
    };
    expect(reparseTitle(series, 'series')).toEqual(series);
  });

  it('restores a full scene filter from its own query string', () => {
    expect(reparseScene(FULL_SCENE_FILTER)).toEqual(FULL_SCENE_FILTER);
  });

  /**
   * The empty filter is the empty string, so /discover/movies and the scope
   * pill's plain href are the same address — otherwise every unfiltered visit
   * would rewrite the URL to a longer spelling of itself.
   */
  it('writes nothing for an empty filter, and reads nothing back as empty', () => {
    expect(titleFilterQuery('movie', EMPTY_TITLE_FILTER)).toBe('');
    expect(sceneFilterQuery(EMPTY_SCENE_FILTER)).toBe('');
    expect(titleFilterHref('movie', EMPTY_TITLE_FILTER)).toBe('/discover/movies');
    expect(titleFilterHref('series', EMPTY_TITLE_FILTER)).toBe('/discover/series');
    expect(sceneFilterHref(EMPTY_SCENE_FILTER)).toBe('/discover/adult');
    expect(parseTitleFilter('movie', new URLSearchParams(''))).toEqual(EMPTY_TITLE_FILTER);
    expect(parseSceneFilter(new URLSearchParams(''))).toEqual(EMPTY_SCENE_FILTER);
  });

  /** The names ride along, so a reloaded chip is labelled without a lookup. */
  it('carries picked names in the URL so reloaded chips are still readable', () => {
    const query = titleFilterQuery('movie', FULL_MOVIE_FILTER);
    expect(new URLSearchParams(query).getAll('people')).toEqual(['1245:Pedro Pascal']);
    expect(titleChips('movie', reparseTitle(FULL_MOVIE_FILTER))).toContainEqual({
      key: 'people:1245',
      label: 'Cast & crew: Pedro Pascal',
    });
  });

  /**
   * Repeated parameters rather than a comma-joined list, on BOTH sides of the
   * URL: a name may contain a comma, and a joined list would split one person
   * into two filters that match nobody.
   */
  it('writes one parameter per ref rather than a joined list', () => {
    const filter: TitleFilter = {
      ...EMPTY_TITLE_FILTER,
      keywords: [
        { id: '1', name: 'lock, stock' },
        { id: '2', name: 'two barrels' },
      ],
    };
    const params = new URLSearchParams(titleFilterQuery('movie', filter));
    expect(params.getAll('keywords')).toEqual(['1:lock, stock', '2:two barrels']);
    expect(reparseTitle(filter)).toEqual(filter);
  });

  it('survives a hand-written URL with bare ids and no names', () => {
    const filter = parseTitleFilter('movie', new URLSearchParams('genres=878&people=1245&rating_min=7'));
    expect(filter.genres).toEqual([{ id: '878', name: '' }]);
    expect(filter.ratingMin).toBe(7);
    // The chip has only the id to show, and shows it rather than a blank.
    expect(titleChips('movie', filter)).toContainEqual({ key: 'genres:878', label: 'Genre: 878' });
  });

  it('drops values it cannot make sense of instead of forwarding them to a 400', () => {
    const filter = parseTitleFilter(
      'movie',
      new URLSearchParams('from=nonsense&runtime_min=-5&rating_min=99&sort=revenue'),
    );
    expect(filter.from).toBe('');
    expect(filter.runtimeMin).toBe(0);
    // `rating_min` is clamped rather than dropped: 99 is a typo for a real
    // intention, and the API refuses anything over 10.
    expect(filter.ratingMin).toBe(10);
    // `revenue` is movie-only at TMDB and is deliberately not in the
    // vocabulary, so it reads as the default rather than as a 400.
    expect(filter.sort).toBe('popularity');
  });

  /** A widened scope with no site to widen is a 400; it is narrowed on the way in. */
  it('refuses to restore a widened site scope with no site', () => {
    expect(parseSceneFilter(new URLSearchParams('scope=network')).scope).toBe('site');
  });

  /**
   * The seam again, read from the URL this time. A cross-scope list used to
   * survive parsing: it was re-emitted on every apply, dropped on the way to
   * the API, and drawn as no chip — an invisible filter with no affordance to
   * remove it, and the server would have answered 400 for the same query
   * string. Dropping it at the door keeps the URL, the chips and the wire
   * query in agreement.
   */
  it('drops a cross-scope list at parse time rather than carrying it invisibly', () => {
    const series = parseTitleFilter(
      'series',
      new URLSearchParams('people=6193:Leonardo%20DiCaprio&genres=878:Sci-Fi'),
    );
    expect(series.people).toEqual([]);
    expect(series.genres).toEqual([{ id: '878', name: 'Sci-Fi' }]);
    expect(titleChips('series', series)).toEqual([{ key: 'genres:878', label: 'Genre: Sci-Fi' }]);
    // And it is gone from the URL the next apply writes, rather than copied
    // forward forever.
    expect(new URLSearchParams(titleFilterQuery('series', series)).getAll('people')).toEqual([]);

    const movie = parseTitleFilter('movie', new URLSearchParams('networks=213:Netflix'));
    expect(movie.networks).toEqual([]);
    expect(titleChips('movie', movie)).toEqual([]);
    expect(titleFilterQuery('movie', movie)).toBe('');
  });

  it('keeps a duplicate ref in the URL from becoming two chips', () => {
    const filter = parseTitleFilter('movie', new URLSearchParams('genres=878:Sci-Fi&genres=878:Science'));
    expect(filter.genres).toEqual([{ id: '878', name: 'Sci-Fi' }]);
  });
});

/* ---------------------------------------------------------------------------
 * Filter → API query.
 * ------------------------------------------------------------------------ */

describe('the movie scope query', () => {
  it('sends every filter the rail offers, and nothing else', () => {
    expect(titleApiQuery('movie', FULL_MOVIE_FILTER, 3)).toEqual({
      genres: '878,28',
      keywords: '9715',
      companies: '41077',
      networks: undefined,
      people: '1245',
      from: '2019-01-01',
      to: '2024-12-31',
      runtime_min: 60,
      runtime_max: 120,
      rating_min: 7.5,
      language: 'ja',
      sort: 'rating',
      order: 'desc',
      page: 3,
    });
  });

  /**
   * The seam this phase documents. TMDB's /discover/tv has no with_cast /
   * with_crew / with_people, and the API answers 400 rather than ignoring one —
   * so a `people` value that survived a scope switch must not be sent.
   */
  it('never sends a person filter to the series scope', () => {
    const query = titleApiQuery('series', { ...FULL_MOVIE_FILTER, networks: [{ id: '213', name: 'Netflix' }] }, 1);
    expect(query.people).toBeUndefined();
    expect(query.networks).toBe('213');
  });

  it('never sends a network filter to the movie scope', () => {
    const query = titleApiQuery('movie', {
      ...EMPTY_TITLE_FILTER,
      networks: [{ id: '213', name: 'Netflix' }],
    }, 1);
    expect(query.networks).toBeUndefined();
  });

  /**
   * `hide` is the browser's own parameter and the endpoint allowlists its query
   * string, so forwarding it would 400 the whole screen rather than one filter.
   */
  it('keeps the client-only hide toggle off the wire', () => {
    const query = titleApiQuery('movie', { ...EMPTY_TITLE_FILTER, hideOwned: true }, 1);
    expect(Object.keys(query)).not.toContain('hide');
    expect(Object.keys(query)).not.toContain('hideOwned');
    // It IS in the URL, which is how a shared link keeps the same view.
    expect(titleFilterQuery('movie', { ...EMPTY_TITLE_FILTER, hideOwned: true })).toBe('hide=1');
  });

  it('sends the ids without the names TMDB has no room for', () => {
    const query = titleApiQuery('movie', FULL_MOVIE_FILTER, 1);
    expect(String(query.genres)).not.toContain('Sci-Fi');
  });
});

describe('the scene scope query', () => {
  it('sends every filter the rail offers, and nothing else', () => {
    expect(sceneApiQuery(FULL_SCENE_FILTER, 2)).toEqual({
      q: 'poolside',
      site: '84060',
      scope: 'network',
      performers: ['1:Sienna Vale', '2:Mara Solis'],
      performers_all: 'true',
      tags: ['70:Outdoor', '71:Threesome'],
      tags_all: 'true',
      year: 2026,
      duration: 2400,
      sort: 'duration',
      order: 'desc',
      page: 2,
    });
  });

  /**
   * The name goes over the wire here, unlike the TMDB scopes: the provider's
   * own filter is spelled `performers[84060]=Mia Malkova`, so the pair is the
   * value rather than decoration on it.
   */
  it('sends refs as id:name, because the provider filters on both', () => {
    const query = sceneApiQuery(
      { ...EMPTY_SCENE_FILTER, performers: [{ id: '84060', name: 'Mia Malkova' }] },
      1,
    );
    expect(query.performers).toEqual(['84060:Mia Malkova']);
  });

  it('reads minutes on the rail and sends the seconds the provider counts', () => {
    expect(sceneApiQuery({ ...EMPTY_SCENE_FILTER, duration: 40 }, 1).duration).toBe(2400);
  });

  /** `scope` without `site` is a 400, so it is never sent alone. */
  it('never sends a widened scope with no site', () => {
    const query = sceneApiQuery({ ...EMPTY_SCENE_FILTER, scope: 'network' }, 1);
    expect(query.scope).toBeUndefined();
    expect(query.site).toBeUndefined();
  });

  /**
   * The all-of switch is only sent alongside something to combine. One id is
   * the same question either way, and a generic stash-box endpoint refuses an
   * any-of with two or more — sending the switch on an empty list would be a
   * filter nobody asked for.
   */
  it('only sends the all-of switch when it changes the question', () => {
    expect(sceneApiQuery({ ...EMPTY_SCENE_FILTER, tagsAll: true }, 1).tags_all).toBeUndefined();
    expect(
      sceneApiQuery({ ...EMPTY_SCENE_FILTER, tags: [{ id: '70', name: 'x' }], tagsAll: true }, 1)
        .tags_all,
    ).toBe('true');
  });

  it('keeps the client-only hide toggle off the wire', () => {
    const query = sceneApiQuery({ ...EMPTY_SCENE_FILTER, hideOwned: true }, 1);
    expect(Object.keys(query)).not.toContain('hide');
  });
});

/* ---------------------------------------------------------------------------
 * Chips.
 * ------------------------------------------------------------------------ */

describe('applied chips', () => {
  it('names every applied movie filter once', () => {
    expect(titleChips('movie', FULL_MOVIE_FILTER).map((c) => c.label)).toEqual([
      'Genre: Sci-Fi',
      'Genre: Action',
      'Cast & crew: Pedro Pascal',
      'Studio: A24',
      'Keyword: superhero',
      'Year: 2019–2024',
      'Runtime: 60–120 min',
      'Rating: 7.5+',
      'Language: Japanese',
    ]);
  });

  /** Nothing applied is no chips — the row disappears rather than saying "none". */
  it('says nothing when nothing is applied', () => {
    expect(titleChips('movie', EMPTY_TITLE_FILTER)).toEqual([]);
    expect(sceneChips(EMPTY_SCENE_FILTER)).toEqual([]);
  });

  /**
   * A chip the current scope cannot serve is not shown. A `people` value can
   * survive a switch from Movies to Series in the URL, and a chip for it would
   * claim a filter that is not being applied.
   */
  it('hides a filter the scope does not serve', () => {
    const labels = titleChips('series', FULL_MOVIE_FILTER).map((c) => c.label);
    expect(labels).not.toContain('Cast & crew: Pedro Pascal');
  });

  it('removes one chip and leaves the rest', () => {
    const next = removeTitleChip(FULL_MOVIE_FILTER, 'genres:878');
    expect(next.genres).toEqual([{ id: '28', name: 'Action' }]);
    expect(next.people).toEqual(FULL_MOVIE_FILTER.people);
  });

  /** A range is one thought, so its chip removes both bounds. */
  it('removes both halves of a range together', () => {
    const next = removeTitleChip(FULL_MOVIE_FILTER, 'runtime');
    expect(next.runtimeMin).toBe(0);
    expect(next.runtimeMax).toBe(0);
    expect(removeTitleChip(FULL_MOVIE_FILTER, 'dates')).toMatchObject({ from: '', to: '' });
  });

  it('ignores a chip key it does not know', () => {
    expect(removeTitleChip(FULL_MOVIE_FILTER, 'nonsense')).toEqual(FULL_MOVIE_FILTER);
  });

  it('names every applied scene filter once', () => {
    expect(sceneChips(FULL_SCENE_FILTER).map((c) => c.label)).toEqual([
      'Search: poolside',
      'Site: Vixen · whole network',
      'Performer: Sienna Vale',
      'Performer: Mara Solis',
      'Tag: Outdoor',
      'Tag: Threesome',
      'Year: 2026',
      'Duration: 40 min',
    ]);
  });

  /** Dropping the site drops the widening with it — `scope` alone is a 400. */
  it('drops the widened scope when the site chip goes', () => {
    const next = removeSceneChip(FULL_SCENE_FILTER, 'site');
    expect(next.site).toBeNull();
    expect(next.scope).toBe('site');
  });

  /**
   * The any/all switch is meaningless over one chip, so removing the second
   * performer takes it down too — otherwise the URL would carry a mode nothing
   * on screen explains.
   */
  it('drops the all-of switch when only one chip is left', () => {
    const next = removeSceneChip(FULL_SCENE_FILTER, 'performers:2');
    expect(next.performers).toHaveLength(1);
    expect(next.performersAll).toBe(false);
    expect(next.tagsAll).toBe(true);
  });

  /**
   * Clear all clears the CHIPS. Sort and the hide toggle are not chips, so a
   * button that appears to be about the chips row must not silently reset them.
   */
  it('clears the chips and keeps what is not one', () => {
    expect(clearedTitleFilter(FULL_MOVIE_FILTER)).toEqual({
      ...EMPTY_TITLE_FILTER,
      sort: 'rating',
      hideOwned: true,
    });
    expect(clearedSceneFilter(FULL_SCENE_FILTER)).toEqual({
      ...EMPTY_SCENE_FILTER,
      sort: 'duration',
      hideOwned: true,
    });
    expect(titleChips('movie', clearedTitleFilter(FULL_MOVIE_FILTER))).toEqual([]);
  });

  it('reads a one-sided range honestly', () => {
    const from = titleChips('movie', { ...EMPTY_TITLE_FILTER, runtimeMin: 90 });
    expect(from[0]?.label).toBe('Runtime: from 90 min');
    const under = titleChips('movie', { ...EMPTY_TITLE_FILTER, runtimeMax: 45 });
    expect(under[0]?.label).toBe('Runtime: under 45 min');
    const one = titleChips('movie', { ...EMPTY_TITLE_FILTER, from: '2019-01-01', to: '2019-12-31' });
    expect(one[0]?.label).toBe('Year: 2019');
  });
});

/* ---------------------------------------------------------------------------
 * Small renderers.
 * ------------------------------------------------------------------------ */

describe('durationBadge', () => {
  it('reads as a run time, not as a number of seconds', () => {
    expect(durationBadge(2472)).toBe('41:12');
    expect(durationBadge(59)).toBe('0:59');
    expect(durationBadge(3661)).toBe('1:01:01');
  });

  /** The provider reports 0 far more often than it reports a real duration. */
  it('says nothing rather than "0:00" when the provider does not know', () => {
    expect(durationBadge(0)).toBeNull();
    expect(durationBadge(-5)).toBeNull();
    expect(durationBadge(Number.NaN)).toBeNull();
  });
});

describe('matchCountLine', () => {
  it('counts in the reader’s own thousands separator', () => {
    expect(matchCountLine(24861, 'movie')).toBe(`${(24861).toLocaleString()} movies match`);
  });

  /** The verb agrees with the noun; "1 scene match" is not a sentence. */
  it('conjugates the singular', () => {
    expect(matchCountLine(1, 'scene')).toBe('1 scene matches');
  });

  it('says nothing when there is nothing to count', () => {
    expect(matchCountLine(0, 'movie')).toBe('');
  });
});
