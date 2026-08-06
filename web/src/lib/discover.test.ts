/**
 * The season maths behind the add/request modal. This is the part with the
 * edge cases — seasons already owned, seasons already requested, whole-title
 * vs partial — so it is proved here rather than through the dialog.
 */
import { describe, expect, it } from 'vitest';
import type { DiscoverSeason } from './api/types';
import {
  absorbNote,
  addSeasons,
  allSeasonNumbers,
  allSelected,
  defaultSeasonSelection,
  canRequestSeason,
  discoverHref,
  languageName,
  libraryHref,
  mediaTypeChip,
  modalSubtitle,
  ratingPresentation,
  ratingText,
  requestSeasons,
  runtimeText,
  seasonMeta,
  selectableSeasons,
  sourceHref,
  submitLabel,
  toggleSeason,
  yearOf,
} from './discover';

function season(
  number: number,
  extra: Partial<DiscoverSeason> = {},
): DiscoverSeason {
  return {
    season_number: number,
    title: `Season ${number}`,
    overview: '',
    poster_url: '',
    air_date: `20${10 + number}-01-01`,
    episode_count: 10,
    in_library: false,
    requested: false,
    ...extra,
  };
}

describe('discover links and labels', () => {
  it('addresses a discover card by TMDB id and a library item by library id', () => {
    expect(discoverHref({ media_type: 'series', tmdb_id: 1396 })).toBe('/discover/series/1396');
    expect(libraryHref('series', 3)).toBe('/series/3');
    expect(libraryHref('movie', 3)).toBe('/movies/3');
    expect(sourceHref({ type: 'network', id: 213 })).toBe('/discover/network/213');
  });

  it('never speaks TMDB "tv"', () => {
    expect(mediaTypeChip('series')).toBe('SERIES');
    expect(mediaTypeChip('movie')).toBe('MOVIE');
  });

  it('formats a positive finite average only when it is backed by votes', () => {
    expect(ratingText(7.862, 1)).toBe('7.9');
    expect(ratingText(8, 42)).toBe('8.0');
    expect(ratingText(7.862, 0)).toBeNull();
    expect(ratingText(0, 42)).toBeNull();
    expect(ratingText(Number.NaN, 42)).toBeNull();
  });

  it('presents a voted score only once the title has been released', () => {
    const today = new Date(2025, 5, 15);

    expect(ratingPresentation(7.862, 1, '2025-06-14', today)).toEqual({
      text: '7.9/10',
      title: 'Rated 7.9/10',
    });
    expect(ratingPresentation(8, 42, '2025-06-15', today)).toEqual({
      text: '8.0/10',
      title: 'Rated 8.0/10',
    });
    expect(ratingPresentation(9.1, 12, '2025-06-16', today)).toEqual({
      text: null,
      title: 'Not yet rated',
    });
  });

  it('does not present unvoted, unknown, or invalidly dated titles as rated', () => {
    const today = new Date(2025, 5, 15);
    const notYetRated = { text: null, title: 'Not yet rated' };

    expect(ratingPresentation(7.5, 0, '2025-06-14', today)).toEqual(notYetRated);
    expect(ratingPresentation(0, 10, '2025-06-14', today)).toEqual(notYetRated);
    expect(ratingPresentation(Number.NaN, 10, '2025-06-14', today)).toEqual(notYetRated);
    expect(ratingPresentation(7.5, 10, '', today)).toEqual(notYetRated);
    expect(ratingPresentation(7.5, 10, 'not-a-date', today)).toEqual(notYetRated);
    expect(ratingPresentation(7.5, 10, '2025-02-30', today)).toEqual(notYetRated);
  });

  it('reads the leading year off a provider date', () => {
    expect(yearOf('2008-01-20')).toBe(2008);
    expect(yearOf('')).toBe(0);
    expect(yearOf(null)).toBe(0);
  });

  it('renders a runtime, and the unknown placeholder at zero', () => {
    expect(runtimeText(49)).toBe('49 min');
    expect(runtimeText(0)).toBe('—');
  });

  it('builds the mono season line from what is known', () => {
    expect(seasonMeta({ episode_count: 12, air_date: '2022-02-18' })).toBe('12 EPS · 2022');
    expect(seasonMeta({ episode_count: 0, air_date: '2022-02-18' })).toBe('2022');
    expect(seasonMeta({ episode_count: 0, air_date: '' })).toBe('');
  });
});

describe('season selection', () => {
  const seasons = [
    season(1, { in_library: true }),
    season(2, { requested: true }),
    season(3),
  ];

  it('never offers a season the library already holds', () => {
    expect(selectableSeasons(seasons).map((s) => s.season_number)).toEqual([2, 3]);
    expect(allSeasonNumbers(seasons)).toEqual([2, 3]);
  });

  // Add mode takes everything missing; request mode also skips what somebody
  // already asked for, because re-requesting it just merges into the same row.
  it('defaults to the missing seasons, minus the pending ones in request mode', () => {
    expect(defaultSeasonSelection(seasons, 'add')).toEqual([2, 3]);
    expect(defaultSeasonSelection(seasons, 'request')).toEqual([3]);
  });

  it('toggles a season in and out, keeping the list sorted', () => {
    expect(toggleSeason([3], 2)).toEqual([2, 3]);
    expect(toggleSeason([2, 3], 2)).toEqual([3]);
  });

  it('knows when select-all should flip to deselect-all', () => {
    expect(allSelected(seasons, [2, 3])).toBe(true);
    expect(allSelected(seasons, [3])).toBe(false);
    // Only owned seasons: there is nothing to select, so "all" is not selected.
    expect(allSelected([season(1, { in_library: true })], [])).toBe(false);
  });
});

describe('request payload', () => {
  const seasons = [season(1), season(2), season(3)];

  it('omits the season list when the whole title is checked', () => {
    expect(requestSeasons(seasons, [1, 2, 3])).toBeUndefined();
    expect(requestSeasons([], [])).toBeUndefined();
  });

  it('sends a sorted partial list otherwise', () => {
    expect(requestSeasons(seasons, [3, 1])).toEqual([1, 3]);
  });
});

describe('add payload', () => {
  const seasons = [season(1), season(2), season(3)];

  it('omits the season list when the add covers the whole series', () => {
    expect(addSeasons(seasons, [1, 2, 3])).toBeUndefined();
    expect(addSeasons([], [])).toBeUndefined();
  });

  it('sends a sorted partial list otherwise', () => {
    expect(addSeasons(seasons, [3, 1])).toEqual([1, 3]);
  });

  /**
   * The endpoint reads the list as "monitor exactly these", so an owned season
   * has to stay in it — the add was never asked to stop tracking it.
   */
  it('always keeps the seasons the library already holds', () => {
    const owned = [season(1, { in_library: true }), season(2), season(3)];
    expect(addSeasons(owned, [2])).toEqual([1, 2]);
    // Owned plus every selectable season is the whole series again.
    expect(addSeasons(owned, [2, 3])).toBeUndefined();
  });
});

describe('per-season request availability', () => {
  /**
   * POST /requests answers 409 for anything already tracked, and it checks the
   * title rather than the season, so an owned series can never take one.
   */
  it('offers nothing on a series the library already holds', () => {
    expect(canRequestSeason(true, season(2))).toBe(false);
    expect(canRequestSeason(true, season(2, { in_library: true }))).toBe(false);
  });

  it('offers a season that is neither owned nor already asked for', () => {
    expect(canRequestSeason(false, season(2))).toBe(true);
    expect(canRequestSeason(false, season(2, { in_library: true }))).toBe(false);
    expect(canRequestSeason(false, season(2, { requested: true }))).toBe(false);
  });
});

describe('language names', () => {
  it('names an ISO code, and degrades to the code or the placeholder', () => {
    expect(languageName('en')).toBe('English');
    expect(languageName('zz')).toBe('zz');
    expect(languageName('')).toBe('—');
  });
});

describe('absorb note', () => {
  const seasons = [
    season(1, { requested: true }),
    season(2, { requested: true }),
    season(3),
    season(4, { in_library: true, requested: true }),
  ];

  it('warns once per checked season that has a pending request', () => {
    expect(absorbNote(seasons, [1, 3], 'add')).toBe(
      'Adding Season 01 will absorb its pending request and mark it approved.',
    );
    expect(absorbNote(seasons, [1, 2], 'add')).toBe(
      'Adding Season 01 and Season 02 will absorb their pending requests and mark them approved.',
    );
  });

  it('stays quiet when nothing checked is requested', () => {
    expect(absorbNote(seasons, [3], 'add')).toBeNull();
  });

  // An owned season cannot be checked, so it can never contribute a warning.
  it('ignores requested seasons the library already holds', () => {
    expect(absorbNote(seasons, [4], 'add')).toBeNull();
  });

  // In request mode the merge is the point, not a side effect worth warning about.
  it('says nothing in request mode', () => {
    expect(absorbNote(seasons, [1, 2], 'request')).toBeNull();
  });
});

describe('modal copy', () => {
  it('counts checked seasons in the primary button', () => {
    expect(submitLabel('add', 'series', 3)).toBe('Add 3 seasons');
    expect(submitLabel('request', 'series', 1)).toBe('Request 1 season');
    expect(submitLabel('add', 'movie', 0)).toBe('Add movie');
    expect(submitLabel('request', 'movie', 0)).toBe('Request movie');
  });

  it('falls back to the whole series when there is no season data', () => {
    expect(submitLabel('add', 'series', 0)).toBe('Add series');
    expect(submitLabel('request', 'series', 0)).toBe('Request series');
  });

  it('summarises the title under the modal header', () => {
    expect(modalSubtitle('movie', 'Blade Runner', 1982, [])).toBe('Blade Runner · 1982');
    expect(modalSubtitle('movie', 'Untitled', 0, [])).toBe('Untitled');
    expect(
      modalSubtitle('series', 'Severance', 2022, [season(1), season(2)]),
    ).toBe('Severance · 2 seasons · 20 episodes');
    expect(modalSubtitle('series', 'Unknown Show', 2022, [])).toBe('Unknown Show');
  });
});
